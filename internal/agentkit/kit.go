package agentkit

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/skilltoolset"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"
	"google.golang.org/genai"

	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
	"agentmate/internal/sandbox"
	"agentmate/internal/terminal"
)

// ModelFactory builds the model.LLM for a provider/model pair.
func NewModelFromProvider(p config.ModelProvider, modelID string) (model.LLM, error) {
	return newModelFromProvider(p, modelID, nil)
}

// NewModelFromProviderWithReasoning builds a model whose requests carry the
// thinking fields resolved from the model's level table. level selects a level
// by name; empty means the model's configured default. Models without a saved
// table fall back to the built-in family table, but only when level is
// non-empty — an unconfigured model keeps the provider default. Side calls
// that should not think (auto-title) keep using NewModelFromProvider.
func NewModelFromProviderWithReasoning(p config.ModelProvider, modelID, level string) (model.LLM, error) {
	var extra map[string]any
	if mi, ok := p.FindModel(modelID); ok {
		var err error
		if extra, err = reasoningExtraBody(mi, level); err != nil {
			return nil, err
		}
	}
	return newModelFromProvider(p, modelID, extra)
}

func newModelFromProvider(p config.ModelProvider, modelID string, extra map[string]any) (model.LLM, error) {
	if modelID == "" {
		return nil, fmt.Errorf("缺少模型 ID")
	}
	switch p.Protocol {
	case config.ProtocolAnthropic:
		return NewAnthropic(AnthropicConfig{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: modelID, ExtraBody: extra})
	case config.ProtocolGemini:
		// The genai SDK owns the request shape; arbitrary extra fields are not
		// merged here. Gemini models therefore ignore the level table at run
		// time (the configuration is still stored and editable).
		return gemini.NewModel(context.Background(), modelID, &genai.ClientConfig{
			APIKey: p.APIKey,
			HTTPOptions: genai.HTTPOptions{
				BaseURL: p.BaseURL, // empty => Google default
			},
		})
	default: // OpenAI-compatible Chat Completions
		return NewOpenAICompat(OpenAICompatConfig{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: modelID, ExtraBody: extra})
	}
}

// Kit assembles ADK agent trees from app configuration.
type Kit struct {
	store     *config.Store
	mcp       *mcpmgr.Manager
	memory    tool.Tool       // long-term memory search tool (nil if memory unavailable)
	knowledge *KnowledgeIndex // workspace knowledge vector index (nil => keyword search)
	// skillMu serializes skill materialization: parallel builds of the same
	// agent (parallel sessions rebuild per turn) must not interleave the
	// write/prune/preload sequence over the shared staging directory.
	skillMu sync.Mutex
	// globalSkillRoot is the user-level agentskills.io discovery root
	// (~/.agents/skills). Captured at construction; tests override it so agent
	// builds never depend on the developer's real home directory.
	globalSkillRoot string
	// globalAgentsFile is the user-level engineering guide
	// (~/.agents/AGENTS.md). Captured at construction, so tests seed it
	// through the home-dir environment before building the Kit.
	globalAgentsFile string
}

// NewKit creates a Kit.
func NewKit(store *config.Store, mcp *mcpmgr.Manager) *Kit {
	return &Kit{store: store, mcp: mcp, globalSkillRoot: config.GlobalSkillRoot(), globalAgentsFile: config.GlobalAgentsFile()}
}

// SetMemoryTool attaches the long-term memory search tool.
func (k *Kit) SetMemoryTool(t tool.Tool) { k.memory = t }

// SetKnowledgeIndex attaches the persistent knowledge vector index; combined
// with an embedding provider in settings this upgrades search_knowledge from
// keyword to hybrid semantic retrieval.
func (k *Kit) SetKnowledgeIndex(idx *KnowledgeIndex) { k.knowledge = idx }

// IndexStats exposes the knowledge index state for observability.
func (k *Kit) IndexStats() KnowledgeIndexStats { return k.knowledge.Stats() }

// TestKnowledgeSearch runs a one-off hybrid knowledge search with current
// settings — a tuning aid for the embedding configuration.
func (k *Kit) TestKnowledgeSearch(query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("检索词不能为空")
	}
	cfg := k.store.Get()
	opts := k.searchOptions(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	opts.Ctx = ctx
	if opts.Workspace == "" {
		return "", fmt.Errorf("请先在设置中配置工作区目录")
	}
	return searchHybrid(opts, query)
}

// ResetKnowledgeIndex wipes the vector index; the next semantic search
// rebuilds it from the workspace files.
func (k *Kit) ResetKnowledgeIndex() error { return k.knowledge.Reset() }

// BuildAgent builds an ADK agent for the given agent config.
func (k *Kit) BuildAgent(ac config.AgentConfig) (agent.Agent, error) {
	return k.BuildAgentWithModelAndWorkspace(ac, "", "")
}

func (k *Kit) buildLLMAgent(ac config.AgentConfig, cfg config.Config, workspace, reasoningLevel string) (agent.Agent, error) {
	return k.buildLLMAgentWithSubAgents(ac, cfg, nil, workspace, reasoningLevel)
}

// BuildAgentWithModel builds an agent pinned to a specific model of its
// provider (per-session model override; empty modelID falls back to the
// agent's configured model).
func (k *Kit) BuildAgentWithModel(ac config.AgentConfig, modelID string) (agent.Agent, error) {
	return k.BuildAgentWithModelAndWorkspace(ac, modelID, "")
}

// BuildAgentWithModelAndWorkspace is the session-aware build entry: model
// override and effective task workspace are resolved together so file tools
// and prompt variables cannot disagree about the directory.
func (k *Kit) BuildAgentWithModelAndWorkspace(ac config.AgentConfig, modelID, workspace string) (agent.Agent, error) {
	return k.BuildAgentWithModelWorkspaceAndReasoning(ac, modelID, workspace, "")
}

// BuildAgentWithModelWorkspaceAndReasoning is the session-aware build entry:
// model override, effective task workspace and the chat's thinking level are
// resolved together so the tools, prompt variables and request body cannot
// disagree about the run.
func (k *Kit) BuildAgentWithModelWorkspaceAndReasoning(ac config.AgentConfig, modelID, workspace, reasoningLevel string) (agent.Agent, error) {
	cfg := k.store.Get()
	// A blank provider/model inherits the global default before any override
	// is clamped, so the per-session picker works on inherited models too.
	ac.ProviderID, ac.Model = cfg.ResolveAgentModel(ac)
	if strings.TrimSpace(modelID) != "" {
		ac.Model = EffectiveModel(cfg, ac.ProviderID, ac.Model, modelID)
	}
	if strings.TrimSpace(workspace) == "" {
		workspace = cfg.Settings.WorkspaceDir
	}
	return k.buildLLMAgent(ac, cfg, workspace, reasoningLevel)
}

// EffectiveModel resolves the model a session actually runs: the per-session
// override when the provider offers it, the agent's configured model
// otherwise. The single clamp shared by agent building, stats pricing and the
// auto-title side call.
func EffectiveModel(cfg config.Config, providerID, configuredModel, override string) string {
	if override != "" && modelSupported(cfg, providerID, override) {
		return override
	}
	return configuredModel
}

