package services

import (
	"net/http"
	"testing"
)

// TestRunRegistersBeforeUserEvent 固定了顺序：使刚启动的回合中 IsRunning 保持
// true。前端在加载新会话时会探测
// IsRunning；如果注册仅在用户事件之后发生，该探测可能返回
// false，并隐藏进行中的回合。
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
