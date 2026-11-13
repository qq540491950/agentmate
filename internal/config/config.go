// Package config 定义了应用的可持久化配置：模型供应商、Agent、Agent
// 团队、技能、MCP 服务器和全局设置。
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Protocol 枚举应用支持的 LLM API 协议。
type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"    // 兼容 OpenAI Chat Completions（OpenAI、DeepSeek、Kimi、Ollama、vLLM...）
	ProtocolAnthropic Protocol = "anthropic" // Anthropic Messages API (Claude)
	ProtocolGemini    Protocol = "gemini"    // Google Gemini API
)

const (
	// ApprovalPolicyRequestApproval 会在每次有副作用的工具调用前进行询问。
	ApprovalPolicyRequestApproval = "request-approval"
	// ApprovalPolicyAutoApprove 保留每个 Agent
	// 的显式提示词，并仅对检测到风险的命令进行询问。
	ApprovalPolicyAutoApprove = "auto-approve"
	// ApprovalPolicyNever 移除所有人工介入提示词。
	ApprovalPolicyNever = "never"

	// SandboxProfileWorkspaceWrite 将本地工具执行限制在任务工作区内。
	SandboxProfileWorkspaceWrite = "workspace-write"
	// SandboxProfileReadOnly 允许读取任务工作区，同时拒绝所有工作区写入。
	SandboxProfileReadOnly = "read-only"
	// SandboxProfileDangerFullAccess 禁用操作系统沙箱。
	SandboxProfileDangerFullAccess = "danger-full-access"

	// SandboxModeRequestApproval 保留操作系统的工作区写入边界，并在每次有副作用的内置工具调用前进行询问。它是
	// 为旧配置和旧前端保留的遗留组合配置档。
	SandboxModeRequestApproval = "request-approval"
	// SandboxModeAutoApprove 保留操作系统的工作区写入边界，且仅对检测到风险的命令或每个 Agent
	// 的显式提示词进行询问。
	SandboxModeAutoApprove = "auto-approve"
	// SandboxModeDangerFullAccess 禁用操作系统沙箱和所有内置审批提示词。它必须仅由用户显式选择。
	SandboxModeDangerFullAccess = "danger-full-access"
	// SandboxModeWorkspaceWrite 是安全默认值的遗留持久化名称。将其保留为别名，以便旧配置文件与测试无需在写入
	// 时进行特殊处理即可迁移。
	SandboxModeWorkspaceWrite = SandboxModeRequestApproval
	SandboxNetworkOff         = "off"
	SandboxNetworkOn          = "on"
	// DefaultCompactionContextPercent 是尾部保留式压缩的默认提示词 token
	// 阈值，以上下文窗口使用率表示。
	DefaultCompactionContextPercent = 65
)

// NormalizeApprovalPolicy 解析执行策略中与审批相关的那一半。接受遗留的组合配置名称，以确保旧调用方仍能
// 继续工作。
func NormalizeApprovalPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case ApprovalPolicyNever, SandboxModeDangerFullAccess:
		return ApprovalPolicyNever
	case ApprovalPolicyAutoApprove, "auto":
		return ApprovalPolicyAutoApprove
	case ApprovalPolicyRequestApproval, "request", SandboxProfileWorkspaceWrite:
		return ApprovalPolicyRequestApproval
	default:
		return ApprovalPolicyRequestApproval
	}
}

// NormalizeSandboxProfile 解析执行策略中与操作系统边界相关的那一半。无法识别的值都会回退到
// workspace-write。
func NormalizeSandboxProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case SandboxProfileDangerFullAccess:
		return SandboxProfileDangerFullAccess
	case SandboxProfileReadOnly:
		return SandboxProfileReadOnly
	case SandboxProfileWorkspaceWrite, SandboxModeRequestApproval, SandboxModeAutoApprove, "request", "auto":
		return SandboxProfileWorkspaceWrite
	default:
		return SandboxProfileWorkspaceWrite
	}
}

// ExecutionPolicyFromLegacySandboxMode 将遗留的组合配置映射到独立的审批维度和沙箱维度。
func ExecutionPolicyFromLegacySandboxMode(mode string) (approvalPolicy, sandboxProfile string) {
	switch NormalizeSandboxMode(mode) {
	case SandboxModeDangerFullAccess:
		return ApprovalPolicyNever, SandboxProfileDangerFullAccess
	case SandboxModeAutoApprove:
		return ApprovalPolicyAutoApprove, SandboxProfileWorkspaceWrite
	default:
		return ApprovalPolicyRequestApproval, SandboxProfileWorkspaceWrite
	}
}

// ResolveExecutionPolicy 应用持久化字段的优先级规则：显式的新字段优先；空字段回退到遗留的组合配置。
func ResolveExecutionPolicy(approvalPolicy, sandboxProfile, legacyMode string) (string, string) {
	legacyApproval, legacySandbox := ExecutionPolicyFromLegacySandboxMode(legacyMode)
	if strings.TrimSpace(approvalPolicy) == "" {
		approvalPolicy = legacyApproval
	}
	if strings.TrimSpace(sandboxProfile) == "" {
		sandboxProfile = legacySandbox
	}
	return NormalizeApprovalPolicy(approvalPolicy), NormalizeSandboxProfile(sandboxProfile)
}

// LegacySandboxMode 将执行策略反向映射回旧的组合字段。遗留 UI
// 无法表示拆分后的策略，因此无论哪一半是危险的，都会映射为
// danger-full-access；这有意比漏报缺失的边界更令人警惕。
func LegacySandboxMode(approvalPolicy, sandboxProfile string) string {
	approvalPolicy = NormalizeApprovalPolicy(approvalPolicy)
	sandboxProfile = NormalizeSandboxProfile(sandboxProfile)
	if approvalPolicy == ApprovalPolicyNever || sandboxProfile == SandboxProfileDangerFullAccess {
		return SandboxModeDangerFullAccess
	}
	if approvalPolicy == ApprovalPolicyAutoApprove {
		return SandboxModeAutoApprove
	}
	return SandboxModeRequestApproval
}

// NormalizeSandboxMode 将空值、未知值或遗留值映射到三种组合配置之一。无法识别的值都会回退到
// request-approval。
func NormalizeSandboxMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case SandboxModeDangerFullAccess:
		return SandboxModeDangerFullAccess
	case SandboxModeAutoApprove, "auto":
		return SandboxModeAutoApprove
	case SandboxModeRequestApproval, "request", "workspace-write":
		return SandboxModeRequestApproval
	default:
		return SandboxModeRequestApproval
	}
}

// NormalizeSandboxNetwork 将空值或未知值映射为网络关闭。
func NormalizeSandboxNetwork(network string) string {
	if strings.EqualFold(strings.TrimSpace(network), SandboxNetworkOn) {
		return SandboxNetworkOn
	}
	return SandboxNetworkOff
}