// ModelSupported reports whether the provider offers the given model id.
// Exported for callers that only need the boolean check.
func ModelSupported(cfg config.Config, providerID, modelID string) bool {
	return modelSupported(cfg, providerID, modelID)
}

func modelSupported(cfg config.Config, providerID, modelID string) bool {
	for i := range cfg.Providers {
		if cfg.Providers[i].ID != providerID {
			continue
		}
		for _, m := range cfg.Providers[i].Models {
			if m.ID == modelID {
				return true
			}
		}
	}
	return false
}

// applyModelOutputLimit fills an unset agent output limit from the model's
// configured MaxOutputTokens; an explicit agent value always wins.
func applyModelOutputLimit(ac *config.AgentConfig, p config.ModelProvider) {
	if ac.MaxTokens > 0 {
		return
	}
	if mi, ok := p.FindModel(ac.Model); ok && mi.MaxOutputTokens > 0 {
		ac.MaxTokens = mi.MaxOutputTokens
	}
}

func (k *Kit) buildLLMAgentWithSubAgents(ac config.AgentConfig, cfg config.Config, subAgents []agent.Agent, workspace, reasoningLevel string) (agent.Agent, error) {
	// Agents defined without an explicit model follow the global default
	// (form: provider/model left blank). Historical configs with empty values
	// are interpreted the same way instead of failing to run (#39).
	ac.ProviderID, ac.Model = cfg.ResolveAgentModel(ac)
	var provider *config.ModelProvider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == ac.ProviderID {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil {
		if strings.TrimSpace(ac.ProviderID) == "" {
			return nil, fmt.Errorf("尚未配置模型供应商，请先在「模型配置」页添加并填入 API Key")
		}
		return nil, fmt.Errorf("Agent「%s」找不到模型供应商，请重新选择", ac.Name)
	}
	m, err := NewModelFromProviderWithReasoning(*provider, ac.Model, reasoningLevel)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
	}
	// The model's output ceiling is the fallback when the agent does not set
	// one explicitly; the agent-level MaxTokens still wins.
	applyModelOutputLimit(&ac, *provider)

	var tools []tool.Tool
	var toolsets []tool.Toolset

	// Built-in tools. An agent without an explicit narrowing list gets the
	// complete built-in set: full capability is the default, and removing a
	// category is the explicit action (#38).
	builtin, err := k.builtinToolsFor(ac, cfg, workspace)
	if err != nil {
		return nil, err
	}
	tools = append(tools, builtin...)

	// Skills (progressive disclosure via ADK skill toolset).
	if len(ac.SkillIDs) > 0 {
		ts, err := k.skillToolset(ac, cfg, workspace)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
		}
		if ts != nil {
			toolsets = append(toolsets, ts)
		}
	}

	// MCP servers. The per-server 启用 switch is authoritative: enabled
	// servers are injected into every session (direct chat included) without
	// per-agent mounting; disabled ones stay off unless an agent explicitly
	// mounts them. A server whose transport cannot even be built (missing
	// command/URL, OAuth misconfiguration) is reported instead of silently
	// running an agent that is missing tools — the same contract as the
	// skill toolset above.
	explicit := make(map[string]bool, len(ac.MCPServerIDs))
	for _, id := range ac.MCPServerIDs {
		explicit[id] = true
	}
	var mcpErrs []string
	seen := make(map[string]bool, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		seen[s.ID] = true
		if s.Disabled && !explicit[s.ID] {
			continue
		}
		ts, err := k.mcp.Toolset(s)
		if err != nil {
			mcpErrs = append(mcpErrs, fmt.Sprintf("MCP「%s」不可用: %v", s.Name, err))
		} else {
			toolsets = append(toolsets, mcpToolsetForApproval(ts, ac.EffectiveApprovalPolicy()))
		}
	}
	for _, id := range ac.MCPServerIDs {
		if !seen[id] {
			mcpErrs = append(mcpErrs, fmt.Sprintf("MCP 服务器 %q 不存在", id))
		}
	}
	if len(mcpErrs) > 0 {
		return nil, fmt.Errorf("agent %q: %s", ac.Name, strings.Join(mcpErrs, "；"))
	}

	lc := k.llmConfig(ac, m, tools, toolsets, workspace)
	lc.SubAgents = subAgents
	return llmagent.New(lc)
}

// llmConfig assembles the llmagent.Config for an agent config, using an
// InstructionProvider so braces in user prompts are never treated as state
// placeholders.
func (k *Kit) llmConfig(ac config.AgentConfig, m model.LLM, tools []tool.Tool, toolsets []tool.Toolset, workspace string) llmagent.Config {
	instruction := applyPromptVars(ac.SystemPrompt, workspace)
	if strings.TrimSpace(instruction) == "" {
		instruction = "You are a helpful assistant."
	}
	instruction = k.appendAgentGuides(instruction, workspace)
	gcc := &genai.GenerateContentConfig{}
	if ac.Temperature > 0 {
		t := float32(ac.Temperature)
		gcc.Temperature = &t
	}
	if ac.MaxTokens > 0 {
		gcc.MaxOutputTokens = int32(ac.MaxTokens)
	}
	if len(ac.StopSequences) > 0 {
		gcc.StopSequences = ac.StopSequences
	}
	return llmagent.Config{
		Name:                     sanitizeAgentName(ac.Name),
		Model:                    m,
		Description:              firstNonEmpty(ac.Description, ac.Name),
		InstructionProvider:      skillAwareInstruction(instruction),
		GenerateContentConfig:    gcc,
		Tools:                    tools,
		Toolsets:                 toolsets,
		DisallowTransferToParent: false,
	}
}

// appendAgentGuides joins the engineering-guide blocks (global
// ~/.agents/AGENTS.md first, then the effective workspace root AGENTS.md)
// onto a base instruction. Guides complement the role prompt and never block
// a build; the per-turn skill context still appends after them.
func (k *Kit) appendAgentGuides(instruction, workspace string) string {
	guides := config.EffectiveAgentGuides(workspace, k.globalAgentsFile)
	if len(guides) == 0 {
		return instruction
	}
	return instruction + "\n\n" + strings.Join(guides, "\n\n")
}

// BuildTeam builds the root agent for a team: lead agent with members as
// sub-agents (ADK handles delegation/transfer between them), or an
// auto-generated coordinator when requested.
func (k *Kit) BuildTeam(tc config.TeamConfig) (agent.Agent, string, error) {
	return k.BuildTeamWithWorkspace(tc, "")
}

// BuildTeamWithWorkspace builds a team whose local members use the session's
// task workspace.
func (k *Kit) BuildTeamWithWorkspace(tc config.TeamConfig, workspace string) (agent.Agent, string, error) {
	return k.BuildTeamWithWorkspaceAndReasoning(tc, workspace, "")
}

// BuildTeamWithWorkspaceAndReasoning applies the chat's thinking level to the
// team's root agent (explicit lead or synthesized coordinator). Member agents
// keep their own configured models and levels.
func (k *Kit) BuildTeamWithWorkspaceAndReasoning(tc config.TeamConfig, workspace, reasoningLevel string) (agent.Agent, string, error) {
	return k.buildTeam(tc, workspace, reasoningLevel)
}

