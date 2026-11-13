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
)

// TestForkSession
// 会分叉一个会话：分叉会携带源历史的完整副本，随后独立分叉（在分叉中发送必须不
// 会影响原始会话）。
func TestForkSession(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		w.Header().Set("Content-Type", "text/event-stream")
		if turn == 1 {
			_, _ = w.Write([]byte(sseText("原始回答") + sseDone))
		} else {
			_, _ = w.Write([]byte(sseText("分叉后的新回答") + sseDone))
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 默认工作区是 $HOME/Documents/AgentMateWorkspace，它在裸 CI runner
	// 上不存在（让每个测试都依赖
	// $HOME，正是得到与代码无关的红色构建的方式）。请改为将会话工作区指向测试的临
	// 时目录。
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = ws
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "助手", ProviderID: "p1", Model: "m"})
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

	orig, err := chat.newLegacySession("agent", "a1", "历史会话")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(orig.ID, "你好", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.RenameSession(orig.ID, "历史会话"); err != nil {
		t.Fatal(err)
	}

	branch, err := chat.ForkSession(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ID == orig.ID || branch.TargetType != "agent" || branch.TargetID != "a1" {
		t.Fatalf("fork metadata wrong: %+v", branch)
	}
	if branch.ForkedFrom != orig.ID {
		t.Fatalf("fork lineage missing: %+v", branch)
	}
	if !strings.Contains(branch.Title, "分支") {
		t.Fatalf("fork title should carry suffix: %q", branch.Title)
	}

	// 分叉必须包含相同的渲染后历史。
	origMsgs, err := chat.GetSessionMessages(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	forkMsgs, err := chat.GetSessionMessages(branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(forkMsgs) != len(origMsgs) || len(forkMsgs) == 0 {
		t.Fatalf("fork history mismatch: orig=%d fork=%d", len(origMsgs), len(forkMsgs))
	}

	// 分化：分叉中的发送仅出现在该分叉中。
	if err := chat.Send(branch.ID, "换个说法", nil); err != nil {
		t.Fatal(err)
	}
	forkMsgs2, _ := chat.GetSessionMessages(branch.ID)
	origMsgs2, _ := chat.GetSessionMessages(orig.ID)
	if !strings.Contains(joinTexts(forkMsgs2), "分叉后的新回答") {
		t.Fatalf("fork divergence missing: %+v", forkMsgs2)
	}
	if strings.Contains(joinTexts(origMsgs2), "分叉后的新回答") || len(origMsgs2) != len(origMsgs) {
		t.Fatalf("original timeline was mutated by fork activity: %+v", origMsgs2)
	}

	// 对不存在的会话执行分叉会干净地报错。
	if _, err := chat.ForkSession("sess_nope"); err == nil {
		t.Fatal("expected error forking unknown session")
	}
}

func joinTexts(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestPinSessionOrdering：已固定的会话无论最近程度如何都最先显示，取消固定后会
// 恢复 updated-at 顺序。
func TestPinSessionOrdering(t *testing.T) {
	chat, _, _, dir := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseDone))
	})
	_ = dir

	first, err := chat.newLegacySession("agent", "agent_c", "第一")
	if err != nil {
		t.Fatal(err)
	}
	second, err := chat.newLegacySession("agent", "agent_c", "第二")
	if err != nil {
		t.Fatal(err)
	}

	if err := chat.PinSession(first.ID, true); err != nil {
		t.Fatal(err)
	}
	sessions := chat.ListSessions()
	if sessions[0].ID != first.ID || !sessions[0].Pinned {
		t.Fatalf("pinned session must sort first: %+v", sessions)
	}

	if err := chat.PinSession(first.ID, false); err != nil {
		t.Fatal(err)
	}
	sessions = chat.ListSessions()
	for _, s := range sessions {
		if s.ID == first.ID && s.Pinned {
			t.Fatal("unpin failed")
		}
	}
	_ = second
}

