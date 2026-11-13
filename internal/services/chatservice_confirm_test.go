package services

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
	"agentmate/internal/sandbox"
)

// sseToolCall 渲染一个兼容 OpenAI 的 SSE 数据块，该数据块承载一次工具调用。
func sseToolCall(id, name, arguments string) string {
	return `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"` + id + `","function":{"name":"` + name + `","arguments":` + arguments + `}}]}}]}` + "\n\n"
}

func sseText(text string) string {
	return `data: {"choices":[{"delta":{"content":"` + text + `"}}]}` + "\n\n"
}

const sseDone = "data: [DONE]\n\n"

// newConfirmTestChat 将 ChatService 接入脚本化的假模型服务器，并使用一个已启用
// files 工具和 HITL 确认的 Agent。
func newConfirmTestChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store, string, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	workspace := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
		// 这些测试检验 cmd.exe 语法和 Windows 沙箱；应显式指定
		// shell，而不是继承依赖于机器的自动顺序。
		cfg.Settings.Terminal = testTerminalSettings()
		// 这些 HITL 测试按 Agent 配置
		// ConfirmTools。自动审批会保留这些显式审批提示，同时允许清除这些提示的测试执
		// 行直接工具调用。
		cfg.Settings.SandboxMode = config.SandboxModeAutoApprove
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_c", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_c", Name: "写手", ProviderID: "prov_c", Model: "fake-mini",
			SystemPrompt: "你负责写文件", BuiltinTools: []string{"files"},
			ConfirmTools: []string{"files"},
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
	return NewChatService(svc, sessions, db), store, workspace, dir
}

func hookEvents(t *testing.T) *[]ChatStreamEvent {
	t.Helper()
	events := []ChatStreamEvent{}
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = nil })
	return &events
}

// TestChatToolConfirmationApprove 检验完整的 HITL
// 循环：模型调用受确认门控的写入工具，运行在 ADK adk_request_confirmation
// 中断处暂停，用户通过 ConfirmToolCall 审批，工具执行，模型生成最终答案。
func TestChatToolConfirmationApprove(t *testing.T) {
	turn := 0
	var callID string
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"notes/hitl.txt\",\"content\":\"确认后写入\"}"`) + sseDone))
		case 2:
			body := readAll(r)
			// ADK 会为模型折叠确认往返：恢复后的回合会显示原始工具调用及其结果（结果在工具消
			// 息内容中经过 JSON 转义）。
			if !strings.Contains(body, `\"ok\":true`) {
				t.Errorf("resume request missing approved write result, got: %s", body)
			}
			_, _ = w.Write([]byte(sseText("文件已确认写入。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "把 notes/hitl.txt 写好", nil); err != nil {
		t.Fatal(err)
	}

	// 第 1 回合必须恰好在一个可操作的确认请求上暂停。
	var req *ChatStreamEvent
	for i := range *events {
		ev := (*events)[i]
		if ev.Kind == "confirm_request" {
			req = &(*events)[i]
		}
	}
	if req == nil {
		t.Fatalf("expected confirm_request event, got: %+v", *events)
	}
	if req.ToolName != "write_workspace_file" || req.CallID == "" {
		t.Fatalf("confirm_request incomplete: %+v", req)
	}
	if req.ToolArgs["path"] != "notes/hitl.txt" {
		t.Fatalf("confirm_request should surface the ORIGINAL call args: %+v", req)
	}
	if !strings.Contains(req.Hint, "写入工作区文件") {
		t.Fatalf("confirm_request should explain the write boundary: %q", req.Hint)
	}
	callID = req.CallID

	// 审批前不得写入任何内容。
	if _, err := os.Stat(filepath.Join(workspace, "notes", "hitl.txt")); !os.IsNotExist(err) {
		t.Fatal("file must not exist before approval")
	}

	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}

	// 审批后工具运行，模型作出回答。
	if data, err := os.ReadFile(filepath.Join(workspace, "notes", "hitl.txt")); err != nil || string(data) != "确认后写入" {
		t.Fatalf("expected approved write, err=%v data=%q", err, data)
	}
	var sawResult, sawAnswer bool
	for _, ev := range *events {
		if ev.Kind == "tool_result" && ev.ToolName == "write_workspace_file" && ev.ToolResp["ok"] == true {
			sawResult = true
		}
		if ev.Kind == "message" && strings.Contains(ev.Text, "已确认写入") {
			sawAnswer = true
		}
	}
	if !sawResult || !sawAnswer {
		t.Fatalf("resume incomplete: result=%v answer=%v events=%+v", sawResult, sawAnswer, *events)
	}

	// 历史记录渲染确认对与工具循环。
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawReq, sawResultMsg, sawConfirmResult bool
	for _, m := range msgs {
		switch m.Kind {
		case "confirm_request":
			sawReq = m.CallID == callID && m.ToolName == "write_workspace_file" && m.ToolArgs["path"] == "notes/hitl.txt"
		case "tool_result":
			sawResultMsg = sawResultMsg || m.ToolName == "write_workspace_file"
		case "confirm_result":
			sawConfirmResult = m.CallID == callID && m.ToolResp["confirmed"] == true
		}
	}
	if !sawReq || !sawResultMsg || !sawConfirmResult {
		t.Fatalf("history missing confirm flow: req=%v result=%v confirmResult=%v\n%+v", sawReq, sawResultMsg, sawConfirmResult, msgs)
	}

	// 再次应答必须被拒绝，不得静默重跑。
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err == nil || !strings.Contains(err.Error(), "已被处理") {
		t.Fatalf("expected double-answer rejection, got %v", err)
	}
}

