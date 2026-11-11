// Package config defines the persistable configuration of the application:
// model providers, agents, agent teams, skills, MCP servers and global settings.
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

// Protocol enumerates the LLM API protocols supported by the app.
type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"    // OpenAI Chat Completions compatible (OpenAI, DeepSeek, Kimi, Ollama, vLLM...)
	ProtocolAnthropic Protocol = "anthropic" // Anthropic Messages API (Claude)
	ProtocolGemini    Protocol = "gemini"    // Google Gemini API
)

const (
	// ApprovalPolicyRequestApproval asks before every side-effectful tool call.
	ApprovalPolicyRequestApproval = "request-approval"
	// ApprovalPolicyAutoApprove keeps explicit per-Agent prompts and asks only
	// for risk-detected commands.
	ApprovalPolicyAutoApprove = "auto-approve"
	// ApprovalPolicyNever removes all human-in-the-loop prompts.
	ApprovalPolicyNever = "never"

	// SandboxProfileWorkspaceWrite confines local tool execution to the task
	// workspace.
	SandboxProfileWorkspaceWrite = "workspace-write"
	// SandboxProfileReadOnly allows reading the task workspace while denying
	// every workspace write.
	SandboxProfileReadOnly = "read-only"
	// SandboxProfileDangerFullAccess disables the OS sandbox.
	SandboxProfileDangerFullAccess = "danger-full-access"

	// SandboxModeRequestApproval keeps the OS workspace-write boundary and
	// asks before every side-effectful builtin tool call. It is the legacy
	// combined profile kept for old configs and old frontends.
	SandboxModeRequestApproval = "request-approval"
	// SandboxModeAutoApprove keeps the OS workspace-write boundary and asks
	// only for risk-detected commands or explicit per-Agent prompts.
	SandboxModeAutoApprove = "auto-approve"
	// SandboxModeDangerFullAccess disables the OS sandbox and all builtin
	// approval prompts. It must only be selected explicitly by the user.
	SandboxModeDangerFullAccess = "danger-full-access"
	// SandboxModeWorkspaceWrite is the legacy persisted name for the safe
	// default. Keep it as an alias so older config files and tests migrate
	// without a write-time special case.
	SandboxModeWorkspaceWrite = SandboxModeRequestApproval
	SandboxNetworkOff         = "off"
	SandboxNetworkOn          = "on"
	// DefaultCompactionContextPercent is the default prompt-token threshold
	// for tail-retention compaction, expressed as context-window usage.
	DefaultCompactionContextPercent = 65
)

// NormalizeApprovalPolicy resolves the approval half of an execution policy.
// Legacy combined-profile names are accepted so old callers keep working.
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

// NormalizeSandboxProfile resolves the OS-boundary half of an execution
// policy. Anything unrecognized falls back to workspace-write.
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

// ExecutionPolicyFromLegacySandboxMode maps a legacy combined profile onto
// the independent approval and sandbox dimensions.
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

// ResolveExecutionPolicy applies the persisted-field precedence: explicit new
// fields win; empty fields fall back to the legacy combined profile.
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

// LegacySandboxMode projects an execution policy back onto the old combined
// field. The legacy UI cannot represent split policies, so either dangerous
// half maps to danger-full-access; this is intentionally more alarming than
// under-reporting a missing boundary.
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

// NormalizeSandboxMode maps empty/unknown/legacy values to one of the three
// combined profiles. Anything unrecognized falls back to request-approval.
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

// NormalizeSandboxNetwork maps empty/unknown values to network off.
func NormalizeSandboxNetwork(network string) string {
	if strings.EqualFold(strings.TrimSpace(network), SandboxNetworkOn) {
		return SandboxNetworkOn
	}
	return SandboxNetworkOff
}

// ReasoningLevel maps one selectable thinking level to the request field it
// sends. Param may be dotted ("thinking.budget_tokens") for nested fields.
// When Param is empty, Value must be a JSON object whose members are merged
// into the request body top level — that is how models needing several
// fields at once (e.g. Anthropic adaptive thinking plus output effort) are
// expressed.
type ReasoningLevel struct {
	Level string `json:"level"` // off / low / medium / high / max or a custom label
	Param string `json:"param,omitempty"`
	Value string `json:"value"`
}