// TerminalModeAuto 终端模式值。`auto` 会按平台顺序解析为检测到的第一个
// shell；其余每个值都是用户的显式选择。
const (
	TerminalModeAuto       = "auto"
	TerminalModePwsh       = "pwsh"
	TerminalModePowerShell = "powershell"
	TerminalModeGitBash    = "git-bash"
	TerminalModeCmd        = "cmd"
	TerminalModeWSL        = "wsl"
	TerminalModeBash       = "bash"
	TerminalModeZsh        = "zsh"
	TerminalModeSh         = "sh"
	TerminalModeCustom     = "custom"
)

// TerminalSettings 选择 run_command 使用的非交互式
// shell。标准模式有意不携带检测到的可执行文件路径，因此从一台机器导出的配置会在
// 另一台机器上重新检测 shell。
type TerminalSettings struct {
	Mode        string `json:"mode,omitempty"`
	WSLDistro   string `json:"wslDistro,omitempty"`
	WSLUser     string `json:"wslUser,omitempty"`
	WSLShell    string `json:"wslShell,omitempty"`
	CustomPath  string `json:"customPath,omitempty"`
	LoadProfile bool   `json:"loadProfile,omitempty"`
}

// NormalizeTerminalSettings 将持久化的值钳制到已知模式，并裁剪机器特定字段。空模式对旧配置表示自动。
func NormalizeTerminalSettings(t TerminalSettings) TerminalSettings {
	t.Mode = strings.ToLower(strings.TrimSpace(t.Mode))
	switch t.Mode {
	case TerminalModeAuto,
		TerminalModePwsh,
		TerminalModePowerShell,
		TerminalModeGitBash,
		TerminalModeCmd,
		TerminalModeWSL,
		TerminalModeBash,
		TerminalModeZsh,
		TerminalModeSh,
		TerminalModeCustom:
	default:
		t.Mode = TerminalModeAuto
	}
	t.WSLDistro = strings.TrimSpace(t.WSLDistro)
	t.WSLUser = strings.TrimSpace(t.WSLUser)
	t.WSLShell = strings.TrimSpace(t.WSLShell)
	t.CustomPath = strings.TrimSpace(t.CustomPath)
	return t
}

// ReasoningLevel 将一个可选择的思考级别映射到其发送的请求字段。对于嵌套字段，Param
// 可使用点号（"thinking.budget_tokens"）。当 Param 为空时，Value 必须是一个
// JSON 对象，其成员会合并到请求体顶层——需要同时使用多个字段的模型（例如
// Anthropic 自适应思考加输出努力）就是这样表达的。
type ReasoningLevel struct {
	Level string `json:"level"` // off / low / medium / high / max 或自定义标签
	Param string `json:"param,omitempty"`
	Value string `json:"value"`
}

// ReasoningConfig 是模型的思考级别表。当聊天未覆盖它时，DefaultLevel
// 选择所使用的级别；为空时回退到第一个级别。Enabled=false 会禁用整张表。
type ReasoningConfig struct {
	Enabled      bool             `json:"enabled"`
	DefaultLevel string           `json:"defaultLevel,omitempty"`
	Levels       []ReasoningLevel `json:"levels,omitempty"`
}

// ModelInfo 描述供应商暴露的模型。
type ModelInfo struct {
	ID string `json:"id"`
	// DisplayName 是供选择器使用的人类友好标签；为空表示使用 ID。
	DisplayName string `json:"displayName,omitempty"`
	// Multimodal 标记接受图像输入的模型；聊天 UI 用它启用图片附件按钮。
	Multimodal bool `json:"multimodal,omitempty"`
	// ContextWindow 是模型的输入上下文大小，单位为 token（0 = 未知）。
	ContextWindow int `json:"contextWindow,omitempty"`
	// MaxOutputTokens 是模型的输出上限，单位为 token（0 = 未知）。
	// 当一次运行未指定最大输出 token 时，它是回退值。
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	// PriceIn/PriceOut 是每 100 万 token 的可选价格（输入/输出），
	// 用于聊天 UI 中的成本估算。
	PriceIn  float64 `json:"priceIn,omitempty"`
	PriceOut float64 `json:"priceOut,omitempty"`
	// Reasoning 是按模型设置的思考级别表。Nil 会保留
	// 供应商的默认行为。
	Reasoning *ReasoningConfig `json:"reasoning,omitempty"`
}

// Label 是选择器中显示的人类可读名称；它会回退到
// 模型 ID，因此空白的显示名称绝不会产生空行。
func (m ModelInfo) Label() string {
	if s := strings.TrimSpace(m.DisplayName); s != "" {
		return s
	}
	return m.ID
}

// ActiveReasoning 解析一次运行在没有按会话选择时使用的级别：
// 先取配置的默认级别，再取第一个级别。当模型没有启用的推理表，或所选级别既无
// 参数也无值时，ok 为 false。
func (r *ReasoningConfig) ActiveReasoning() (param, value string, ok bool) {
	return r.Resolve("")
}

// Resolve 返回某个级别的请求字段和值。空级别
// 表示配置的默认级别，然后取第一个级别。未知级别会返回
// false，以便调用方回退到模型默认值，而不是发送
// 过期的会话覆盖值。
func (r *ReasoningConfig) Resolve(level string) (param, value string, ok bool) {
	if r == nil || !r.Enabled || len(r.Levels) == 0 {
		return "", "", false
	}
	wanted := strings.TrimSpace(level)
	if wanted == "" {
		wanted = strings.TrimSpace(r.DefaultLevel)
	}
	chosen, found := r.Levels[0], wanted == ""
	if wanted != "" {
		for _, l := range r.Levels {
			if l.Level == wanted {
				chosen, found = l, true
				break
			}
		}
	}
	if !found {
		return "", "", false
	}
	if strings.TrimSpace(chosen.Param) == "" && strings.TrimSpace(chosen.Value) == "" {
		return "", "", false
	}
	return chosen.Param, chosen.Value, true
}

// MatchLevel 返回表中某个级别名称的存储拼写，匹配时
// 不区分大小写。对于空白或未知名称，ok 为 false，以便调用方
// 继续尝试下一个选择（按回合 → 会话 → 模型默认值），
// 而不是发送过期的偏好。
func (r *ReasoningConfig) MatchLevel(level string) (string, bool) {
	if r == nil || !r.Enabled {
		return "", false
	}
	wanted := strings.ToLower(strings.TrimSpace(level))
	if wanted == "" {
		return "", false
	}
	for _, l := range r.Levels {
		if strings.ToLower(strings.TrimSpace(l.Level)) == wanted {
			return strings.TrimSpace(l.Level), true
		}
	}
	return "", false
}

// DefaultLevelName 是未明确选择时运行所使用的级别：当配置的默认值指向真实级别时使用该默认值，否
// 则使用第一个级别。当表被禁用、为空或其默认名称已失效时，它返回 ""。
func (r *ReasoningConfig) DefaultLevelName() string {
	if r == nil || !r.Enabled || len(r.Levels) == 0 {
		return ""
	}
	wanted := strings.TrimSpace(r.DefaultLevel)
	if wanted == "" {
		return strings.TrimSpace(r.Levels[0].Level)
	}
	if name, ok := r.MatchLevel(wanted); ok {
		return name
	}
	return ""
}

