package services

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// newGuardrailTestChat 将 ChatService 接入一个脚本化的模型服务器，并让 Agent
// 携带指定的运行护栏。
func newGuardrailTestChat(t *testing.T, maxToolCalls int, handler http.HandlerFunc) (*ChatService, *[]ChatStreamEvent, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "a1", Name: "助手", ProviderID: "p1", Model: "m",
			BuiltinTools: []string{"time"},
			MaxToolCalls: maxToolCalls,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)

	events := []ChatStreamEvent{}
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = nil })
	return chat, &events, dir
}

// TestGuardrailMaxToolCalls 在达到配置的工具调用次数后停止回合，而不是让模型无限循环。
func TestGuardrailMaxToolCalls(t *testing.T) {
	var calls atomic.Int64
	chat, events, _ := newGuardrailTestChat(t, 2, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) <= 4 {
			// 持续请求时间工具：第 1..4 轮交替出现 tool_call → result；护栏必须在第 3
			// 轮及之后停止。
			_, _ = w.Write([]byte(sseToolCall("c"+fmt.Sprint(calls.Load()), "get_current_time", `"{}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	})

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "查询时间", nil); err != nil {
		t.Fatal(err)
	}

	var sawGuardrail, sawDone bool
	for _, ev := range *events {
		if ev.Kind == "error" && strings.Contains(ev.Text, "工具调用上限（2") {
			sawGuardrail = true
		}
		if ev.Kind == "done" {
			sawDone = true
		}
	}
	if !sawGuardrail || !sawDone {
		t.Fatalf("guardrail stop incomplete: guardrail=%v done=%v events=%+v", sawGuardrail, sawDone, *events)
	}
}

// TestGuardrailUnlimitedKeepsOldBehavior：0 表示不设工具调用护栏。
func TestGuardrailUnlimitedKeepsOldBehavior(t *testing.T) {
	chat, events, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("直接回答") + sseDone))
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "error" {
			t.Fatalf("unexpected error with unlimited config: %+v", ev)
		}
	}
	var answered bool
	msgs, _ := chat.GetSessionMessages(sess.ID)
	for _, m := range msgs {
		if m.Kind == "assistant" && strings.Contains(m.Text, "直接回答") {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("answer missing: %+v", msgs)
	}
}

// TestGuardrailSessionTokenBudget：会话累计消耗超出预算时，允许完成跨过预算的那
// 个回合，之后拒绝新的用户回合。
func TestGuardrailSessionTokenBudget(t *testing.T) {
	var modelCalls atomic.Int64
	chat, events, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[],"usage":{"prompt_tokens":300,"completion_tokens":200,"total_tokens":500}}` + "\n\n"))
		_, _ = w.Write([]byte(sseText("回答") + sseDone))
	})
	// 通过 Agent 配置将预算提高到 600。
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Agents[0].MaxSessionTokens = 600
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "第一轮", nil); err != nil {
		t.Fatal(err)
	}
	if modelCalls.Load() != 1 {
		t.Fatalf("turn 1 model calls = %d", modelCalls.Load())
	}

	// 第 2 回合在回合中途超出预算：该回合会完成，随后发出警告。
	if err := chat.Send(sess.ID, "第二轮", nil); err != nil {
		t.Fatal(err)
	}
	var sawWarn bool
	for _, ev := range *events {
		if ev.Kind == "error" && strings.Contains(ev.Text, "已达会话 token 预算") {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Fatalf("crossing-turn warning missing: %+v", *events)
	}

	// 第 3 回合在调用模型之前就被拒绝。
	before := modelCalls.Load()
	if err := chat.Send(sess.ID, "第三轮", nil); err != nil {
		t.Fatal(err)
	}
	if modelCalls.Load() != before {
		t.Fatal("refused turn must not call the model")
	}
	var sawRefusal bool
	for _, ev := range *events {
		if ev.Kind == "error" && strings.Contains(ev.Text, "本轮未执行") {
			sawRefusal = true
		}
	}
	if !sawRefusal {
		t.Fatalf("refusal message missing: %+v", *events)
	}
}

