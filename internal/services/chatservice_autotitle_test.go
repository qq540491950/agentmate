package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// TestMain 保持脚本化模型测试夹具的确定性：自动标题在生产环境中默认开启，因此无关测试原
// 本会与一个额外的非流式请求发生竞态。专用标题测试通过 autoTitleInline
// 选择启用。
func TestMain(m *testing.M) {
	orig := autoTitleStart
	autoTitleStart = func(func()) {}
	code := m.Run()
	autoTitleStart = orig
	os.Exit(code)
}

func autoTitleInline(t *testing.T) {
	t.Helper()
	prev := autoTitleStart
	autoTitleStart = func(run func()) { run() }
	t.Cleanup(func() { autoTitleStart = prev })
}

// TestAutoTitleGeneratesTitle：首次交流后，会向配置的标题模型请求一个简短标题（
// 一次非流式辅助调用），并替换截断文本回退。失败时保留该回退。
func TestAutoTitleGeneratesTitle(t *testing.T) {
	autoTitleInline(t)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAllBody(r)
		bodies = append(bodies, body)
		if strings.Contains(body, `"stream":false`) {
			// 标题请求：普通补全。
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"周报规划讨论"}}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好的，我们来规划。") + sseDone))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, dir+"/sessions.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_at", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-mini"}, {ID: "fake-large"}},
		})
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_title", Name: "Title", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "fake-title"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_at", Name: "助手", ProviderID: "prov_at", Model: "fake-mini",
		})
	}); err != nil {
		t.Fatal(err)
	}
	// 自动标题默认需要显式启用：为本测试启用它。
	on := true
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.AutoTitle = &on
		cfg.Settings.TitleProviderID = "prov_title"
		cfg.Settings.TitleModel = "fake-title"
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_at", "")
	if err != nil {
		t.Fatal(err)
	}
	// 将对话固定到另一个模型：标题辅助调用应使用显式标题模型，而不是会话覆盖。
	if err := chat.SetSessionModel(sess.ID, "fake-large"); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "帮我规划写周报的完整流程", nil); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		title := ""
		for _, s := range chat.S.Store.Get().Sessions {
			if s.ID == sess.ID {
				title = s.Title
			}
		}
		if title == "周报规划讨论" {
			// 标题请求经由配置的标题模型完成。
			found := false
			for _, b := range bodies {
				if strings.Contains(b, "中文标题") {
					if !strings.Contains(b, `"model":"fake-title"`) {
						t.Fatalf("title side call ignored the configured title model: %s", b)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("title generated without the model prompt")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("title not generated; last=%q", title)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAutoTitleDisabledKeepsFallback：关闭该设置（或辅助调用失败）会保留截断的
// 首条消息回退。
func TestAutoTitleDisabledKeepsFallback(t *testing.T) {
	chat, store, _ := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好") + sseDone))
	})
	hookEvents(t)
	off := false
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.AutoTitle = &off
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "帮我规划写周报的完整流程这条消息足够长会被截断", nil); err != nil {
		t.Fatal(err)
	}
	title := ""
	for _, s := range chat.S.Store.Get().Sessions {
		if s.ID == sess.ID {
			title = s.Title
		}
	}
	if title == "" || len([]rune(title)) > 30 {
		t.Fatalf("fallback title unexpected: %q", title)
	}
}

// TestGenerateTitleSkipsReasoningContent 锁定了所报告的思考模型行为：标题调用会收到推理部分以及最终答案，但侧边栏必须
// 仅使用答案。伪造实现还复现了旧的 40 token
// 上限：推理消耗了整个预算，因此没有标题。
func TestGenerateTitleSkipsReasoningContent(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = readAllBody(r)
		var req struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.Unmarshal([]byte(seen), &req)
		if req.MaxTokens < 256 {
			// 在旧预算下复现实时中继：思考消耗了全部 40 个输出 token，content 保持为空。
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"","reasoning_content":"我们需要回答标题，不超过12汉字，只输出标题本身。需要概括对话开头"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"空指针报错排查","reasoning_content":"我们需要回答中文标题，不超过12汉字。标题可“空指针报错排查”。只输出标题。"}}]}`))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.Providers = append(cfg.Providers, config.ModelProvider{
		ID: "prov_title", Name: "Title", Protocol: config.ProtocolOpenAI,
		BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "thinking-title"}},
	})
	cfg.Settings.TitleProviderID = "prov_title"
	cfg.Settings.TitleModel = "thinking-title"

	title, ok := (&ChatService{}).generateTitle(cfg, "帮我看看这个报错", "这是空指针导致的。")
	if !ok {
		t.Fatalf("title generation failed; request=%s", seen)
	}
	if title != "空指针报错排查" {
		t.Fatalf("title = %q, want %q (reasoning leaked into the title)", title, "空指针报错排查")
	}
}

// TestAutoTitleUsesMainModelByDefault 锁定所报告的行为：在没有显式 autoTitle
// 设置时，固定使用次要模型的会话仍会从全局默认（"main"）模型获得精炼后的标题。
func TestAutoTitleUsesMainModelByDefault(t *testing.T) {
	autoTitleInline(t)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAllBody(r)
		bodies = append(bodies, body)
		if strings.Contains(body, `"stream":false`) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"会话统计命令"}}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("这是统计说明。") + sseDone))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, dir+"/sessions.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_main", Name: "Fake", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, IsDefault: true,
			Models: []config.ModelInfo{{ID: "main-model"}, {ID: "session-model"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "agent_main", Name: "助手", ProviderID: "prov_main", Model: "session-model",
		})
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	chat := NewChatService(svc, sessions, db)
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_main", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "/stats", nil); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		title := ""
		for _, s := range chat.S.Store.Get().Sessions {
			if s.ID == sess.ID {
				title = s.Title
			}
		}
		if title == "会话统计命令" {
			for _, b := range bodies {
				if strings.Contains(b, "中文标题") {
					if !strings.Contains(b, `"model":"main-model"`) {
						t.Fatalf("title side call should use the main model: %s", b)
					}
					return
				}
			}
			t.Fatal("title generated without a model request")
		}
		if time.Now().After(deadline) {
			t.Fatalf("title not refined by the main model; last=%q", title)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRefineSessionTitleCleansExistingFallback 覆盖在自动标题之前创建的会话，或带有原始斜杠命令回退的会话：该显式操作会读取
// 已存储的首条用户消息/最新回复并重写标题。
func TestRefineSessionTitleCleansExistingFallback(t *testing.T) {
	turn := 0
	chat, store, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		turn++
		if turn > 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"会话统计说明"}}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("`/stats` 是本地斜杠命令。") + sseDone))
	})
	hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "/stats", nil); err != nil {
		t.Fatal(err)
	}
	// 自动标题被 TestMain 抑制，因此在显式操作运行之前，该行仍保留原始回退值。
	if got := sessionTitle(store, sess.ID); got != "/stats" {
		t.Fatalf("precondition: fallback title = %q", got)
	}

	title, err := chat.RefineSessionTitle(sess.ID)
	if err != nil {
		t.Fatalf("refine title: %v", err)
	}
	if title != "会话统计说明" {
		t.Fatalf("refined title = %q", title)
	}
	if got := sessionTitle(store, sess.ID); got != title {
		t.Fatalf("stored title = %q, want %q", got, title)
	}
	found := false
	for _, b := range *bodies {
		if strings.Contains(b, "中文标题") {
			found = strings.Contains(b, `"model":"fake-mini"`)
		}
	}
	if !found {
		t.Fatal("refine side call did not use the main model")
	}
}

func sessionTitle(store *config.Store, id string) string {
	for _, s := range store.Get().Sessions {
		if s.ID == id {
			return s.Title
		}
	}
	return ""
}