// ReasoningConfig is a model's thinking-level table. DefaultLevel picks the
// level used when a chat does not override it; empty falls back to the first
// level. Enabled=false disables the whole table.
type ReasoningConfig struct {
	Enabled      bool             `json:"enabled"`
	DefaultLevel string           `json:"defaultLevel,omitempty"`
	Levels       []ReasoningLevel `json:"levels,omitempty"`
}

// ModelInfo describes a model exposed by a provider.
type ModelInfo struct {
	ID string `json:"id"`
	// DisplayName is the human-friendly label for pickers; empty means use ID.
	DisplayName string `json:"displayName,omitempty"`
	// Multimodal marks models that accept image input; the chat UI uses it
	// to enable the image attachment button.
	Multimodal bool `json:"multimodal,omitempty"`
	// ContextWindow is the model's input context size in tokens (0 = unknown).
	ContextWindow int `json:"contextWindow,omitempty"`
	// MaxOutputTokens is the model's output limit in tokens (0 = unknown).
	// It is the fallback when a run does not specify max output tokens.
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	// PriceIn/PriceOut are optional prices per 1M tokens (input/output),
	// used for cost estimation in the chat UI.
	PriceIn  float64 `json:"priceIn,omitempty"`
	PriceOut float64 `json:"priceOut,omitempty"`
	// Reasoning is the per-model thinking-level table. Nil keeps the
	// provider's default behavior.
	Reasoning *ReasoningConfig `json:"reasoning,omitempty"`
}

// Label is the human-readable name shown in pickers; it falls back to the
// model ID so a blank display name never produces an empty row.
func (m ModelInfo) Label() string {
	if s := strings.TrimSpace(m.DisplayName); s != "" {
		return s
	}
	return m.ID
}

// ActiveReasoning resolves the level a run uses when it has no per-session
// selection: the configured default, else the first level. ok is false when
// the model has no enabled reasoning table or the chosen level has neither a
// parameter nor a value.
func (r *ReasoningConfig) ActiveReasoning() (param, value string, ok bool) {
	return r.Resolve("")
}

// Resolve returns the request field and value for one level. An empty level
// means the configured default, then the first level. An unknown level reports
// false so callers can fall back to the model default instead of sending a
// stale session override.
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

// MatchLevel returns the table's stored spelling of one level name, matched
// case-insensitively. ok is false for a blank or unknown name so callers can
// fall through to the next choice (per-turn → session → model default)
// instead of sending a stale preference.
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

// DefaultLevelName is the level a run with no explicit choice uses: the
// configured default when it names a real level, else the first level. It
// returns "" when the table is disabled, empty, or its default name is stale.
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

// UnmarshalJSON accepts both the legacy entries ("model-id") and the current
// object form, so old config files load unchanged.
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

// ModelProvider describes a configurable LLM provider endpoint.
type ModelProvider struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Protocol  Protocol    `json:"protocol"`
	BaseURL   string      `json:"baseUrl"`
	APIKey    string      `json:"apiKey"`
	Models    []ModelInfo `json:"models"` // model ids usable with this provider
	IsDefault bool        `json:"isDefault"`
	// PriceIn/PriceOut are the legacy provider-level prices. They are copied
	// down to each model and cleared on load (migrateLegacy); kept only so
	// old config files migrate instead of losing data.
	PriceIn  float64 `json:"priceIn,omitempty"`
	PriceOut float64 `json:"priceOut,omitempty"`
}