// TestChatToolConfirmationReject 验证拒绝路径：工具绝不运行，模型会看到该拒绝。
func TestChatToolConfirmationReject(t *testing.T) {
	turn := 0
	var callID string
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"nope.txt\",\"content\":\"不应写入\"}"`) + sseDone))
		case 2:
			body := readAll(r)
			if !strings.Contains(body, "call is rejected") {
				t.Errorf("resume request missing rejection error, got: %s", body)
			}
			_, _ = w.Write([]byte(sseText("好的，已取消写入。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 nope.txt", nil); err != nil {
		t.Fatal(err)
	}
	for i := range *events {
		if (*events)[i].Kind == "confirm_request" {
			callID = (*events)[i].CallID
		}
	}
	if callID == "" {
		t.Fatal("expected confirm_request event")
	}

	if err := chat.ConfirmToolCall(sess.ID, callID, false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(workspace, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("rejected write must not touch the workspace")
	}
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	sawConfirmResult := false
	for _, m := range msgs {
		if m.Kind == "confirm_result" && m.CallID == callID && m.ToolResp["confirmed"] == false {
			sawConfirmResult = true
		}
	}
	if !sawConfirmResult {
		t.Fatalf("history missing rejected confirm_result: %+v", msgs)
	}
}

// TestChatToolConfirmationRestartResume 证明待处理的确认在应用重启后仍然存活：基于同一会话 DB 的新 ChatService
// 会从已存储的事件中重新渲染未应答的请求，并且仍能对其作出应答。
func TestChatToolConfirmationRestartResume(t *testing.T) {
	turn := 0
	var callID string
	chat, _, workspace, dir := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"later.txt\",\"content\":\"重启后写入\"}"`) + sseDone))
		case 2:
			if !strings.Contains(readAll(r), `\"ok\":true`) {
				t.Errorf("expected approved write result on post-restart resume")
			}
			_, _ = w.Write([]byte(sseText("重启后确认成功。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 later.txt", nil); err != nil {
		t.Fatal(err)
	}

	// "Restart"：基于相同的 sessions DB 与配置重建服务。
	db2, sessions2, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc2 := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat2 := NewChatService(svc2, sessions2, db2)

	msgs, err := chat2.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, m := range msgs {
		if m.Kind == "confirm_request" && m.CallID != "" {
			callID = m.CallID
			pending++
		}
		if m.Kind == "confirm_result" {
			t.Fatal("no confirm_result should exist before answering")
		}
	}
	if pending != 1 || callID == "" {
		t.Fatalf("expected exactly one pending confirm_request after restart, got %d", pending)
	}

	if err := chat2.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "later.txt")); err != nil || string(data) != "重启后写入" {
		t.Fatalf("expected post-restart write, err=%v data=%q", err, data)
	}
}

