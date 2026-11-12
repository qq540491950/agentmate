package services

import (
	"net/http"
	"testing"
)

// TestRunRegistersBeforeUserEvent pins the ordering that keeps IsRunning true
// for a just-started turn. The frontend probes IsRunning while loading a new
// session; if registration happens only after the user event, that probe can
// return false and hide the live turn.
func TestRunRegistersBeforeUserEvent(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}

	var sawUser bool
	var runningAtUser bool
	appEventHook = func(ev ChatStreamEvent) {
		if ev.Kind == "user" {
			sawUser = true
			runningAtUser = chat.IsRunning(ev.SessionID)
		}
	}
	t.Cleanup(func() { appEventHook = nil })

	if err := chat.Send(sess.ID, "run start probe", nil); err != nil {
		t.Fatal(err)
	}
	if !sawUser {
		t.Fatal("expected a user stream event")
	}
	if !runningAtUser {
		t.Fatal("expected IsRunning=true when the user event is emitted")
	}
	if chat.IsRunning(sess.ID) {
		t.Fatal("run handle should be released after Send returns")
	}
}
