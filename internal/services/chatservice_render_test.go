package services

import (
	"testing"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"agentmate/internal/config"
)

func TestRenderEventKeepsUserTextAndImagesInOneTurn(t *testing.T) {
	ev := &session.Event{
		ID:     "ev1",
		Author: "user",
		LLMResponse: adkmodel.LLMResponse{
			Content: &genai.Content{
				Role: genai.RoleUser,
				Parts: []*genai.Part{
					{Text: "看这两张图"},
					{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte{1}}},
					{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte{2}}},
				},
			},
		},
	}

	out := renderEvent(config.Config{}, ev, 1)
	if len(out) != 1 {
		t.Fatalf("text + images should render one user turn, got %d: %+v", len(out), out)
	}
	msg := out[0]
	if msg.ID != "ev1-1" || msg.Kind != "user" || msg.Text != "看这两张图" {
		t.Fatalf("unexpected user turn: %+v", msg)
	}
	if len(msg.Attachments) != 2 {
		t.Fatalf("want 2 attachments, got %d: %+v", len(msg.Attachments), msg.Attachments)
	}
	if msg.Attachments[0].Name != "attachment-1" || msg.Attachments[1].Name != "attachment-2" {
		t.Fatalf("attachment names = %q, %q", msg.Attachments[0].Name, msg.Attachments[1].Name)
	}
}

func TestRenderEventAttachmentOnlyUserTurn(t *testing.T) {
	ev := &session.Event{
		ID:     "ev2",
		Author: "user",
		LLMResponse: adkmodel.LLMResponse{
			Content: &genai.Content{
				Role: genai.RoleUser,
				Parts: []*genai.Part{
					{InlineData: &genai.Blob{MIMEType: "image/jpeg", Data: []byte{3}}},
					{InlineData: &genai.Blob{MIMEType: "image/jpeg", Data: []byte{4}}},
				},
			},
		},
	}

	out := renderEvent(config.Config{}, ev, 2)
	if len(out) != 1 {
		t.Fatalf("attachment-only event should render one turn, got %d: %+v", len(out), out)
	}
	if out[0].Text != "" || len(out[0].Attachments) != 2 {
		t.Fatalf("unexpected attachment-only turn: %+v", out[0])
	}
}

func TestRenderEventMarksToolNarrationAsCommentary(t *testing.T) {
	ev := &session.Event{
		ID:     "ev3",
		Author: "assistant",
		LLMResponse: adkmodel.LLMResponse{
			Content: &genai.Content{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					{Text: "我先读取文件。"},
					{FunctionCall: &genai.FunctionCall{Name: "read_workspace_file", Args: map[string]any{"path": "a.txt"}}},
				},
			},
		},
	}

	out := renderEvent(config.Config{}, ev, 3)
	if len(out) != 2 {
		t.Fatalf("want narration + tool call, got %d: %+v", len(out), out)
	}
	if out[0].Kind != "assistant" || out[0].Phase != messagePhaseCommentary {
		t.Fatalf("tool narration phase = %q, want commentary: %+v", out[0].Phase, out[0])
	}
	if out[1].Kind != "tool_call" || out[1].ToolName != "read_workspace_file" {
		t.Fatalf("unexpected tool call: %+v", out[1])
	}
}

func TestRenderEventMarksTextOnlyResponseAsFinal(t *testing.T) {
	ev := &session.Event{
		ID:     "ev4",
		Author: "assistant",
		LLMResponse: adkmodel.LLMResponse{
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{Text: "已经完成。"}},
			},
		},
	}

	out := renderEvent(config.Config{}, ev, 4)
	if len(out) != 1 {
		t.Fatalf("want one assistant message, got %d: %+v", len(out), out)
	}
	if out[0].Kind != "assistant" || out[0].Phase != messagePhaseFinal {
		t.Fatalf("text-only phase = %q, want final: %+v", out[0].Phase, out[0])
	}
}