// FindModel returns the provider's entry for a model ID (exact match).
func (p ModelProvider) FindModel(id string) (ModelInfo, bool) {
	for _, m := range p.Models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// AgentConfig describes a user-defined agent.
type AgentConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ProviderID   string   `json:"providerId"`
	Model        string   `json:"model"`
	SystemPrompt string   `json:"systemPrompt"`
	Temperature  float64  `json:"temperature"`
	MaxTokens    int      `json:"maxTokens"`
	SkillIDs     []string `json:"skillIds"`     // skills attached to this agent
	MCPServerIDs []string `json:"mcpServerIds"` // MCP servers whose tools are available
	// BuiltinTools is an explicit narrowing of the built-in tool categories
	// (time, files, knowledge, memory, todos, shell). Empty means the full
	// set — see AllBuiltinTools — so narrowing is always a deliberate act.
	BuiltinTools []string  `json:"builtinTools"`
	CreatedAt    time.Time `json:"createdAt"`
	// EnableGuardrails masks secrets in user input/model output (default on
	// when unset). Pointers distinguish "unset" from explicit false.
	EnableGuardrails *bool `json:"enableGuardrails,omitempty"`
	// ConfirmTools lists builtin tool categories whose side-effectful calls
	// need human approval before execution (ADK HITL). "files" gates
	// write_workspace_file; "shell" gates every run_command call. Empty =
	// never ask.
	ConfirmTools []string `json:"confirmTools,omitempty"`
	// ApprovalPolicy and SandboxProfile are runtime-only fields filled by the
	// chat service for this build. SandboxMode is the legacy combined profile
	// kept for compatibility with old call sites.
	ApprovalPolicy string `json:"-"`
	SandboxProfile string `json:"-"`
	SandboxMode    string `json:"-"`
	SandboxNetwork string `json:"-"`
	// MaxToolCalls stops a runaway tool loop. 0 = unlimited. Turn duration
	// is intentionally unbounded: long agent tasks must not be killed by a
	// wall-clock cap.
	MaxToolCalls int `json:"maxToolCalls,omitempty"`
	// MaxSessionTokens caps cumulative tokens across the whole session
	// (0 = unlimited). The turn that crosses the budget still completes,
	// the next one is refused.
	MaxSessionTokens int64 `json:"maxSessionTokens,omitempty"`
	// StopSequences halts generation when the model emits one of them
	// (OpenAI "stop" / Anthropic "stop_sequences"). Empty = provider default.
	StopSequences []string `json:"stopSequences,omitempty"`
}

// AllBuiltinTools is the complete built-in tool set an agent gets when its
// BuiltinTools narrowing list is empty. Agents are full-featured by default;
// removing a capability is the explicit action. Keep the frontend
// ALL_BUILTIN_TOOLS list in AgentsPage.tsx in sync.
var AllBuiltinTools = []string{"time", "files", "knowledge", "memory", "todos", "shell"}

// EffectiveBuiltinTools resolves the tool categories an agent mounts: an
// explicit narrowing list wins, an empty one inherits the full set.
func (a AgentConfig) EffectiveBuiltinTools() []string {
	if len(a.BuiltinTools) > 0 {
		return a.BuiltinTools
	}
	return AllBuiltinTools
}

// DefaultProvider returns the provider marked default, falling back to the
// first configured provider. nil means no provider is configured yet.
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

// FindProvider returns the provider with the given ID, or nil when absent.
func (c Config) FindProvider(id string) *ModelProvider {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return &c.Providers[i]
		}
	}
	return nil
}

// ResolveAgentModel fills an agent's unset provider/model from the global
// defaults: a blank provider follows the default provider, and a blank model
// that provider's first model. Explicit values are preserved. This is what
// lets an Agent be defined by name + description + system prompt alone (#39).
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

// RemoteAgentConfig describes a remote agent reached over the A2A protocol
// (Agent-To-Agent, a2aproject/a2a-go). The agent card is discovered from
// CardURL's well-known location.
type RemoteAgentConfig struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CardURL     string    `json:"cardUrl"`
	CreatedAt   time.Time `json:"createdAt"`
}