func (k *Kit) buildTeam(tc config.TeamConfig, workspace, reasoningLevel string) (agent.Agent, string, error) {
	cfg := k.store.Get()
	if strings.TrimSpace(workspace) == "" {
		workspace = cfg.Settings.WorkspaceDir
	}

	memberByID := map[string]config.AgentConfig{}
	for _, a := range cfg.Agents {
		memberByID[a.ID] = a
	}
	// A chat shares one execution policy across its coordinator and every
	// local member.
	approvalPolicy, sandboxProfile := config.ResolveExecutionPolicy(
		tc.ApprovalPolicy, tc.SandboxProfile, tc.SandboxMode,
	)
	for id, ac := range memberByID {
		ac.SandboxNetwork = tc.SandboxNetwork
		ac.ApplyExecutionPolicy(approvalPolicy, sandboxProfile)
		ac.Terminal = tc.Terminal
		memberByID[id] = ac
	}

	var members []agent.Agent
	var memberNames []string
	// memberByKey maps agent/remote IDs to their built agent, so the explicit
	// lead path can assemble its peers without positional indexing (a dangling
	// member ID used to shift the slice and mis-assign or panic).
	memberByKey := map[string]agent.Agent{}
	for _, id := range tc.MemberAgentIDs {
		ac, ok := memberByID[id]
		if !ok {
			continue
		}
		a, err := k.buildLLMAgent(ac, cfg, workspace, "")
		if err != nil {
			return nil, "", err
		}
		members = append(members, a)
		memberNames = append(memberNames, sanitizeAgentName(ac.Name))
		memberByKey[id] = a
	}

	// Remote A2A members join as sub-agents the coordinator can transfer to.
	for _, id := range tc.RemoteMemberIDs {
		for _, r := range cfg.RemoteAgents {
			if r.ID != id {
				continue
			}
			ra, err := BuildRemoteAgent(r)
			if err != nil {
				return nil, "", err
			}
			members = append(members, ra)
			memberNames = append(memberNames, sanitizeAgentName(r.Name))
			memberByKey[id] = ra
		}
	}

	rootName := sanitizeAgentName(tc.Name)

	// Explicit lead: use that agent as root; the other members — local AND
	// remote — become transferable peers.
	if !tc.AutoCoordinate {
		if _, ok := memberByID[tc.LeadAgentID]; ok {
			var peers []agent.Agent
			for _, id := range tc.MemberAgentIDs {
				if id != tc.LeadAgentID {
					if a, ok := memberByKey[id]; ok {
						peers = append(peers, a)
					}
				}
			}
			for _, id := range tc.RemoteMemberIDs {
				if a, ok := memberByKey[id]; ok {
					peers = append(peers, a)
				}
			}
			leadAc := memberByID[tc.LeadAgentID]
			leadAgent, err := k.buildLLMAgentWithSubAgents(leadAc, cfg, peers, workspace, reasoningLevel)
			if err != nil {
				return nil, "", err
			}
			return leadAgent, sanitizeAgentName(leadAc.Name), nil
		}
	}

	// Auto coordinator: synthesized team-lead agent.
	var provider *config.ModelProvider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == tc.ProviderID {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil {
		return nil, "", fmt.Errorf("团队「%s」的协调员缺少模型供应商", tc.Name)
	}
	m, err := NewModelFromProviderWithReasoning(*provider, tc.Model, reasoningLevel)
	if err != nil {
		return nil, "", err
	}
	maxOutput := 0
	if mi, ok := provider.FindModel(tc.Model); ok {
		maxOutput = mi.MaxOutputTokens
	}
	coordInstruction := firstNonEmpty(tc.SystemPrompt,
		"You are the coordinator of an agent team. Analyze the user's request, "+
			"then transfer the task to the most suitable member agent and synthesize the final answer.")
	coordInstruction = applyPromptVars(coordInstruction, workspace)
	instruction := fmt.Sprintf("%s\n\nTeam members: %s. Transfer to a member to work on the task, then summarize the result for the user.",
		coordInstruction, strings.Join(memberNames, ", "))
	instruction = k.appendAgentGuides(instruction, workspace)

	coord, err := llmagent.New(llmagent.Config{
		Name:                rootName,
		Model:               m,
		Description:         firstNonEmpty(tc.Description, tc.Name),
		InstructionProvider: func(agent.ReadonlyContext) (string, error) { return instruction, nil },
		SubAgents:           members,
		GenerateContentConfig: func() *genai.GenerateContentConfig {
			if maxOutput <= 0 {
				return nil
			}
			return &genai.GenerateContentConfig{MaxOutputTokens: int32(maxOutput)}
		}(),
	})
	if err != nil {
		return nil, "", err
	}
	return coord, rootName, nil
}

// searchOptions assembles the knowledge tool options from current settings:
// workspace plus the configured embedding backend, if any.
func (k *Kit) searchOptions(cfg config.Config) SearchOptions {
	opts := SearchOptions{Workspace: cfg.Settings.WorkspaceDir, Index: k.knowledge}
	if cfg.Settings.EmbeddingProviderID == "" || cfg.Settings.EmbeddingModel == "" {
		return opts // keyword-only
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == cfg.Settings.EmbeddingProviderID {
			if cfg.Providers[i].Protocol == config.ProtocolAnthropic || cfg.Providers[i].Protocol == config.ProtocolGemini {
				return opts // embeddings endpoint is OpenAI-compatible only
			}
			opts.Embedding = &EmbeddingConfig{
				BaseURL: cfg.Providers[i].BaseURL,
				APIKey:  cfg.Providers[i].APIKey,
				Model:   cfg.Settings.EmbeddingModel,
			}
			break
		}
	}
	return opts
}

// skillToolset builds an ADK skill toolset covering the selected skills by
// materializing them as SKILL.md under a per-agent staging directory. Failing
// to write a skill is reported instead of silently running an agent that is
// missing a capability the user attached.
//
// Mounted IDs resolve against the effective skill set: project/global skills
// discovered under <workspace>/.agents/skills and ~/.agents/skills take
// precedence over same-named app skills (issue #52), so an app skill that got
// shadowed still materializes the winning definition instead of breaking the
// build. A discovered skill whose directory vanished (workspace switch) is
// skipped silently: external roots are transient by nature. A missing app
// skill keeps the old hard error.
//
// The staging directory is scoped per agent (skills/agents/<key>): the ADK
// filesystem source exposes every skill in the tree it is pointed at, so a
// shared directory would leak skills between agents and keep unmounted or
// deleted skills loadable. Directories not in the wanted set are pruned before
// loading, so detaching or deleting a skill takes effect on the next build.
func (k *Kit) skillToolset(ac config.AgentConfig, cfg config.Config, workspace string) (tool.Toolset, error) {
	k.skillMu.Lock()
	defer k.skillMu.Unlock()
	ids := ac.SkillIDs

	effective := config.EffectiveSkills(cfg, workspace, k.globalSkillRoot)
	byID := make(map[string]config.SkillConfig, len(effective))
	byName := make(map[string]config.SkillConfig, len(effective))
	for _, s := range effective {
		byID[s.ID] = s
		byName[config.NormalizeSkillName(s.Name)] = s
	}
	cfgByID := make(map[string]config.SkillConfig, len(cfg.Skills))
	for _, s := range cfg.Skills {
		cfgByID[s.ID] = s
	}

	wantNames := map[string]bool{}
	var selected []config.SkillConfig
	seen := map[string]bool{}
	// Same no-silent-degradation contract as MCP: a mounted app skill that no
	// longer exists (imported share files are the usual source) is reported,
	// not quietly dropped — the agent would otherwise claim capabilities it
	// never had.
	var missing []string
	for _, id := range ids {
		s, ok := byID[id]
		if !ok {
			if _, discovered := config.DiscoveredSkillSource(id); discovered {
				// The root may have moved with the workspace; fall back to a
				// same-named skill if one is still visible.
				s, ok = byName[config.DiscoveredSkillName(id)]
			} else if old, exists := cfgByID[id]; exists {
				// App skill shadowed by a same-named discovered skill: mount
				// the winning definition (project > global > app).
				s, ok = byName[config.NormalizeSkillName(old.Name)]
			}
			if !ok {
				if _, discovered := config.DiscoveredSkillSource(id); !discovered {
					missing = append(missing, id)
				}
				continue
			}
		}
		key := config.NormalizeSkillName(s.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		wantNames[key] = true
		selected = append(selected, s)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("agent %q: 挂载的技能不存在（请重新选择或导入）: %s", ac.Name, strings.Join(missing, ", "))
	}

	dir := filepath.Join(k.store.SkillsDir(), "agents", agentSkillKey(ac))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create skills dir: %w", err)
	}
	for _, s := range selected {
		name := config.NormalizeSkillName(s.Name)
		dirSkill := filepath.Join(dir, name)
		if err := os.MkdirAll(dirSkill, 0o755); err != nil {
			return nil, fmt.Errorf("create skill dir %q: %w", s.Name, err)
		}
		body, err := skill.Build(&skill.Frontmatter{Name: name, Description: s.Description}, s.Content)
		if err != nil {
			return nil, fmt.Errorf("build skill %q: %w", s.Name, err)
		}
		// Atomic replace: a concurrent preload must never read a half-written
		// (O_TRUNC) SKILL.md as valid-but-empty instructions.
		tmp := filepath.Join(dirSkill, "SKILL.md.tmp")
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return nil, fmt.Errorf("write skill %q: %w", s.Name, err)
		}
		if err := os.Rename(tmp, filepath.Join(dirSkill, "SKILL.md")); err != nil {
			_ = os.Remove(tmp)
			return nil, fmt.Errorf("write skill %q: %w", s.Name, err)
		}
	}
	// Prune skills that are no longer attached to this agent (dangling IDs
	// included: they materialize nothing, so any leftover dir is stale).
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() && !wantNames[e.Name()] {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	src := skill.NewFileSystemSource(os.DirFS(dir))
	preloaded, cleanup, err := skill.WithCompletePreloadSource(context.Background(), src)
	if err != nil {
		return nil, fmt.Errorf("load skills: %w", err)
	}
	_ = cleanup // sources live for the process lifetime
	ts, err := skilltoolset.New(context.Background(), skilltoolset.Config{Source: preloaded})
	if err != nil {
		return nil, fmt.Errorf("create skill toolset: %w", err)
	}
	return ts, nil
}

// agentSkillKey is the per-agent skill staging directory name. The stable
// agent ID is preferred; the sanitized name covers configs without an ID
// (tests, ad-hoc builds).
func agentSkillKey(ac config.AgentConfig) string {
	if ac.ID != "" {
		return ac.ID
	}
	return sanitizeAgentName(ac.Name)
}

// MaterializedSkillName returns the on-disk directory name for a skill.
// Exported so config validation can reject names that would collide with an
// existing skill's materialized directory.
func MaterializedSkillName(name string) string { return sanitizeSkillName(name) }

// SkillContextStateKey carries the turn context — a slash-invoked skill's
// SKILL.md body and the inlined content of @-referenced workspace files — into
// one run. The "temp:" prefix is ADK's temporary-state convention: the value
// reaches the running invocation (so the instruction provider can append it)
// but is trimmed before it is written back to the session, so the next turn
// never inherits a previous turn's context.
const SkillContextStateKey = session.KeyPrefixTemp + "agentmate.skill_context"

// skillAwareInstruction wraps a base system instruction so a turn that was
// started with a "/<skill-name>" command (or @-referenced files) gets that
// turn context appended for the duration of the run. This is the per-turn
// counterpart of mounting a
// skill on an agent: the model sees the full instructions without the skill
// body being stored as part of the user's chat history.
func skillAwareInstruction(base string) func(agent.ReadonlyContext) (string, error) {
	return func(ctx agent.ReadonlyContext) (string, error) {
		out := base
		if ctx != nil {
			if st := ctx.ReadonlyState(); st != nil {
				if v, err := st.Get(SkillContextStateKey); err == nil {
					if extra, ok := v.(string); ok && strings.TrimSpace(extra) != "" {
						out = base + "\n\n" + extra
					}
				}
			}
		}
		return out, nil
	}
}

// RenderSkillMD renders a configured skill exactly as it is materialized on
// disk (frontmatter + body), so a slash-invoked skill and an agent-mounted
// skill receive identical instructions.
func RenderSkillMD(name, description, content string) (string, error) {
	body, err := skill.Build(&skill.Frontmatter{
		Name:        sanitizeSkillName(name),
		Description: description,
	}, content)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// RemoveMaterializedSkill deletes the materialized SKILL.md directories of a
// skill: the per-agent staging dirs and the legacy shared layout. Best effort
// — the next agent build prunes stale entries anyway. Runs under skillMu so a
// concurrent build's write/prune/preload sequence is never torn apart.
func (k *Kit) RemoveMaterializedSkill(skillName string) {
	k.skillMu.Lock()
	defer k.skillMu.Unlock()
	skillsDir := k.store.SkillsDir()
	name := sanitizeSkillName(skillName)
	if agents, err := os.ReadDir(filepath.Join(skillsDir, "agents")); err == nil {
		for _, a := range agents {
			if a.IsDir() {
				_ = os.RemoveAll(filepath.Join(skillsDir, "agents", a.Name(), name))
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(skillsDir, name))
}

// applyPromptVars resolves template variables in system prompts:
// {{date}}, {{time}}, {{datetime}} and {{workspace}} (resolved at each agent
// build, i.e. per turn, so long-running apps stay current).
func applyPromptVars(prompt, workspace string) string {
	now := time.Now()
	return strings.NewReplacer(
		"{{datetime}}", now.Format("2006-01-02 15:04"),
		"{{date}}", now.Format("2006-01-02"),
		"{{time}}", now.Format("15:04"),
		"{{workspace}}", workspace,
	).Replace(prompt)
}

// SanitizeAgentName makes a safe ADK agent name (identifier-like). Exported
// so callers (tests, tooling) can predict the sub-agent name a team's
// coordinator will transfer to.
func SanitizeAgentName(name string) string { return sanitizeAgentName(name) }

// sanitizeAgentName makes a safe ADK agent name (identifier-like).
func sanitizeAgentName(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			sb.WriteRune('_')
		}
	}
	out := strings.Trim(sb.String(), "_")
	if out == "" {
		// Deterministic fallback for non-ASCII names.
		h := fnv.New32a()
		_, _ = h.Write([]byte(name))
		out = fmt.Sprintf("agent_%x", h.Sum32())
	}
	if out == "user" {
		out = "user_agent"
	}
	return out
}

// sanitizeSkillName makes a valid agentskills.io skill name: lowercase
// alphanumerics and hyphens only, no leading/trailing hyphen. The shared
// implementation lives in config so discovery and materialization agree on
// which names collide.
func sanitizeSkillName(name string) string { return config.NormalizeSkillName(name) }

// GuardrailsEnabled reports whether guardrails are on for an agent (default on).
func GuardrailsEnabled(ac config.AgentConfig) bool {
	return ac.EnableGuardrails == nil || *ac.EnableGuardrails
}

// confirmsTool reports whether a builtin tool category was marked as needing
// human-in-the-loop confirmation for this agent.
func confirmsTool(ac config.AgentConfig, category string) bool {
	for _, c := range ac.ConfirmTools {
		if c == category {
			return true
		}
	}
	return false
}

// mcpToolsetForApproval keeps the manager's server-ID cache policy-free and
// applies the session approval policy to a short-lived wrapper for this agent
// build. MCP tools have no reliable risk detector, so every approval policy
// except "never" keeps the confirmation gate.
func mcpToolsetForApproval(ts tool.Toolset, approvalPolicy string) tool.Toolset {
	requireConfirmation := config.NormalizeApprovalPolicy(approvalPolicy) != config.ApprovalPolicyNever
	return tool.WithConfirmation(ts, requireConfirmation, nil)
}

// builtinToolsFor assembles the built-in tools an agent runs with. An empty
// narrowing list means the complete built-in set (config.AllBuiltinTools):
// agents are full-featured unless the user explicitly removed categories.
func (k *Kit) builtinToolsFor(ac config.AgentConfig, cfg config.Config, workspace string) ([]tool.Tool, error) {
	var tools []tool.Tool
	policy := k.sandboxPolicy(ac, workspace)
	fileConfirmation := confirmsTool(ac, "files")
	shellConfirmation := confirmsTool(ac, "shell")
	for _, name := range ac.EffectiveBuiltinTools() {
		switch name {
		case "time":
			t, err := TimeTool()
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, t)
		case "files":
			builtin, err := workspaceFileTools(workspace, fileConfirmation, policy)
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, builtin...)
		case "knowledge":
			t, err := SearchKnowledgeTool(k.searchOptions(cfg))
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, t)
		case "memory":
			if k.memory != nil {
				tools = append(tools, k.memory)
			}
		case "todos":
			t, err := TodosTool()
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, t)
		case "shell":
			allowEscalation := policy.Mode != sandbox.ModeDangerFullAccess
			termSettings := cfg.Settings.Terminal
			if ac.Terminal != nil {
				termSettings = *ac.Terminal
			}
			spec, err := terminal.Resolve(context.Background(), termSettings, terminal.DefaultHost(), string(policy.Mode))
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			t, err := ShellToolWithSpec(workspace, shellConfirmation, ac.EffectiveApprovalPolicy(), policy, allowEscalation, spec)
			if err != nil {
				return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
			}
			tools = append(tools, t)
		}
	}
	return tools, nil
}

