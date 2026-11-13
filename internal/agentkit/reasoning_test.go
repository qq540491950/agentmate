package agentkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"agentmate/internal/config"
)

func openAIReasoningProvider(baseURL string) config.ModelProvider {
	return config.ModelProvider{
		ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, BaseURL: baseURL,
		Models: []config.ModelInfo{{
			ID: "deepseek-v3.2",
			Reasoning: &config.ReasoningConfig{
				Enabled:      true,
				DefaultLevel: "high",
				Levels: []config.ReasoningLevel{
					{Level: "off", Param: "thinking", Value: `{"type":"disabled"}`},
					{Level: "high", Param: "reasoning_effort", Value: "high"},
				},
			},
		}},
	}
}

func captureOpenAI(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	captured := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func TestReasoningInjectionOpenAI(t *testing.T) {
	srv, captured := captureOpenAI(t)
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}}

	// 未指定级别：发送模型配置的默认值（high）。
	llm, err := NewModelFromProviderWithReasoning(openAIReasoningProvider(srv.URL), "deepseek-v3.2", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := (*captured)["reasoning_effort"]; got != "high" {
		t.Fatalf("default reasoning_effort = %v, want high", got)
	}
	if _, ok := (*captured)["thinking"]; ok {
		t.Fatalf("unexpected thinking field: %v", (*captured)["thinking"])
	}

	// 会话级覆盖优先，并且可以完全切换形态。
	llm, err = NewModelFromProviderWithReasoning(openAIReasoningProvider(srv.URL), "deepseek-v3.2", "off")
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	thinking, ok := (*captured)["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("off thinking = %v", (*captured)["thinking"])
	}
	if _, ok := (*captured)["reasoning_effort"]; ok {
		t.Fatalf("off level must not send reasoning_effort: %v", (*captured)["reasoning_effort"])
	}

	// 未知级别会被忽略，而不是发送过期的覆盖值。
	llm, err = NewModelFromProviderWithReasoning(openAIReasoningProvider(srv.URL), "deepseek-v3.2", "turbo")
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := (*captured)["reasoning_effort"]; ok {
		t.Fatalf("unknown level must not inject fields: %v", *captured)
	}
}

func TestReasoningInjectionAnthropicAdaptive(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	p := config.ModelProvider{
		ID: "p1", Name: "Claude", Protocol: config.ProtocolAnthropic, BaseURL: srv.URL,
		Models: []config.ModelInfo{{
			ID: "claude-sonnet-4-6",
			Reasoning: &config.ReasoningConfig{
				Enabled:      true,
				DefaultLevel: "high",
				Levels: []config.ReasoningLevel{{
					Level: "high",
					Value: `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`,
				}},
			},
		}},
	}
	llm, err := NewModelFromProviderWithReasoning(p, "claude-sonnet-4-6", "")
	if err != nil {
		t.Fatal(err)
	}
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	thinking, ok := captured["thinking"].(map[string]any)
	if !ok || thinking["type"] != "adaptive" {
		t.Fatalf("thinking = %v", captured["thinking"])
	}
	output, ok := captured["output_config"].(map[string]any)
	if !ok || output["effort"] != "high" {
		t.Fatalf("output_config = %v", captured["output_config"])
	}
	if _, ok := captured["max_tokens"]; !ok {
		t.Fatal("anthropic request lost max_tokens")
	}
}

func TestReasoningExtraBodyValidation(t *testing.T) {
	mi := config.ModelInfo{
		ID: "m",
		Reasoning: &config.ReasoningConfig{
			Enabled: true, DefaultLevel: "bad",
			Levels: []config.ReasoningLevel{{Level: "bad", Value: "high"}},
		},
	}
	if _, err := reasoningExtraBody(mi, ""); err == nil {
		t.Fatal("empty Param with a non-object value must be rejected")
	}

	mi = config.ModelInfo{
		ID: "m",
		Reasoning: &config.ReasoningConfig{
			Enabled: true, DefaultLevel: "nested",
			Levels: []config.ReasoningLevel{{Level: "nested", Param: "thinking.budget_tokens", Value: "4096"}},
		},
	}
	extra, err := reasoningExtraBody(mi, "")
	if err != nil {
		t.Fatal(err)
	}
	thinking, ok := extra["thinking"].(map[string]any)
	if !ok || thinking["budget_tokens"] != float64(4096) {
		t.Fatalf("nested extra = %#v", extra)
	}

	// 普通标量在不是有效 JSON 时仍保持为字符串。
	mi.Reasoning.Levels = []config.ReasoningLevel{{Level: "nested", Param: "reasoning_effort", Value: "high"}}
	extra, _ = reasoningExtraBody(mi, "")
	if extra["reasoning_effort"] != "high" {
		t.Fatalf("scalar extra = %#v", extra)
	}
}

func TestApplyModelOutputLimit(t *testing.T) {
	p := config.ModelProvider{Models: []config.ModelInfo{{ID: "m", MaxOutputTokens: 8192}}}
	ac := config.AgentConfig{Model: "m"}
	applyModelOutputLimit(&ac, p)
	if ac.MaxTokens != 8192 {
		t.Fatalf("fallback = %d, want 8192", ac.MaxTokens)
	}
	ac = config.AgentConfig{Model: "m", MaxTokens: 1024}
	applyModelOutputLimit(&ac, p)
	if ac.MaxTokens != 1024 {
		t.Fatalf("explicit agent limit overwritten: %d", ac.MaxTokens)
	}
	ac = config.AgentConfig{Model: "unknown"}
	applyModelOutputLimit(&ac, p)
	if ac.MaxTokens != 0 {
		t.Fatalf("unknown model got a limit: %d", ac.MaxTokens)
	}
}

// TestReasoningFallbackOnlyExplicit 锁定升级契约：未保存级别表的推理系列（级别表出现之前写入的配置状态，以及报告
// 者的
// `codearts/glm-5.3-flash`）会为选择器提供级别，但在实际选择级别之前不发送任何
// 内容。
func TestReasoningFallbackOnlyExplicit(t *testing.T) {
	srv, captured := captureOpenAI(t)
	provider := config.ModelProvider{
		ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, BaseURL: srv.URL,
		Models: []config.ModelInfo{{ID: "codearts/glm-5.3-flash"}},
	}
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}}
	send := func(level string) {
		t.Helper()
		llm, err := NewModelFromProviderWithReasoning(provider, "codearts/glm-5.3-flash", level)
		if err != nil {
			t.Fatal(err)
		}
		for _, err := range llm.GenerateContent(context.Background(), req, false) {
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	send("")
	if _, ok := (*captured)["reasoning_effort"]; ok {
		t.Fatalf("fallback sent an implicit default: %v", *captured)
	}
	send("max")
	if got := (*captured)["reasoning_effort"]; got != "max" {
		t.Fatalf("explicit fallback level not sent: %v", *captured)
	}
}
