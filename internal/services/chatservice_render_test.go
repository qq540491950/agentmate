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
