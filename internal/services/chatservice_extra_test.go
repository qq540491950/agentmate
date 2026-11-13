package services

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"agentmate/internal/memory"
	adkmemory "google.golang.org/adk/v2/memory"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// newChatFixture 会启动一个伪造的 OpenAI SSE
// 服务器，其回复由调用方控制，还会启动一个由临时 SQLite 数据库支持的聊天服务。
func newChatFixture(t *testing.T, reply *atomic.Value) (*ChatService, config.ChatSession, *config.Store) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		text, _ := reply.Load().(string)
		if text == "" {
			text = "first-reply"
		}
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"` + text + `"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = filepath.Join(dir, "workspace")
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_1", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_1", Name: "助手", ProviderID: "prov_1", Model: "fake-mini",
		})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.newLegacySession("agent", "agent_1", "")
	if err != nil {
		t.Fatal(err)
	}
	return chat, sess, store
}

func TestRegenerateTrimsAndReruns(t *testing.T) {
	var reply atomic.Value
	reply.Store("first-reply")
	chat, sess, _ := newChatFixture(t, &reply)

	if err := chat.Send(sess.ID, "你好", nil); err != nil {
		t.Fatal(err)
	}
	msgs, _ := chat.GetSessionMessages(sess.ID)
	if len(msgs) != 2 || msgs[1].Text != "first-reply" {
		t.Fatalf("pre-regenerate history = %+v", msgs)
	}

	reply.Store("second-reply")
	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatal(err)
	}
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after regenerate, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Kind != "user" || msgs[0].Text != "你好" {
		t.Fatalf("user message lost: %+v", msgs[0])
	}
	if msgs[1].Kind != "assistant" || msgs[1].Text != "second-reply" {
		t.Fatalf("expected regenerated reply, got %+v", msgs[1])
	}
}

func TestRegenerateWithoutUserMessage(t *testing.T) {
	var reply atomic.Value
	chat, sess, _ := newChatFixture(t, &reply)
	if err := chat.Regenerate(sess.ID); err == nil {
		t.Fatal("expected error regenerating empty session")
	}
}

func TestExportSessionWritesMarkdown(t *testing.T) {
	var reply atomic.Value
	reply.Store("导出测试回复 **加粗**")
	chat, sess, store := newChatFixture(t, &reply)
	if err := chat.Send(sess.ID, "导出我这段话", nil); err != nil {
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
	content := string(data)
	for _, want := range []string{"# 导出我这段话", "## 🧑 用户", "导出我这段话", "## 🤖", "导出测试回复"} {
		if !strings.Contains(content, want) {
			t.Errorf("export missing %q\n%s", want, content)
		}
	}
	if !strings.Contains(path, filepath.Join(store.Get().Settings.WorkspaceDir, "exports")) {
		t.Errorf("export path outside workspace exports dir: %s", path)
	}
	if !strings.HasSuffix(path, ".md") {
		t.Errorf("export should be .md, got %s", path)
	}
	_ = context.Background
}

// TestShortIDAndExportRejectShortIDs 锁定该边界修复：导出文件名会将会话 ID
// 截取为 6 个字符，对短于该长度的 ID 会触发 panic。这些方法可由 JS
// 以任意输入调用。
func TestShortIDAndExportRejectShortIDs(t *testing.T) {
	if got := shortID("sess_0123456789"); got != "456789" {
		t.Fatalf("shortID long id = %q", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Fatalf("shortID short id = %q", got)
	}
	if got := shortID(""); got != "" {
		t.Fatalf("shortID empty = %q", got)
	}

	var reply atomic.Value
	reply.Store("x")
	chat, _, _ := newChatFixture(t, &reply)
	for _, id := range []string{"", "ab", "sess"} {
		if _, err := chat.ExportSessionHTML(id); err == nil {
			t.Fatalf("ExportSessionHTML(%q) should fail for unknown session", id)
		}
	}
}

func TestSendAttachesUsageToEvents(t *testing.T) {
	var reply atomic.Value
	reply.Store("usage-reply")
	chat, sess, _ := newChatFixture(t, &reply)

	var events []ChatStreamEvent
	origApp := appEventHook
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = origApp })

	if err := chat.Send(sess.ID, "统计一下", nil); err != nil {
		t.Fatal(err)
	}
	var msgUsage, doneUsage *UsageInfo
	for _, ev := range events {
		if ev.Kind == "message" {
			msgUsage = ev.Usage
		}
		if ev.Kind == "done" {
			doneUsage = ev.SessionUsage
		}
	}
	if msgUsage == nil || msgUsage.TotalTokens != 14 {
		t.Fatalf("message usage = %+v", msgUsage)
	}
	if doneUsage == nil || doneUsage.TotalTokens != 14 || doneUsage.PromptTokens != 10 {
		t.Fatalf("done session usage = %+v", doneUsage)
	}
}

func TestEditAndResendReplacesLastTurn(t *testing.T) {
	var reply atomic.Value
	reply.Store("first-reply")
	chat, sess, _ := newChatFixture(t, &reply)
	if err := chat.Send(sess.ID, "原始问题", nil); err != nil {
		t.Fatal(err)
	}
	reply.Store("edited-reply")
	if err := chat.EditAndResend(sess.ID, "修改后的问题"); err != nil {
		t.Fatal(err)
	}
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages after edit+resend, got %d", len(msgs))
	}
	if msgs[0].Kind != "user" || msgs[0].Text != "修改后的问题" {
		t.Fatalf("user message = %+v", msgs[0])
	}
	if msgs[1].Text != "edited-reply" {
		t.Fatalf("assistant reply = %+v", msgs[1])
	}
	if err := chat.EditAndResend(sess.ID, "   "); err == nil {
		t.Fatal("expected error for blank message")
	}
}

