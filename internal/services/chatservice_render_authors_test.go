package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// TestRenderHistoryAuthorsConsistent 校验历史回放为同一 Agent 的整个回合分配
// 相同的显示作者：tool_call/tool_result 不得回退到原始清洗后的 ADK 作者，而
// thinking/assistant 文本却使用显示名——这种不一致曾让 groupToolMessages 在每个
// 工具边界刷新，把应用重启后的一个回合切成多个「执行过程」块。
func TestRenderHistoryAuthorsConsistent(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "read_workspace_file", `"{\"path\":\"notes/a.txt\"}"`) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseText("目录已查看。") + sseDone))
		default:
			_, _ = w.Write([]byte(sseDone))
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	workspace := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_r", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_r", Name: "写手", ProviderID: "prov_r", Model: "fake-mini",
			SystemPrompt: "你负责看文件", BuiltinTools: []string{"files"},
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

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "看看 notes/a.txt", nil); err != nil {
		t.Fatal(err)
	}

	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var callAuthor, resultAuthor, replyAuthor string
	for _, m := range msgs {
		switch m.Kind {
		case "tool_call":
			if m.ToolName == "read_workspace_file" {
				callAuthor = m.Author
			}
		case "tool_result":
			if m.ToolName == "read_workspace_file" {
				resultAuthor = m.Author
			}
		case "assistant":
			if strings.Contains(m.Text, "目录已查看") {
				replyAuthor = m.Author
			}
		}
	}
	if callAuthor == "" || resultAuthor == "" || replyAuthor == "" {
		t.Fatalf("history missing turn events: call=%q result=%q reply=%q\n%+v", callAuthor, resultAuthor, replyAuthor, msgs)
	}
	if callAuthor != replyAuthor || resultAuthor != replyAuthor {
		t.Fatalf("history authors inconsistent: call=%q result=%q reply=%q", callAuthor, resultAuthor, replyAuthor)
	}
	if callAuthor != "写手" {
		t.Fatalf("expected display author 写手, got %q", callAuthor)
	}
}