// TeamConfig describes an agent team: a lead agent coordinating member agents.
type TeamConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	LeadAgentID    string   `json:"leadAgentId"`    // empty => auto coordinator
	AutoCoordinate bool     `json:"autoCoordinate"` // synthesize a coordinator instead of using a member as lead
	MemberAgentIDs []string `json:"memberAgentIds"`
	// RemoteMemberIDs reference RemoteAgents reachable over A2A; they act as
	// sub-agents the coordinator can transfer to. The lead is always local.
	RemoteMemberIDs []string  `json:"remoteMemberIds,omitempty"`
	ProviderID      string    `json:"providerId"` // used by the auto coordinator
	Model           string    `json:"model"`
	SystemPrompt    string    `json:"systemPrompt"`
	CreatedAt       time.Time `json:"createdAt"`
	// Runtime-only execution policy copied to every local member for this
	// build. SandboxMode is the legacy combined profile kept for compatibility.
	ApprovalPolicy string `json:"-"`
	SandboxProfile string `json:"-"`
	SandboxMode    string `json:"-"`
	SandboxNetwork string `json:"-"`
}

// SkillConfig is a reusable instruction bundle (agentskills.io SKILL.md).
type SkillConfig struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"` // markdown body of the skill
	CreatedAt   time.Time `json:"createdAt"`
}

// MCPTransport selects how the MCP server is reached.
type MCPTransport string

const (
	MCPStdio          MCPTransport = "stdio"
	MCPStreamableHTTP MCPTransport = "streamable-http"
)

// MCPServerConfig describes an MCP server connection.
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
	// Streamable-HTTP server authentication:
	// AuthType "" | "bearer" | "api-key" | "oauth2" (authorization code + PKCE
	// per the MCP authorization spec, handled by the official go-sdk handler).
	AuthType          string `json:"authType,omitempty"`
	AuthToken         string `json:"authToken,omitempty"`  // bearer token / api-key value
	AuthHeader        string `json:"authHeader,omitempty"` // api-key header name (default X-API-Key)
	OAuthClientID     string `json:"oauthClientId,omitempty"`
	OAuthRedirectPort int    `json:"oauthRedirectPort,omitempty"` // 0 => ephemeral port
}

// MCPOAuthToken is a cached OAuth token for an MCP server. TokenURL enables
// silent refresh across app restarts (no re-consent while the refresh token
// stays valid).
type MCPOAuthToken struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	TokenURL     string    `json:"tokenUrl,omitempty"`
}

// Execution display modes control how much of a turn's thinking and tool
// process the chat renders. They are display preferences only: the underlying
// events stay persisted so switching modes never loses history.
const (
	ExecutionDisplayAuto     = "auto"
	ExecutionDisplayConcise  = "concise"
	ExecutionDisplayDetailed = "detailed"
	ExecutionDisplayNone     = "none"
)

// NormalizeExecutionDisplayMode clamps a stored global preference to a known
// mode. Empty and stale values fall back to auto so an old config never hides
// the execution block by accident.
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

