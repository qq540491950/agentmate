package services

import (
	"net/http"
	"testing"
)

// TestMessagePhaseSeparatesCommentaryFromFinalAnswer 固定实时流的语义：同一模型
// 响应含工具调用时，文本是过程旁白；工具循环结束后的纯文本才是最终答复。
func TestMessagePhaseSeparatesCommentaryFromFinalAnswer(t *testing.T) {
	turn := 0
	chat, _, _ := newDirectChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(
				sseText("我先查看工作区。") +
					sseToolCall("phase_call", "list_workspace_files", `"{}"`) +
					sseDone,
			))
			return
		}
		_, _ = w.Write([]byte(sseText("工作区已确认。") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "看看工作区", nil); err != nil {
		t.Fatal(err)
	}

	var commentary, final bool
	for _, ev := range *events {
		if ev.Kind != "message" {
			continue
		}
		switch ev.Phase {
		case messagePhaseCommentary:
			commentary = true
		case messagePhaseFinal:
			final = true
		}
	}
	if !commentary || !final {
		t.Fatalf("message phases missing: commentary=%v final=%v events=%+v", commentary, final, *events)
	}
}
