package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// slowTurnServer 将首个回合以多个延迟块流式传输（测试对其进行探查时，该运行确实仍在执行中），
// 并对之后的任何回合都用单条快速的替换回复作答。
func slowTurnServer(t *testing.T, fastReply string, firstDelta chan struct{}) *httptest.Server {
	t.Helper()
	turn := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn > 1 {
			_, _ = w.Write([]byte(sseText(fastReply) + sseDone))
			return
		}
		opened := false
		for i, chunk := range []string{"甲", "乙", "丙", "丁"} {
			if i == 1 && !opened {
				opened = true
				close(firstDelta)
			}
			_, _ = w.Write([]byte(sseText(chunk)))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(150 * time.Millisecond)
		}
		_, _ = w.Write([]byte(sseDone))
	}))
}

// newTrimRaceChat 将一个单 Agent 的存储 + 聊天服务接入 srv。
func newTrimRaceChat(t *testing.T, srv *httptest.Server) (*ChatService, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "pv", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "agent_1", Name: "甲", ProviderID: "pv", Model: "m"})
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
	return chat, sess.ID
}

// TestRegenerateWaitsForOldRun 锁定了以下不变量：Regenerate
// 必须取消正在执行的回合，并且必须等待其完全收尾后才能裁剪历史记录。若旧运行在
// 裁剪之后仍持续流式传输，就会使已删除的事件重新出现（旧回复文本会出现在重新生
// 成的时间线中）。
func TestRegenerateWaitsForOldRun(t *testing.T) {
	firstDelta := make(chan struct{})
	srv := slowTurnServer(t, "重答内容：精简版答案", firstDelta)
	defer srv.Close()
	chat, sessID := newTrimRaceChat(t, srv)

	oldDone := make(chan error, 1)
	go func() { oldDone <- chat.Send(sessID, "第一问", nil) }()
	<-firstDelta

	if err := chat.Regenerate(sessID); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if err := <-oldDone; err == nil {
		t.Log("old turn finished with nil error (it had already completed)")
	}

	if chat.IsRunning(sessID) {
		t.Fatal("session still running after Regenerate returned")
	}
	msgs, err := chat.GetSessionMessages(sessID)
	if err != nil {
		t.Fatal(err)
	}
	var users, assistants int
	var answer strings.Builder
	for _, m := range msgs {
		switch m.Kind {
		case "user":
			users++
		case "assistant":
			assistants++
			answer.WriteString(m.Text)
		}
	}
	if users != 1 || assistants != 1 {
		t.Fatalf("regenerated timeline must hold exactly one user turn and one reply, got users=%d assistants=%d: %+v", users, assistants, msgs)
	}
	if !strings.Contains(answer.String(), "重答内容") {
		t.Fatalf("new reply missing: %q", answer.String())
	}
	if strings.Contains(answer.String(), "甲乙丙丁") {
		t.Fatal("trimmed old turn resurrected after Regenerate")
	}
}

// TestDeleteSessionWaitsForOldRun 为 DeleteSession 锁定了同一契约：在
// DeleteSession 返回之后，正在执行的运行必须已经结束，此后不得再为已删除的会话写入任何事件行
// 。
func TestDeleteSessionWaitsForOldRun(t *testing.T) {
	firstDelta := make(chan struct{})
	srv := slowTurnServer(t, "无关", firstDelta)
	defer srv.Close()
	chat, sessID := newTrimRaceChat(t, srv)
	db := chat.db

	oldDone := make(chan error, 1)
	go func() { oldDone <- chat.Send(sessID, "第一问", nil) }()
	<-firstDelta

	if err := chat.DeleteSession(sessID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if err := <-oldDone; err == nil {
		t.Log("old turn finished with nil error (it had already completed)")
	}

	var n int64
	if err := db.Raw("SELECT count(*) FROM events WHERE session_id = ?", sessID).Row().Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("deleted session has %d event rows (orphaned or resurrected)", n)
	}
}

// TestEditAndResendWaitsForOldRun 锁定了第三个会改动历史的入口点：编辑后的回合会替换旧回合，且旧文本不会重新混
// 入。
func TestEditAndResendWaitsForOldRun(t *testing.T) {
	firstDelta := make(chan struct{})
	srv := slowTurnServer(t, "编辑后的答案", firstDelta)
	defer srv.Close()
	chat, sessID := newTrimRaceChat(t, srv)

	oldDone := make(chan error, 1)
	go func() { oldDone <- chat.Send(sessID, "原始问题", nil) }()
	<-firstDelta
	if err := chat.EditAndResend(sessID, "修改后的问题"); err != nil {
		t.Fatalf("edit and resend: %v", err)
	}
	<-oldDone

	msgs, err := chat.GetSessionMessages(sessID)
	if err != nil {
		t.Fatal(err)
	}
	var userText, assistantText strings.Builder
	for _, m := range msgs {
		switch m.Kind {
		case "user":
			userText.WriteString(m.Text)
		case "assistant":
			assistantText.WriteString(m.Text)
		}
	}
	if !strings.Contains(userText.String(), "修改后的问题") || strings.Contains(userText.String(), "原始问题") {
		t.Fatalf("edited turn not cleanly replaced, user text=%q", userText.String())
	}
	if !strings.Contains(assistantText.String(), "编辑后的答案") || strings.Contains(assistantText.String(), "甲乙丙丁") {
		t.Fatalf("edited turn leaked old reply: %q", assistantText.String())
	}
}
