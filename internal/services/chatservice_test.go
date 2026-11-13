package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"gorm.io/gorm"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// testTerminalSettings 使 shell 执行测试在各平台上保持确定性：Windows 需要
// cmd.exe 语法，而 Unix 使用原生自动 shell。
func testTerminalSettings() config.TerminalSettings {
	if runtime.GOOS == "windows" {
		return config.TerminalSettings{Mode: config.TerminalModeCmd}
	}
	return config.TerminalSettings{Mode: config.TerminalModeAuto}
}

// TestChatEndToEndToolLoop 演练完整流水线：ChatService.Send -> ADK runner ->
// llmagent -> 自定义 OpenAI 兼容模型适配器（针对伪造的 SSE 服务器）->
// 函数工具循环 -> SQLite 会话持久化。
func TestChatEndToEndToolLoop(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_current_time","arguments":"{}"}}]}}]}` + "\n\n"))
		} else {
			body := readAll(r)
			if !strings.Contains(body, `"tool"`) {
				t.Errorf("expected function response message in turn 2, got: %s", body)
			}
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"现在时间是上午，任务完成。"}}]}` + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_1", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_1", Name: "时间助手", ProviderID: "prov_1", Model: "fake-mini",
			SystemPrompt: "你负责查询时间", Temperature: 0.3, MaxTokens: 512,
			BuiltinTools: []string{"time"},
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

	sess, err := chat.newLegacySession("agent", "agent_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "现在几点了？", nil); err != nil {
		t.Fatal(err)
	}

	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	var sawUser, sawToolCall, sawToolResult, sawAnswer bool
	for _, m := range msgs {
		switch {
		case m.Kind == "user" && strings.Contains(m.Text, "几点"):
			sawUser = true
		case m.Kind == "tool_call" && m.ToolName == "get_current_time":
			sawToolCall = true
		case m.Kind == "tool_result" && m.ToolName == "get_current_time":
			sawToolResult = true
		case m.Kind == "assistant" && strings.Contains(m.Text, "任务完成"):
			sawAnswer = true
		}
	}
	if !sawUser || !sawToolCall || !sawToolResult || !sawAnswer {
		t.Fatalf("pipeline incomplete: user=%v toolCall=%v toolResult=%v answer=%v\nmessages: %+v",
			sawUser, sawToolCall, sawToolResult, sawAnswer, msgs)
	}

	// 会话标题根据第一条消息自动设置。
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID && s.Title == "新对话" {
			t.Fatal("expected session title to be set from first message")
		}
	}

	// 历史记录已持久化：全新的服务会读取同一会话。
	chat2 := NewChatService(svc, sessions, db)
	msgs2, err := chat2.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs2) != len(msgs) {
		t.Fatalf("persisted history mismatch: %d vs %d", len(msgs2), len(msgs))
	}
}

func readAll(r *http.Request) string {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

func newTestSessionService(t *testing.T, path string) (*gorm.DB, session.Service, error) {
	// 与 main.go 保持一致：WAL + busy_timeout + 单个池化连接，使并行会话绝不会触发
	// SQLITE_BUSY 快照冲突。
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"))
	if err != nil {
		return nil, nil, err
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	svc, err := database.NewSessionServiceFromDB(db)
	if err != nil {
		return nil, nil, err
	}
	if err := database.AutoMigrate(svc); err != nil {
		return nil, nil, err
	}
	// 在清理时关闭：打开的连接池会使 SQLite 文件保持锁定，且测试的 TempDir
	// RemoveAll 在 Windows 上会失败。
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db, svc, nil
}