func (k *Kit) sandboxPolicy(ac config.AgentConfig, workspace string) sandbox.Policy {
	var protectedDirs []string
	if k.store != nil {
		protectedDirs = append(protectedDirs, k.store.Dir())
	}
	policy := SessionPolicy(workspace, ac.EffectiveSandboxProfile(), ac.SandboxNetwork, protectedDirs...)
	if k.store != nil {
		policy = policy.WithProxy(k.store.Settings().HTTPProxy)
	}
	return policy
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// SessionPolicy builds the execution policy shared by built-in tools and the
// chat service's read-only confirmation preview. It is the single place that
// maps an approval-independent sandbox profile + network setting onto a
// sandbox.Policy, so a prompt can never disagree with the boundary that will
// execute the call.
func SessionPolicy(workspace, profile, network string, protectedDirs ...string) sandbox.Policy {
	policy := sandbox.DefaultPolicy(workspace)
	switch config.NormalizeSandboxProfile(profile) {
	case config.SandboxProfileDangerFullAccess:
		policy.Mode = sandbox.ModeDangerFullAccess
		// The direct backend has no AppContainer capability gate; report the
		// effective network state instead of leaving a misleading "off".
		policy.Network = sandbox.NetworkOn
	case config.SandboxProfileReadOnly:
		policy.Mode = sandbox.ModeReadOnly
		policy.Network = sandbox.Network(config.NormalizeSandboxNetwork(network))
	default:
		policy.Mode = sandbox.ModeWorkspaceWrite
		policy.Network = sandbox.Network(config.NormalizeSandboxNetwork(network))
	}
	for _, dir := range protectedDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		// Tests and unusual deployments can place the config store above the
		// selected workspace; protecting an ancestor would block every write.
		if !sandbox.Within(dir, policy.Workspace) {
			policy = policy.WithProtected(dir)
		}
	}
	return policy
}