// Settings holds global application settings.
type Settings struct {
	WorkspaceDir string `json:"workspaceDir"`
	Language     string `json:"language"`
	// Appearance: theme mode "light"|"dark"|"system", theme preset
	// "nord"|"graphite"|"paper"|"contrast", font scale "sm"|"md"|"lg",
	// and whether to speak assistant replies automatically.
	ThemeMode   string `json:"themeMode"`
	ThemePreset string `json:"themePreset,omitempty"`
	// Accent is retained only for backwards-compatible config files. The UI
	// no longer exposes or applies it; themePreset owns the full palette.
	Accent    string `json:"accent,omitempty"`
	FontScale string `json:"fontScale"`
	// ChatWidth selects the chat column width: "narrow" | "medium" | "wide".
	// Widths are percentages of the chat pane, so the column follows window
	// resizing instead of a fixed pixel cap. Empty normalizes to medium.
	ChatWidth string `json:"chatWidth,omitempty"`
	AutoSpeak bool   `json:"autoSpeak"`
	// Semantic knowledge search: an OpenAI-compatible provider id + embedding
	// model. Empty keeps keyword-only search.
	EmbeddingProviderID string `json:"embeddingProviderId,omitempty"`
	EmbeddingModel      string `json:"embeddingModel,omitempty"`
	// A2A server auto-start: publish this agent on launch (port 0 = ephemeral).
	A2AAutoStart bool   `json:"a2aAutoStart,omitempty"`
	A2AAgentID   string `json:"a2aAgentId,omitempty"`
	A2APort      int    `json:"a2aPort,omitempty"`
	// ToolsAudit writes a JSONL audit trail of every tool call/result to the
	// app logs directory.
	ToolsAudit bool `json:"toolsAudit,omitempty"`
	// ApprovalPolicy and SandboxProfile are the default execution policy copied
	// into every newly created chat. Existing chats keep their own snapshot.
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	SandboxProfile string `json:"sandboxProfile,omitempty"`
	// SandboxMode is the legacy combined profile kept for old configs and old
	// frontends. New fields take precedence when both are present.
	SandboxMode string `json:"sandboxMode,omitempty"`
	// SandboxNetwork selects the child-process network policy: off (default)
	// or on.
	SandboxNetwork string `json:"sandboxNetwork,omitempty"`
	// AutoRemember ingests every completed turn into long-term memory
	// automatically (best-effort; failures are silent).
	AutoRemember bool `json:"autoRemember,omitempty"`
	// AutoTitle generates a short conversation title with the model after the
	// first exchange. On by default (nil = on); an explicit false keeps the
	// truncated first-message fallback and avoids the side model call.
	AutoTitle *bool `json:"autoTitle,omitempty"`
	// TitleProviderID/TitleModel select the title-refinement side call.
	// Empty follows the global default provider's first model ("main model")
	// so titles stay clean even when a chat runs on a weak or remote target.
	TitleProviderID string `json:"titleProviderId,omitempty"`
	TitleModel      string `json:"titleModel,omitempty"`
	// HTTPProxy is a global outbound proxy URL (e.g. http://127.0.0.1:7890)
	// applied to model/embedding/MCP requests at startup. Empty = direct.
	HTTPProxy string `json:"httpProxy,omitempty"`
	// Context compaction (ADK session/compaction). Interval counts completed
	// user turns between sliding-window summarizations. ContextPercent is the
	// prompt-token threshold as a percentage of the effective model's context
	// window; nil means the default (65), and 0 explicitly disables tail
	// retention. The old absolute CompactionTokenThreshold is retained only for
	// reading older config files and is no longer used at runtime.
	CompactionInterval       int  `json:"compactionInterval,omitempty"`
	CompactionContextPercent *int `json:"compactionContextPercent,omitempty"`
	CompactionTokenThreshold int  `json:"compactionTokenThreshold,omitempty"`
	// SidebarCollapsed keeps the conversation list collapsed across restarts.
	SidebarCollapsed bool `json:"sidebarCollapsed,omitempty"`
	// SidebarGrouping selects the conversation-list grouping: "time" (default)
	// or "project" (sessions grouped by their workspace root directory).
	SidebarGrouping string `json:"sidebarGrouping,omitempty"`
	// ExecutionDisplayMode controls the default rendering density for a turn's
	// thinking and tool process: "auto" | "concise" | "detailed" | "none".
	// Empty normalizes to auto.
	ExecutionDisplayMode string `json:"executionDisplayMode,omitempty"`
	// AutoCheckUpdate runs a silent update check shortly after startup and
	// then every 24h. Opt-out (nil = on) because an app that only ever updates
	// through a settings-page button effectively never updates.
	AutoCheckUpdate *bool `json:"autoCheckUpdate,omitempty"`
	// SkippedVersion is the release the user asked to ignore. It is the
	// persisted half of "跳过此版本": the updater is told to skip it on every
	// launch, so a check keeps returning "已是最新" until a newer release
	// appears.
	SkippedVersion string `json:"skippedVersion,omitempty"`
}

// AutoTitleEnabled reports whether model-generated titles are wanted.
// Unset means on: the reported UX is a refined one-line title by default;
// users who care about the extra side call can switch it off explicitly.
func (s Settings) AutoTitleEnabled() bool {
	return s.AutoTitle == nil || *s.AutoTitle
}

// CompactionPercent returns the effective tail-retention threshold. A nil
// pointer means the default applies (65%). Values are clamped to 0..100 so a
// hand-edited config cannot overflow the multiplication in the runner.
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

