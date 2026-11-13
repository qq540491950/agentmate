// Package agentkit 提供 Agent 集成层的护栏：对用户输入和模型输出应用分层防御——
// 敏感密钥脱敏与提示词注入启发式检测。仅有系统提示词不构成护栏；这些检查在双向均运行。
package agentkit

import (
	"regexp"
	"strings"
)

var secretPatterns = []*regexp.Regexp{
	// OpenAI / DeepSeek / 通用 "sk-" 密钥
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
	// AWS 访问密钥
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	// GitHub 令牌
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
	// Slack 令牌
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	// Google API 密钥
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`),
	// Anthropic 密钥
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`),
	// 私钥块
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	// 通用高熵十六进制密钥（标记为 key/token/secret 的 32 个以上十六进制字符）
	regexp.MustCompile(`(?i)\b(?:key|token|secret|password)\s*[:=]\s*["']?[0-9a-f]{32,64}\b`),
}

const redactedPlaceholder = "[REDACTED]"

// SanitizeSecrets 将看似密钥的子串替换为占位符。
func SanitizeSecrets(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, redactedPlaceholder)
	}
	return s
}

var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+(instructions|prompts?|rules)`),
	// 必须同时有动词 + 宾语：单独的 "reveal"/"print" 会匹配到 "print the JSON"
	// 这类日常开发请求。
	regexp.MustCompile(`(?i)(reveal|print|repeat|show)\s+(your|the|你的)\s+(system\s+)?(prompt|instructions|提示词)`),
	regexp.MustCompile(`(?i)(system\s+prompt|开发提示词|系统提示词).{0,20}(是什么|泄露|输出|打印)`),
	regexp.MustCompile(`(?i)(泄露|输出|打印|导出).{0,10}(系统提示词|system prompt|开发提示词)`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(a|an|no longer)`),
}

// InjectionRisk 报告文本是否匹配提示词注入启发式规则，并返回首个匹配的模式描述。
func InjectionRisk(s string) (bool, string) {
	for _, re := range injectionPatterns {
		if loc := re.FindString(s); loc != "" {
			return true, strings.TrimSpace(loc)
		}
	}
	return false, ""
}

// GuardrailResult 概括护栏对一段文本所做的处理。
type GuardrailResult struct {
	Text        string
	Redacted    bool
	InjectRisk  bool
	InjectMatch string
}

// ApplyInputGuardrail 清洗用户输入并标记注入尝试。
func ApplyInputGuardrail(text string) GuardrailResult {
	sanitized := SanitizeSecrets(text)
	risk, match := InjectionRisk(sanitized)
	return GuardrailResult{
		Text:        sanitized,
		Redacted:    sanitized != text,
		InjectRisk:  risk,
		InjectMatch: match,
	}
}

// ApplyOutputGuardrail 在模型输出到达 UI 之前对其进行清洗。
func ApplyOutputGuardrail(text string) string {
	return SanitizeSecrets(text)
}
