package services

import (
	"net/http"
	"testing"

	"agentmate/internal/config"
)

func TestCompactionTokenThreshold(t *testing.T) {
	tests := []struct {
		name          string
		contextWindow int
		percent       int
		want          int
	}{
		{name: "default 65 percent", contextWindow: 128000, percent: 65, want: 83200},
		{name: "rounds down", contextWindow: 1001, percent: 65, want: 650},
		{name: "zero percent disables", contextWindow: 128000, percent: 0, want: 0},
		{name: "unknown context disables", contextWindow: 0, percent: 65, want: 0},
		{name: "clamps over 100", contextWindow: 1000, percent: 140, want: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactionTokenThreshold(tt.contextWindow, tt.percent); got != tt.want {
				t.Fatalf("compactionTokenThreshold(%d, %d) = %d, want %d", tt.contextWindow, tt.percent, got, tt.want)
			}
		})
	}
}

func TestCompactionPercentUsesEffectiveModelContextWindow(t *testing.T) {
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	percent := 65
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Providers[0].Models[0].ContextWindow = 128000
		cfg.Settings.CompactionContextPercent = &percent
	}); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	// A valid percent + context window must produce a compaction config that
	// ADK accepts even when the prompt does not reach the threshold yet.
	if err := chat.Send(sess.ID, "你好", nil); err != nil {
		t.Fatalf("turn with percent compaction failed: %v", err)
	}
}
