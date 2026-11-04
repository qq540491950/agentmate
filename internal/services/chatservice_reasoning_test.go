package services

import (
	"net/http"
	"strings"
	"testing"

	"go-workbuddy/internal/config"
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
