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

// TestA2AServiceLoopback 通过 A2A 发布本地 Agent，然后用应用自身的远程 Agent
// 客户端（loopback）连接它，并验证一次完整的会话往返以及状态转换。
func TestA2AServiceLoopback(t *testing.T) {
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(readAll(r), `"stream":false`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"本机 A2A 服务已响应"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("本机 A2A 服务已响应") + sseDone))
	}))
	defer modelSrv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: modelSrv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "a1", Name: "本地服务助手", Description: "对外提供服务的 Agent", ProviderID: "p1", Model: "m",
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
	a2a := NewA2AService(svc, sessions)

	if sts := a2a.Status(); len(sts) != 0 {
		t.Fatalf("must start with no published agents: %+v", sts)
	}

	st, err := a2a.Start("a1", 0) // 端口 0：选择一个空闲端口
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.URL == "" || st.AgentName != agentkit.SanitizeAgentName("本地服务助手") {
		t.Fatalf("bad start status: %+v", st)
	}

	// 卡片发现必须解析到已发布的 Agent。
	info, err := agentkit.CheckRemoteAgent(t.Context(), st.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != st.AgentName || !info.Streaming {
		t.Fatalf("card mismatch: %+v vs %+v", info, st)
	}
	_ = info

	// Loopback 会话：客户端 ChatService 通过完整的远程 Agent 客户端栈与已发布的
	// Agent 通信。
	clientDir := t.TempDir()
	clientStore, err := config.Open(clientDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientStore.Update(func(cfg *config.Config) {
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{
			ID: "ra_loop", Name: "回环客户端", CardURL: st.URL,
		})
	}); err != nil {
		t.Fatal(err)
	}
	_, clientSessions, err := newTestSessionService(t, filepath.Join(clientDir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	clientSvc := &Services{Store: clientStore, Kit: agentkit.NewKit(clientStore, mcpmgr.New()), MCP: mcpmgr.New()}
	clientChat := NewChatService(clientSvc, clientSessions, db)
	sess, err := clientChat.newLegacySession("remote", "ra_loop", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := clientChat.Send(sess.ID, "在吗？", nil); err != nil {
		t.Fatal(err)
	}
	msgs, err := clientChat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var answered bool
	for _, m := range msgs {
		if m.Kind == "assistant" && strings.Contains(m.Text, "本机 A2A 服务已响应") {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("loopback answer missing: %+v", msgs)
	}

	// 对同一个 Agent 启动两次是幂等的。
	if st2, err := a2a.Start("a1", 0); err != nil || !st2.Running || st2.URL != st.URL {
		t.Fatalf("double start should keep existing server: %+v err=%v", st2, err)
	}

	// StopAgent 仅清除该 Agent 的条目。
	if err := a2a.StopAgent("a1"); err != nil {
		t.Fatal(err)
	}
	if sts := a2a.Status(); len(sts) != 0 {
		t.Fatalf("still published after StopAgent: %+v", sts)
	}
}

func TestA2AAgentConfigUsesNonInteractivePolicy(t *testing.T) {
	got := a2aAgentConfig(config.AgentConfig{
		ID:           "a1",
		Name:         "对外服务",
		ConfirmTools: []string{"files", "shell"},
	})
	if approval := got.EffectiveApprovalPolicy(); approval != config.ApprovalPolicyNever {
		t.Fatalf("A2A approval policy = %q, want %q", approval, config.ApprovalPolicyNever)
	}
	if profile := got.EffectiveSandboxProfile(); profile != config.SandboxProfileWorkspaceWrite {
		t.Fatalf("A2A sandbox profile = %q, want %q", profile, config.SandboxProfileWorkspaceWrite)
	}
	if got.SandboxNetwork != config.SandboxNetworkOff {
		t.Fatalf("A2A network = %q, want %q", got.SandboxNetwork, config.SandboxNetworkOff)
	}
	if len(got.ConfirmTools) != 0 {
		t.Fatalf("A2A per-agent confirmations must be cleared, got %v", got.ConfirmTools)
	}
}

// TestConfigMutationStopsPublishedAgent：编辑或删除已发布的 Agent 会使其 A2A
// 服务器下线（执行器持有在 Start 时构建的树；否则远程客户端会继续调用陈旧的
// Agent）。
func TestConfigMutationStopsPublishedAgent(t *testing.T) {
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(readAll(r), `"stream":false`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	}))
	defer modelSrv.Close()

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: modelSrv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "a1", Name: "发布助手", ProviderID: "p1", Model: "m"})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	a2a := NewA2AService(svc, sessions)
	svc.A2A = a2a // 配置变更必须到达已发布服务器的注册表。

	if _, err := a2a.Start("a1", 0); err != nil {
		t.Fatal(err)
	}
	if len(a2a.Status()) != 1 {
		t.Fatal("expected one published agent")
	}

	// 编辑 Agent 会将其取消发布。
	cs := NewConfigService(svc)
	edited := store.Get().Agents[0]
	edited.SystemPrompt = "改过的提示词"
	if err := cs.SaveAgent(edited); err != nil {
		t.Fatal(err)
	}
	if len(a2a.Status()) != 0 {
		t.Fatalf("edited agent still published: %+v", a2a.Status())
	}

	// 重新发布后删除：删除也会取消发布。
	if _, err := a2a.Start("a1", 0); err != nil {
		t.Fatal(err)
	}
	if err := cs.DeleteAgent("a1"); err != nil {
		t.Fatal(err)
	}
	if len(a2a.Status()) != 0 {
		t.Fatalf("deleted agent still published: %+v", a2a.Status())
	}
}
