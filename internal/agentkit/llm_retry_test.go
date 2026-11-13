package agentkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// TestOpenAICompatRetriesTransient503: 在任何响应字节返回之前出现 503
// 时会重试， 模型会在下一次尝试时恢复。
func TestOpenAICompatRetriesTransient503(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"恢复\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
	var text string
	var lastErr error
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			lastErr = err
			continue
		}
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p != nil {
					text += p.Text
				}
			}
		}
	}
	if lastErr != nil {
		t.Fatalf("unexpected error: %v", lastErr)
	}
	// SSE 模式会同时产生部分事件和最终聚合事件，因此文本
	// 可能会在各事件之间重复；我们仅需要恢复内容出现即可。
	if !strings.Contains(text, "恢复") || calls.Load() != 3 {
		t.Fatalf("retry recovery wrong: text=%q calls=%d", text, calls.Load())
	}
}
