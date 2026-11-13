package config

import "strings"

// 内置模型默认值。模型 ID
// 按家族前缀进行不区分大小写的匹配；下表遵循各供应商文档所记录的请求字段（核对
// 于 2026-11）：
//
// - OpenAI： reasoning_effort none/minimal/low/medium/high/xhigh/max -
// Anthropic： 4.6+ 使用自适应思考 + output_config.effort； 3.x/4.0/4.1/4.5 使用
// thinking.budget_tokens - DeepSeek： thinking {"type": "enabled/disabled"}
// 外加 reasoning_effort low/high/max（thinking 默认开启） - Kimi： k2.x 切换
// thinking；k3+ 使用 reasoning_effort - GLM： thinking.type 切换；5.2+ 增加
// reasoning_effort - Qwen： enable_thinking / thinking_budget
//
// 这些只是起点：UI
// 允许用户修改每个级别、参数名和值，添加自定义级别，或删除级别。请求形态不同的
// 供应商，可以用空 Param 加 JSON 对象值来表达，该值会被合并到请求体顶层。

// SuggestModelInfo 返回单个模型 ID
// 的内置默认值。未知家族不会得到推理表（nil），从而保留供应商默认值。显示名初始
// 为 ID，因此刚抓取的行绝不会为空白；编辑器允许用户替换它。
func SuggestModelInfo(id string) ModelInfo {
	return ModelInfo{ID: id, DisplayName: strings.TrimSpace(id), Reasoning: suggestReasoning(id)}
}

// SuggestModelInfos 对一批 ID 应用 SuggestModelInfo。
func SuggestModelInfos(ids []string) []ModelInfo {
	out := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, SuggestModelInfo(id))
	}
	return out
}

// ReasoningTable 返回模型提供的思考级别表：启用时返回其已配置的表，否则返回已知推理家族的内置
// 建议。显式禁用的表保持禁用——关闭选择器是有意为之——而 nil 表表示 "not
// configured"，这正是内置回退所覆盖的情形（#42）。
func (m ModelInfo) ReasoningTable() *ReasoningConfig {
	if m.Reasoning != nil {
		if !m.Reasoning.Enabled || len(m.Reasoning.Levels) == 0 {
			return nil
		}
		return m.Reasoning
	}
	return suggestReasoning(m.ID)
}

// ReasoningConfigured 报告 ReasoningTable
// 是否来自已保存的配置，而非内置的家族回退。仅已配置的表才会应用隐式默认级别：
// 回退表为选择器提供支持，但在用户实际选择某个级别之前不发送任何内容，因此升级
// 现有安装绝不会静默改写请求体。
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
			// K3/K3.1 始终思考；仅 effort 可调。
			return reasoningConfig("high",
				level("low", "reasoning_effort", "low"),
				level("high", "reasoning_effort", "high"),
				level("max", "reasoning_effort", "max"),
			)
		case strings.Contains(id, "k2.7"):
			// kimi-k2.7-code 仅接受 thinking 处于启用状态。
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
			// 5.3 无法关闭 thinking；effort 为 low/high/max。
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
		// Claude 4.6+ 用自适应思考加上单独的 output_config.effort 替代
		// budget_tokens；这两个字段必须一起使用。
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

// claudeUsesBudgetTokens 报告模型是否早于自适应思考（Claude
// 3.x、4.0、4.1、4.5）。后续模型会拒绝 budget_tokens。
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

// isGPTSeries 仅匹配 OpenAI 推理系列：普通 gpt-4o 和聊天模型不接受
// reasoning_effort。
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