// UnmarshalJSON 同时接受遗留条目（"model-id"）和当前的对象形式，因此旧配置文件可原样加载。
func (m *ModelInfo) UnmarshalJSON(data []byte) error {
	var id string
	if err := json.Unmarshal(data, &id); err == nil {
		*m = ModelInfo{ID: id}
		return nil
	}
	type plain ModelInfo
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*m = ModelInfo(p)
	return nil
}

// ModelProvider 描述一个可配置的 LLM 供应商端点。
type ModelProvider struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Protocol  Protocol    `json:"protocol"`
	BaseURL   string      `json:"baseUrl"`
	APIKey    string      `json:"apiKey"`
	Models    []ModelInfo `json:"models"` // 该供应商可用的模型 ID
	IsDefault bool        `json:"isDefault"`
	// PriceIn 和 PriceOut
	// 是遗留的供应商级价格。它们会被复制到每个模型，并在加载时清除（migrateLegacy
	// ）；保留它们只是为了让旧配置文件得以迁移，而不是丢失数据。
	PriceIn  float64 `json:"priceIn,omitempty"`
	PriceOut float64 `json:"priceOut,omitempty"`
}

// FindModel 返回该供应商中某个模型 ID 的条目（精确匹配）。
func (p ModelProvider) FindModel(id string) (ModelInfo, bool) {
	for _, m := range p.Models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// AgentConfig 描述一个用户定义的 Agent。
type AgentConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ProviderID   string   `json:"providerId"`
	Model        string   `json:"model"`
	SystemPrompt string   `json:"systemPrompt"`
	Temperature  float64  `json:"temperature"`
	MaxTokens    int      `json:"maxTokens"`
	SkillIDs     []string `json:"skillIds"`     // 附加到该 Agent 的技能
	MCPServerIDs []string `json:"mcpServerIds"` // 工具可用的 MCP 服务器
	// BuiltinTools 显式收窄内置工具类别（time、files、knowledge、memory、todos、shell）。为空表
	// 示全集——见 AllBuiltinTools——因此收窄始终是刻意之举。
	BuiltinTools []string  `json:"builtinTools"`
	CreatedAt    time.Time `json:"createdAt"`
	// EnableGuardrails 对用户输入和模型输出中的敏感信息做掩码（未设置时默认开启）。指针用于区分
	// "unset" 与显式 false。
	EnableGuardrails *bool `json:"enableGuardrails,omitempty"`
	// ConfirmTools 列出有副作用的调用在执行前需要人工审批的内置工具类别（ADK
	// HITL）。"files" 控制 write_workspace_file；"shell" 控制每一次 run_command
	// 调用。为空 = 绝不询问。
	ConfirmTools []string `json:"confirmTools,omitempty"`
	// ApprovalPolicy 与 SandboxProfile
	// 是仅运行时的字段，由聊天服务在本次构建中填充。SandboxMode
	// 是遗留的合并配置档，保留以兼容旧的调用点。
	ApprovalPolicy string `json:"-"`
	SandboxProfile string `json:"-"`
	SandboxMode    string `json:"-"`
	SandboxNetwork string `json:"-"`
	// Terminal 是仅运行时的、按会话隔离的 shell 快照。对于应使用 Settings.Terminal
	// 的调用方，它为 nil。
	Terminal *TerminalSettings `json:"-"`
	// MaxToolCalls 停止失控的工具循环。0 = 不限。回合时长有意不设上限：长时间的
	// Agent 任务不得被挂钟时间上限终止。
	MaxToolCalls int `json:"maxToolCalls,omitempty"`
	// MaxSessionTokens 限制整个会话的累计 token 数（0 =
	// 不限）。跨越该预算的那个回合仍会完成，下一个回合会被拒绝。
	MaxSessionTokens int64 `json:"maxSessionTokens,omitempty"`
	// StopSequences 在模型输出其中任意一个时停止生成（OpenAI "stop" / Anthropic
	// "stop_sequences"）。空 = 供应商默认值。
	StopSequences []string `json:"stopSequences,omitempty"`
}

// AllBuiltinTools 是当 Agent 的 BuiltinTools 收窄列表为空时，该 Agent
// 获得的完整内置工具集。Agent 默认功能齐全；移除某项能力是显式操作。保持
// AgentsPage.tsx 中的前端 ALL_BUILTIN_TOOLS 列表同步。
var AllBuiltinTools = []string{"time", "files", "knowledge", "memory", "todos", "shell"}

// EffectiveBuiltinTools 解析 Agent
// 挂载的工具类别：显式收窄列表优先，空列表则继承完整集合。
func (a AgentConfig) EffectiveBuiltinTools() []string {
	if len(a.BuiltinTools) > 0 {
		return a.BuiltinTools
	}
	return AllBuiltinTools
}

// DefaultProvider 返回标记为默认的供应商，若不存在则回退到第一个已配置的供应商。nil
// 表示尚未配置任何供应商。
func (c Config) DefaultProvider() *ModelProvider {
	for i := range c.Providers {
		if c.Providers[i].IsDefault {
			return &c.Providers[i]
		}
	}
	if len(c.Providers) > 0 {
		return &c.Providers[0]
	}
	return nil
}

// FindProvider 返回具有给定 ID 的供应商，若不存在则返回 nil。
func (c Config) FindProvider(id string) *ModelProvider {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return &c.Providers[i]
		}
	}
	return nil
}

// ResolveAgentModel 从全局默认值填充 Agent
// 未设置的供应商/模型：空的供应商跟随默认供应商，空的模型则采用该供应商的第一个
// 模型。显式值会保留。这正是让 Agent 仅靠名称 + 描述 +
// 系统提示词即可定义的原因（#39）。
func (c Config) ResolveAgentModel(a AgentConfig) (providerID, modelID string) {
	providerID, modelID = a.ProviderID, a.Model
	p := c.FindProvider(providerID)
	if p == nil && strings.TrimSpace(providerID) == "" {
		if p = c.DefaultProvider(); p != nil {
			providerID = p.ID
		}
	}
	if p != nil && strings.TrimSpace(modelID) == "" && len(p.Models) > 0 {
		modelID = p.Models[0].ID
	}
	return providerID, modelID
}

// RemoteAgentConfig 描述通过 A2A
// 协议（Agent-To-Agent，a2aproject/a2a-go）访问的远程 Agent。Agent 卡片从
// CardURL 的 well-known 位置发现。
type RemoteAgentConfig struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CardURL     string    `json:"cardUrl"`
	CreatedAt   time.Time `json:"createdAt"`
}

