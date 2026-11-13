package services

import (
	"encoding/json"
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

// newDirectChat 将 ChatService 接入一个仅带默认供应商（无
// Agent）的假模型服务器，使测试能够执行无目标的直接会话和 @mention 路由。
func newDirectChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store, *[]string) {
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
		cfg.Settings.Terminal = testTerminalSettings()
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_d", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}}, IsDefault: true,
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

// TestDirectChatWithoutAgent 锁定零配置聊天契约：无目标的会话在默认供应商的模型上运行，且绝不修改会话绑定
// 。
func TestDirectChatWithoutAgent(t *testing.T) {
	chat, store, bodies := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("你好！") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "在吗", nil); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], "在吗") {
		t.Fatalf("model did not receive the message: %v", *bodies)
	}
	var sawUser, sawReply bool
	for _, ev := range *events {
		if ev.Kind == "user" && ev.Text == "在吗" {
			sawUser = true
		}
		if ev.Kind == "message" && ev.Author == "助手" && ev.Text == "你好！" {
			sawReply = true
		}
	}
	if !sawUser || !sawReply {
		t.Fatalf("missing stream events: user=%v reply=%v", sawUser, sawReply)
	}
	cfg := store.Get()
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sess.ID && cfg.Sessions[i].TargetType != "" {
			t.Fatalf("direct session target mutated: %+v", cfg.Sessions[i])
		}
	}
}

// TestMentionRoutesTurnToAgent pins @mention routing: a leading "@名称" sends
// that one turn to the named agent while the session binding stays direct,
// and Regenerate re-runs the stored mention with the same routing.
func TestMentionRoutesTurnToAgent(t *testing.T) {
	chat, store, bodies := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("收到") + sseDone))
	})
	if err := store.Update(func(cfg *config.Config) {
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_w", Name: "写手助手", ProviderID: "prov_d", Model: "fake-mini",
		})
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "@写手助手 帮我起草周报", nil); err != nil {
		t.Fatal(err)
	}
	author := ""
	for _, ev := range *events {
		if ev.Kind == "message" {
			author = ev.Author
		}
	}
	if author != "写手助手" {
		t.Fatalf("mention did not route the turn, author=%q", author)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], "帮我起草周报") {
		t.Fatalf("mentioned agent missing the message text: %v", *bodies)
	}
	cfg := store.Get()
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sess.ID && (cfg.Sessions[i].TargetType != "" || cfg.Sessions[i].TargetID != "") {
			t.Fatalf("mention mutated the session binding: %+v", cfg.Sessions[i])
		}
	}

	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatal(err)
	}
	author = ""
	for _, ev := range *events {
		if ev.Kind == "message" {
			author = ev.Author
		}
	}
	if author != "写手助手" {
		t.Fatalf("regenerate lost the mention routing, author=%q", author)
	}
	if len(*bodies) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(*bodies))
	}
}

// TestMentionUnknownFallsBackToDirect：未匹配任何对象的 @name
// 会将该回合作为普通直接聊天运行，而不是失败。
func TestMentionUnknownFallsBackToDirect(t *testing.T) {
	chat, _, _ := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好的") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "@不存在的人 你好", nil); err != nil {
		t.Fatal(err)
	}
	author := ""
	for _, ev := range *events {
		if ev.Kind == "message" {
			author = ev.Author
		}
	}
	if author != "助手" {
		t.Fatalf("unknown mention should fall back to direct chat, author=%q", author)
	}
}