// TestToolAuditTrail：启用审计后，工具循环会将调用记录和结果记录写入 JSONL
// 文件；禁用审计的会话不写入任何内容。
func TestToolAuditTrail(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "tools-audit.jsonl")
	turn := 0
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "get_current_time", `"{}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("查询完成") + sseDone))
	})
	chat.SetToolAuditPath(auditPath)

	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings.ToolsAudit = true
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "查询时间", nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("audit file missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var sawCall, sawResult bool
	for _, ln := range lines {
		if strings.Contains(ln, `"phase":"call"`) && strings.Contains(ln, `"get_current_time"`) && strings.Contains(ln, sess.ID) {
			sawCall = true
		}
		if strings.Contains(ln, `"phase":"result"`) && strings.Contains(ln, "timezone") {
			sawResult = true
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("audit records incomplete: call=%v result=%v\n%s", sawCall, sawResult, data)
	}
}

// TestCompactionIntervalSummarizes：启用滑动窗口后，运行器会在回合结束后执行一
// 次额外的摘要模型调用。
func TestCompactionIntervalSummarizes(t *testing.T) {
	var modelCalls atomic.Int64
	chat, events, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		// 压缩摘要器以非流式方式调用模型。
		if strings.Contains(readAll(r), `"stream":false`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"摘要"},"finish_reason":"stop"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("带压缩的回答") + sseDone))
	})
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings.CompactionInterval = 1
		cfg.Settings.CompactionTokenThreshold = 0
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好", nil); err != nil {
		t.Fatal(err)
	}
	// 一次回答调用 + 至少一次摘要调用。
	if modelCalls.Load() < 2 {
		t.Fatalf("expected summarization model call, got %d total", modelCalls.Load())
	}
	var answered bool
	for _, ev := range *events {
		if ev.Kind == "message" && strings.Contains(ev.Text, "带压缩的回答") {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("answer missing under compaction: %+v", *events)
	}

	// 默认关闭：间隔为 0 的新会话恰好发起一次调用。
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings.CompactionInterval = 0
	}); err != nil {
		t.Fatal(err)
	}
	before := modelCalls.Load()
	if err := chat.Send(sess.ID, "再来一次", nil); err != nil {
		t.Fatal(err)
	}
	if modelCalls.Load() != before+1 {
		t.Fatalf("without compaction expected exactly one extra model call: %d -> %d", before, modelCalls.Load())
	}
}

// TestSearchSessionsMatchesContent：content LIKE
// 探针会找出历史记录包含查询的会话，即使标题不包含查询。
func TestSearchSessionsMatchesContent(t *testing.T) {
	turn := 0
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseText("讨论量子纠缠的机制") + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("无关回答") + sseDone))
	})

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "聊聊量子纠缠", nil); err != nil {
		t.Fatal(err)
	}

	res := chat.SearchSessions("量子纠缠")
	if len(res) != 1 || res[0].ID != sess.ID {
		t.Fatalf("content search failed: %+v", res)
	}
	if empty := chat.SearchSessions("  "); len(empty) != 1 {
		t.Fatalf("empty query should list all: %+v", empty)
	}
	if none := chat.SearchSessions("不存在的词xyz"); len(none) != 0 {
		t.Fatalf("non-matching query should return none: %+v", none)
	}
}

// TestToolAuditRotation：当审计文件达到上限时，会轮转至
// <path>.old，新记录继续写入新文件。
func TestToolAuditRotation(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	turn := 0
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "get_current_time", `"{}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	})
	chat.SetToolAuditPath(auditPath)
	if err := chat.S.Store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}
	// 预先填充审计文件，使下一条记录超过上限。
	if err := os.WriteFile(auditPath, []byte(strings.Repeat("x", auditMaxBytes-2)), 0o640); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "触发轮转", nil); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(auditPath + ".old"); err != nil {
		t.Fatalf("rotation did not produce .old file: %v", err)
	}
	fresh, err := os.ReadFile(auditPath)
	if err != nil || len(fresh) == 0 || len(fresh) >= auditMaxBytes {
		t.Fatalf("fresh audit file wrong: len=%d err=%v", len(fresh), err)
	}
}