// TeamConfig 描述一个 Agent 团队：一个牵头 Agent 协调成员 Agent。
type TeamConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	LeadAgentID    string   `json:"leadAgentId"`    // 为空 => 自动协调器
	AutoCoordinate bool     `json:"autoCoordinate"` // 合成一个协调器，而不是使用某个成员作为负责人
	MemberAgentIDs []string `json:"memberAgentIds"`
	// RemoteMemberIDs 引用可通过 A2A 访问的
	// RemoteAgents；它们充当协调器可转交到的子 Agent。负责人始终在本地。
	RemoteMemberIDs []string  `json:"remoteMemberIds,omitempty"`
	ProviderID      string    `json:"providerId"` // 由自动协调器使用
	Model           string    `json:"model"`
	SystemPrompt    string    `json:"systemPrompt"`
	CreatedAt       time.Time `json:"createdAt"`
	// ApprovalPolicy 是仅运行时使用的执行策略，会在本次构建中复制到每个本地成员。SandboxMode
	// 是为兼容性保留的遗留组合配置。
	ApprovalPolicy string `json:"-"`
	SandboxProfile string `json:"-"`
	SandboxMode    string `json:"-"`
	SandboxNetwork string `json:"-"`
	// Terminal 是由每个本地成员共享的仅运行时 shell 快照。
	Terminal *TerminalSettings `json:"-"`
}

// SkillConfig 是可复用的指令包（agentskills.io SKILL.md）。
type SkillConfig struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"` // 技能的 markdown 正文
	CreatedAt   time.Time `json:"createdAt"`
	// SourceDir 是只读发现项的原始目录，仅用于物化 references/ assets/ scripts/；
	// 不写入 config.json，也不暴露给前端。
	SourceDir string `json:"-"`
}

// MCPTransport 选择如何连接到 MCP 服务器。
type MCPTransport string

const (
	MCPStdio          MCPTransport = "stdio"
	MCPStreamableHTTP MCPTransport = "streamable-http"
)

// MCPServerConfig 描述一个 MCP 服务器连接。
type MCPServerConfig struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Transport MCPTransport      `json:"transport"`
	Command   string            `json:"command"`           // stdio
	Args      []string          `json:"args"`              // stdio
	Env       map[string]string `json:"env,omitempty"`     // stdio
	URL       string            `json:"url"`               // streamable-http
	Headers   map[string]string `json:"headers,omitempty"` // streamable-http
	// 启用开关（默认启用）：启用服务随应用启动自动连接，并注入所有会话；
	// Disabled 服务不连接、不注入，除非某个 Agent 显式挂载（单独启用）。
	Disabled bool `json:"disabled,omitempty"`
	// AuthType：Streamable-HTTP 服务器认证：
	// AuthType "" | "bearer" | "api-key" | "oauth2"（授权码 + PKCE，遵循 MCP
	// 授权规范，由官方 go-sdk handler 处理）。
	AuthType          string `json:"authType,omitempty"`
	AuthToken         string `json:"authToken,omitempty"`  // bearer token / api-key 值
	AuthHeader        string `json:"authHeader,omitempty"` // api-key 请求头名称（默认 X-API-Key）
	OAuthClientID     string `json:"oauthClientId,omitempty"`
	OAuthRedirectPort int    `json:"oauthRedirectPort,omitempty"` // 0 => 临时端口
}

// MCPOAuthToken 是 MCP 服务器缓存的 OAuth 令牌。TokenURL
// 使应用重启后仍能静默刷新（refresh token 保持有效期间无需重新授权）。
type MCPOAuthToken struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	TokenURL     string    `json:"tokenUrl,omitempty"`
}

// ExecutionDisplayAuto 执行显示模式控制聊天渲染一个回合中多少思考内容和工具过程。它们仅是显示偏好：
// 底层事件会保持持久化，因此切换模式绝不会丢失历史。
const (
	ExecutionDisplayAuto     = "auto"
	ExecutionDisplayConcise  = "concise"
	ExecutionDisplayDetailed = "detailed"
	ExecutionDisplayNone     = "none"
)

// NormalizeExecutionDisplayMode 将存储的全局偏好限制为已知模式。空值和过期值会回退到
// auto，因此旧配置绝不会意外隐藏执行块。
func NormalizeExecutionDisplayMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ExecutionDisplayConcise:
		return ExecutionDisplayConcise
	case ExecutionDisplayDetailed:
		return ExecutionDisplayDetailed
	case ExecutionDisplayNone:
		return ExecutionDisplayNone
	default:
		return ExecutionDisplayAuto
	}
}

