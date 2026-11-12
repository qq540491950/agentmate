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

// newDirectChat wires a ChatService against a fake model server with ONLY a
// default provider (no agents), so tests can exercise target-less direct
// sessions and @mention routing.
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

// TestDirectChatWithoutAgent pins the zero-config chat contract: a session
// with no target runs on the default provider's model and never mutates the
// session binding.
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

// TestMentionUnknownFallsBackToDirect: an @name matching nothing runs the
// turn as a plain direct chat instead of failing.
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

	// The direct agent must advertise the workspace read tools and tell the
	// model which directory it may inspect.
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
	// The model needs the real argument schema to call the tools correctly;
	// declaring them as an argument-less object (the issue #23 symptom) makes
	// read_workspace_file unusable.
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

	// The tool result streams back into the next model call: first the
	// directory listing, then the file content.
	if !strings.Contains((*bodies)[1], "README.md") {
		t.Fatalf("listing result missing from second model call: %s", (*bodies)[1])
	}
	if !strings.Contains((*bodies)[2], "cloud 是一个云原生示例项目。") {
		t.Fatalf("file content missing from third model call: %s", (*bodies)[2])
	}

	var sawListCall, sawListResult, sawReadCall, sawFinal bool
	for _, ev := range *events {
		switch {
		case ev.Kind == "tool_call" && ev.ToolName == "list_workspace_files":
			sawListCall = true
		case ev.Kind == "tool_result" && ev.ToolName == "list_workspace_files":
			sawListResult = true
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
}

// TestDirectChatWriteAsksConfirmation: direct chats get the file tools too,
// but writes stay behind the HITL confirmation contract (same as configured
// agents) — nothing is written before the user approves.
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

// TestDirectChatAdvertisesShellTool pins the issue #40 regression: the
// implicit default agent behind a target-less chat must mount run_command and
// tell the model it has terminal capability, not only workspace file tools.
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
}

// TestDirectChatShellAsksConfirmation: a direct chat gets the terminal tool
// behind the same HITL gate as a new agent — nothing runs before the user
// approves, and the command executes in the session workspace afterwards.
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
	// The resumed model call must carry the executed result (exitCode 0), not
	// the bare confirmation roundtrip ADK hides from the model.
	if len(*bodies) < 2 || !strings.Contains((*bodies)[1], `\"exitCode\":0`) {
		t.Fatalf("resume request missing successful shell result: %v", *bodies)
	}
	if data, err := os.ReadFile(filepath.Join(root, "shell-marker.txt")); err != nil || strings.TrimSpace(string(data)) != "hi" {
		t.Fatalf("expected approved command output, err=%v data=%q", err, data)
	}
}

// TestDirectChatWithoutProviderFails: with no provider configured the turn
// is refused with an actionable message instead of a generic error.
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
