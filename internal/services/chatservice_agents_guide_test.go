package services

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	a2acore "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/runner"
	adka2av2 "google.golang.org/adk/v2/server/adka2a/v2"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// newGuideTestChat wires a ChatService against a scripted SSE model server
// whose requests are forwarded to a channel, with the user-level
// ~/.agents/AGENTS.md seeded through the home-dir environment before the
// Kit (and its global guide path) is built.
func newGuideTestChat(t *testing.T) (*ChatService, <-chan string, string) {
	t.Helper()
	bodies := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodies <- readAll(r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	globalDir := filepath.Join(home, ".agents")
	if err := os.Mkdir(globalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "AGENTS.md"), []byte("全局个人约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "a1", Name: "助手", ProviderID: "p1", Model: "m",
			SystemPrompt: "你是配置助手的角色提示词",
		})
		cfg.Skills = append(cfg.Skills, config.SkillConfig{
			ID: "s1", Name: "Guide Order Skill", Description: "顺序测试",
			Content: "# 技能规则\n只输出结论。",
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
	return NewChatService(svc, sessions, db), bodies, home
}

// TestAgentsGuideConfiguredAgentInjectsGlobalThenWorkspace asserts the
// workspace AGENTS.md and the global ~/.agents/AGENTS.md both reach the model,
// global first, after the agent's own role prompt.
func TestAgentsGuideConfiguredAgentInjectsGlobalThenWorkspace(t *testing.T) {
	chat, bodies, _ := newGuideTestChat(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("工作区项目约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithWorkspace("agent", "a1", "", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "汇报进展", nil); err != nil {
		t.Fatal(err)
	}
	body := <-bodies
	for _, want := range []string{"你是配置助手的角色提示词", "全局个人约定内容", "工作区项目约定内容"} {
		if !strings.Contains(body, want) {
			t.Fatalf("model request must contain %q, got: %s", want, body)
		}
	}
	globalAt := strings.Index(body, "全局个人约定内容")
	workspaceAt := strings.Index(body, "工作区项目约定内容")
	if globalAt >= workspaceAt {
		t.Fatalf("global guide must precede the workspace guide: global@%d workspace@%d", globalAt, workspaceAt)
	}
	roleAt := strings.Index(body, "你是配置助手的角色提示词")
	if roleAt > globalAt || globalAt > workspaceAt {
		t.Fatalf("order must be role < global < workspace: role@%d global@%d workspace@%d", roleAt, globalAt, workspaceAt)
	}
}

// TestAgentsGuideDirectAssistantInjectsGlobal asserts the implicit direct
// assistant also loads the global engineering guide without any agent config.
func TestAgentsGuideDirectAssistantInjectsGlobal(t *testing.T) {
	chat, bodies, _ := newGuideTestChat(t)
	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好", nil); err != nil {
		t.Fatal(err)
	}
	body := <-bodies
	if !strings.Contains(body, "全局个人约定内容") {
		t.Fatalf("direct assistant request must contain the global guide, got: %s", body)
	}
}

// TestAgentsGuideOversizeWarns asserts a >48KB guide reaches the model in
// full with a trailing size warning instead of being truncated.
func TestAgentsGuideOversizeWarns(t *testing.T) {
	chat, bodies, _ := newGuideTestChat(t)
	workspace := t.TempDir()
	content := strings.Repeat("超长项目约定", 10000) // 180,000 bytes, above the 48KB threshold
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithWorkspace("agent", "a1", "", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "汇报进展", nil); err != nil {
		t.Fatal(err)
	}
	body := <-bodies
	if !strings.Contains(body, content) {
		t.Fatal("oversize guide content must not be truncated")
	}
	if !strings.Contains(body, "超过 48KB") || !strings.Contains(body, "176 KB") || !strings.Contains(body, "挤占对话上下文") {
		t.Fatalf("oversize guide must carry a size warning, got tail: %q", body[len(body)-200:])
	}
}

// TestAgentsGuideMissingKeepsBaseline asserts a home and workspace without any
// AGENTS.md leaves the request exactly as before — no empty guide blocks.
func TestAgentsGuideMissingKeepsBaseline(t *testing.T) {
	chat, bodies, home := newGuideTestChat(t)
	if err := os.Remove(filepath.Join(home, ".agents", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "汇报进展", nil); err != nil {
		t.Fatal(err)
	}
	body := <-bodies
	if strings.Contains(body, "全局个人约定内容") || strings.Contains(body, "工作区项目约定内容") {
		t.Fatalf("removed guide must not reach the model, got: %s", body)
	}
	if !strings.Contains(body, "你是配置助手的角色提示词") {
		t.Fatalf("role prompt must still be present, got: %s", body)
	}
}

// TestAgentsGuideWorkspaceOnly asserts a workspace guide reaches the model on
// its own, with no global guide present.
func TestAgentsGuideWorkspaceOnly(t *testing.T) {
	chat, bodies, home := newGuideTestChat(t)
	if err := os.Remove(filepath.Join(home, ".agents", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("仅工作区约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithWorkspace("agent", "a1", "", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "汇报进展", nil); err != nil {
		t.Fatal(err)
	}
	body := <-bodies
	if !strings.Contains(body, "仅工作区约定内容") {
		t.Fatalf("workspace-only guide must reach the model, got: %s", body)
	}
	if strings.Contains(body, "全局个人约定内容") {
		t.Fatalf("removed global guide must not reach the model, got: %s", body)
	}
}

// TestAgentsGuidePrecedesSkillContext asserts the /skill body still appends
// after the engineering guides: role prompt -> guides -> skill context.
func TestAgentsGuidePrecedesSkillContext(t *testing.T) {
	chat, bodies, _ := newGuideTestChat(t)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("工作区项目约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithWorkspace("agent", "a1", "", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "/guide-order-skill 完成任务", nil); err != nil {
		t.Fatal(err)
	}
	body := <-bodies
	guideAt := strings.Index(body, "工作区项目约定内容")
	skillAt := strings.Index(body, "只输出结论。")
	if guideAt < 0 || skillAt < 0 {
		t.Fatalf("guide and skill body must both reach the model, got: %s", body)
	}
	if guideAt > skillAt {
		t.Fatalf("guides must precede the skill context: guide@%d skill@%d", guideAt, skillAt)
	}
}

// TestAgentsGuideRemoteAgentUnaffected asserts a remote A2A agent never sees
// the local engineering guides: local context stays local.
func TestAgentsGuideRemoteAgentUnaffected(t *testing.T) {
	remoteBodies := make(chan string, 4)
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		remoteBodies <- body
		if strings.Contains(body, `"stream":false`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"远程已收到。"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("远程已收到。") + sseDone))
	}))
	t.Cleanup(modelSrv.Close)

	remoteModel, err := agentkit.NewOpenAICompat(agentkit.OpenAICompatConfig{BaseURL: modelSrv.URL, Model: "fake-mini"})
	if err != nil {
		t.Fatal(err)
	}
	hosted, err := llmagent.New(llmagent.Config{Name: "remote_helper", Description: "远程测试助手", Model: remoteModel})
	if err != nil {
		t.Fatal(err)
	}
	_, serverSessions, err := newTestSessionService(t, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	a2aMux := http.NewServeMux()
	a2aSrv := httptest.NewServer(a2aMux)
	t.Cleanup(a2aSrv.Close)
	card := &a2acore.AgentCard{
		Name: "remote_helper", Description: "远程测试助手", Version: "1.0.0",
		DefaultInputModes: []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2acore.AgentCapabilities{Streaming: true},
		SupportedInterfaces: []*a2acore.AgentInterface{{
			URL: a2aSrv.URL + "/a2a/v1/invoke", ProtocolBinding: a2acore.TransportProtocolJSONRPC, ProtocolVersion: a2acore.Version,
		}},
	}
	executor := adka2av2.NewExecutor(adka2av2.ExecutorConfig{
		RunnerConfig: runner.Config{AppName: "remote_helper", Agent: hosted, SessionService: serverSessions},
	})
	a2aMux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	a2aMux.Handle("/a2a/v1/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	if err := os.Mkdir(filepath.Join(home, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", "AGENTS.md"), []byte("全局个人约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{ID: "ra_1", Name: "远程助手", CardURL: a2aSrv.URL})
	}); err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	sess, err := chat.newLegacySession("remote", "ra_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好，远程的伙伴", nil); err != nil {
		t.Fatal(err)
	}
	body := <-remoteBodies
	if strings.Contains(body, "全局个人约定内容") {
		t.Fatalf("local engineering guide leaked to the remote agent, got: %s", body)
	}
}
