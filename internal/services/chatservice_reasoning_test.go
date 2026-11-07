package services

import (
	"net/http"
	"strings"
	"testing"

	"agentmate/internal/config"
)

// TestSessionReasoningLevel: the chat-level thinking picker persists per
// session and changes the request body; switching models clears the stale
// level so the new model's default takes over.
func TestSessionReasoningLevel(t *testing.T) {
	chat, store, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好") + sseDone))
	})
	hookEvents(t)

	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == "prov_r" {
				cfg.Providers[i].Models[0].Reasoning = &config.ReasoningConfig{
					Enabled:      true,
					DefaultLevel: "high",
					Levels: []config.ReasoningLevel{
						{Level: "off", Param: "thinking", Value: `{"type":"disabled"}`},
						{Level: "high", Param: "reasoning_effort", Value: "high"},
					},
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "默认档", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*bodies)[0], `"reasoning_effort":"high"`) {
		t.Fatalf("model default level not sent: %s", (*bodies)[0])
	}

	if err := chat.SetSessionReasoningLevel(sess.ID, "off"); err != nil {
		t.Fatal(err)
	}
	if got := chat.resolveSessionTarget(store.Get(), sess.ID).ReasoningLevel; got != "off" {
		t.Fatalf("stored reasoning level = %q, want off", got)
	}
	if err := chat.Send(sess.ID, "关思考", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*bodies)[1], `"thinking":{"type":"disabled"}`) ||
		strings.Contains((*bodies)[1], `"reasoning_effort"`) {
		t.Fatalf("session level not sent: %s", (*bodies)[1])
	}

	// Re-picking the same model drops the session level: the model default
	// resumes (and a level valid for the old model must not leak across).
	if err := chat.SetSessionModel(sess.ID, "fake-mini"); err != nil {
		t.Fatal(err)
	}
	if got := chat.resolveSessionTarget(store.Get(), sess.ID).ReasoningLevel; got != "" {
		t.Fatalf("reasoning level survived a model switch: %q", got)
	}
	if err := chat.Send(sess.ID, "恢复默认", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*bodies)[2], `"reasoning_effort":"high"`) {
		t.Fatalf("default not restored after model switch: %s", (*bodies)[2])
	}

	if err := chat.SetSessionReasoningLevel("sess_missing", "high"); err == nil {
		t.Fatal("unknown session must be rejected")
	}
}

// TestPerTurnReasoningLevel covers the per-message picker (#42): the choice
// applies to one turn only, outranks the session default, never rewrites it,
// and is recorded on that turn's user message so history and exports can show
// what was actually sent. Regenerate re-runs the turn with its recorded level.
func TestPerTurnReasoningLevel(t *testing.T) {
	chat, store, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("好") + sseDone))
	})
	events := hookEvents(t)

	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == "prov_r" {
				cfg.Providers[i].Models[0].Reasoning = &config.ReasoningConfig{
					Enabled:      true,
					DefaultLevel: "high",
					Levels: []config.ReasoningLevel{
						{Level: "off", Param: "thinking", Value: `{"type":"disabled"}`},
						{Level: "low", Param: "reasoning_effort", Value: "low"},
						{Level: "high", Param: "reasoning_effort", Value: "high"},
					},
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionReasoningLevel(sess.ID, "high"); err != nil {
		t.Fatal(err)
	}

	// A per-turn pick outranks the session default and is what gets sent.
	if err := chat.SendWithReasoning(sess.ID, "本轮关闭", nil, "off"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*bodies)[0], `"thinking":{"type":"disabled"}`) ||
		strings.Contains((*bodies)[0], `"reasoning_effort"`) {
		t.Fatalf("per-turn level not sent: %s", (*bodies)[0])
	}
	if got := chat.resolveSessionTarget(store.Get(), sess.ID).ReasoningLevel; got != "high" {
		t.Fatalf("per-turn pick leaked into the session default: %q", got)
	}
	var sawUser bool
	for _, ev := range *events {
		if ev.Kind == "user" && ev.Text == "本轮关闭" {
			sawUser = true
			if ev.Reasoning != "off" {
				t.Fatalf("user stream event reasoning = %q, want off", ev.Reasoning)
			}
		}
	}
	if !sawUser {
		t.Fatal("user stream event missing")
	}

	// History records the level on that user message (persisted state delta),
	// and the next turn without a pick follows the session default again.
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var historyLevel string
	for _, m := range msgs {
		if m.Kind == "user" && m.Text == "本轮关闭" {
			historyLevel = m.Reasoning
		}
	}
	if historyLevel != "off" {
		t.Fatalf("history reasoning = %q, want off", historyLevel)
	}

	if err := chat.SendWithReasoning(sess.ID, "跟随会话", nil, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*bodies)[1], `"reasoning_effort":"high"`) {
		t.Fatalf("session default not restored: %s", (*bodies)[1])
	}

	// A stale/unknown pick falls through to the session default rather than
	// sending an arbitrary field.
	if err := chat.SendWithReasoning(sess.ID, "未知档", nil, "turbo"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*bodies)[2], `"reasoning_effort":"high"`) {
		t.Fatalf("unknown level should fall back to the session default: %s", (*bodies)[2])
	}

	// Regenerate re-runs the trimmed turn with the level it recorded.
	if err := chat.Regenerate(sess.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*bodies)[3], `"reasoning_effort":"high"`) {
		t.Fatalf("regenerate lost the recorded level: %s", (*bodies)[3])
	}
}

// TestResolveTurnReasoning pins the precedence and the fallback-table rule
// without spinning up a model: per-turn > session > configured default; a
// built-in fallback records nothing until someone picks a level.
func TestResolveTurnReasoning(t *testing.T) {
	configured := config.ModelInfo{
		ID: "m1",
		Reasoning: &config.ReasoningConfig{
			Enabled:      true,
			DefaultLevel: "high",
			Levels: []config.ReasoningLevel{
				{Level: "low", Param: "reasoning_effort", Value: "low"},
				{Level: "high", Param: "reasoning_effort", Value: "high"},
			},
		},
	}
	if apply, rec := resolveTurnReasoning(configured, "low", "high"); apply != "low" || rec != "low" {
		t.Fatalf("per-turn pick = (%q,%q)", apply, rec)
	}
	if apply, rec := resolveTurnReasoning(configured, "", "low"); apply != "low" || rec != "low" {
		t.Fatalf("session default = (%q,%q)", apply, rec)
	}
	if apply, rec := resolveTurnReasoning(configured, "turbo", ""); apply != "" || rec != "high" {
		t.Fatalf("configured default = (%q,%q)", apply, rec)
	}

	fallback := config.ModelInfo{ID: "codearts/glm-5.3-flash"}
	if apply, rec := resolveTurnReasoning(fallback, "", ""); apply != "" || rec != "" {
		t.Fatalf("fallback without a pick = (%q,%q), want nothing sent or shown", apply, rec)
	}
	if apply, rec := resolveTurnReasoning(fallback, "max", ""); apply != "max" || rec != "max" {
		t.Fatalf("fallback explicit pick = (%q,%q)", apply, rec)
	}

	if apply, rec := resolveTurnReasoning(config.ModelInfo{ID: "llama-3"}, "high", "high"); apply != "" || rec != "" {
		t.Fatalf("model without a table = (%q,%q)", apply, rec)
	}
}