// PreviewGuard is a read-only view of the same workspace policy used by the
// file and shell tools. The confirmation providers and the chat service's
// hint builder share it: a preflight never creates, changes or removes files.
type PreviewGuard struct {
	workspace string
	policy    sandbox.Policy
}

// NewPreviewGuard builds a guard for a workspace and sandbox profile. It is
// safe to call on every turn; no state is written.
func NewPreviewGuard(workspace, profile, network string, protectedDirs ...string) PreviewGuard {
	return PreviewGuard{
		workspace: workspace,
		policy:    SessionPolicy(workspace, profile, network, protectedDirs...),
	}
}

// NewPreviewGuardWithPolicy reuses a policy that was already built for the
// tools, avoiding a second, potentially disagreeing resolution.
func NewPreviewGuardWithPolicy(workspace string, policy sandbox.Policy) PreviewGuard {
	return PreviewGuard{workspace: workspace, policy: policy}
}

// CheckWritePath reports whether a workspace-relative path may be written.
// It resolves traversal and symlinks but never touches the target file.
func (g PreviewGuard) CheckWritePath(rel string) (bool, string, string) {
	root, err := safeWorkspacePath(g.workspace, rel)
	if err != nil {
		return false, "outside_workspace", err.Error()
	}
	return g.policy.CheckWrite(root)
}

// CheckCommand runs the shell preflight without starting a process.
func (g PreviewGuard) CheckCommand(command string) (bool, string, string) {
	return g.policy.CheckCommand(command)
}

// ---------------------------------------------------------------------------
// Built-in tools

type timeArgs struct {
	Timezone string `json:"timezone,omitempty" jsonschema:"IANA timezone such as Asia/Shanghai; empty for local time"`
}
type timeResult struct {
	Now      string `json:"now"`
	Timezone string `json:"timezone"`
}

