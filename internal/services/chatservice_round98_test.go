package services

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// newRecordingChat 是一个与 newConfirmTestChat
// 类似的测试夹具，其假模型服务器会记录每个请求体，因此测试可以断言模型在各回合
// 实际接收到的内容。
func newRecordingChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, readAll(r))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_r", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_r", Name: "助手", ProviderID: "prov_r", Model: "fake-mini",
		})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewChatService(svc, sessions, db), store, &bodies
}

// TestRegeneratePreservesAttachments 锁定 Regenerate
// 的契约：重新运行必须携带原始回合的图像附件，而不只是其文本。
func TestRegeneratePreservesAttachments(t *testing.T) {
	turn := 0
	chat, _, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		_, _ = w.Write([]byte(sseText("第"+string(rune('0'+turn))+"次回答") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	atts := []AttachmentIn{{Name: "shot.png", MIME: "image/png", Data: base64.StdEncoding.EncodeToString(png)}}
	if err := chat.Send(sess.ID, "看看这张图", atts); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], base64.StdEncoding.EncodeToString(png)) {
		t.Fatalf("first send missing image payload")
	}
	// 用户流式事件必须携带该附件，以便乐观 UI
	// 无需等待历史记录重新加载即可渲染图像。
	var sawAtt bool
	for _, ev := range *events {
		if ev.Kind == "user" && len(ev.Attachments) == 1 && strings.Contains(ev.Attachments[0].DataURL, base64.StdEncoding.EncodeToString(png)) {
			sawAtt = true
		}
	}
	if !sawAtt {
		t.Fatal("user stream event missing attachment")
	}

	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if len(*bodies) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(*bodies))
	}
	if !strings.Contains((*bodies)[1], base64.StdEncoding.EncodeToString(png)) {
		t.Fatal("regenerated turn lost the image attachment")
	}
	if !strings.Contains((*bodies)[1], "看看这张图") {
		t.Fatal("regenerated turn lost the message text")
	}
	// 历史记录必须再次渲染该附件（恰好一次）。
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	imgs := 0
	for _, m := range msgs {
		if m.Kind == "user" {
			imgs += len(m.Attachments)
		}
	}
	if imgs != 1 {
		t.Fatalf("expected 1 attachment in history, got %d: %+v", imgs, msgs)
	}
}

// TestEditAndResendKeepsAttachments：编辑文本时不得静默丢弃原始回合的图像。
func TestEditAndResendKeepsAttachments(t *testing.T) {
	chat, _, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("回答") + sseDone))
	})
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G'}
	atts := []AttachmentIn{{Name: "a.png", MIME: "image/png", Data: base64.StdEncoding.EncodeToString(png)}}
	if err := chat.Send(sess.ID, "原始问题", atts); err != nil {
		t.Fatal(err)
	}
	if err := chat.EditAndResend(sess.ID, "改后的问题"); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(*bodies))
	}
	if !strings.Contains((*bodies)[1], base64.StdEncoding.EncodeToString(png)) {
		t.Fatal("edited resend lost the image attachment")
	}
	if !strings.Contains((*bodies)[1], "改后的问题") {
		t.Fatal("edited resend missing the new text")
	}
}

// TestRegenerateAfterHitlConfirm 是裁剪边界的回归测试：HITL 恢复事件曾以用户
// 作者存储（一个 FunctionResponse 事件），并吞掉真正的用户消息，导致之后每次
// Regenerate 都报「没有可重新生成的用户消息」。
func TestRegenerateAfterHitlConfirm(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"re.txt\",\"content\":\"v\"}"`) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseText("已确认写入。") + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("重新生成的回复") + sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 re.txt", nil); err != nil {
		t.Fatal(err)
	}
	var callID string
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if callID == "" {
		t.Fatal("expected confirm_request")
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "re.txt")); err != nil {
		t.Fatal(err)
	}

	// Regenerate 必须正常工作：整个确认轨迹会被裁剪，且原始用户消息会被重新发送。
	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatalf("regenerate after HITL confirm: %v", err)
	}
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Text != "写 re.txt" || msgs[1].Text != "重新生成的回复" {
		t.Fatalf("post-HITL regenerate history = %+v", msgs)
	}
	for _, m := range msgs {
		if m.Kind == "confirm_request" || m.Kind == "confirm_result" || m.Kind == "tool_result" {
			t.Fatalf("confirm trail not trimmed: %+v", m)
		}
	}
}

// TestExportHTMLIncludesHITLAndNames 锁定 HTML 导出的对等性：HITL
// 决策、解析后的目标名称和元数据行都会出现，就像在 Markdown 导出中一样。
func TestExportHTMLIncludesHITLAndNames(t *testing.T) {
	turn := 0
	var callID string
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "write_workspace_file", `"{\"path\":\"h.html\",\"content\":\"v\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("好") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 h.html", nil); err != nil {
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

	path, err := chat.ExportSessionHTML(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{"HITL 确认", "HITL 决策", "批准", "创建时间", "写手"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("HTML export missing %q", want)
		}
	}
}

