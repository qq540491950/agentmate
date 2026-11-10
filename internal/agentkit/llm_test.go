package agentkit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// sseServer hosts a fake OpenAI-compatible /chat/completions endpoint that
// streams the given chunks as SSE, then [DONE].
func sseServer(t *testing.T, chunks []string, captures *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captures != nil {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			*captures = body
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
}

func TestOpenAICompatStreamingText(t *testing.T) {
	srv := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"你好"}}]}`,
		`{"choices":[{"delta":{"content":"，世界"}}]}`,
	}, nil)
	defer srv.Close()

	llm, err := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}
	var collected strings.Builder
	finals := 0
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Partial {
			for _, p := range resp.Content.Parts {
				collected.WriteString(p.Text)
			}
		} else {
			finals++
		}
	}
	if collected.String() != "你好，世界" {
		t.Fatalf("streamed text = %q", collected.String())
	}
	if finals != 1 {
		t.Fatalf("expected exactly 1 final response, got %d", finals)
	}
}

// TestOpenAICompatStreamsReasoningContent pins DeepSeek's reasoning_content
// handling: deltas must surface as Thought parts (the UI's thinking block)
// and the aggregated final response must keep the full reasoning.
func TestOpenAICompatStreamsReasoningContent(t *testing.T) {
	srv := sseServer(t, []string{
		`{"choices":[{"delta":{"reasoning_content":"先确认天气"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"，再回答。"}}]}`,
		`{"choices":[{"delta":{"content":"北京 25 度。"}}]}`,
	}, nil)
	defer srv.Close()

	llm, err := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "deepseek-reasoner"})
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("北京天气", genai.RoleUser)},
	}
	var thoughts, texts strings.Builder
	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Partial {
			for _, p := range resp.Content.Parts {
				if p.Thought {
					thoughts.WriteString(p.Text)
				} else {
					texts.WriteString(p.Text)
				}
			}
			continue
		}
		final = resp
	}
	if got := thoughts.String(); got != "先确认天气，再回答。" {
		t.Fatalf("streamed reasoning = %q", got)
	}
	if got := texts.String(); got != "北京 25 度。" {
		t.Fatalf("streamed answer = %q", got)
	}
	if final == nil {
		t.Fatal("missing final response")
	}
	var finalThought, finalText string
	for _, p := range final.Content.Parts {
		if p.Thought {
			finalThought = p.Text
		} else {
			finalText += p.Text
		}
	}
	if finalThought != "先确认天气，再回答。" || finalText != "北京 25 度。" {
		t.Fatalf("final response thought=%q text=%q", finalThought, finalText)
	}
}

// TestOpenAICompatDoesNotReplayReasoning guards the other half of the fix:
// reasoning captured for display must never be sent back as assistant text.
// DeepSeek rejects its own reasoning_content on the next request.
func TestOpenAICompatDoesNotReplayReasoning(t *testing.T) {
	llm, err := NewOpenAICompat(OpenAICompatConfig{BaseURL: "http://127.0.0.1:1", Model: "deepseek-reasoner"})
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{Contents: []*genai.Content{{
		Role:  genai.RoleModel,
		Parts: []*genai.Part{{Text: "内部推理", Thought: true}, {Text: "回答"}},
	}}}
	msgs := llm.(*openaiCompatLLM).buildMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("expected one message, got %d", len(msgs))
	}
	content, _ := msgs[0].Content.(string)
	if content != "回答" {
		t.Fatalf("assistant replay content = %q", content)
	}
}

func TestOpenAICompatToolCallRoundTrip(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if captured == nil {
			captured = body
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if msgs, _ := body["messages"].([]any); len(msgs) > 0 {
			last := msgs[len(msgs)-1].(map[string]any)
			if last["role"] == "tool" {
				_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"北京今天 25 度，晴。"}}]}` + "\n\n"))
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				return
			}
		}
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}}]}}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	llm, err := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}

	temp := float32(0.5)
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("北京天气怎么样", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{
			Temperature:       &temp,
			SystemInstruction: genai.NewContentFromText("你是天气助手", genai.RoleUser),
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        "get_weather",
				Description: "query weather",
				Parameters: &genai.Schema{
					Type:       genai.TypeObject,
					Properties: map[string]*genai.Schema{"city": {Type: genai.TypeString}},
					Required:   []string{"city"},
				},
			}}}},
		},
	}

	// Turn 1: model should emit a function call.
	var call *genai.FunctionCall
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if !resp.Partial && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p.FunctionCall != nil {
					call = p.FunctionCall
				}
			}
		}
	}
	if call == nil || call.Name != "get_weather" || call.ID != "call_1" {
		t.Fatalf("expected get_weather function call, got %+v", call)
	}
	if call.Args["city"] != "北京" {
		t.Fatalf("expected city=北京, got %v", call.Args)
	}

	// Verify tools reached the request payload.
	if captured["tools"] == nil {
		t.Fatal("tools missing from request payload")
	}
	if captured["temperature"] == nil {
		t.Fatal("temperature missing from request payload")
	}

	// Turn 2: send the function response back; model should answer in text.
	req.Contents = append(req.Contents,
		&genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: call}}},
		&genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID: call.ID, Name: call.Name, Response: map[string]any{"temp": "25", "sky": "晴"},
		}}}},
	)
	var answer strings.Builder
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Partial {
			answer.WriteString(resp.Content.Parts[0].Text)
		}
	}
	if !strings.Contains(answer.String(), "25") {
		t.Fatalf("expected answer to contain 25, got %q", answer.String())
	}
}

func TestAnthropicStreamingTextAndTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"你好\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_time\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"tz\\\":\\\"Asia/Shanghai\\\"}\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	llm, err := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, Model: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("现在几点", genai.RoleUser)},
	}
	var text strings.Builder
	var call *genai.FunctionCall
	final := false
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp.Partial {
			text.WriteString(resp.Content.Parts[0].Text)
		} else {
			final = true
			for _, p := range resp.Content.Parts {
				if p.FunctionCall != nil {
					call = p.FunctionCall
				}
			}
		}
	}
	if text.String() != "你好" {
		t.Fatalf("text = %q", text.String())
	}
	if !final {
		t.Fatal("missing final response")
	}
	if call == nil || call.Name != "get_time" || call.Args["tz"] != "Asia/Shanghai" {
		t.Fatalf("bad function call: %+v", call)
	}
}

func TestOpenAICompatUsageExtraction(t *testing.T) {
	// Non-streaming: top-level usage.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
	var got *genai.GenerateContentResponseUsageMetadata
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil {
			got = resp.UsageMetadata
		}
	}
	if got == nil || got.PromptTokenCount != 10 || got.CandidatesTokenCount != 5 || got.TotalTokenCount != 15 {
		t.Fatalf("non-stream usage = %+v", got)
	}
	if got.CachedContentTokenCount != 0 {
		t.Fatalf("no cache info should stay 0, got %d", got.CachedContentTokenCount)
	}

	// Streaming: usage arrives on a choices-less chunk via stream_options.
	var askedForUsage bool
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, askedForUsage = body["stream_options"]
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"hey"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv2.Close()
	llm2, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv2.URL, Model: "m"})
	got = nil
	for resp, err := range llm2.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil && !resp.Partial {
			got = resp.UsageMetadata
		}
	}
	if !askedForUsage {
		t.Fatal("stream request missing stream_options.include_usage")
	}
	if got == nil || got.TotalTokenCount != 10 {
		t.Fatalf("stream usage = %+v", got)
	}
}

// TestOpenAICompatStreamUsageOnContentChunk pins usage parsing for gateways
// that attach the usage object to the final content chunk instead of sending
// a choices-less trailer — the trailer-only capture dropped those numbers and
// left such turns without any usage data.
func TestOpenAICompatStreamUsageOnContentChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"hey"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
	var got *genai.GenerateContentResponseUsageMetadata
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil && !resp.Partial {
			got = resp.UsageMetadata
		}
	}
	if got == nil || got.PromptTokenCount != 7 || got.TotalTokenCount != 10 {
		t.Fatalf("stream usage on content chunk = %+v", got)
	}
}

