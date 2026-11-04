package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSuggestModelInfoFamilies(t *testing.T) {
	cases := []struct {
		id         string
		wantNil    bool
		wantLevels []string
	}{
		{id: "deepseek-v3.2", wantLevels: []string{"off", "low", "medium", "high", "max"}},
		{id: "moonshot/kimi-k2.5", wantLevels: []string{"off", "high"}},
		{id: "kimi-k3", wantLevels: []string{"low", "high", "max"}},
		{id: "glm-5.2", wantLevels: []string{"off", "low", "medium", "high", "max"}},
		{id: "glm-5.3-flash", wantLevels: []string{"low", "high", "max"}},
		{id: "claude-3-7-sonnet", wantLevels: []string{"off", "low", "medium", "high", "max"}},
		{id: "claude-sonnet-4-6", wantLevels: []string{"off", "low", "medium", "high", "max"}},
		{id: "gpt-5.2", wantLevels: []string{"off", "low", "medium", "high", "max"}},
		{id: "o3-mini", wantLevels: []string{"off", "low", "medium", "high", "max"}},
		{id: "qwen3-max", wantLevels: []string{"off", "low", "medium", "high", "max"}},
		{id: "llama-3.3-70b", wantNil: true},
		{id: "", wantNil: true},
	}
	for _, tc := range cases {
		info := SuggestModelInfo(tc.id)
		if info.ID != tc.id {
			t.Fatalf("%q: id not copied: %+v", tc.id, info)
		}
		if tc.id != "" && info.DisplayName != tc.id {
			t.Fatalf("%q: display name should start as the id: %+v", tc.id, info)
		}
		if tc.wantNil {
			if info.Reasoning != nil {
				t.Fatalf("%q: expected no reasoning table, got %+v", tc.id, info.Reasoning)
			}
			continue
		}
		if info.Reasoning == nil || !info.Reasoning.Enabled {
			t.Fatalf("%q: expected enabled reasoning table, got %+v", tc.id, info.Reasoning)
		}
		var got []string
		for _, l := range info.Reasoning.Levels {
			got = append(got, l.Level)
		}
		if strings.Join(got, ",") != strings.Join(tc.wantLevels, ",") {
			t.Fatalf("%q: levels = %v, want %v", tc.id, got, tc.wantLevels)
		}
	}
}

func TestSuggestModelInfoRequestShapes(t *testing.T) {
	// OpenAI reasoning families send a scalar reasoning_effort.
	openAI := SuggestModelInfo("gpt-5.2").Reasoning
	param, value, ok := openAI.ActiveReasoning()
	if !ok || param != "reasoning_effort" || value != "medium" {
		t.Fatalf("gpt default = (%q,%q,%v), want reasoning_effort/medium", param, value, ok)
	}

	// Claude 4.6+ needs a JSON object merged at the top level (adaptive
	// thinking + output_config.effort), so Param stays empty.
	claude := SuggestModelInfo("claude-sonnet-4-6").Reasoning
	param, value, ok = claude.ActiveReasoning()
	if !ok || param != "" || !strings.Contains(value, `"output_config"`) {
		t.Fatalf("claude default = (%q,%q,%v), want merged JSON", param, value, ok)
	}

	// DeepSeek's off level disables thinking through a JSON object.
	param, value, ok = SuggestModelInfo("deepseek-v3.2").Reasoning.Resolve("off")
	if !ok || param != "thinking" || value != `{"type":"disabled"}` {
		t.Fatalf("deepseek off = (%q,%q,%v)", param, value, ok)
	}
}

func TestActiveReasoningFallbacks(t *testing.T) {
	var nilCfg *ReasoningConfig
	if _, _, ok := nilCfg.ActiveReasoning(); ok {
		t.Fatal("nil config must not resolve")
	}
	disabled := &ReasoningConfig{Enabled: false, Levels: []ReasoningLevel{{Level: "off"}}}
	if _, _, ok := disabled.ActiveReasoning(); ok {
		t.Fatal("disabled config must not resolve")
	}
	empty := &ReasoningConfig{Enabled: true}
	if _, _, ok := empty.ActiveReasoning(); ok {
		t.Fatal("empty level table must not resolve")
	}
	// Empty DefaultLevel falls back to the first level.
	first := &ReasoningConfig{Enabled: true, Levels: []ReasoningLevel{
		{Level: "high", Param: "reasoning_effort", Value: "high"},
	}}
	if p, v, ok := first.ActiveReasoning(); !ok || p != "reasoning_effort" || v != "high" {
		t.Fatalf("first-level fallback = (%q,%q,%v)", p, v, ok)
	}
	// A DefaultLevel that no longer exists is a configuration error, not a
	// silent switch to another level.
	stale := &ReasoningConfig{Enabled: true, DefaultLevel: "max", Levels: []ReasoningLevel{
		{Level: "high", Param: "reasoning_effort", Value: "high"},
	}}
	if _, _, ok := stale.ActiveReasoning(); ok {
		t.Fatal("unknown default level must not resolve")
	}
}

func TestCloneDeepCopiesModelReasoning(t *testing.T) {
	original := Config{Providers: []ModelProvider{{
		ID:   "p1",
		Name: "P",
		Models: []ModelInfo{{
			ID:          "m1",
			DisplayName: "Model One",
			Reasoning: &ReasoningConfig{
				Enabled:      true,
				DefaultLevel: "high",
				Levels:       []ReasoningLevel{{Level: "high", Param: "reasoning_effort", Value: "high"}},
			},
		}},
	}}}
	clone := original.Clone()
	clone.Providers[0].Models[0].DisplayName = "changed"
	clone.Providers[0].Models[0].Reasoning.DefaultLevel = "low"
	clone.Providers[0].Models[0].Reasoning.Levels[0].Level = "low"
	clone.Providers[0].Models[0].Reasoning.Levels = append(clone.Providers[0].Models[0].Reasoning.Levels, ReasoningLevel{Level: "max"})

	got := original.Providers[0].Models[0]
	if got.DisplayName != "Model One" || got.Reasoning.DefaultLevel != "high" ||
		got.Reasoning.Levels[0].Level != "high" || len(got.Reasoning.Levels) != 1 {
		t.Fatalf("clone mutated original: %+v", got)
	}
	// The models slice itself is independent as well.
	clone.Providers[0].Models = append(clone.Providers[0].Models, ModelInfo{ID: "m2"})
	if len(original.Providers[0].Models) != 1 {
		t.Fatal("models slice shares backing array")
	}
}

func TestModelInfoReasoningJSONRoundTrip(t *testing.T) {
	info := ModelInfo{
		ID:              "deepseek-v3.2",
		DisplayName:     "DeepSeek V3.2",
		MaxOutputTokens: 8192,
		Reasoning:       SuggestModelInfo("deepseek-v3.2").Reasoning,
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var back ModelInfo
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.DisplayName != info.DisplayName || back.MaxOutputTokens != 8192 ||
		back.Reasoning == nil || len(back.Reasoning.Levels) != len(info.Reasoning.Levels) {
		t.Fatalf("round trip lost fields: %+v", back)
	}
}