// workspaceFileTools builds the list/read/write/edit set for the "files"
// category.
func workspaceFileTools(workspace string, requireConfirmation bool, policy sandbox.Policy) ([]tool.Tool, error) {
	list, err := ListFilesTool(workspace)
	if err != nil {
		return nil, err
	}
	read, err := ReadFileTool(workspace)
	if err != nil {
		return nil, err
	}
	write, err := WriteFileToolWithPolicy(workspace, requireConfirmation, policy)
	if err != nil {
		return nil, err
	}
	edit, err := EditFileToolWithPolicy(workspace, requireConfirmation, policy)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{list, read, write, edit}, nil
}

// TimeTool reports the current time.
func TimeTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "get_current_time",
		Description: "Get the current date and time, optionally in a specific IANA timezone.",
	}, func(ctx agent.Context, args timeArgs) (timeResult, error) {
		tz := time.Local
		name := "local"
		if args.Timezone != "" {
			loc, err := time.LoadLocation(args.Timezone)
			if err != nil {
				return timeResult{}, fmt.Errorf("未知时区 %q", args.Timezone)
			}
			tz = loc
			name = args.Timezone
		}
		return timeResult{Now: time.Now().In(tz).Format(time.RFC3339), Timezone: name}, nil
	})
}

type pathArgs struct {
	Path string `json:"path" jsonschema:"relative path inside the workspace"`
}

// listArgs keeps Path optional: listing the workspace root is the common
// first call and should not fail JSON-schema validation.
type listArgs struct {
	Path string `json:"path,omitempty" jsonschema:"relative path inside the workspace; omit for the workspace root"`
}

// SafeWorkspacePath resolves rel inside workspace and blocks traversal.
func SafeWorkspacePath(workspace, rel string) (string, error) {
	return safeWorkspacePath(workspace, rel)
}

const (
	maxReadBytes  = 64 * 1024
	maxWriteBytes = 6 << 20 // single write cap, same order as image attachments
)

// ReadBoundedFile reads at most limit bytes from path, stopping as soon as
// the cap is reached (a huge file must not be loaded into memory just to be
// truncated). The returned bool reports truncation; truncated content is cut
// back to the nearest UTF-8 rune boundary and carries a trailing marker.
func ReadBoundedFile(path string, limit int) (content string, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	buf := make([]byte, limit+1)
	n, readErr := io.ReadFull(f, buf)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", false, readErr
	}
	truncated = n > limit
	if truncated {
		buf = buf[:limit]
		// Trim back to the last COMPLETE rune: a cut may land inside a
		// multi-byte sequence (a bare lead byte is RuneStart too).
		for len(buf) > 0 {
			if r, size := utf8.DecodeLastRune(buf); r != utf8.RuneError || size > 1 {
				break
			}
			buf = buf[:len(buf)-1]
		}
		content = string(buf) + "\n...[truncated]"
		return content, true, nil
	}
	return string(buf[:n]), false, nil
}

// WriteBoundedFile refuses payloads above limit bytes up front so a runaway
// generation cannot fill the disk; the file is not created on rejection.
func WriteBoundedFile(path, content string, limit int) error {
	if len(content) > limit {
		return fmt.Errorf("写入内容超过 %d MB 上限（%d 字节）", limit>>20, len(content))
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func safeWorkspacePath(workspace, rel string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("未配置工作区目录")
	}
	absRoot, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(absRoot, rel))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(full, absRoot+string(filepath.Separator)) && full != absRoot {
		return "", fmt.Errorf("路径越出工作区范围")
	}
	// The lexical check above cannot see symlinks: a link inside the
	// workspace pointing at ~/.ssh passes it. Resolve both sides and compare
	// again. A path that does not exist yet (write target, possibly with new
	// parent directories) is validated through its deepest existing ancestor.
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", err
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err == nil {
		if !isWithin(realRoot, realFull) {
			return "", fmt.Errorf("路径越出工作区范围")
		}
		return realFull, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	ancestor := filepath.Dir(full)
	for {
		realAncestor, aerr := filepath.EvalSymlinks(ancestor)
		if aerr == nil {
			if !isWithin(realRoot, realAncestor) {
				return "", fmt.Errorf("路径越出工作区范围")
			}
			// Use the resolved root spelling for not-yet-existing targets so
			// Windows 8.3 (short) and long paths compare as one workspace.
			return filepath.Join(realRoot, rel), nil
		}
		if !os.IsNotExist(aerr) {
			return "", aerr
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", aerr
		}
		ancestor = parent
	}
}

// isWithin reports whether target is root itself or lives under it.
func isWithin(root, target string) bool {
	return target == root || strings.HasPrefix(target, root+string(filepath.Separator))
}

// ListFilesTool lists workspace files. Path is optional so a model can ask
// for the workspace root without having to guess "." (issue #21: the first
// "look at the project" call is usually argument-less).
func ListFilesTool(workspace string) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "list_workspace_files",
		Description: "List files and folders under a path inside the user's workspace (omit path for the workspace root).",
	}, func(ctx agent.Context, args listArgs) (filesResult, error) {
		root, err := safeWorkspacePath(workspace, args.Path)
		if err != nil {
			return filesResult{}, err
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return filesResult{}, err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name()+"/")
			} else {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		return filesResult{Entries: strings.Join(names, "\n")}, nil
	})
}

type readFileResult struct {
	Content string `json:"content"`
}

// ReadFileTool reads a file within the workspace.
func ReadFileTool(workspace string) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "read_workspace_file",
		Description: "Read a text file inside the user's workspace (max 64KB).",
	}, func(ctx agent.Context, args pathArgs) (readFileResult, error) {
		root, err := safeWorkspacePath(workspace, args.Path)
		if err != nil {
			return readFileResult{}, err
		}
		content, _, err := ReadBoundedFile(root, maxReadBytes)
		if err != nil {
			return readFileResult{}, err
		}
		return readFileResult{Content: content}, nil
	})
}

type writeArgs struct {
	Path    string `json:"path" jsonschema:"relative path inside the workspace"`
	Content string `json:"content"`
}

type writeResult struct {
	OK    bool `json:"ok"`
	Bytes int  `json:"bytes"`
}

type editArgs struct {
	Path       string `json:"path" jsonschema:"relative path inside the workspace"`
	OldText    string `json:"oldText" jsonschema:"exact text to replace; include enough surrounding context to be unique"`
	NewText    string `json:"newText" jsonschema:"replacement text"`
	ReplaceAll bool   `json:"replaceAll,omitempty" jsonschema:"replace every occurrence when true; the default requires a unique match"`
}

type editResult struct {
	OK           bool   `json:"ok"`
	Bytes        int    `json:"bytes"`
	AddedLines   int    `json:"addedLines"`
	RemovedLines int    `json:"removedLines"`
	Diff         string `json:"diff"`
}

// WriteFileTool writes a file within the workspace. When requireConfirmation
// is true the tool asks the human operator to approve each call first (ADK
// HITL): the run pauses on an adk_request_confirmation interrupt and only
// proceeds after a FunctionResponse with {"confirmed": true}.
func WriteFileTool(workspace string, requireConfirmation bool) (tool.Tool, error) {
	return WriteFileToolWithPolicy(workspace, requireConfirmation, sandbox.DefaultPolicy(workspace))
}