// TestOpenAICompatCacheUsageExtraction pins the cached-prompt passthrough for
// the two spellings gateways use: the nested prompt_tokens_details.cached_tokens
// (OpenAI) and the flat cached_tokens (several Chinese gateways). Without this
// the composer's「背景信息窗口」bubble could never show a cache hit.
func TestOpenAICompatCacheUsageExtraction(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int32
	}{
		{
			"nested",
			`{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,"prompt_tokens_details":{"cached_tokens":768}}}`,
			768,
		},
		{
			"flat",
			`{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,"cached_tokens":512}}`,
			512,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
			req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
			var got *genai.GenerateContentResponseUsageMetadata
			for resp, err := range llm.GenerateContent(context.Background(), req, false) {
				if err != nil {
					t.Fatal(err)
				}
				if resp != nil {
					got = resp.UsageMetadata
				}
			}
			if got == nil {
				t.Fatal("no usage")
			}
			if got.CachedContentTokenCount != tc.want {
				t.Fatalf("cached tokens = %d, want %d", got.CachedContentTokenCount, tc.want)
			}
			if got.PromptTokenCount != 1000 {
				t.Fatalf("prompt tokens = %d, want the full 1000 (cache is a subset)", got.PromptTokenCount)
			}
		})
	}
}

func TestAnthropicUsageExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()
	llm, _ := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, Model: "claude-test"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
	var got *genai.GenerateContentResponseUsageMetadata
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil && !resp.Partial {
			got = resp.UsageMetadata
		}
	}
	if got == nil || got.PromptTokenCount != 11 || got.CandidatesTokenCount != 4 || got.TotalTokenCount != 15 {
		t.Fatalf("anthropic usage = %+v", got)
	}
}

// TestAnthropicCacheUsageExtraction pins prompt-cache accounting: the read
// counter rides on message_start in the documented shape, and some proxies fold
// it into message_delta instead. It is a SUBSET of input_tokens, so the prompt
// total must stay unchanged.
func TestAnthropicCacheUsageExtraction(t *testing.T) {
	cases := []struct {
		name       string
		startUsage string
		deltaUsage string
	}{
		{
			name:       "message_start",
			startUsage: `{"input_tokens":1000,"cache_read_input_tokens":900,"cache_creation_input_tokens":40}`,
			deltaUsage: `{"output_tokens":7}`,
		},
		{
			name:       "message_delta",
			startUsage: `{"input_tokens":1000}`,
			deltaUsage: `{"output_tokens":7,"cache_read_input_tokens":900}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":" + tc.startUsage + "}}\n\n"))
				_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
				_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":" + tc.deltaUsage + "}\n\n"))
				_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			}))
			defer srv.Close()
			llm, _ := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, Model: "claude-test"})
			req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
			var got *genai.GenerateContentResponseUsageMetadata
			for resp, err := range llm.GenerateContent(context.Background(), req, true) {
				if err != nil {
					t.Fatal(err)
				}
				if resp != nil && !resp.Partial {
					got = resp.UsageMetadata
				}
			}
			if got == nil {
				t.Fatal("no usage")
			}
			if got.PromptTokenCount != 1000 || got.CandidatesTokenCount != 7 {
				t.Fatalf("usage = %+v", got)
			}
			if got.CachedContentTokenCount != 900 {
				t.Fatalf("cached tokens = %d, want 900", got.CachedContentTokenCount)
			}
		})
	}
}

func TestOpenAICompatHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)}}
	errGot := ""
	for _, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			errGot = err.Error()
		}
	}
	if !strings.Contains(errGot, "401") {
		t.Fatalf("expected 401 error, got %q", errGot)
	}
}

func TestOpenAICompatImageAttachment(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"图里是一只猫"}}]}`))
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "vision"})
	png := []byte{0x89, 'P', 'N', 'G'}
	req := &model.LLMRequest{Contents: []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{Text: "这张图里是什么？"},
			{InlineData: &genai.Blob{MIMEType: "image/png", Data: png}},
		},
	}}}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	msgs := captured["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	parts := last["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(parts))
	}
	p0 := parts[0].(map[string]any)
	if p0["type"] != "text" || p0["text"] != "这张图里是什么？" {
		t.Fatalf("text part = %v", p0)
	}
	p1 := parts[1].(map[string]any)
	if p1["type"] != "image_url" {
		t.Fatalf("image part = %v", p1)
	}
	url := p1["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("image url = %q", url)
	}
	if !strings.Contains(url, base64.StdEncoding.EncodeToString(png)) {
		t.Fatal("image payload mismatch")
	}
}