// Chat column width modes. Each maps to a percentage of the chat pane in the
// frontend theme layer, keeping the column adaptive to window resizing.
const (
	ChatWidthNarrow = "narrow"
	ChatWidthMedium = "medium"
	ChatWidthWide   = "wide"
)

// NormalizeChatWidth clamps a stored width preference to a known mode.
// Empty and stale values fall back to medium.
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

// UpdateAutoCheckEnabled reports whether the silent update check runs.
// Unset means on: an opt-in default would leave most installs stuck on the
// version they installed.
func (s Settings) UpdateAutoCheckEnabled() bool {
	return s.AutoCheckUpdate == nil || *s.AutoCheckUpdate
}

// normalize fills zero values (older config files) with defaults.
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

// EffectiveExecutionPolicy resolves the settings' default policy.
func (s Settings) EffectiveExecutionPolicy() (approvalPolicy, sandboxProfile string) {
	return ResolveExecutionPolicy(s.ApprovalPolicy, s.SandboxProfile, s.SandboxMode)
}

// normalizeExecutionPolicy keeps the split fields and the legacy combined
// field consistent. When called after an update, a legacy-only change from an
// old frontend updates the split fields; an explicit new-field change wins.
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

// Config is the root configuration document persisted to disk.
type Config struct {
	Settings     Settings            `json:"settings"`
	Providers    []ModelProvider     `json:"providers"`
	Agents       []AgentConfig       `json:"agents"`
	RemoteAgents []RemoteAgentConfig `json:"remoteAgents,omitempty"`
	Teams        []TeamConfig        `json:"teams"`
	// Projects are named root directories chats can be attached to. A chat
	// bound to a project resolves its workspace to the project root, so many
	// sessions can share one directory without tripping the one-task-per-dir
	// conflict that guards explicitly bound task workspaces.
	Projects []Project `json:"projects,omitempty"`
	// MCPTokens caches OAuth tokens for streamable-http MCP servers
	// (server ID -> token). Secrets live in this 0600 file only.
	MCPTokens  map[string]MCPOAuthToken `json:"mcpTokens,omitempty"`
	Skills     []SkillConfig            `json:"skills"`
	MCPServers []MCPServerConfig        `json:"mcpServers"`
	Sessions   []ChatSession            `json:"sessions"`
}