func TestSendWithImageAttachment(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAllBody(r)
		captured = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"图里是一只猫。"}}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_1", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "vision"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "看图", ProviderID: "prov_1", Model: "vision"})
	})
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChatService(&Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}, sessions, db)
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}

	png := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'})
	err = chat.Send(sess.ID, "这张图里是什么？", []AttachmentIn{{Name: "cat.png", MIME: "image/png", Data: png}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(captured, "data:image/png;base64,") || !strings.Contains(captured, base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'})) {
		t.Fatal("image payload missing from provider request")
	}

	// 历史记录会往返传递该附件以用于渲染。
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var atts []AttachmentOut
	userMessages := 0
	for _, m := range msgs {
		if m.Kind == "user" {
			userMessages++
			if m.Text != "这张图里是什么？" {
				t.Fatalf("user history lost its text: %+v", m)
			}
		}
		atts = append(atts, m.Attachments...)
	}
	if userMessages != 1 {
		t.Fatalf("text + image should render as one user turn, got %d: %+v", userMessages, msgs)
	}
	if len(atts) != 1 || atts[0].MIME != "image/png" || !strings.HasPrefix(atts[0].DataURL, "data:image/png;base64,") {
		t.Fatalf("history attachments = %+v", atts)
	}

	// 被拒绝的类型会干净地失败。
	err = chat.Send(sess.ID, "再看看", []AttachmentIn{{Name: "x.exe", MIME: "application/exe", Data: "AAAA"}})
	if err == nil || !strings.Contains(err.Error(), "不支持的附件类型") {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func readAllBody(r *http.Request) string {
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 2048)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

func TestSessionStatsAggregation(t *testing.T) {
	var reply atomic.Value
	reply.Store("统计回复")
	chat, sess, _ := newChatFixture(t, &reply)
	if err := chat.Send(sess.ID, "第一句", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "第二句", nil); err != nil {
		t.Fatal(err)
	}
	st, err := chat.SessionStats(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Messages != 2 {
		t.Errorf("messages = %d, want 2", st.Messages)
	}
	if st.TokensTotal != 28 || st.TokensIn != 20 || st.TokensOut != 8 {
		t.Errorf("tokens = %d/%d/%d, want 20/8/28", st.TokensIn, st.TokensOut, st.TokensTotal)
	}
	if st.FirstAt == nil || st.LastAt == nil {
		t.Fatal("timestamps missing")
	}
	if st.AssistantTurns < 2 {
		t.Errorf("assistantTurns = %d, want >= 2", st.AssistantTurns)
	}
}

func TestGuardrailRedactsUserInput(t *testing.T) {
	var reply atomic.Value
	reply.Store("好的")
	chat, sess, _ := newChatFixture(t, &reply)

	var events []ChatStreamEvent
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = nil })

	secret := "sk-abcdefghijklmnop123456"
	if err := chat.Send(sess.ID, "帮我用这个 key："+secret, nil); err != nil {
		t.Fatal(err)
	}
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 存储的历史记录必须保存已脱敏的文本，绝不能保存原始密钥。
	for _, m := range msgs {
		if strings.Contains(m.Text, secret) {
			t.Fatalf("raw secret leaked into history: %+v", m)
		}
	}
	var redactNotice bool
	for _, ev := range events {
		if ev.Kind == "error" && strings.Contains(ev.Text, "已自动遮蔽") {
			redactNotice = true
		}
	}
	if !redactNotice {
		t.Fatalf("expected redaction notice, events: %+v", events)
	}
}

func TestRememberSessionIngestsMemory(t *testing.T) {
	var reply atomic.Value
	reply.Store("好的")
	chat, sess, store := newChatFixture(t, &reply)
	if err := chat.Send(sess.ID, "项目部署在 europe-west4 区", nil); err != nil {
		t.Fatal(err)
	}

	// 记忆服务初始为空。
	db, sessions, err := newTestSessionService(t, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	memSvc, err := memory.NewSQLiteService(db)
	if err != nil {
		t.Fatal(err)
	}
	chat.S.Memory = memSvc
	if _, err := memSvc.Count("agentmate", "local"); err != nil {
		t.Fatal(err)
	}
	_ = sessions

	n, err := chat.RememberSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n <= 0 {
		t.Fatalf("expected memories after ingest, got %d", n)
	}
	// 部署事实必须能够被召回。
	resp, err := memSvc.SearchMemory(context.Background(), &adkmemory.SearchRequest{
		AppName: "agentmate", UserID: "local", Query: "europe-west4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) == 0 {
		t.Fatal("deployment fact not recallable after RememberSession")
	}
	_ = store

	// 在没有记忆服务时，RememberSession 会干净地报错。
	chat2, sess2, _ := newChatFixture(t, &reply)
	chat2.S.Memory = nil
	if _, err := chat2.RememberSession(sess2.ID); err == nil {
		t.Fatal("expected error when memory service unavailable")
	}
}
