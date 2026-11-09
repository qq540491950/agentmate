package services

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestThinkingStreamsAndPersists covers issue #46: a provider that returns
// reasoning (DeepSeek's reasoning_content) must produce a streamed thinking
// block, then a durable history message before the answer.
func TestThinkingStreamsAndPersists(t *testing.T) {
	chat, events, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"先分析问题"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"，再给出结论。"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(sseText("结论如下。") + sseDone))
	})

	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "需要推理的问题", nil); err != nil {
		t.Fatal(err)
	}

	var deltas, finals []string
	messageBeforeThinking := false
	for _, ev := range *events {
		switch ev.Kind {
		case "thinking_delta":
			deltas = append(deltas, ev.Text)
		case "thinking":
			finals = append(finals, ev.Text)
		case "message":
			if len(finals) == 0 {
				messageBeforeThinking = true
			}
		}
	}
	const want = "先分析问题，再给出结论。"
	if got := strings.Join(deltas, ""); got != want {
		t.Fatalf("streamed thinking = %q", got)
	}
	if len(finals) != 1 || finals[0] != want {
		t.Fatalf("final thinking events = %q", finals)
	}
	if messageBeforeThinking {
		t.Fatal("answer message arrived before the thinking block closed")
	}

	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var historyThinking string
	for _, m := range msgs {
		kinds = append(kinds, m.Kind)
		if m.Kind == "thinking" {
			historyThinking += m.Text
		}
	}
	if historyThinking != want {
		t.Fatalf("persisted thinking = %q (kinds %v)", historyThinking, kinds)
	}
	ti, ai := slices.Index(kinds, "thinking"), slices.Index(kinds, "assistant")
	if ti < 0 || ai < 0 || ti > ai {
		t.Fatalf("thinking must render before the answer: kinds=%v", kinds)
	}
}
