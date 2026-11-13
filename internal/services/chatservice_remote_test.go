package services

import (
	"net/http"
	"net/http/httptest"
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

// TestChatRemoteAgentE2E 在进程内 A2A 服务器后面托管一个真实的 llmagent（官方
// a2asrv + ADK executor 方案，与 ADK 启动器的连接方式相同），并通过 ChatService
// -> ADK remote agent -> A2A -> remote llmagent -> SQLite history
// 驱动一个完整的聊天回合。
func TestChatRemoteAgentE2E(t *testing.T) {
	// 远程模型：遵循远程执行器可能使用的两种调用模式。
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(readAll(r), `"stream":false`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"远程 agent 已收到消息。"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("远程 agent 已收到消息。") + sseDone))
	}))
	defer modelSrv.Close()

	remoteModel, err := agentkit.NewOpenAICompat(agentkit.OpenAICompatConfig{
		BaseURL: modelSrv.URL, Model: "fake-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	hosted, err := llmagent.New(llmagent.Config{
		Name:        "remote_helper",
		Description: "远程测试助手",
		Model:       remoteModel,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 通过 A2A 托管它（卡片发现 + JSON-RPC 调用）。
	_, serverSessions, err := newTestSessionService(t, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	a2aMux := http.NewServeMux()
	a2aSrv := httptest.NewServer(a2aMux)
	defer a2aSrv.Close()
	card := &a2acore.AgentCard{
		Name:               "remote_helper",
		Description:        "远程测试助手",
		Version:            "1.0.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities:       a2acore.AgentCapabilities{Streaming: true},
		SupportedInterfaces: []*a2acore.AgentInterface{{
			URL:             a2aSrv.URL + "/a2a/v1/invoke",
			ProtocolBinding: a2acore.TransportProtocolJSONRPC,
			ProtocolVersion: a2acore.Version,
		}},
	}
	executor := adka2av2.NewExecutor(adka2av2.ExecutorConfig{
		RunnerConfig: runner.Config{
			AppName:        "remote_helper",
			Agent:          hosted,
			SessionService: serverSessions,
		},
	})
	a2aMux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	a2aMux.Handle("/a2a/v1/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))

	// 本地应用配置：一个指向 A2A 服务器的远程 Agent。
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{
			ID: "ra_1", Name: "远程助手", Description: "跨进程测试",
			CardURL: a2aSrv.URL,
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

	sess, err := chat.newLegacySession("remote", "ra_1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好，远程的伙伴", nil); err != nil {
		t.Fatal(err)
	}

	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawUser, sawAnswer bool
	for _, m := range msgs {
		switch {
		case m.Kind == "user" && strings.Contains(m.Text, "远程的伙伴"):
			sawUser = true
		case m.Kind == "assistant" && strings.Contains(m.Text, "远程 agent 已收到消息"):
			sawAnswer = true
		}
	}
	if !sawUser || !sawAnswer {
		t.Fatalf("remote chat incomplete: user=%v answer=%v\n%+v", sawUser, sawAnswer, msgs)
	}

	// 远程 Agent 卡片可供连通性检查发现。
	info, err := agentkit.CheckRemoteAgent(t.Context(), a2aSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "remote_helper" || !info.Streaming {
		t.Fatalf("unexpected card info: %+v", info)
	}
}