// TestChatToolConfirmationGuards 覆盖参数校验与自动模式不变式：未配置
// ConfirmTools 的 Agent 直接运行工具调用。
func TestChatToolConfirmationGuards(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"direct.txt\",\"content\":\"直接写入\"}"`) + sseDone))
		} else {
			_, _ = w.Write([]byte(sseText("完成。") + sseDone))
		}
	})
	events := hookEvents(t)

	if err := chat.ConfirmToolCall("sess_missing", "call_x", true); err == nil {
		t.Fatal("expected error for unknown session")
	}

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.ConfirmToolCall(sess.ID, "", true); err == nil {
		t.Fatal("expected error for empty callID")
	}
	if err := chat.ConfirmToolCall(sess.ID, "call_nonexistent", true); err == nil {
		t.Fatal("expected error for unknown callID with no pending interrupt")
	}

	// 默认关闭：清空 ConfirmTools 后，写入运行时不出现任何 HITL 暂停。
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Agents {
			if cfg.Agents[i].ID == "agent_c" {
				cfg.Agents[i].ConfirmTools = nil
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "直接写 direct.txt", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("no confirmation expected when ConfirmTools is empty: %+v", ev)
		}
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "direct.txt")); err != nil || string(data) != "直接写入" {
		t.Fatalf("expected direct write without HITL, err=%v data=%q", err, data)
	}
}

// TestAutoApproveConfirmsOnlyRiskyCommands 验证自动审批配置：
// 安全命令会在不触发中断的情况下运行，而检测到风险的命令会在常规 HITL
// 流程中暂停。
func TestAutoApproveConfirmsOnlyRiskyCommands(t *testing.T) {
	if !sandbox.Available() {
		t.Skipf("OS sandbox unavailable: %s", sandbox.BackendStatus().Error)
	}
	turn := 0
	chat, store, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("safe_1", "run_command", `"{\"command\":\"echo safe\"}"`) + sseDone))
		case 2:
			if !strings.Contains(body, `\"exitCode\":0`) {
				t.Errorf("safe command did not execute: %s", body)
			}
			_, _ = w.Write([]byte(sseText("safe done") + sseDone))
		case 3:
			_, _ = w.Write([]byte(sseToolCall("risk_1", "run_command", `"{\"command\":\"git push origin main\"}"`) + sseDone))
		case 4:
			_, _ = w.Write([]byte(sseText("risk handled") + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("done") + sseDone))
		}
	})
	events := hookEvents(t)

	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.SandboxMode = config.SandboxModeAutoApprove
		for i := range cfg.Agents {
			cfg.Agents[i].BuiltinTools = []string{"shell"}
			cfg.Agents[i].ConfirmTools = nil
		}
	}); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "运行安全命令", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("safe command unexpectedly asked for approval: %+v", ev)
		}
	}
	sawSafeResult := false
	for _, ev := range *events {
		if ev.Kind == "tool_result" && ev.ToolName == "run_command" && ev.ToolResp["exitCode"] == float64(0) {
			sawSafeResult = true
		}
	}
	if !sawSafeResult {
		t.Fatal("safe command produced no successful tool result")
	}

	*events = nil
	if err := chat.Send(sess.ID, "推送代码", nil); err != nil {
		t.Fatal(err)
	}
	var pending *ChatStreamEvent
	for i := range *events {
		if (*events)[i].Kind == "confirm_request" {
			pending = &(*events)[i]
			break
		}
	}
	if pending == nil || pending.CallID == "" {
		t.Fatalf("risky command did not ask for approval: %+v", *events)
	}
	if err := chat.ConfirmToolCall(sess.ID, pending.CallID, false); err != nil {
		t.Fatal(err)
	}
}

// TestToolAuditRecordsConfirmation：批准/拒绝决定会连同其调用 ID 记录到 JSONL
// 审计跟踪中。
func TestToolAuditRecordsConfirmation(t *testing.T) {
	turn := 0
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	var callID string
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "write_workspace_file", `"{\"path\":\"audit.txt\",\"content\":\"x\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	chat.SetToolAuditPath(auditPath)
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings.ToolsAudit = true
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { appEventHook = nil })

	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings.ToolsAudit = true
	}); err != nil {
		t.Fatal(err)
	}

	events := []ChatStreamEvent{}
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 audit.txt", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if callID == "" {
		t.Fatal("no confirm_request captured")
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, `"phase":"confirm"`) || !strings.Contains(doc, "approved") {
		t.Fatalf("confirmation decision missing from audit:\n%s", doc)
	}
	_ = workspace
}

// TestExportMarkdownIncludesHITL：Markdown 导出会携带已批准 HITL
// 循环的确认请求/决定轨迹。
func TestExportMarkdownIncludesHITL(t *testing.T) {
	turn := 0
	var callID string
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "write_workspace_file", `"{\"path\":\"hitl-md.txt\",\"content\":\"v\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("好的") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 hitl-md.txt", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}

	path, err := chat.ExportSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "HITL 确认请求") || !strings.Contains(string(data), "批准") {
		t.Fatalf("markdown export missing HITL trail:\n%s", data)
	}
	_ = workspace
}