// WriteFileToolWithPolicy adds the session's protected-path policy to the
// in-process write path. The OS sandbox covers child processes; this check is
// the equivalent boundary for file tools, which do not spawn a process.
func WriteFileToolWithPolicy(workspace string, requireConfirmation bool, policy sandbox.Policy) (tool.Tool, error) {
	guard := NewPreviewGuardWithPolicy(workspace, policy)
	return functiontool.New(functiontool.Config{
		Name:                "write_workspace_file",
		Description:         "Create or overwrite a text file inside the user's workspace (single write up to 6MB).",
		RequireConfirmation: requireConfirmation,
		RequireConfirmationProvider: func(args writeArgs) bool {
			// A path the handler will reject must not interrupt the user with a
			// dead-end approval: skip HITL and let the handler return the error.
			if strings.TrimSpace(args.Path) == "" {
				return false
			}
			if ok, _, _ := guard.CheckWritePath(args.Path); !ok {
				return false
			}
			return requireConfirmation
		},
	}, func(ctx agent.Context, args writeArgs) (writeResult, error) {
		return writeWorkspaceFile(workspace, args, policy)
	})
}

func writeWorkspaceFile(workspace string, args writeArgs, policy sandbox.Policy) (writeResult, error) {
	root, err := safeWorkspacePath(workspace, args.Path)
	if err != nil {
		return writeResult{}, err
	}
	if ok, code, reason := policy.CheckWrite(root); !ok {
		return writeResult{}, fmt.Errorf("%s: %s", code, reason)
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return writeResult{}, err
	}
	if err := WriteBoundedFile(root, args.Content, maxWriteBytes); err != nil {
		return writeResult{}, err
	}
	return writeResult{OK: true, Bytes: len(args.Content)}, nil
}

// EditFileTool edits an existing workspace file by replacing oldText with
// newText. It is deliberately separate from write_workspace_file: a targeted
// replacement keeps unrelated bytes (notably CRLF line endings) intact and
// gives the model a diff instead of forcing a full rewrite.
func EditFileTool(workspace string, requireConfirmation bool) (tool.Tool, error) {
	return EditFileToolWithPolicy(workspace, requireConfirmation, sandbox.DefaultPolicy(workspace))
}

// EditFileToolWithPolicy adds the session's protected-path policy to the
// in-process edit path, the same boundary write_workspace_file uses.
func EditFileToolWithPolicy(workspace string, requireConfirmation bool, policy sandbox.Policy) (tool.Tool, error) {
	guard := NewPreviewGuardWithPolicy(workspace, policy)
	return functiontool.New(functiontool.Config{
		Name: "edit_workspace_file",
		Description: "修改已有文件优先用本工具：用 oldText/newText 做精确替换；" +
			"新建或整体重写用 write_workspace_file。默认 oldText 必须唯一匹配，" +
			"replaceAll=true 可替换全部；返回 unified diff（最多 200 行 / 32KB）。",
		RequireConfirmation: requireConfirmation,
		RequireConfirmationProvider: func(args editArgs) bool {
			if strings.TrimSpace(args.Path) == "" || args.OldText == "" {
				return false
			}
			if ok, _, _ := guard.CheckWritePath(args.Path); !ok {
				return false
			}
			return requireConfirmation
		},
	}, func(ctx agent.Context, args editArgs) (editResult, error) {
		return editWorkspaceFile(workspace, args, policy)
	})
}

func editWorkspaceFile(workspace string, args editArgs, policy sandbox.Policy) (editResult, error) {
	if args.OldText == "" {
		return editResult{}, fmt.Errorf("oldText 不能为空")
	}
	root, err := safeWorkspacePath(workspace, args.Path)
	if err != nil {
		return editResult{}, err
	}
	if ok, code, reason := policy.CheckWrite(root); !ok {
		return editResult{}, fmt.Errorf("%s: %s", code, reason)
	}
	info, err := os.Stat(root)
	if err != nil {
		return editResult{}, err
	}
	if info.IsDir() {
		return editResult{}, fmt.Errorf("目标路径是目录，不能编辑")
	}
	if info.Size() > int64(maxWriteBytes) {
		return editResult{}, fmt.Errorf("文件超过 %d MB 上限", maxWriteBytes>>20)
	}
	raw, err := os.ReadFile(root)
	if err != nil {
		return editResult{}, err
	}
	original := string(raw)
	count := strings.Count(original, args.OldText)
	if count == 0 {
		return editResult{}, fmt.Errorf("未找到匹配内容")
	}
	if count > 1 && !args.ReplaceAll {
		return editResult{}, fmt.Errorf("匹配到 %d 处，请提供更精确上下文或 replaceAll=true", count)
	}
	updated := strings.Replace(original, args.OldText, args.NewText, 1)
	if args.ReplaceAll {
		updated = strings.ReplaceAll(original, args.OldText, args.NewText)
	}
	if len(updated) > maxWriteBytes {
		return editResult{}, fmt.Errorf("写入内容超过 %d MB 上限（%d 字节）", maxWriteBytes>>20, len(updated))
	}
	if err := writeFilePreservingMode(root, []byte(updated), info.Mode()); err != nil {
		return editResult{}, err
	}
	diff, added, removed := unifiedWorkspaceDiff(args.Path, original, updated, maxDiffLines, maxDiffBytes)
	return editResult{
		OK:           true,
		Bytes:        len(updated),
		AddedLines:   added,
		RemovedLines: removed,
		Diff:         diff,
	}, nil
}

// writeFilePreservingMode writes through a sibling temporary file and renames
// it over the target. The directory is the same, so the rename is atomic on
// the same volume, and an interrupted write never leaves a half-written file.
func writeFilePreservingMode(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentmate-edit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}
	defer cleanup()
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

const (
	maxDiffLines = 200
	maxDiffBytes = 32 * 1024
)

type diffOp struct {
	kind byte // ' ' context, '-' removed, '+' added
	text string
}

// unifiedWorkspaceDiff renders a bounded unified diff with a small line LCS.
// A pathological pair of large files falls back to a coarse remove/add block:
// still a valid diff, but without spending quadratic memory.
func unifiedWorkspaceDiff(path, oldText, newText string, lineLimit, byteLimit int) (string, int, int) {
	ops := lineDiffOps(oldText, newText)
	added, removed := 0, 0
	for _, op := range ops {
		switch op.kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	var lines []string
	var changes []int
	for i, op := range ops {
		if op.kind != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return "", added, removed
	}
	oldBefore := make([]int, len(ops)+1)
	newBefore := make([]int, len(ops)+1)
	for i, op := range ops {
		oldBefore[i+1] = oldBefore[i]
		newBefore[i+1] = newBefore[i]
		if op.kind != '+' {
			oldBefore[i+1]++
		}
		if op.kind != '-' {
			newBefore[i+1]++
		}
	}
	const context = 3
	displayPath := filepath.ToSlash(strings.TrimPrefix(path, "./"))
	if displayPath == "" {
		displayPath = "file"
	}
	lines = append(lines, "--- a/"+displayPath, "+++ b/"+displayPath)
	start := changes[0] - context
	if start < 0 {
		start = 0
	}
	end := changes[0] + context + 1
	if end > len(ops) {
		end = len(ops)
	}
	flushHunk := func(hunkStart, hunkEnd int) {
		oldCount := oldBefore[hunkEnd] - oldBefore[hunkStart]
		newCount := newBefore[hunkEnd] - newBefore[hunkStart]
		oldStart := oldBefore[hunkStart]
		newStart := newBefore[hunkStart]
		if oldCount > 0 {
			oldStart++
		}
		if newCount > 0 {
			newStart++
		}
		lines = append(lines, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount))
		for _, op := range ops[hunkStart:hunkEnd] {
			lines = append(lines, string(op.kind)+op.text)
		}
	}
	for _, idx := range changes[1:] {
		nextStart := idx - context
		if nextStart <= end {
			nextEnd := idx + context + 1
			if nextEnd > len(ops) {
				nextEnd = len(ops)
			}
			if nextEnd > end {
				end = nextEnd
			}
			continue
		}
		flushHunk(start, end)
		start = nextStart
		if start < 0 {
			start = 0
		}
		end = idx + context + 1
		if end > len(ops) {
			end = len(ops)
		}
	}
	flushHunk(start, end)
	return capDiffLines(lines, lineLimit, byteLimit), added, removed
}