// Settings 保存全局应用设置。
type Settings struct {
	WorkspaceDir string `json:"workspaceDir"`
	Language     string `json:"language"`
	// ThemeMode 外观：主题模式 "light"|"dark"|"system"，主题预设
	// "nord"|"graphite"|"paper"|"contrast"，字体缩放
	// "sm"|"md"|"lg"，以及是否自动朗读助手回复。
	ThemeMode   string `json:"themeMode"`
	ThemePreset string `json:"themePreset,omitempty"`
	// Accent 仅为向后兼容的配置文件保留。UI 不再暴露或应用它；themePreset
	// 拥有完整调色板。
	Accent    string `json:"accent,omitempty"`
	FontScale string `json:"fontScale"`
	// ChatWidth 选择聊天栏宽度："narrow" | "medium" |
	// "wide"。宽度为聊天窗格的百分比，因此该栏会随窗口调整大小，而不是固定像素上限
	// 。空值会归一化为 medium。
	ChatWidth string `json:"chatWidth,omitempty"`
	AutoSpeak bool   `json:"autoSpeak"`
	// EmbeddingProviderID：语义知识搜索：兼容 OpenAI 的供应商 id +
	// 嵌入模型。空值保持仅关键字搜索。
	EmbeddingProviderID string `json:"embeddingProviderId,omitempty"`
	EmbeddingModel      string `json:"embeddingModel,omitempty"`
	// A2AAutoStart：A2A 服务器自动启动：启动时发布此 Agent（端口 0 = 临时端口）。
	A2AAutoStart bool   `json:"a2aAutoStart,omitempty"`
	A2AAgentID   string `json:"a2aAgentId,omitempty"`
	A2APort      int    `json:"a2aPort,omitempty"`
	// ToolsAudit 将每次工具调用/结果的 JSONL 审计记录写入应用日志目录。
	ToolsAudit bool `json:"toolsAudit,omitempty"`
	// ApprovalPolicy 和 SandboxProfile
	// 是默认执行策略，会复制到每个新建聊天中。现有聊天保留各自的快照。
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	SandboxProfile string `json:"sandboxProfile,omitempty"`
	// SandboxMode 是遗留的组合配置档，为旧配置和旧前端保留。当两者同时存在时，新字段优先。
	SandboxMode string `json:"sandboxMode,omitempty"`
	// SandboxNetwork 选择子进程网络策略：off（默认）或 on。
	SandboxNetwork string `json:"sandboxNetwork,omitempty"`
	// AutoRemember 会自动将每个已完成的回合摄取到长期记忆中（尽力而为；失败时静默）。
	AutoRemember bool `json:"autoRemember,omitempty"`
	// AutoTitle 会在首次交流后使用模型生成简短的会话标题。默认开启（nil =
	// 开启）；显式设为 false 会保留截断的首条消息回退，并避免额外的模型调用。
	AutoTitle *bool `json:"autoTitle,omitempty"`
	// TitleProviderID/TitleModel
	// 选择标题精炼的辅助调用。为空时跟随全局默认供应商的第一个模型（"main
	// model"），因此即使聊天运行在性能较弱或远程的目标上，标题也能保持干净。
	TitleProviderID string `json:"titleProviderId,omitempty"`
	TitleModel      string `json:"titleModel,omitempty"`
	// HTTPProxy 是全局出站代理 URL（例如
	// http://127.0.0.1:7890），在启动时应用于模型/嵌入/MCP 请求。为空 = 直连。
	HTTPProxy string `json:"httpProxy,omitempty"`
	// CompactionInterval 用于上下文压缩（ADK 会话/压缩）。Interval
	// 统计滑动窗口摘要之间已完成的用户回合数。ContextPercent
	// 是以有效模型上下文窗口百分比表示的提示词 token 阈值；nil 表示默认值（65），0
	// 显式禁用尾部保留。旧的绝对阈值 CompactionTokenThreshold
	// 仅为读取旧配置文件而保留，运行时不再使用。
	CompactionInterval       int  `json:"compactionInterval,omitempty"`
	CompactionContextPercent *int `json:"compactionContextPercent,omitempty"`
	CompactionTokenThreshold int  `json:"compactionTokenThreshold,omitempty"`
	// SidebarCollapsed 使会话列表在重启后保持折叠状态。
	SidebarCollapsed bool `json:"sidebarCollapsed,omitempty"`
	// SidebarGrouping 选择会话列表的分组方式："time"（默认）或
	// "project"（按工作区根目录对会话分组）。
	SidebarGrouping string `json:"sidebarGrouping,omitempty"`
	// ExecutionDisplayMode 控制回合的思考与工具过程的默认渲染密度："auto" |
	// "concise" | "detailed" | "none"。空值会归一化为 auto。
	ExecutionDisplayMode string `json:"executionDisplayMode,omitempty"`
	// AutoCheckUpdate 在启动后不久运行一次静默更新检查，之后每 24h
	// 运行一次。采用退出式（nil =
	// 开启），因为只通过设置页按钮更新的应用实际上绝不更新。
	AutoCheckUpdate *bool `json:"autoCheckUpdate,omitempty"`
	// SkippedVersion 是用户要求忽略的版本，也是「跳过此版本」的持久化部分：
	// 每次启动都会告知更新器跳过它，因此在新版本出现之前，检查会一直返回
	// 「已是最新」。
	SkippedVersion string `json:"skippedVersion,omitempty"`
	// Terminal 选择本地 run_command 工具所使用的
	// shell。对于旧配置，空模式会规范化为 auto。
	Terminal TerminalSettings `json:"terminal,omitempty"`
}

// AutoTitleEnabled 报告是否需要模型生成的标题。未设置表示开启：所体现的用户体验默认是经过润色的
// 一行标题；在意额外附带调用的用户可以显式将其关闭。
func (s Settings) AutoTitleEnabled() bool {
	return s.AutoTitle == nil || *s.AutoTitle
}

