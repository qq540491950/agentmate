package services

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// newTodosTestChat 将 Agent 接上 plan_todos 内置工具，并连接到脚本化模型：第 1
// 回合写入计划，第 2 回合在不带参数的情况下读回该计划。
func newTodosTestChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_t", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_t", Name: "规划师", ProviderID: "prov_t", Model: "fake-mini",
			BuiltinTools: []string{"todos"},
		})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, dir+"/sessions.db")
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewChatService(svc, sessions, db), store
}

// TestPlanTodosRoundTrip：该工具会规范化状态，将计划存入会话状态，随后的一次只
// 读调用会返回已存储的列表——模型可以跨回合查阅自己的计划。
func TestPlanTodosRoundTrip(t *testing.T) {
	turn := 0
	chat, _ := newTodosTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("c1", "plan_todos",
				`"{\"todos\":[{\"content\":\"收集素材\",\"status\":\"in_progress\"},{\"content\":\"  \",\"status\":\"completed\"},{\"content\":\"写初稿\",\"status\":\"weird\"}]}"`) + sseDone))
		case 2:
			// 只读调用：模型省略了 todos 参数。
			_, _ = w.Write([]byte(sseToolCall("c2", "plan_todos", `"{}"`) + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("计划已更新。") + sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_t", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "帮我规划写周报", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "继续", nil); err != nil {
		t.Fatal(err)
	}

	var writeResp, readResp map[string]any
	for _, ev := range *events {
		if ev.Kind != "tool_result" || ev.ToolName != "plan_todos" {
			continue
		}
		if _, ok := ev.ToolResp["todos"]; !ok {
			continue
		}
		if writeResp == nil {
			writeResp = ev.ToolResp
		} else {
			readResp = ev.ToolResp
		}
	}
	if writeResp == nil || readResp == nil {
		t.Fatalf("missing tool_result payloads: %+v", *events)
	}
	writeJSON := mustJSON(writeResp["todos"])
	// 空白步骤被丢弃，未知状态被规范化，两个真实步骤被保留。
	if !strings.Contains(string(writeJSON), "收集素材") || !strings.Contains(string(writeJSON), "in_progress") {
		t.Fatalf("write payload unexpected: %s", writeJSON)
	}
	if strings.Count(string(writeJSON), "content") != 2 {
		t.Fatalf("expected 2 cleaned steps, got: %s", writeJSON)
	}
	readJSON := mustJSON(readResp["todos"])
	if !strings.Contains(string(readJSON), "写初稿") || !strings.Contains(string(readJSON), "pending") {
		t.Fatalf("read-back lost the stored plan: %s", readJSON)
	}

	// Markdown 导出会将计划渲染为清单，而不是原始 JSON。
	path, err := chat.ExportSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if !strings.Contains(doc, "任务计划") || !strings.Contains(doc, "▶ 收集素材") || !strings.Contains(doc, "○ 写初稿") {
		t.Fatalf("export missing plan checklist:\n%s", doc)
	}
}