func TestAnthropicImageAttachment(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"好图"}]}`))
	}))
	defer srv.Close()
	llm, _ := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, Model: "claude-vision"})
	req := &model.LLMRequest{Contents: []*genai.Content{{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{InlineData: &genai.Blob{MIMEType: "image/jpeg", Data: []byte{0xFF, 0xD8}},
				Text: "看看"},
		},
	}}}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	msgs := captured["messages"].([]any)
	m0 := msgs[0].(map[string]any)
	blocks := m0["content"].([]any)
	var img map[string]any
	for _, b := range blocks {
		bb := b.(map[string]any)
		if bb["type"] == "image" {
			img = bb
		}
	}
	if img == nil {
		t.Fatalf("no image block: %v", blocks)
	}
	src := img["source"].(map[string]any)
	if src["media_type"] != "image/jpeg" || src["type"] != "base64" {
		t.Fatalf("image source = %v", src)
	}
}

// TestPostWithRetryRetries429: rate limiting is transient — the request is
// retried instead of failing the turn, and a Retry-After header raises the
// wait (capped so a turn keeps its timeout budget).
func TestPostWithRetryRetries429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	build := func() (*http.Request, error) { return http.NewRequest(http.MethodPost, srv.URL, nil) }
	resp, err := postWithRetry(context.Background(), build, "m", "test")
	if err != nil {
		t.Fatalf("postWithRetry: %v", err)
	}
	defer resp.Body.Close()
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (two 429 retries)", attempts)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// TestPostWithRetryUsesFiveRetries locks the built-in retry budget: five
// retries after the initial attempt, with no user-facing configuration.
func TestPostWithRetryUsesFiveRetries(t *testing.T) {
	if maxProviderRetries != 5 || len(retryDelays) != maxProviderRetries {
		t.Fatalf("retry budget = %d, delays = %d, want 5", maxProviderRetries, len(retryDelays))
	}
	oldDelays := retryDelays
	retryDelays = make([]time.Duration, maxProviderRetries)
	t.Cleanup(func() { retryDelays = oldDelays })

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= maxProviderRetries {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	build := func() (*http.Request, error) { return http.NewRequest(http.MethodPost, srv.URL, nil) }
	resp, err := postWithRetry(context.Background(), build, "m", "test")
	if err != nil {
		t.Fatalf("postWithRetry: %v", err)
	}
	defer resp.Body.Close()
	if attempts != maxProviderRetries+1 {
		t.Fatalf("attempts = %d, want %d (initial + five retries)", attempts, maxProviderRetries+1)
	}
}

func TestRetryAfterDelayCap(t *testing.T) {
	if got := retryAfterDelay(""); got != 0 {
		t.Fatalf("empty header = %v", got)
	}
	if got := retryAfterDelay("abc"); got != 0 {
		t.Fatalf("junk header = %v", got)
	}
	if got := retryAfterDelay("2"); got != 2*time.Second {
		t.Fatalf("2s header = %v", got)
	}
	if got := retryAfterDelay("3600"); got != maxRetryAfter {
		t.Fatalf("huge header = %v, want cap %v", got, maxRetryAfter)
	}
}

// TestStopSequencesWireFormat: the configured stop sequences reach both
// providers under their native field names (OpenAI "stop", Anthropic
// "stop_sequences").
func TestStopSequencesWireFormat(t *testing.T) {
	var oaBody map[string]any
	oaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&oaBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer oaSrv.Close()
	oa, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: oaSrv.URL, Model: "m"})
	req := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{StopSequences: []string{"END", "STOP"}},
	}
	for _, err := range oa.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	stop, ok := oaBody["stop"].([]any)
	if !ok || len(stop) != 2 || stop[0] != "END" {
		t.Fatalf("openai stop = %v", oaBody["stop"])
	}

	var anBody map[string]any
	anSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&anBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`))
	}))
	defer anSrv.Close()
	an, _ := NewAnthropic(AnthropicConfig{BaseURL: anSrv.URL, APIKey: "k", Model: "m"})
	for _, err := range an.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	seqs, ok := anBody["stop_sequences"].([]any)
	if !ok || len(seqs) != 2 || seqs[1] != "STOP" {
		t.Fatalf("anthropic stop_sequences = %v", anBody["stop_sequences"])
	}
}