// TestDeleteSessionCancelsRunningTurn：删除会话必须中止其进行中的运行，而不是让
// 它一直消耗 token 直到超时。
func TestDeleteSessionCancelsRunningTurn(t *testing.T) {
	chat, _, _ := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 保持流式连接打开，直到客户端离开（取消），而不是固定休眠——t.Cleanup
		// 会等待处理程序结束。
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
		_, _ = w.Write([]byte(sseText("太迟了") + sseDone))
	})
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- chat.Send(sess.ID, "慢慢回答", nil) }()

	deadline := time.Now().Add(5 * time.Second)
	for !chat.IsRunning(sess.ID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !chat.IsRunning(sess.ID) {
		t.Fatal("turn never started")
	}
	start := time.Now()
	if err := chat.DeleteSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the cancelled send to report an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("send did not return after session delete")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("delete blocked %v", elapsed)
	}
	if chat.IsRunning(sess.ID) {
		t.Fatal("run still registered after delete")
	}
}

// TestTurnUsageAccumulatesAcrossToolLoop：一个回合进行多次模型调用时，会在最终
// 消息事件中报告这些调用用量之和，而不只是最后一次调用的数字。
func TestTurnUsageAccumulatesAcrossToolLoop(t *testing.T) {
	turn := 0
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			// 工具调用轮次：10 个提示词 + 4 个补全（夹具用量由下方的 sseDone
			// 风格用量数据块追加）。
			_, _ = w.Write([]byte(sseToolCall("c1", "write_workspace_file", `"{\"path\":\"u.txt\",\"content\":\"v\"}"`) +
				`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}` + "\n\n" + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("done") +
			`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":140}}` + "\n\n" + sseDone))
	})
	events := hookEvents(t)

	if err := chat.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Agents {
			cfg.Agents[i].ConfirmTools = nil // 无 HITL：直接执行
		}
	}); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 u.txt", nil); err != nil {
		t.Fatal(err)
	}
	var msgUsage *UsageInfo
	var doneUsage *UsageInfo
	for i := range *events {
		ev := (*events)[i]
		if ev.Kind == "message" && ev.Usage != nil {
			msgUsage = ev.Usage
		}
		if ev.Kind == "done" && ev.SessionUsage != nil {
			doneUsage = ev.SessionUsage
		}
	}
	if msgUsage == nil {
		t.Fatal("final message carried no usage")
	}
	if msgUsage.PromptTokens != 110 || msgUsage.CompletionTokens != 44 || msgUsage.TotalTokens != 154 {
		t.Fatalf("usage not accumulated: %+v", msgUsage)
	}
	// ContextTokens 是当前上下文大小——最后一次调用的提示词，而不是累计总和（后者会重复计算共享历
	// 史前缀）。
	if doneUsage == nil {
		t.Fatal("done carried no session usage")
	}
	if doneUsage.ContextTokens != 100 {
		t.Fatalf("done context tokens = %d, want the last call's prompt 100", doneUsage.ContextTokens)
	}
	if doneUsage.TotalTokens != 154 {
		t.Fatalf("done total tokens = %d, want cumulative 154", doneUsage.TotalTokens)
	}
	st, err := chat.SessionStats(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.ContextTokens != 100 {
		t.Fatalf("stats context tokens = %d, want the last call's prompt 100", st.ContextTokens)
	}
	if st.TokensTotal != 154 {
		t.Fatalf("stats total tokens = %d, want cumulative 154", st.TokensTotal)
	}
}

