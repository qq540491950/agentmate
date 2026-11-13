package services

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
)

// A panicking or failing agent runtime must never take the app down: the
// session stays usable after a recovered turn.
func TestSendTurnKeepsSessionUsableAfterFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, dir+"/s.db")
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, nil), MCP: nil}
	chat := NewChatService(svc, sessions, db)

	var events []ChatStreamEvent
	appEventHook = func(ev ChatStreamEvent) { events = append(events, ev) }
	t.Cleanup(func() { appEventHook = nil })

	sess, err := chat.newLegacySession("agent", "agent_1", "")
	if err != nil {
		t.Fatal(err)
	}
	// 供应商不可达：回合失败，但应用必须保持健康。
	_ = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "X", Protocol: config.ProtocolOpenAI, BaseURL: "http://127.0.0.1:1", Models: []config.ModelInfo{{ID: "m"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "agent_1", Name: "A", ProviderID: "p1", Model: "m"})
	})

	_ = chat.Send(sess.ID, "hello", nil)
	if _, err := chat.GetSessionMessages(sess.ID); err != nil {
		t.Fatalf("session unusable after failed turn: %v", err)
	}
	// 已向 UI 暴露错误事件。
	if len(events) == 0 {
		t.Fatal("expected stream events for the failed turn")
	}
}

// TestCorruptSessionDBSelfHeals 损坏的会话数据库会被备份并透明地重新创建。
func TestCorruptSessionDBSelfHeals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database at all, just noise bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, svc, err := openSessionServiceImpl(t, path)
	if err != nil {
		t.Fatalf("self-heal failed: %v", err)
	}
	if _, err := svc.Create(context.Background(), &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "s"}); err != nil {
		t.Fatalf("recreated db unusable: %v", err)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("expected one corrupt backup, got %v", matches)
	}
	_ = db
}

// TestConfigFilePermissionRestricted config.json 必须为 0600，因为它保存 API
// 密钥。
func TestConfigFilePermissionRestricted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位语义（Chmod 0600 不生效）；文件仍受用户目录 ACL 保护")
	}
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{ID: "p", Name: "n"})
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(store.Dir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config.json perms = %o, want 600", perm)
	}
}