// TestOpenAICompatBrokenToolArgsSkipped: truncated/corrupt argument fragments
// must not reach the tool as silent zero-value {} arguments — the call is
// skipped and the reply text says so.
func TestOpenAICompatBrokenToolArgsSkipped(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"write_workspace_file","arguments":"{\"path\":\"x"}}]}}]}`,
	}
	srv := sseServer(t, chunks, nil)
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("go", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name: "write_workspace_file",
		}}}}},
	}, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("no final response")
	}
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil {
			t.Fatalf("broken-args call leaked to the model: %+v", p.FunctionCall)
		}
	}
	if !strings.Contains(final.Content.Parts[0].Text, "参数流损坏") {
		t.Fatalf("expected skip note in text: %q", final.Content.Parts[0].Text)
	}
}

// holdOpenServer answers once and then keeps the response body open until the
// test releases it — the shape of a gateway/proxy that never signals EOF after
// the stream's terminator.
func holdOpenServer(t *testing.T, send func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		send(w)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv
}

// TestOpenAICompatStopsAtDone pins issue #23: [DONE] terminates the stream.
// Providers/gateways that keep the HTTP response open afterwards used to leave
// the adapter blocked until the whole-turn deadline, with no final response —
// no answer, no tool call, just an endless "正在回复…".
func TestOpenAICompatStopsAtDone(t *testing.T) {
	srv := holdOpenServer(t, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"完成"}}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	var text strings.Builder
	finals := 0
	for resp, err := range llm.GenerateContent(ctx, &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Partial {
			text.WriteString(resp.Content.Parts[0].Text)
		} else {
			finals++
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("adapter waited for EOF instead of stopping at [DONE] (%v)", elapsed)
	}
	if text.String() != "完成" || finals != 1 {
		t.Fatalf("text=%q finals=%d", text.String(), finals)
	}
}

func TestAnthropicStopsAtMessageStop(t *testing.T) {
	srv := holdOpenServer(t, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"完成\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	})
	llm, _ := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	var text strings.Builder
	for resp, err := range llm.GenerateContent(ctx, &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Partial {
			text.WriteString(resp.Content.Parts[0].Text)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("adapter waited for EOF instead of stopping at message_stop (%v)", elapsed)
	}
	if text.String() != "完成" {
		t.Fatalf("text=%q", text.String())
	}
}

// TestOpenAICompatStreamRequestAnsweredWithJSONError: a gateway that answers
// the stream request with a plain JSON error object (the reported ECONNRESET
// case) must surface that error — not end the turn with an empty answer.
func TestOpenAICompatStreamRequestAnsweredWithJSONError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"ECONNRESET: The socket connection was closed unexpectedly"}}`))
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})

	var got error
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}, true) {
		if err != nil {
			got = err
		}
	}
	if got == nil || !strings.Contains(got.Error(), "ECONNRESET") || !strings.Contains(got.Error(), "模型供应商错误") {
		t.Fatalf("error = %v", got)
	}
}

func TestAnthropicStreamRequestAnsweredWithJSONError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
	}))
	defer srv.Close()
	llm, _ := NewAnthropic(AnthropicConfig{BaseURL: srv.URL, APIKey: "k", Model: "m"})

	var got error
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}, true) {
		if err != nil {
			got = err
		}
	}
	if got == nil || !strings.Contains(got.Error(), "overloaded") {
		t.Fatalf("error = %v", got)
	}
}