// TestTurnUsageCarriesCachedTokens：供应商的提示词缓存计数器会传递到
// UI。CachedTokens
// 是当前上下文（最后一次调用）的缓存份额，因此两次命中缓存的工具循环不得报告它
// 们的总和——否则气泡会声称缓存大于提示词。
func TestTurnUsageCarriesCachedTokens(t *testing.T) {
	turn := 0
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "write_workspace_file", `"{\"path\":\"u.txt\",\"content\":\"v\"}"`) +
				`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":8}}}` + "\n\n" + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("done") +
			`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":40,"total_tokens":140,"prompt_tokens_details":{"cached_tokens":90}}}` + "\n\n" + sseDone))
	})
	events := hookEvents(t)

	if err := chat.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Agents {
			cfg.Agents[i].ConfirmTools = nil // 无 HITL：直接执行
		}
	}); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 u.txt", nil); err != nil {
		t.Fatal(err)
	}
	var msgUsage *UsageInfo
	var doneUsage *UsageInfo
	for i := range *events {
		ev := (*events)[i]
		if ev.Kind == "message" && ev.Usage != nil {
			msgUsage = ev.Usage
		}
		if ev.Kind == "done" && ev.SessionUsage != nil {
			doneUsage = ev.SessionUsage
		}
	}
	if msgUsage == nil || doneUsage == nil {
		t.Fatal("missing usage on message/done events")
	}
	// 提示词/补全仍会累计（计费），但缓存份额不会。
	if msgUsage.PromptTokens != 110 || msgUsage.CompletionTokens != 44 {
		t.Fatalf("billing usage not accumulated: %+v", msgUsage)
	}
	if msgUsage.CachedTokens != 90 {
		t.Fatalf("cached tokens = %d, want the last call's 90 (not the 8+90 sum)", msgUsage.CachedTokens)
	}
	if doneUsage.CachedTokens != 90 {
		t.Fatalf("done cached tokens = %d, want 90", doneUsage.CachedTokens)
	}
	st, err := chat.SessionStats(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.CachedTokens != 90 {
		t.Fatalf("stats cached tokens = %d, want 90", st.CachedTokens)
	}
	if st.TokensTotal != 154 {
		t.Fatalf("stats total tokens = %d, want cumulative 154", st.TokensTotal)
	}
}

// TestChatPreviewStripsControlChars: 模型输出可能携带 C0 控制字节（例如
// U+0001）；侧边栏预览不得将其显示出来。
func TestChatPreviewStripsControlChars(t *testing.T) {
	chat, _, _ := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// \u0001 位于 JSON 字符串内部（原始 C0 字节会导致 JSON
		// 无效）：解析后，回复携带真实的 U+0001。
		_, _ = w.Write([]byte(sseText("代码已拉取，当前分支已是最新\\u0001（already up to date）") + sseDone))
	})
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "拉取代码", nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range chat.S.Store.Get().Sessions {
		if s.ID == sess.ID {
			if strings.ContainsAny(s.Preview, "\x01\x02\x1b\x7f") {
				t.Fatalf("preview contains control chars: %q", s.Preview)
			}
			return
		}
	}
	t.Fatal("session not found")
}

// TestChatPreviewTruncatedByRune: 侧边栏预览绝不拆分 UTF-8 rune（80 个 rune
// 边界处的多字节中文文本）。
func TestChatPreviewTruncatedByRune(t *testing.T) {
	chat, _, _ := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText(strings.Repeat("汉", 120)) + sseDone))
	})
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "长回复", nil); err != nil {
		t.Fatal(err)
	}
	var preview string
	for _, s := range chat.S.Store.Get().Sessions {
		if s.ID == sess.ID {
			preview = s.Preview
		}
	}
	if got := len([]rune(preview)); got != 80 {
		t.Fatalf("preview runes = %d, want 80", got)
	}
}

