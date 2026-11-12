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

// TestMain keeps scripted model fixtures deterministic: auto-title is on by
// default in production, so an unrelated test would otherwise race an extra
// non-streaming request. Dedicated title tests opt in with autoTitleInline.
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

// TestAutoTitleGeneratesTitle: after the first exchange the configured title
// model is asked for a short title (a non-streaming side call) and the
// truncated-text fallback is replaced. Failure keeps the fallback.
func TestAutoTitleGeneratesTitle(t *testing.T) {
	autoTitleInline(t)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAllBody(r)
		bodies = append(bodies, body)
		if strings.Contains(body, `"stream":false`) {
			// Title request: plain completion.
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
	// Auto-title is opt-in by default: enable it for this test.
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
	// Pin the conversation to another model: the title side call should use
	// the explicit title model, not the session override.
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
			// The title request went through the configured title model.
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

// TestAutoTitleDisabledKeepsFallback: turning the setting off (or a failing
// side call) keeps the truncated first-message fallback.
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

// TestGenerateTitleSkipsReasoningContent pins the reported thinking-model
// behavior: the title call receives a reasoning part plus the final answer,
// but the sidebar must use only the answer. The fake also reproduces the old
// 40-token cap, where reasoning consumed the whole budget and no title existed.
func TestGenerateTitleSkipsReasoningContent(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = readAllBody(r)
		var req struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.Unmarshal([]byte(seen), &req)
		if req.MaxTokens < 256 {
			// Mirrors the live relay under the old budget: thinking consumed
			// all 40 output tokens and content stayed empty.
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

// TestAutoTitleUsesMainModelByDefault pins the reported behavior: without an
// explicit autoTitle setting, a chat pinned to a secondary model still gets a
// refined title from the global default ("main") model.
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

// TestRefineSessionTitleCleansExistingFallback covers sessions created before
// auto-title or with a raw slash-command fallback: the explicit action reads
// the stored first user message/latest reply and rewrites the title.
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
	// auto-title is suppressed by TestMain, so the row still has the raw
	// fallback until the explicit action runs.
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
