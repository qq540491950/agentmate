package services

import (
	"net/http"
	"testing"

	"agentmate/internal/config"
)

func TestSessionExecutionDisplayOverride(t *testing.T) {
	chat, store, _ := newRecordingChat(t, func(http.ResponseWriter, *http.Request) {})
	sess, err := chat.newLegacySession("agent", "agent_r", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := chat.SetSessionExecutionDisplay(sess.ID, "concise"); err != nil {
		t.Fatal(err)
	}
	stored := chat.resolveSessionTarget(store.Get(), sess.ID)
	if stored == nil || stored.ExecutionDisplayMode != config.ExecutionDisplayConcise {
		t.Fatalf("stored display mode = %+v", stored)
	}
	if got := stored.EffectiveExecutionDisplayMode(config.ExecutionDisplayDetailed); got != config.ExecutionDisplayConcise {
		t.Fatalf("session override should win, got %q", got)
	}

	if err := chat.SetSessionExecutionDisplay(sess.ID, ""); err != nil {
		t.Fatal(err)
	}
	stored = chat.resolveSessionTarget(store.Get(), sess.ID)
	if stored == nil || stored.ExecutionDisplayMode != "" {
		t.Fatalf("clearing override left %+v", stored)
	}
	if got := stored.EffectiveExecutionDisplayMode(config.ExecutionDisplayDetailed); got != config.ExecutionDisplayDetailed {
		t.Fatalf("global fallback after clear = %q", got)
	}

	if err := chat.SetSessionExecutionDisplay(sess.ID, "stale"); err == nil {
		t.Fatal("unknown display mode must be rejected")
	}
	if err := chat.SetSessionExecutionDisplay("sess_missing", "auto"); err == nil {
		t.Fatal("unknown session must be rejected")
	}
}