// CompactionPercent 返回生效的尾部保留阈值。nil
// 指针表示应用默认值（65%）。取值会被限制在 0..100，以免手动编辑的配置导致
// runner 中的乘法溢出。
func (s Settings) CompactionPercent() int {
	if s.CompactionContextPercent == nil {
		return DefaultCompactionContextPercent
	}
	p := *s.CompactionContextPercent
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// ChatWidthNarrow 聊天栏宽度模式。每个模式在前端主题层映射为聊天窗格的一个百分比，使该栏能随窗
// 口大小调整而自适应。
const (
	ChatWidthNarrow = "narrow"
	ChatWidthMedium = "medium"
	ChatWidthWide   = "wide"
)

// NormalizeChatWidth 将存储的宽度偏好限制为已知模式。空值和过期值会回退到
// medium。
func NormalizeChatWidth(width string) string {
	switch strings.ToLower(strings.TrimSpace(width)) {
	case ChatWidthNarrow:
		return ChatWidthNarrow
	case ChatWidthWide:
		return ChatWidthWide
	default:
		return ChatWidthMedium
	}
}

// UpdateAutoCheckEnabled 报告是否运行静默更新检查。未设置表示开启：如果默认采用选择加入式，大多数安装
// 会一直停留在其安装时的版本。
func (s Settings) UpdateAutoCheckEnabled() bool {
	return s.AutoCheckUpdate == nil || *s.AutoCheckUpdate
}

// normalize 用默认值填充零值（较旧的配置文件）。
func (s *Settings) normalize() {
	if s.ThemeMode == "" {
		s.ThemeMode = "dark"
	}
	if s.ThemePreset == "" {
		s.ThemePreset = "nord"
	}
	switch strings.ToLower(strings.TrimSpace(s.ThemePreset)) {
	case "nord", "graphite", "paper", "contrast":
		s.ThemePreset = strings.ToLower(strings.TrimSpace(s.ThemePreset))
	default:
		s.ThemePreset = "nord"
	}
	if s.Accent == "" {
		s.Accent = "blue"
	}
	if s.FontScale == "" {
		s.FontScale = "md"
	}
	if s.SidebarGrouping != "project" {
		s.SidebarGrouping = "time"
	}
	s.ExecutionDisplayMode = NormalizeExecutionDisplayMode(s.ExecutionDisplayMode)
	s.ChatWidth = NormalizeChatWidth(s.ChatWidth)
	s.normalizeExecutionPolicy(nil)
	s.Terminal = NormalizeTerminalSettings(s.Terminal)
	if s.CompactionContextPercent == nil {
		p := DefaultCompactionContextPercent
		s.CompactionContextPercent = &p
	} else {
		p := *s.CompactionContextPercent
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		s.CompactionContextPercent = &p
	}
}

// EffectiveExecutionPolicy 解析设置的默认策略。
func (s Settings) EffectiveExecutionPolicy() (approvalPolicy, sandboxProfile string) {
	return ResolveExecutionPolicy(s.ApprovalPolicy, s.SandboxProfile, s.SandboxMode)
}

// normalizeExecutionPolicy 保持拆分字段与遗留组合字段一致。更新后调用时，旧前端仅对遗留字段所做的变更会
// 更新拆分字段；显式的新字段变更优先。
func (s *Settings) normalizeExecutionPolicy(previous *Settings) {
	if previous != nil &&
		s.ApprovalPolicy == previous.ApprovalPolicy &&
		s.SandboxProfile == previous.SandboxProfile &&
		NormalizeSandboxMode(s.SandboxMode) != NormalizeSandboxMode(previous.SandboxMode) {
		s.ApprovalPolicy, s.SandboxProfile = ExecutionPolicyFromLegacySandboxMode(s.SandboxMode)
	}
	s.ApprovalPolicy, s.SandboxProfile = s.EffectiveExecutionPolicy()
	s.SandboxMode = LegacySandboxMode(s.ApprovalPolicy, s.SandboxProfile)
	s.SandboxNetwork = NormalizeSandboxNetwork(s.SandboxNetwork)
}

// Config 是持久化到磁盘的根配置文档。
type Config struct {
	Settings     Settings            `json:"settings"`
	Providers    []ModelProvider     `json:"providers"`
	Agents       []AgentConfig       `json:"agents"`
	RemoteAgents []RemoteAgentConfig `json:"remoteAgents,omitempty"`
	Teams        []TeamConfig        `json:"teams"`
	// Projects 是聊天可以关联到的具名根目录。绑定到项目的聊天会将其工作区解析为项目根目录，
	// 因此许多会话可以共享一个目录，而不会触发用于保护显式绑定任务工作区的每目录一
	// 个任务冲突。
	Projects []Project `json:"projects,omitempty"`
	// MCPTokens 为 streamable-http MCP 服务器缓存 OAuth 令牌（server ID ->
	// token）。机密仅存放于此 0600 文件中。
	MCPTokens  map[string]MCPOAuthToken `json:"mcpTokens,omitempty"`
	Skills     []SkillConfig            `json:"skills"`
	MCPServers []MCPServerConfig        `json:"mcpServers"`
	Sessions   []ChatSession            `json:"sessions"`
}

// ChatSession 是绑定到 Agent、远程 Agent 或团队的聊天对话。
type ChatSession struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	TargetType string    `json:"targetType"` // "agent" | "team" | "remote"
	TargetID   string    `json:"targetId"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// ForkedFrom 是此聊天通过 ForkSession（分支）创建时的原始会话
	// ID。对于原生会话为空。
	ForkedFrom string `json:"forkedFrom,omitempty"`
	// Pinned 会话在侧边栏中排在最前。
	Pinned bool `json:"pinned,omitempty"`
	// Preview 是最新一条助手回复的尾部，用于侧边栏。
	Preview string `json:"preview,omitempty"`
	// Archived 会话会从主侧边栏列表中隐藏。
	Archived bool `json:"archived,omitempty"`
	// ModelOverride 将此聊天固定到目标 Agent 供应商的特定模型（空 = 该 Agent
	// 配置的模型）。与桌面 AI 客户端的聊天级模型选择器保持一致。
	ModelOverride string `json:"modelOverride,omitempty"`
	// ReasoningLevel 是此聊天的思考级别（来自生效模型的 Reasoning
	// 表中的级别名称；空 = 模型的默认级别）。它是一项聊天级 UI
	// 选择，绝不写回供应商配置。
	ReasoningLevel string `json:"reasoningLevel,omitempty"`
	// ApprovalPolicy/SandboxProfile 是此聊天独立的审批维度和 OS
	// 边界维度。SandboxMode
	// 是为旧前端保留的遗留合并快照。新聊天会对全局默认值进行快照；之后更改设置绝不
	// 重写现有聊天。
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	SandboxProfile string `json:"sandboxProfile,omitempty"`
	SandboxMode    string `json:"sandboxMode,omitempty"`
	SandboxNetwork string `json:"sandboxNetwork,omitempty"`
	// WorkspaceDir 是文件工具、提示词变量和产物预览使用的任务级目录。为空会使遗留会话保留在全局
	// 工作区。
	WorkspaceDir string `json:"workspaceDir,omitempty"`
	// ProjectID 将此聊天关联到一个已注册项目。项目聊天会保持 WorkspaceDir
	// 为空：其生效工作区是项目根目录（参见
	// EffectiveWorkspaceFor），该目录会动态解析，因此重命名项目或重新指定项目根目
	// 录会传播到其中的每个聊天。
	ProjectID string `json:"projectId,omitempty"`
}

// EffectiveWorkspace 解析会话应使用的目录。显式任务工作区优先；遗留会话回退到应用级工作区。
func (s ChatSession) EffectiveWorkspace(global string) string {
	if ws := strings.TrimSpace(s.WorkspaceDir); ws != "" {
		return ws
	}
	return global
}

// Project 是聊天可挂接的具名根目录。
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RootDir   string    `json:"rootDir"`
	CreatedAt time.Time `json:"createdAt"`
}

// EffectiveWorkspaceFor 是考虑项目的工作区解析：显式绑定的任务目录优先，其次是聊天的项目根目录，最后
// 是全局工作区。
func (s ChatSession) EffectiveWorkspaceFor(cfg Config) string {
	if ws := strings.TrimSpace(s.WorkspaceDir); ws != "" {
		return ws
	}
	if s.ProjectID != "" {
		for _, p := range cfg.Projects {
			if p.ID == s.ProjectID {
				return p.RootDir
			}
		}
	}
	return cfg.Settings.WorkspaceDir
}

// EffectiveExecutionPolicy 解析此聊天的拆分策略，仅对完全没有快照的会话回退到全局默认值。
func (s ChatSession) EffectiveExecutionPolicy(cfg Config) (approvalPolicy, sandboxProfile string) {
	if strings.TrimSpace(s.ApprovalPolicy) != "" ||
		strings.TrimSpace(s.SandboxProfile) != "" ||
		strings.TrimSpace(s.SandboxMode) != "" {
		return ResolveExecutionPolicy(s.ApprovalPolicy, s.SandboxProfile, s.SandboxMode)
	}
	return cfg.Settings.EffectiveExecutionPolicy()
}

// EffectiveApprovalPolicy 解析此聊天的审批维度。
func (s ChatSession) EffectiveApprovalPolicy(cfg Config) string {
	approval, _ := s.EffectiveExecutionPolicy(cfg)
	return approval
}

// EffectiveSandboxProfile 解析此聊天的 OS 边界维度。
func (s ChatSession) EffectiveSandboxProfile(cfg Config) string {
	_, profile := s.EffectiveExecutionPolicy(cfg)
	return profile
}

// EffectiveSandboxMode 将拆分策略映射到遗留的合并字段，供旧前端使用。
func (s ChatSession) EffectiveSandboxMode(cfg Config) string {
	return LegacySandboxMode(s.EffectiveExecutionPolicy(cfg))
}

// EffectiveSandboxNetwork 解析聊天级覆盖，然后是全局设置，最后是网络关闭。无论持久化的值是什么，danger-
// full-access 配置档都会报告网络开启；切换回 workspace-write
// 会恢复该持久化的值。
func (s ChatSession) EffectiveSandboxNetwork(cfg Config) string {
	if s.EffectiveSandboxProfile(cfg) == SandboxProfileDangerFullAccess {
		return SandboxNetworkOn
	}
	return s.PersistedSandboxNetwork(cfg)
}

// PersistedSandboxNetwork 解析网络偏好，且不应用 danger-profile 覆盖。Forking
// 会保留此值，以便之后切换回 workspace-write 时恢复用户原来的选择。
func (s ChatSession) PersistedSandboxNetwork(cfg Config) string {
	if network := strings.TrimSpace(s.SandboxNetwork); network != "" {
		return NormalizeSandboxNetwork(network)
	}
	return NormalizeSandboxNetwork(cfg.Settings.SandboxNetwork)
}

// EffectiveApprovalPolicy 解析用于 agent 构建的审批维度。
func (a AgentConfig) EffectiveApprovalPolicy() string {
	approval, _ := ResolveExecutionPolicy(a.ApprovalPolicy, a.SandboxProfile, a.SandboxMode)
	return approval
}

// EffectiveSandboxProfile 解析用于 agent 构建的 OS-boundary 维度。
func (a AgentConfig) EffectiveSandboxProfile() string {
	_, profile := ResolveExecutionPolicy(a.ApprovalPolicy, a.SandboxProfile, a.SandboxMode)
	return profile
}

// ApplyExecutionPolicy 将独立的审批和沙箱维度应用到 agent
// 构建。request-approval 会针对每个内置写入/命令请求审批，auto-approve 会保留
// agent 的显式提示词，且绝不将其清除。
func (a *AgentConfig) ApplyExecutionPolicy(approvalPolicy, sandboxProfile string) {
	a.ApprovalPolicy = NormalizeApprovalPolicy(approvalPolicy)
	a.SandboxProfile = NormalizeSandboxProfile(sandboxProfile)
	a.SandboxMode = LegacySandboxMode(a.ApprovalPolicy, a.SandboxProfile)
	switch a.ApprovalPolicy {
	case ApprovalPolicyNever:
		a.ConfirmTools = nil
	case ApprovalPolicyRequestApproval:
		a.ConfirmTools = []string{"files", "shell"}
	default: // auto-approve 保留 agent 的显式额外提示词
	}
}

// ApplySandboxProfile 是遗留的组合 profile
// 包装器。新的运行时代码应使用两个维度调用 ApplyExecutionPolicy。
func (a *AgentConfig) ApplySandboxProfile(mode string) {
	approval, profile := ExecutionPolicyFromLegacySandboxMode(mode)
	a.ApplyExecutionPolicy(approval, profile)
}

// Clone 返回 config 的独立副本。切片、嵌套切片和 map
// 都会被复制，因此修改克隆（例如为不含密钥的导出清空 API
// 密钥）绝不会触及当前正在使用的 config 或其底层数组。
func (c Config) Clone() Config {
	out := c
	if c.Settings.AutoTitle != nil {
		v := *c.Settings.AutoTitle
		out.Settings.AutoTitle = &v
	}
	if c.Settings.AutoCheckUpdate != nil {
		v := *c.Settings.AutoCheckUpdate
		out.Settings.AutoCheckUpdate = &v
	}
	if c.Settings.CompactionContextPercent != nil {
		v := *c.Settings.CompactionContextPercent
		out.Settings.CompactionContextPercent = &v
	}
	out.Providers = make([]ModelProvider, len(c.Providers))
	for i, p := range c.Providers {
		src := p.Models
		p.Models = make([]ModelInfo, len(src))
		for j, m := range src {
			if m.Reasoning != nil {
				r := *m.Reasoning
				r.Levels = append([]ReasoningLevel(nil), m.Reasoning.Levels...)
				m.Reasoning = &r
			}
			p.Models[j] = m
		}
		out.Providers[i] = p
	}
	if c.Agents != nil {
		out.Agents = make([]AgentConfig, len(c.Agents))
		for i, a := range c.Agents {
			a.SkillIDs = append([]string(nil), a.SkillIDs...)
			a.MCPServerIDs = append([]string(nil), a.MCPServerIDs...)
			a.BuiltinTools = append([]string(nil), a.BuiltinTools...)
			a.ConfirmTools = append([]string(nil), a.ConfirmTools...)
			a.StopSequences = append([]string(nil), a.StopSequences...)
			if a.EnableGuardrails != nil {
				v := *a.EnableGuardrails
				a.EnableGuardrails = &v
			}
			out.Agents[i] = a
		}
	}
	if c.Teams != nil {
		out.Teams = make([]TeamConfig, len(c.Teams))
		for i, t := range c.Teams {
			t.MemberAgentIDs = append([]string(nil), t.MemberAgentIDs...)
			t.RemoteMemberIDs = append([]string(nil), t.RemoteMemberIDs...)
			out.Teams[i] = t
		}
	}
	if c.MCPServers != nil {
		out.MCPServers = make([]MCPServerConfig, len(c.MCPServers))
		for i, m := range c.MCPServers {
			m.Args = append([]string(nil), m.Args...)
			m.Env = cloneStringMap(m.Env)
			m.Headers = cloneStringMap(m.Headers)
			out.MCPServers[i] = m
		}
	}
	out.RemoteAgents = append([]RemoteAgentConfig(nil), c.RemoteAgents...)
	out.Skills = append([]SkillConfig(nil), c.Skills...)
	out.Projects = append([]Project(nil), c.Projects...)
	out.Sessions = append([]ChatSession(nil), c.Sessions...)
	out.MCPTokens = make(map[string]MCPOAuthToken, len(c.MCPTokens))
	for k, v := range c.MCPTokens {
		out.MCPTokens[k] = v
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Store 以 JSON 持久化 Config，并保护并发访问。
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func defaultWorkspace() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents", "AgentMateWorkspace")
}

// Default 返回一个预置了有用默认值的配置。
func Default() Config {
	compactionPercent := DefaultCompactionContextPercent
	return Config{
		Settings: Settings{
			WorkspaceDir:             defaultWorkspace(),
			Language:                 "zh-CN",
			ThemeMode:                "dark",
			ThemePreset:              "nord",
			Accent:                   "blue",
			FontScale:                "md",
			ChatWidth:                ChatWidthMedium,
			ExecutionDisplayMode:     ExecutionDisplayAuto,
			ApprovalPolicy:           ApprovalPolicyRequestApproval,
			SandboxProfile:           SandboxProfileWorkspaceWrite,
			SandboxMode:              SandboxModeRequestApproval,
			SandboxNetwork:           SandboxNetworkOff,
			Terminal:                 TerminalSettings{Mode: TerminalModeAuto},
			CompactionContextPercent: &compactionPercent,
		},
	}
}

// legacySeededSkills 是旧版本首次启动时写入 config.json 的两个示例技能。
// 只移除仍与原始内容完全一致的条目；用户改过名称、描述或正文的技能会保留。
var legacySeededSkills = []SkillConfig{
	{
		ID:          "skill-weekly-report",
		Name:        "weekly-report",
		Description: "根据本周工作记录撰写结构化周报",
		Content: `# 周报撰写技能

按照以下结构撰写周报：
1. 本周完成事项（按优先级排列，量化成果）
2. 进行中事项（注明进度百分比与风险）
3. 下周计划（明确目标与衡量标准）
4. 需要协调的问题

语气专业简洁，使用中文，避免空话。`,
	},
	{
		ID:          "skill-code-review",
		Name:        "code-review",
		Description: "系统化代码审查清单与输出格式",
		Content: `# 代码审查技能

审查代码时依次检查：
- 正确性：边界条件、错误处理、并发安全
- 可读性：命名、函数长度、注释质量
- 设计：职责划分、重复代码、可测试性
- 安全：输入校验、敏感信息、权限

输出格式：按严重程度（P0 阻断 / P1 应修 / P2 建议）分组列出问题，每条给出文件、位置与修复建议。`,
	},
}

func isLegacySeededSkill(s SkillConfig) bool {
	for _, seed := range legacySeededSkills {
		if s.ID == seed.ID &&
			s.Name == seed.Name &&
			s.Description == seed.Description &&
			strings.TrimSpace(s.Content) == strings.TrimSpace(seed.Content) {
			return true
		}
	}
	return false
}

// removeLegacySeededSkills 让旧安装不再显示两个示例技能；若某个 Agent
// 曾经挂载过它们，同步移除悬空引用，避免下次构建报“技能不存在”。
func (c *Config) removeLegacySeededSkills() {
	if len(c.Skills) == 0 {
		return
	}
	removed := make(map[string]bool)
	out := c.Skills[:0]
	for _, s := range c.Skills {
		if isLegacySeededSkill(s) {
			removed[s.ID] = true
			continue
		}
		out = append(out, s)
	}
	c.Skills = out
	if len(removed) == 0 {
		return
	}
	for i := range c.Agents {
		ids := c.Agents[i].SkillIDs[:0]
		for _, id := range c.Agents[i].SkillIDs {
			if !removed[id] {
				ids = append(ids, id)
			}
		}
		c.Agents[i].SkillIDs = ids
	}
}

// migrateLegacy 将较旧的配置 schema 收敛到当前 schema：遗留的
// 供应商级价格下移到每个模型，并丢弃未命名的模型条目。
// 每次加载运行一次；结果由 Open 持久化。
func (c *Config) migrateLegacy() {
	c.removeLegacySeededSkills()
	// 在执行策略出现之前创建的会话没有快照。为
	// 每个此类会话恰好赋予一次当前默认值，因此后续设置变更不会
	// 追溯性地改变现有聊天的边界。
	for i := range c.Sessions {
		s := &c.Sessions[i]
		if strings.TrimSpace(s.ApprovalPolicy) == "" &&
			strings.TrimSpace(s.SandboxProfile) == "" &&
			strings.TrimSpace(s.SandboxMode) == "" {
			s.ApprovalPolicy = c.Settings.ApprovalPolicy
			s.SandboxProfile = c.Settings.SandboxProfile
		} else {
			s.ApprovalPolicy, s.SandboxProfile = ResolveExecutionPolicy(s.ApprovalPolicy, s.SandboxProfile, s.SandboxMode)
		}
		s.SandboxMode = LegacySandboxMode(s.ApprovalPolicy, s.SandboxProfile)
		if strings.TrimSpace(s.SandboxNetwork) == "" {
			s.SandboxNetwork = NormalizeSandboxNetwork(c.Settings.SandboxNetwork)
		} else {
			s.SandboxNetwork = NormalizeSandboxNetwork(s.SandboxNetwork)
		}
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.PriceIn > 0 || p.PriceOut > 0 {
			for j := range p.Models {
				if p.Models[j].PriceIn == 0 {
					p.Models[j].PriceIn = p.PriceIn
				}
				if p.Models[j].PriceOut == 0 {
					p.Models[j].PriceOut = p.PriceOut
				}
			}
			p.PriceIn, p.PriceOut = 0, 0
		}
		models := p.Models[:0]
		for _, m := range p.Models {
			if m.ID != "" {
				models = append(models, m)
			}
		}
		p.Models = models
	}
}

// Open 加载（或创建）dir 下的配置存储。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "config.json"), cfg: Default()}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, err
	}
	if len(data) > 0 {
		clearMissingExecutionPolicyFields(data, &s.cfg)
		if err := json.Unmarshal(data, &s.cfg); err != nil {
			// 配置损坏：保留该文件作为备份并重新开始。备份可能包含 API 密钥，
			// 因此会获得与正在使用的配置相同的 0600 权限（WriteFile 不会收紧现有
			// 文件的权限模式，因此显式执行 Chmod）。
			if werr := os.WriteFile(s.path+".bak", data, 0o600); werr == nil {
				_ = os.Chmod(s.path+".bak", 0o600)
			}
			s.cfg = Default()
		}
	}
	s.cfg.Settings.normalize()
	s.cfg.migrateLegacy()
	// 持久化规范化后的默认值，使旧配置文件收敛到新 schema。
	if _, err := os.Stat(s.path); err == nil {
		_ = s.saveLocked()
	}
	return s, nil
}

// clearMissingExecutionPolicyFields 防止 Default() 伪装成显式的新 schema
// 选择。当较旧的配置没有 approvalPolicy / sandboxProfile 键时，
// 遗留的 sandboxMode 必须驱动迁移。
func clearMissingExecutionPolicyFields(data []byte, cfg *Config) {
	var doc struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return
	}
	if raw, ok := doc.Settings["approvalPolicy"]; !ok || string(raw) == "null" {
		cfg.Settings.ApprovalPolicy = ""
	}
	if raw, ok := doc.Settings["sandboxProfile"]; !ok || string(raw) == "null" {
		cfg.Settings.SandboxProfile = ""
	}
}

// NormalizeImported 应用与 Open 为每个已持久化配置提供的相同加载时规范化
// （设置默认值 + 遗留价格迁移），因此由较旧 schema 生成的备份可以顺利导入。
func (c *Config) NormalizeImported() {
	c.Settings.normalize()
	c.migrateLegacy()
}

// Dir 返回存放配置文件的目录。
func (s *Store) Dir() string { return filepath.Dir(s.path) }

// SkillsDir 返回将技能物化为 SKILL.md 的目录。
func (s *Store) SkillsDir() string { return filepath.Join(s.Dir(), "skills") }

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	return s.writeLocked(data)
}

// writeLocked 使用给定字节原子地替换配置文件。
func (s *Store) writeLocked(data []byte) error {
	tmp := s.path + ".tmp"
	// 0600：该文件包含 API 密钥。
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// Get 返回当前配置的深拷贝。调用方可以自由修改结果——切片和映射不与实时配置共享存储
// 。
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Clone()
}

// Settings 返回全局设置的副本。Settings
// 仅包含值字段，因此对于高频读取路径，这比 Get 开销更低。
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Settings
}

// Update 应用 fn
// 并持久化结果。变更会应用于深拷贝，并且仅在磁盘写入成功后才换入实时配置：过去
// ，保存失败（磁盘已满、权限）会使内存与磁盘不一致，直到下次重启才将其静默还原
// 。
func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previousSettings := s.cfg.Settings
	next := s.cfg.Clone()
	fn(&next)
	next.Settings.normalizeExecutionPolicy(&previousSettings)
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := s.writeLocked(data); err != nil {
		return err
	}
	s.cfg = next
	return nil
}