// TestExportSessionIncludesStats：导出的 Markdown
// 带有根据已存储事件生成的统计摘要标头。
func TestExportSessionIncludesStats(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseText("回答内容") + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = dir
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好", nil); err != nil {
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
	if !strings.Contains(string(data), "- 统计:") {
		t.Fatalf("export missing stats header:\n%s", data)
	}
}

// TestExportSessionHTML：HTML 导出会渲染一个自包含的带样式文档——标题、Markdown
// 段落，且不含原始 Markdown 语法。
func TestExportSessionHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("HTML 导出回答") + sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = dir
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "导出测试", nil); err != nil {
		t.Fatal(err)
	}

	path, err := chat.ExportSessionHTML(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".html") {
		t.Fatalf("expected .html export: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{"<!DOCTYPE html>", "<h1>", "<h2", "HTML 导出回答", "用量统计", "</html>"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("html doc missing %q", want)
		}
	}
}

// TestSessionPreviewUpdated:
// 一个回合后，会话记录会携带最新的助手回复末尾片段，供侧边栏使用。
func TestSessionPreviewUpdated(t *testing.T) {
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseText("第一版回答") + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("第二版回答") + sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "问题一", nil); err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID {
			found = s.Preview
		}
	}
	if !strings.Contains(found, "第一版回答") {
		t.Fatalf("preview missing after turn: %q", found)
	}
	if err := chat.Send(sess.ID, "问题二", nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID && !strings.Contains(s.Preview, "第二版回答") {
			t.Fatalf("preview not refreshed: %q", s.Preview)
		}
	}
}

// TestExportSessionMarksForkLine: 从已有历史的会话分叉时，导出的 Markdown
// 会标注来源。
func TestExportSessionMarksForkLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("回答") + sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = dir
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "原始", nil); err != nil {
		t.Fatal(err)
	}
	branch, err := chat.ForkSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	path, err := chat.ExportSession(branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "分叉自") {
		t.Fatalf("fork origin missing in export:\n%s", data)
	}
}

// TestSessionLifecycleAudit: 启用后，创建/分叉/删除/重命名都会写入 JSONL
// 审计轨迹。
func TestSessionLifecycleAudit(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = dir
		cfg.Settings.ToolsAudit = true
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	chat.SetToolAuditPath(auditPath)

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.RenameSession(sess.ID, "改名"); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.ForkSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := chat.DeleteSession(sess.ID); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, phase := range []string{"session_created", "session_renamed", "session_forked", "session_deleted"} {
		if !strings.Contains(doc, phase) {
			t.Fatalf("audit missing %s:\n%s", phase, doc)
		}
	}
}

// TestArchiveSessionRoundTrip:
// 归档/取消归档会切换持久化标志，并将两个阶段都写入审计轨迹。
func TestArchiveSessionRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(dir, "audit.jsonl")
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	chat.SetToolAuditPath(auditPath)
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.ArchiveSession(sess.ID, true); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID {
			found = s.Archived
		}
	}
	if !found {
		t.Fatal("archived flag not persisted")
	}
	if err := chat.ArchiveSession(sess.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, s := range chat.ListSessions() {
		if s.ID == sess.ID && s.Archived {
			t.Fatal("unarchive failed")
		}
	}
	audit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(audit), "session_archived") || !strings.Contains(string(audit), "session_unarchived") {
		t.Fatalf("archive phases missing from audit:\n%s", audit)
	}
}

// TestExportAllSessions: 批量导出会为每个会话写入一个 Markdown 文件。
func TestExportAllSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("批量导出回答") + sseDone))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = dir
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "A", ProviderID: "p1", Model: "m"})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	s1, err := chat.newLegacySession("agent", "a1", "会话一")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := chat.newLegacySession("agent", "a1", "会话二")
	if err != nil {
		t.Fatal(err)
	}
	_ = s2
	if err := chat.Send(s1.ID, "内容", nil); err != nil {
		t.Fatal(err)
	}

	n, err := chat.ExportAllSessions()
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Fatalf("expected at least 2 exported sessions, got %d", n)
	}
}
