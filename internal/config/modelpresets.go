package config

import "strings"

// Built-in model defaults. Model IDs are matched case-insensitively by
// family prefix; the tables below follow each vendor's documented request
// fields (checked 2026-11):
//
//   - OpenAI:      reasoning_effort none/minimal/low/medium/high/xhigh/max
//   - Anthropic:   4.6+ use adaptive thinking + output_config.effort;
//     3.x/4.0/4.1/4.5 use thinking.budget_tokens
//   - DeepSeek:    thinking {"type": "enabled/disabled"} plus
//     reasoning_effort low/high/max (thinking is on by default)
//   - Kimi:        k2.x toggles thinking; k3+ uses reasoning_effort
//   - GLM:         thinking.type toggles; 5.2+ adds reasoning_effort
//   - Qwen:        enable_thinking / thinking_budget
//
// These are only starting points: the UI lets users change every level,
// parameter name and value, add custom levels, or delete levels. Providers
// whose request shape differs can be expressed with an empty Param and a
// JSON object value, which is merged into the request body top level.

// SuggestModelInfo returns the built-in defaults for one model ID. Unknown
// families get no reasoning table (nil), leaving the provider default. The
// display name starts as the ID so a freshly fetched row is never blank; the
// editor lets the user replace it.
func SuggestModelInfo(id string) ModelInfo {
	return ModelInfo{ID: id, DisplayName: strings.TrimSpace(id), Reasoning: suggestReasoning(id)}
}

// SuggestModelInfos applies SuggestModelInfo to a batch of IDs.
func SuggestModelInfos(ids []string) []ModelInfo {
	out := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, SuggestModelInfo(id))
	}
	return out
}

// ReasoningTable returns the thinking-level table a model offers: its
// configured table when enabled, else the built-in suggestion for a known
// reasoning family. An explicitly disabled table stays disabled — turning the
// picker off is deliberate — while a nil table means "not configured", which
// is exactly the case the built-in fallback covers (#42).
func (m ModelInfo) ReasoningTable() *ReasoningConfig {
	if m.Reasoning != nil {
		if !m.Reasoning.Enabled || len(m.Reasoning.Levels) == 0 {
			return nil
		}
		return m.Reasoning
	}
	return suggestReasoning(m.ID)
}

// ReasoningConfigured reports whether ReasoningTable comes from the saved
// configuration rather than the built-in family fallback. Only a configured
// table applies an implicit default level: a fallback table powers the picker
// but sends nothing until the user actually picks a level, so upgrading an
// existing install never silently rewrites request bodies.
func (m ModelInfo) ReasoningConfigured() bool {
	return m.Reasoning != nil && m.Reasoning.Enabled && len(m.Reasoning.Levels) > 0
}