// TestRenameSessionAuditOnlyOnSuccess 保持生命周期审计的真实性：重命名未知会话会失败，并且不得留下 "renamed" 记录。
func TestRenameSessionAuditOnlyOnSuccess(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	var reply atomic.Value
	reply.Store("ok")
	chat, sess, store := newChatFixture(t, &reply)
	chat.SetToolAuditPath(auditPath)
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}

	if err := chat.RenameSession(sess.ID, "新名字"); err != nil {
		t.Fatal(err)
	}
	if err := chat.RenameSession("sess_missing", "不该成功"); err == nil {
		t.Fatal("expected error renaming unknown session")
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"phase":"session_renamed"`) != 1 {
		t.Fatalf("expected exactly one renamed audit record:\n%s", data)
	}
}

// TestSearchSessionsEscapesLikeWildcards 校验：搜索「符%」必须只匹配字面文本——
// 未转义的 %/_ 曾把内容搜索变成模糊子串匹配。
func TestSearchSessionsEscapesLikeWildcards(t *testing.T) {
	var reply atomic.Value
	reply.Store("ok")
	chat, sess1, _ := newChatFixture(t, &reply)

	sess2, err := chat.newLegacySession("agent", "agent_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess1.ID, "包含符%号的内容", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess2.ID, "包含符号的内容", nil); err != nil {
		t.Fatal(err)
	}

	matched := chat.SearchSessions("符%")
	if len(matched) != 1 || matched[0].ID != sess1.ID {
		for _, m := range matched {
			t.Logf("matched: %s %s", m.ID, m.Title)
		}
		t.Fatalf("wildcard not escaped: got %d matches", len(matched))
	}
}

// TestSessionModelOverride: 聊天头部模型选择器将会话固定到 Agent
// 的供应商下的另一个模型——请求携带覆盖后的模型 ID，统计定价也随之变化。
func TestSessionModelOverride(t *testing.T) {
	chat, store, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好") + sseDone))
	})
	hookEvents(t)

	// 选择器仅提供供应商的模型列表——添加覆盖目标。
	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == "prov_r" {
				cfg.Providers[i].Models = append(cfg.Providers[i].Models, config.ModelInfo{ID: "fake-large"})
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "默认模型", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionModel(sess.ID, "fake-large"); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "覆盖后", nil); err != nil {
		t.Fatal(err)
	}

	if len(*bodies) != 2 {
		t.Fatalf("model calls = %d", len(*bodies))
	}
	if !strings.Contains((*bodies)[0], `"model":"fake-mini"`) || !strings.Contains((*bodies)[1], `"model":"fake-large"`) {
		t.Fatalf("override not applied: %q", *bodies)
	}
	// 清除后会恢复 Agent 默认值。
	if err := chat.SetSessionModel(sess.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "恢复默认", nil); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 3 || !strings.Contains((*bodies)[2], `"model":"fake-mini"`) {
		t.Fatalf("override not cleared: %q", *bodies)
	}
}

// TestSetSessionModelUnknownSession: 选择器的 RPC 必须拒绝未知会话
// ID，而不是静默成功（与 RenameSession 的契约相同）。
func TestSetSessionModelUnknownSession(t *testing.T) {
	var reply atomic.Value
	reply.Store("ok")
	chat, _, _ := newChatFixture(t, &reply)
	if err := chat.SetSessionModel("sess_missing", "fake-large"); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

// TestRenderTodoListEscapes: todo 内容会回显模型控制的工具参数——HTML
// 导出必须转义它们（存储型 XSS 向量），而 Markdown 导出保留原始文本。
func TestRenderTodoListEscapes(t *testing.T) {
	resp := map[string]any{"todos": []any{
		map[string]any{"content": `<img src=x onerror=alert(1)>`, "status": "pending"},
	}}
	html := renderTodoList(resp, true)
	if strings.Contains(html, "<img") || !strings.Contains(html, "&lt;img") {
		t.Fatalf("HTML export not escaped: %q", html)
	}
	md := renderTodoList(resp, false)
	if !strings.Contains(md, "<img") {
		t.Fatalf("markdown export should keep raw text: %q", md)
	}
}