// TestOpenAICompatStreamRequestAnsweredWithJSONCompletion: some gateways ignore
// stream=true and return the whole completion as JSON. Both tool calls must
// survive (complete responses have no per-call index field).
func TestOpenAICompatStreamRequestAnsweredWithJSONCompletion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"好的","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"read_workspace_file","arguments":"{\"path\":\"README.md\"}"}},
			{"id":"c2","type":"function","function":{"name":"list_workspace_files","arguments":"{}"}}
		]}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`))
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})

	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resp.Partial {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("no final response")
	}
	var calls []*genai.FunctionCall
	text := ""
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil {
			calls = append(calls, p.FunctionCall)
		}
		text += p.Text
	}
	if text != "好的" || len(calls) != 2 {
		t.Fatalf("text=%q calls=%+v", text, calls)
	}
	if calls[0].Name != "read_workspace_file" || calls[0].Args["path"] != "README.md" {
		t.Fatalf("first call = %+v", calls[0])
	}
	if calls[1].Name != "list_workspace_files" {
		t.Fatalf("second call merged/lost: %+v", calls[1])
	}
}

// TestStreamIdleTimeoutAbortsStalledProvider: a provider that sends a first
// chunk and then goes silent must not hold the turn open indefinitely.
func TestStreamIdleTimeoutAbortsStalledProvider(t *testing.T) {
	old := streamIdleTimeout
	streamIdleTimeout = 400 * time.Millisecond
	defer func() { streamIdleTimeout = old }()

	srv := holdOpenServer(t, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"半"}}]}` + "\n\n"))
	})
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	var got error
	for _, err := range llm.GenerateContent(ctx, &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}, true) {
		if err != nil {
			got = err
			break
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("stalled stream not aborted in time (%v)", elapsed)
	}
	if got == nil || !strings.Contains(got.Error(), "没有返回任何数据") {
		t.Fatalf("error = %v", got)
	}
}

// TestFunctionParametersFromJSONSchema pins the tool-schema wire format:
// ADK's built-in/MCP/skill tools publish ParametersJsonSchema, and providers
// must receive the real properties instead of an argument-less object.
func TestFunctionParametersFromJSONSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []string{"path"},
	}

	var oaBody map[string]any
	oaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&oaBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer oaSrv.Close()
	oa, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: oaSrv.URL, Model: "m"})
	for _, err := range oa.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:                 "read_workspace_file",
			ParametersJsonSchema: schema,
		}}}}},
	}, true) {
		if err != nil {
			t.Fatal(err)
		}
	}
	oaParams := oaBody["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	if props, _ := oaParams["properties"].(map[string]any); props["path"] == nil {
		t.Fatalf("openai tool parameters lost the schema: %v", oaParams)
	}

	var anBody map[string]any
	anSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&anBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer anSrv.Close()
	an, _ := NewAnthropic(AnthropicConfig{BaseURL: anSrv.URL, APIKey: "k", Model: "m"})
	for _, err := range an.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("x", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:                 "read_workspace_file",
			ParametersJsonSchema: schema,
		}}}}},
	}, true) {
		if err != nil {
			t.Fatal(err)
		}
	}
	anParams := anBody["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any)
	if props, _ := anParams["properties"].(map[string]any); props["path"] == nil {
		t.Fatalf("anthropic tool input_schema lost the schema: %v", anParams)
	}
}

// TestFunctionParametersLegacySchemaStillWorks keeps the genai.Schema path for
// callers that build declarations the old way (tests, remote tool metadata).
func TestFunctionParametersLegacySchemaStillWorks(t *testing.T) {
	got := functionParameters(&genai.FunctionDeclaration{
		Name: "f",
		Parameters: &genai.Schema{
			Type:       genai.TypeObject,
			Properties: map[string]*genai.Schema{"q": {Type: genai.TypeString}},
			Required:   []string{"q"},
		},
	})
	props, _ := got["properties"].(map[string]any)
	if props["q"] == nil || got["type"] != "object" {
		t.Fatalf("legacy schema lost: %v", got)
	}
}

// TestOpenAICompatNonSSEBodySurfacesError: a proxy/block page (HTML instead of
// SSE) must produce a readable error rather than an empty turn.
func TestOpenAICompatNonSSEBodySurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>blocked by proxy</body></html>"))
	}))
	defer srv.Close()
	llm, _ := NewOpenAICompat(OpenAICompatConfig{BaseURL: srv.URL, Model: "m"})

	var got error
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
	}, true) {
		if err != nil {
			got = err
		}
	}
	if got == nil || !strings.Contains(got.Error(), "非 SSE 响应") || !strings.Contains(got.Error(), "blocked by proxy") {
		t.Fatalf("error = %v", got)
	}
}
