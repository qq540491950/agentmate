package services

import (
	"net/http"
	"strings"
	"testing"

	"agentmate/internal/config"
)

// TestAgentWithoutExplicitModelRunsOnDefault: an agent created from the
// simplified form carries no provider/model; the session must still run on the
// global default model, and the request body proves which model was used (#39).
func TestAgentWithoutExplicitModelRunsOnDefault(t *testing.T) {
	chat, store, bodies := newRecordingChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("你好，我在。") + sseDone))
	})
	hookEvents(t)

	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers[0].IsDefault = true
		cfg.Agents[0].ProviderID = ""
		cfg.Agents[0].Model = ""
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "你好", nil); err != nil {
		t.Fatalf("send with inherited model failed: %v", err)
	}
	if len(*bodies) == 0 {
		t.Fatal("no model request recorded")
	}
	if !strings.Contains((*bodies)[0], `"model":"fake-mini"`) {
		t.Fatalf("agent should run on the default provider's first model, got %s", (*bodies)[0])
	}
}