func lineDiffOps(oldText, newText string) []diffOp {
	a := splitDiffLines(oldText)
	b := splitDiffLines(newText)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && diffLineText(a[prefix]) == diffLineText(b[prefix]) {
		prefix++
	}
	endA, endB := len(a), len(b)
	for endA > prefix && endB > prefix && diffLineText(a[endA-1]) == diffLineText(b[endB-1]) {
		endA--
		endB--
	}
	ops := make([]diffOp, 0, len(a)+len(b))
	for i := 0; i < prefix; i++ {
		ops = append(ops, diffOp{' ', diffLineText(a[i])})
	}
	ops = append(ops, lineLCSOps(a[prefix:endA], b[prefix:endB])...)
	for i := endA; i < len(a); i++ {
		ops = append(ops, diffOp{' ', diffLineText(a[i])})
	}
	return ops
}

func lineLCSOps(a, b []string) []diffOp {
	const maxCells = 1_000_000
	if len(a) == 0 || len(b) == 0 || len(a)*len(b) > maxCells {
		out := make([]diffOp, 0, len(a)+len(b))
		for _, line := range a {
			out = append(out, diffOp{'-', diffLineText(line)})
		}
		for _, line := range b {
			out = append(out, diffOp{'+', diffLineText(line)})
		}
		return out
	}
	an := make([]string, len(a))
	bn := make([]string, len(b))
	for i, line := range a {
		an[i] = diffLineText(line)
	}
	for i, line := range b {
		bn[i] = diffLineText(line)
	}
	width := len(bn) + 1
	dp := make([]int32, (len(an)+1)*width)
	for i := len(an) - 1; i >= 0; i-- {
		row := i * width
		next := (i + 1) * width
		for j := len(bn) - 1; j >= 0; j-- {
			switch {
			case an[i] == bn[j]:
				dp[row+j] = dp[next+j+1] + 1
			case dp[next+j] >= dp[row+j+1]:
				dp[row+j] = dp[next+j]
			default:
				dp[row+j] = dp[row+j+1]
			}
		}
	}
	out := make([]diffOp, 0, len(an)+len(bn))
	i, j := 0, 0
	for i < len(an) && j < len(bn) {
		switch {
		case an[i] == bn[j]:
			out = append(out, diffOp{' ', an[i]})
			i++
			j++
		case dp[(i+1)*width+j] >= dp[i*width+j+1]:
			out = append(out, diffOp{'-', an[i]})
			i++
		default:
			out = append(out, diffOp{'+', bn[j]})
			j++
		}
	}
	for ; i < len(an); i++ {
		out = append(out, diffOp{'-', an[i]})
	}
	for ; j < len(bn); j++ {
		out = append(out, diffOp{'+', bn[j]})
	}
	return out
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			return out
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
		if s == "" {
			return out
		}
	}
}

func diffLineText(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}

func capDiffLines(lines []string, lineLimit, byteLimit int) string {
	if lineLimit <= 0 {
		lineLimit = maxDiffLines
	}
	if byteLimit <= 0 {
		byteLimit = maxDiffBytes
	}
	const marker = "... [diff 已截断]"
	var b strings.Builder
	written := 0
	for i, line := range lines {
		// Reserve room for the truncation marker unless this is the final
		// line, so a diff that fits exactly is never marked as truncated.
		reserve := 0
		if i < len(lines)-1 {
			reserve = len(marker) + 1
		}
		if written+1 > lineLimit ||
			(i < len(lines)-1 && written+2 > lineLimit) ||
			b.Len()+len(line)+1+reserve > byteLimit {
			if b.Len() > 0 {
				b.WriteString(marker)
				b.WriteByte('\n')
			} else {
				b.WriteString(marker)
			}
			return b.String()
		}
		b.WriteString(line)
		b.WriteByte('\n')
		written++
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Session plan (todo list) tool

// todosStateKey stores the plan in the ADK session state so it survives
// restarts and stays scoped to the conversation.
const todosStateKey = "agentmate.todos"

type todoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

type todosArgs struct {
	// Todos is the FULL replacement list. Omitted => read the current plan
	// without writing; empty => clear it.
	Todos []todoItem `json:"todos,omitempty"`
}

type todosResult struct {
	OK    bool       `json:"ok"`
	Todos []todoItem `json:"todos"`
}

// TodosTool gives the model a session-scoped plan (todo list) for multi-step
// work: it rewrites the list as steps are created, started and completed, and
// the user follows along on the tool-result card. Mirrors the plan tools of
// the coding-agent desktops this app takes after.
func TodosTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "plan_todos",
		Description: "Maintain the session's task plan (todo list). Pass the FULL list of steps with status pending/in_progress/completed each time — mark a step in_progress before starting it and completed right after finishing it, creating new steps as work emerges. Omit 'todos' to read the current plan. Use it for any multi-step task so the user can follow progress.",
	}, func(ctx agent.Context, args todosArgs) (todosResult, error) {
		if ctx.State() == nil {
			return todosResult{}, fmt.Errorf("会话状态不可用")
		}
		if args.Todos == nil {
			// Read-only: return the stored plan.
			cur, err := ctx.State().Get(todosStateKey)
			if err != nil {
				return todosResult{OK: true}, nil // no plan yet
			}
			var todos []todoItem
			b, _ := json.Marshal(cur)
			if json.Unmarshal(b, &todos) != nil {
				return todosResult{OK: true}, nil
			}
			return todosResult{OK: true, Todos: todos}, nil
		}
		cleaned := make([]todoItem, 0, len(args.Todos))
		for _, td := range args.Todos {
			td.Content = strings.TrimSpace(td.Content)
			if td.Content == "" {
				continue
			}
			switch td.Status {
			case "pending", "in_progress", "completed":
			default:
				td.Status = "pending"
			}
			cleaned = append(cleaned, td)
		}
		if err := ctx.State().Set(todosStateKey, cleaned); err != nil {
			return todosResult{}, err
		}
		return todosResult{OK: true, Todos: cleaned}, nil
	})
}