// TestDirectChatReadsWorkspaceFiles pins the issue #21 contract: a project
// chat with no configured agent must be able to inspect its workspace instead
// of only promising "我来查看当前项目情况". The fake model lists the directory,
// reads README.md and answers from its actual content.
func TestDirectChatReadsWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("cloud 是一个云原生示例项目。"), 0o644); err != nil {
		t.Fatal(err)
	}
	turn := 0
	chat, _, bodies := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseText("我来查看当前项目情况。") +
				sseToolCall("call_1", "list_workspace_files", `"{}"`) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseToolCall("call_2", "read_workspace_file", `"{\"path\":\"README.md\"}"`) + sseDone))
		case 3:
			_, _ = w.Write([]byte(sseText("cloud 是一个云原生示例项目。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.NewSessionWithWorkspace("", "", "", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "当前项目主要是做什么的？", nil); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 3 {
		t.Fatalf("expected 3 model calls (list, read, answer), got %d", len(*bodies))
	}

	// 直接 Agent 必须公布工作区读取工具，并告知模型它可以检查哪个目录。
	first := (*bodies)[0]
	if !strings.Contains(first, `"name":"list_workspace_files"`) || !strings.Contains(first, `"name":"read_workspace_file"`) {
		t.Fatalf("direct chat request is missing workspace tools: %s", first)
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(first), &req); err != nil {
		t.Fatal(err)
	}
	// 模型需要真实的参数 schema
	// 才能正确调用这些工具；将它们声明为无参数对象（issue #23 的症状）会使
	// read_workspace_file 无法使用。
	if !strings.Contains(first, `"properties":{"path"`) || !strings.Contains(first, `"required":["path","content"]`) {
		t.Fatalf("direct chat tools lost their parameter schema: %s", first)
	}
	sys := ""
	for _, m := range req.Messages {
		if m.Role == "system" {
			if s, ok := m.Content.(string); ok {
				sys = s
			}
		}
	}
	if !strings.Contains(sys, canonicalDir(t, root)) {
		t.Fatalf("system prompt does not mention the workspace %q: %q", root, sys)
	}

	// 工具结果会流式回传到下一次模型调用：先是目录列表，然后是文件内容。
	if !strings.Contains((*bodies)[1], "README.md") {
		t.Fatalf("listing result missing from second model call: %s", (*bodies)[1])
	}
	if !strings.Contains((*bodies)[2], "cloud 是一个云原生示例项目。") {
		t.Fatalf("file content missing from third model call: %s", (*bodies)[2])
	}

	var sawListCall, sawListResult, sawReadCall, sawFinal bool
	var listCallID, listResultID string
	for _, ev := range *events {
		switch {
		case ev.Kind == "tool_call" && ev.ToolName == "list_workspace_files":
			sawListCall = true
			listCallID = ev.CallID
		case ev.Kind == "tool_result" && ev.ToolName == "list_workspace_files":
			sawListResult = true
			listResultID = ev.CallID
		case ev.Kind == "tool_call" && ev.ToolName == "read_workspace_file":
			sawReadCall = true
		case ev.Kind == "message" && ev.Text == "cloud 是一个云原生示例项目。":
			sawFinal = true
		}
	}
	if !sawListCall || !sawListResult || !sawReadCall || !sawFinal {
		t.Fatalf("missing tool/answer stream events: list=%v result=%v read=%v final=%v",
			sawListCall, sawListResult, sawReadCall, sawFinal)
	}
	if listCallID == "" || listCallID != listResultID {
		t.Fatalf("tool call/result must share the ADK CallID: call=%q result=%q", listCallID, listResultID)
	}
	history, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var historyCallID, historyResultID string
	for _, m := range history {
		switch {
		case m.Kind == "tool_call" && m.ToolName == "list_workspace_files":
			historyCallID = m.CallID
		case m.Kind == "tool_result" && m.ToolName == "list_workspace_files":
			historyResultID = m.CallID
		}
	}
	if historyCallID == "" || historyCallID != historyResultID {
		t.Fatalf("history must preserve the shared CallID: call=%q result=%q", historyCallID, historyResultID)
	}
}

// TestDirectChatWriteAsksConfirmation：直接聊天也会获得文件工具，但写入仍受
// HITL 确认契约约束（与已配置 Agent 相同）——在用户批准前不会写入任何内容。
func TestDirectChatWriteAsksConfirmation(t *testing.T) {
	root := t.TempDir()
	chat, _, _ := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file",
			`"{\"path\":\"notes/new.txt\",\"content\":\"hello\"}"`) + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.NewSessionWithWorkspace("", "", "", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写个文件", nil); err != nil {
		t.Fatal(err)
	}
	var req *ChatStreamEvent
	for i := range *events {
		if (*events)[i].Kind == "confirm_request" {
			req = &(*events)[i]
		}
	}
	if req == nil || req.ToolName != "write_workspace_file" || req.CallID == "" {
		t.Fatalf("expected a write confirmation request, got: %+v", *events)
	}
	if _, err := os.Stat(filepath.Join(root, "notes", "new.txt")); !os.IsNotExist(err) {
		t.Fatal("file must not be written before the user approves")
	}
}