// ChatSession is a chat conversation bound to an agent, remote agent or team.
type ChatSession struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	TargetType string    `json:"targetType"` // "agent" | "team" | "remote"
	TargetID   string    `json:"targetId"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// ForkedFrom is the original session ID when this chat was created via
	// ForkSession (branching). Empty for organic sessions.
	ForkedFrom string `json:"forkedFrom,omitempty"`
	// Pinned sessions sort first in the sidebar.
	Pinned bool `json:"pinned,omitempty"`
	// Preview is the tail of the latest assistant reply, for the sidebar.
	Preview string `json:"preview,omitempty"`
	// Archived sessions are hidden from the main sidebar list.
	Archived bool `json:"archived,omitempty"`
	// ModelOverride pins this chat to a specific model of the target agent's
	// provider (empty = the agent's configured model). Mirrors the per-chat
	// model picker of the desktop AI clients.
	ModelOverride string `json:"modelOverride,omitempty"`
	// ReasoningLevel is this chat's thinking level (a level name from the
	// effective model's Reasoning table; empty = the model's default level).
	// It is a per-chat UI choice and never writes back to provider config.
	ReasoningLevel string `json:"reasoningLevel,omitempty"`
	// ApprovalPolicy/SandboxProfile are this chat's independent approval and
	// OS-boundary dimensions. SandboxMode is the legacy combined snapshot kept
	// for old frontends. New chats snapshot the global defaults; changing
	// Settings afterwards never rewrites existing chats.
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	SandboxProfile string `json:"sandboxProfile,omitempty"`
	SandboxMode    string `json:"sandboxMode,omitempty"`
	SandboxNetwork string `json:"sandboxNetwork,omitempty"`
	// WorkspaceDir is the task-level directory used by file tools, prompt
	// variables and artifact previews. Empty keeps legacy sessions on the
	// global workspace.
	WorkspaceDir string `json:"workspaceDir,omitempty"`
	// ProjectID attaches this chat to a registered project. Project chats
	// keep WorkspaceDir empty: their effective workspace is the project root
	// (see EffectiveWorkspaceFor), resolved dynamically so renaming or
	// re-rooting a project propagates to every chat in it.
	ProjectID string `json:"projectId,omitempty"`
}

// EffectiveWorkspace resolves the directory a session should use. Explicit
// task workspaces win; legacy sessions fall back to the app-level workspace.
func (s ChatSession) EffectiveWorkspace(global string) string {
	if ws := strings.TrimSpace(s.WorkspaceDir); ws != "" {
		return ws
	}
	return global
}

// Project is a named root directory chats can be attached to.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RootDir   string    `json:"rootDir"`
	CreatedAt time.Time `json:"createdAt"`
}

// EffectiveWorkspaceFor is the project-aware workspace resolution: an
// explicitly bound task dir wins, then the chat's project root, then the
// global workspace.
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

// EffectiveExecutionPolicy resolves this chat's split policy, falling back to
// the global default only for sessions with no snapshot at all.
func (s ChatSession) EffectiveExecutionPolicy(cfg Config) (approvalPolicy, sandboxProfile string) {
	if strings.TrimSpace(s.ApprovalPolicy) != "" ||
		strings.TrimSpace(s.SandboxProfile) != "" ||
		strings.TrimSpace(s.SandboxMode) != "" {
		return ResolveExecutionPolicy(s.ApprovalPolicy, s.SandboxProfile, s.SandboxMode)
	}
	return cfg.Settings.EffectiveExecutionPolicy()
}

// EffectiveApprovalPolicy resolves the approval dimension for this chat.
func (s ChatSession) EffectiveApprovalPolicy(cfg Config) string {
	approval, _ := s.EffectiveExecutionPolicy(cfg)
	return approval
}

// EffectiveSandboxProfile resolves the OS-boundary dimension for this chat.
func (s ChatSession) EffectiveSandboxProfile(cfg Config) string {
	_, profile := s.EffectiveExecutionPolicy(cfg)
	return profile
}

// EffectiveSandboxMode projects the split policy onto the legacy combined
// field for old frontends.
func (s ChatSession) EffectiveSandboxMode(cfg Config) string {
	return LegacySandboxMode(s.EffectiveExecutionPolicy(cfg))
}

// EffectiveSandboxNetwork resolves the chat override, then the global
// setting, then network off. A danger-full-access profile reports network on
// regardless of the persisted value; switching back to workspace-write
// restores that persisted value.
func (s ChatSession) EffectiveSandboxNetwork(cfg Config) string {
	if s.EffectiveSandboxProfile(cfg) == SandboxProfileDangerFullAccess {
		return SandboxNetworkOn
	}
	return s.PersistedSandboxNetwork(cfg)
}

// PersistedSandboxNetwork resolves the network preference without applying
// the danger-profile override. Forking keeps this value so a later switch
// back to workspace-write restores the user's original choice.
func (s ChatSession) PersistedSandboxNetwork(cfg Config) string {
	if network := strings.TrimSpace(s.SandboxNetwork); network != "" {
		return NormalizeSandboxNetwork(network)
	}
	return NormalizeSandboxNetwork(cfg.Settings.SandboxNetwork)
}

// EffectiveApprovalPolicy resolves the approval dimension for an agent build.
func (a AgentConfig) EffectiveApprovalPolicy() string {
	approval, _ := ResolveExecutionPolicy(a.ApprovalPolicy, a.SandboxProfile, a.SandboxMode)
	return approval
}

// EffectiveSandboxProfile resolves the OS-boundary dimension for an agent
// build.
func (a AgentConfig) EffectiveSandboxProfile() string {
	_, profile := ResolveExecutionPolicy(a.ApprovalPolicy, a.SandboxProfile, a.SandboxMode)
	return profile
}

// ApplyExecutionPolicy applies independent approval and sandbox dimensions to
// an agent build. request-approval asks for every builtin write/command,
// auto-approve keeps the agent's explicit prompts, and never clears them.
func (a *AgentConfig) ApplyExecutionPolicy(approvalPolicy, sandboxProfile string) {
	a.ApprovalPolicy = NormalizeApprovalPolicy(approvalPolicy)
	a.SandboxProfile = NormalizeSandboxProfile(sandboxProfile)
	a.SandboxMode = LegacySandboxMode(a.ApprovalPolicy, a.SandboxProfile)
	switch a.ApprovalPolicy {
	case ApprovalPolicyNever:
		a.ConfirmTools = nil
	case ApprovalPolicyRequestApproval:
		a.ConfirmTools = []string{"files", "shell"}
	default: // auto-approve keeps the agent's explicit extra prompts
	}
}

// ApplySandboxProfile is the legacy combined-profile wrapper. New runtime
// code should call ApplyExecutionPolicy with both dimensions.
func (a *AgentConfig) ApplySandboxProfile(mode string) {
	approval, profile := ExecutionPolicyFromLegacySandboxMode(mode)
	a.ApplyExecutionPolicy(approval, profile)
}

// Clone returns an independent copy of the config. Slices, nested slices and
// maps are duplicated, so mutating the clone (e.g. blanking API keys for a
// secret-free export) never touches the live config or its backing arrays.
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

// Store persists Config as JSON and guards concurrent access.
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

// Default returns a config seeded with useful defaults.
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
			CompactionContextPercent: &compactionPercent,
		},
		Skills: []SkillConfig{
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
		},
	}
}

// migrateLegacy converges older config schemas onto the current one: legacy
// provider-level prices move down to each model, and nameless model entries
// are dropped. Runs once per load; the result is persisted by Open.
func (c *Config) migrateLegacy() {
	// Sessions created before execution policies existed have no snapshot. Give
	// each one the current default exactly once, so later Settings changes do
	// not retroactively alter an existing chat's boundary.
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

// Open loads (or creates) the config store under dir.
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
			// Corrupt config: keep the file as backup and start fresh. The
			// backup may contain API keys, so it gets the same 0600 as the
			// live config (WriteFile does not tighten an existing file's
			// mode, hence the explicit Chmod).
			if werr := os.WriteFile(s.path+".bak", data, 0o600); werr == nil {
				_ = os.Chmod(s.path+".bak", 0o600)
			}
			s.cfg = Default()
		}
	}
	s.cfg.Settings.normalize()
	s.cfg.migrateLegacy()
	// Persist normalized defaults so old config files converge on the new schema.
	if _, err := os.Stat(s.path); err == nil {
		_ = s.saveLocked()
	}
	return s, nil
}

// clearMissingExecutionPolicyFields keeps Default() from masquerading as an
// explicit new-schema choice. When an older config has no approvalPolicy /
// sandboxProfile keys, the legacy sandboxMode must drive migration.
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

// NormalizeImported applies the same load-time normalization (settings
// defaults + legacy price migration) that Open gives every persisted config,
// so a backup produced by an older schema imports cleanly.
func (c *Config) NormalizeImported() {
	c.Settings.normalize()
	c.migrateLegacy()
}

// Dir returns the directory holding the config file.
func (s *Store) Dir() string { return filepath.Dir(s.path) }

// SkillsDir returns the directory where skills are materialized as SKILL.md.
func (s *Store) SkillsDir() string { return filepath.Join(s.Dir(), "skills") }

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	return s.writeLocked(data)
}

// writeLocked atomically replaces the config file with the given bytes.
func (s *Store) writeLocked(data []byte) error {
	tmp := s.path + ".tmp"
	// 0600: the file contains API keys.
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// Get returns a deep copy of the current config. Callers may mutate the
// result freely — slices and maps do not share storage with the live config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Clone()
}

// Settings returns a copy of the global settings. Settings contains only value
// fields, so this is cheaper than Get for hot read paths.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Settings
}

// Update applies fn and persists the result. The mutation is applied to a
// deep copy and only swapped into the live config after the disk write
// succeeds: a failed save (disk full, permissions) used to leave memory
// diverged from disk until the next restart silently reverted it.
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