func suggestReasoning(modelID string) *ReasoningConfig {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return nil
	}
	switch {
	case strings.Contains(id, "deepseek"):
		return reasoningConfig("high",
			level("off", "thinking", `{"type":"disabled"}`),
			level("low", "reasoning_effort", "low"),
			level("medium", "reasoning_effort", "medium"),
			level("high", "reasoning_effort", "high"),
			level("max", "reasoning_effort", "max"),
		)
	case strings.Contains(id, "kimi") || strings.Contains(id, "moonshot"):
		switch {
		case strings.Contains(id, "k3"):
			// K3/K3.1 always think; only the effort is adjustable.
			return reasoningConfig("high",
				level("low", "reasoning_effort", "low"),
				level("high", "reasoning_effort", "high"),
				level("max", "reasoning_effort", "max"),
			)
		case strings.Contains(id, "k2.7"):
			// kimi-k2.7-code only accepts thinking enabled.
			return reasoningConfig("high",
				level("high", "thinking", `{"type":"enabled"}`),
			)
		default:
			return reasoningConfig("high",
				level("off", "thinking", `{"type":"disabled"}`),
				level("high", "thinking", `{"type":"enabled"}`),
			)
		}
	case strings.Contains(id, "glm"):
		switch {
		case strings.Contains(id, "glm-5.3"):
			// 5.3 cannot turn thinking off; effort is low/high/max.
			return reasoningConfig("high",
				level("low", "reasoning_effort", "low"),
				level("high", "reasoning_effort", "high"),
				level("max", "reasoning_effort", "max"),
			)
		case strings.Contains(id, "glm-5.2"):
			return reasoningConfig("high",
				level("off", "reasoning_effort", "none"),
				level("low", "reasoning_effort", "low"),
				level("medium", "reasoning_effort", "medium"),
				level("high", "reasoning_effort", "high"),
				level("max", "reasoning_effort", "max"),
			)
		default:
			return reasoningConfig("high",
				level("off", "thinking", `{"type":"disabled"}`),
				level("high", "thinking", `{"type":"enabled"}`),
			)
		}
	case strings.Contains(id, "claude"):
		if claudeUsesBudgetTokens(id) {
			return reasoningConfig("high",
				level("off", "thinking", `{"type":"disabled"}`),
				level("low", "thinking", `{"type":"enabled","budget_tokens":1024}`),
				level("medium", "thinking", `{"type":"enabled","budget_tokens":2048}`),
				level("high", "thinking", `{"type":"enabled","budget_tokens":4096}`),
				level("max", "thinking", `{"type":"enabled","budget_tokens":6144}`),
			)
		}
		// Claude 4.6+ replaces budget_tokens with adaptive thinking plus a
		// separate output_config.effort; both fields must go together.
		return reasoningConfig("high",
			level("off", "thinking", `{"type":"disabled"}`),
			level("low", "", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`),
			level("medium", "", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"}}`),
			level("high", "", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`),
			level("max", "", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`),
		)
	case isGPTSeries(id):
		return reasoningConfig("medium",
			level("off", "reasoning_effort", "none"),
			level("low", "reasoning_effort", "low"),
			level("medium", "reasoning_effort", "medium"),
			level("high", "reasoning_effort", "high"),
			level("max", "reasoning_effort", "max"),
		)
	case strings.Contains(id, "qwen"):
		return reasoningConfig("high",
			level("off", "", `{"enable_thinking":false}`),
			level("low", "", `{"enable_thinking":true,"thinking_budget":2048}`),
			level("medium", "", `{"enable_thinking":true,"thinking_budget":8192}`),
			level("high", "", `{"enable_thinking":true,"thinking_budget":16384}`),
			level("max", "", `{"enable_thinking":true,"thinking_budget":32768}`),
		)
	default:
		return nil
	}
}

// claudeUsesBudgetTokens reports whether the model predates adaptive thinking
// (Claude 3.x, 4.0, 4.1, 4.5). Later models reject budget_tokens.
func claudeUsesBudgetTokens(id string) bool {
	for _, marker := range []string{
		"claude-3", "claude-4-0", "claude-4-1", "claude-4-5",
		"sonnet-4-2025", "opus-4-2025", "haiku-4-2025",
	} {
		if strings.Contains(id, marker) {
			return true
		}
	}
	return false
}

// isGPTSeries matches OpenAI reasoning families only: plain gpt-4o and the
// chat models do not accept reasoning_effort.
func isGPTSeries(id string) bool {
	for _, marker := range []string{
		"gpt-5", "gpt-6", "gpt-7", "o1", "o3", "o4", "o5",
	} {
		if strings.Contains(id, marker) {
			return true
		}
	}
	return false
}

func level(name, param, value string) ReasoningLevel {
	return ReasoningLevel{Level: name, Param: param, Value: value}
}

func reasoningConfig(defaultLevel string, levels ...ReasoningLevel) *ReasoningConfig {
	return &ReasoningConfig{Enabled: true, DefaultLevel: defaultLevel, Levels: levels}
}