// TestDirectChatAdvertisesShellTool 锁定 issue #40
// 的回归：无目标聊天背后的隐式默认 Agent 必须挂载
// run_command，并告知模型其具备终端能力，而不只是工作区文件工具。
func TestDirectChatAdvertisesShellTool(t *testing.T) {
	chat, _, bodies := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好的") + sseDone))
	})
	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "拉取最新代码", nil); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(*bodies))
	}
	first := (*bodies)[0]
	if !strings.Contains(first, `"name":"run_command"`) {
		t.Fatalf("direct chat request is missing run_command: %s", first)
	}
	if !strings.Contains(first, "run_command") || !strings.Contains(first, "终端") {
		t.Fatalf("system prompt does not advertise terminal capability: %s", first)
	}
	for _, want := range []string{`"name":"plan_todos"`, "互不依赖", "当前进展", "默认保持只读"} {
		if !strings.Contains(first, want) {
			t.Fatalf("direct chat prompt/tools missing %q: %s", want, first)
		}
	}
}

// TestDirectChatShellAsksConfirmation：直接会话通过与新 Agent 相同的 HITL
// 门控获得终端工具——在用户批准之前不会运行任何内容，随后命令会在会话工作区中执
// 行。
func TestDirectChatShellAsksConfirmation(t *testing.T) {
	if !sandbox.Available() {
		t.Skipf("OS sandbox unavailable: %s", sandbox.BackendStatus().Error)
	}
	root := t.TempDir()
	turn := 0
	chat, _, bodies := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "run_command",
				`"{\"command\":\"echo hi > shell-marker.txt\"}"`) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseText("代码已拉取。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.NewSessionWithWorkspace("", "", "", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "拉取最新代码", nil); err != nil {
		t.Fatal(err)
	}
	var req *ChatStreamEvent
	for i := range *events {
		if (*events)[i].Kind == "confirm_request" {
			req = &(*events)[i]
		}
	}
	if req == nil || req.ToolName != "run_command" || req.CallID == "" {
		t.Fatalf("expected a shell confirmation request, got: %+v", *events)
	}
	if req.ToolArgs["command"] != "echo hi > shell-marker.txt" {
		t.Fatalf("confirm_request should surface the command: %+v", req)
	}
	if _, err := os.Stat(filepath.Join(root, "shell-marker.txt")); !os.IsNotExist(err) {
		t.Fatal("command must not run before the user approves")
	}
	if err := chat.ConfirmToolCall(sess.ID, req.CallID, true); err != nil {
		t.Fatal(err)
	}
	// 恢复后的模型调用必须携带已执行的结果（exitCode 0），而不是 ADK
	// 对模型隐藏的仅确认往返。
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], `\"exitCode\":0`) {
		t.Fatalf("resume request missing successful shell result: %v", *bodies)
	}
	if data, err := os.ReadFile(filepath.Join(root, "shell-marker.txt")); err != nil || strings.TrimSpace(string(data)) != "hi" {
		t.Fatalf("expected approved command output, err=%v data=%q", err, data)
	}
}

// TestDirectChatWithoutProviderFails：在未配置供应商时，该回合会被拒绝，并给出
// 可操作的提示消息，而不是泛化错误。
func TestDirectChatWithoutProviderFails(t *testing.T) {
	chat, store, _ := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("model must not be called without a provider")
	})
	if err := store.Update(func(cfg *config.Config) { cfg.Providers = nil }); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = chat.Send(sess.ID, "你好", nil)
	if err == nil || !strings.Contains(err.Error(), "模型配置") {
		t.Fatalf("expected provider guidance error, got %v", err)
	}
}
