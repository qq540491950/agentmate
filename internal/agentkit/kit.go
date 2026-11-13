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

// NewModelFromProvider 为供应商/模型组合构建 model.LLM。
func NewModelFromProvider(p config.ModelProvider, modelID string) (model.LLM, error) {
	return newModelFromProvider(p, modelID, nil)
}

// NewModelFromProviderWithReasoning 构建一个模型，其请求会携带由该模型级别表解析出的 thinking 字段。level
// 按名称选择级别；为空表示使用该模型配置的默认值。没有已保存级别表的模型会回退
// 到内置的家族表，但仅在 level
// 非空时如此——未配置的模型保留供应商默认值。不应进行思考的旁路调用（自动标题）
// 继续使用 NewModelFromProvider。
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
		// 请求的结构由 genai SDK 掌管；任意的额外字段不会在此合并。因此 Gemini
		// 模型在运行时忽略级别表（配置仍会被存储且可编辑）。
		return gemini.NewModel(context.Background(), modelID, &genai.ClientConfig{
			APIKey: p.APIKey,
			HTTPOptions: genai.HTTPOptions{
				BaseURL: p.BaseURL, // 为空 => Google 默认值
			},
		})
	default: // OpenAI 兼容的 Chat Completions
		return NewOpenAICompat(OpenAICompatConfig{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: modelID, ExtraBody: extra})
	}
}

// Kit 从应用配置组装 ADK Agent 树。
type Kit struct {
	store     *config.Store
	mcp       *mcpmgr.Manager
	memory    tool.Tool       // 长期记忆搜索工具（记忆不可用时为 nil）
	knowledge *KnowledgeIndex // 工作区知识向量索引（nil => 关键词搜索）
	// skillMu 串行化技能物化：同一 agent
	// 的并行构建（并行会话按回合重建）不得在共享暂存目录上交错执行写入/清理/预加载
	// 序列。
	skillMu sync.Mutex
	// globalSkillRoot 是用户级 agentskills.io
	// 发现根目录（~/.agents/skills）。在构造时捕获；测试会覆盖它，因此 agent
	// 构建绝不依赖开发者的真实主目录。
	globalSkillRoot string
	// globalAgentsFile 是用户级工程指南（~/.agents/AGENTS.md）。在构造时捕获，因此测试会在构建 Kit
	// 之前通过 home-dir 环境预置它。
	globalAgentsFile string
}

// NewKit 创建一个 Kit。
func NewKit(store *config.Store, mcp *mcpmgr.Manager) *Kit {
	return &Kit{store: store, mcp: mcp, globalSkillRoot: config.GlobalSkillRoot(), globalAgentsFile: config.GlobalAgentsFile()}
}

// SetMemoryTool 挂接长期记忆搜索工具。
func (k *Kit) SetMemoryTool(t tool.Tool) { k.memory = t }

// SetKnowledgeIndex 挂接持久化的知识向量索引；与设置中的嵌入供应商结合后，这会将 search_knowledge
// 从关键词检索升级为混合语义检索。
func (k *Kit) SetKnowledgeIndex(idx *KnowledgeIndex) { k.knowledge = idx }

// IndexStats 为可观测性暴露知识索引状态。
func (k *Kit) IndexStats() KnowledgeIndexStats { return k.knowledge.Stats() }

// TestKnowledgeSearch 使用当前设置运行一次性的混合知识搜索——这是用于嵌入配置调优的辅助手段。
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

// ResetKnowledgeIndex 会清空向量索引；下一次语义搜索会从工作区文件重建它。
func (k *Kit) ResetKnowledgeIndex() error { return k.knowledge.Reset() }

// BuildAgent 为给定的 agent 配置构建一个 ADK agent。
func (k *Kit) BuildAgent(ac config.AgentConfig) (agent.Agent, error) {
	return k.BuildAgentWithModelAndWorkspace(ac, "", "")
}

func (k *Kit) buildLLMAgent(ac config.AgentConfig, cfg config.Config, workspace, reasoningLevel string) (agent.Agent, error) {
	return k.buildLLMAgentWithSubAgents(ac, cfg, nil, workspace, reasoningLevel)
}

// BuildAgentWithModel 构建一个固定使用其供应商特定模型的
// agent（按会话的模型覆盖；modelID 为空时回退到 agent 配置的模型）。
func (k *Kit) BuildAgentWithModel(ac config.AgentConfig, modelID string) (agent.Agent, error) {
	return k.BuildAgentWithModelAndWorkspace(ac, modelID, "")
}

// BuildAgentWithModelAndWorkspace 是会话感知的构建入口：模型覆盖和有效任务工作区会一起解析，因此文件工具与提示
// 词变量不会对目录产生不一致。
func (k *Kit) BuildAgentWithModelAndWorkspace(ac config.AgentConfig, modelID, workspace string) (agent.Agent, error) {
	return k.BuildAgentWithModelWorkspaceAndReasoning(ac, modelID, workspace, "")
}

// BuildAgentWithModelWorkspaceAndReasoning 是会话感知的构建入口：模型覆盖、有效任务工作区以及聊天的思考级别会一起解析，
// 因此工具、提示词变量和请求体不会对本次运行产生不一致。
func (k *Kit) BuildAgentWithModelWorkspaceAndReasoning(ac config.AgentConfig, modelID, workspace, reasoningLevel string) (agent.Agent, error) {
	cfg := k.store.Get()
	// 为空的供应商/模型会在任何覆盖被钳制之前继承全局默认值，因此按会话的选择器也
	// 能对继承的模型生效。
	ac.ProviderID, ac.Model = cfg.ResolveAgentModel(ac)
	if strings.TrimSpace(modelID) != "" {
		ac.Model = EffectiveModel(cfg, ac.ProviderID, ac.Model, modelID)
	}
	if strings.TrimSpace(workspace) == "" {
		workspace = cfg.Settings.WorkspaceDir
	}
	return k.buildLLMAgent(ac, cfg, workspace, reasoningLevel)
}

// EffectiveModel 解析会话实际运行的模型：当供应商提供按会话覆盖时使用该覆盖，否则使用 agent
// 配置的模型。它是 agent 构建、统计定价和自动标题旁路调用共享的单一钳制点。
func EffectiveModel(cfg config.Config, providerID, configuredModel, override string) string {
	if override != "" && modelSupported(cfg, providerID, override) {
		return override
	}
	return configuredModel
}

// ModelSupported 报告供应商是否提供给定的模型
// id。它导出供仅需要布尔检查的调用方使用。
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

// applyModelOutputLimit 从模型配置的 MaxOutputTokens 填充未设置的 Agent
// 输出上限；显式设置的 Agent 值始终优先。
func applyModelOutputLimit(ac *config.AgentConfig, p config.ModelProvider) {
	if ac.MaxTokens > 0 {
		return
	}
	if mi, ok := p.FindModel(ac.Model); ok && mi.MaxOutputTokens > 0 {
		ac.MaxTokens = mi.MaxOutputTokens
	}
}

func (k *Kit) buildLLMAgentWithSubAgents(ac config.AgentConfig, cfg config.Config, subAgents []agent.Agent, workspace, reasoningLevel string) (agent.Agent, error) {
	// 定义时未显式指定模型的 Agent 遵循全局默认值（形式：provider/model
	// 留空）。值为空的历史配置也按相同方式解释，而不是运行失败（#39）。
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
	// 当 Agent 未显式设置输出上限时，模型的输出上限作为回退；Agent 级别的
	// MaxTokens 仍然优先。
	applyModelOutputLimit(&ac, *provider)

	var tools []tool.Tool
	var toolsets []tool.Toolset

	// 内置工具。没有显式收窄列表的 Agent
	// 会获得完整的内置工具集：完整能力是默认值，移除某个类别是显式操作（#38）。
	builtin, err := k.builtinToolsFor(ac, cfg, workspace)
	if err != nil {
		return nil, err
	}
	tools = append(tools, builtin...)

	// 技能（通过 ADK 技能工具集进行渐进式披露）。
	if len(ac.SkillIDs) > 0 {
		ts, err := k.skillToolset(ac, cfg, workspace)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", ac.Name, err)
		}
		if ts != nil {
			toolsets = append(toolsets, ts)
		}
	}

	// MCP 服务器。每个服务器的「启用」开关是权威来源：启用的服务器会注入每个会话
	// （包括直接对话），无需按 Agent 挂载；未启用的服务器保持关闭，除非某个 Agent
	// 显式挂载它。连传输层都无法构建的服务器（缺少命令/URL、OAuth 配置错误）会被
	// 报告，而不是静默运行一个缺少工具的 Agent——这与上面的技能工具集契约一致。
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

// llmConfig 为 Agent 配置组装 llmagent.Config，使用
// InstructionProvider，确保用户提示词中的花括号绝不会被视为状态占位符。
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

// appendAgentGuides 将工程指南块（先是全局
// ~/.agents/AGENTS.md，然后是有效工作区根目录的
// AGENTS.md）拼接到基础指令上。指南补充角色提示词，且绝不阻塞构建；每回合的技能
// 上下文仍会在它们之后追加。
func (k *Kit) appendAgentGuides(instruction, workspace string) string {
	guides := config.EffectiveAgentGuides(workspace, k.globalAgentsFile)
	if len(guides) == 0 {
		return instruction
	}
	return instruction + "\n\n" + strings.Join(guides, "\n\n")
}

// BuildTeam 构建团队的根 Agent：主导 Agent 将成员作为子 Agent（ADK
// 处理它们之间的委派/转移），或在请求时使用自动生成的协调器。
func (k *Kit) BuildTeam(tc config.TeamConfig) (agent.Agent, string, error) {
	return k.BuildTeamWithWorkspace(tc, "")
}

// BuildTeamWithWorkspace 构建一个团队，其本地成员使用会话的任务工作区。
func (k *Kit) BuildTeamWithWorkspace(tc config.TeamConfig, workspace string) (agent.Agent, string, error) {
	return k.BuildTeamWithWorkspaceAndReasoning(tc, workspace, "")
}

// BuildTeamWithWorkspaceAndReasoning 将聊天的思考级别应用到团队的根
// Agent（显式负责人或合成的协调器）。成员 Agent 保留各自配置的模型和级别。
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
	// 聊天在其协调器和每个本地成员之间共享同一执行策略。
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
	// memberByKey 将 Agent/远程 ID 映射到其构建后的
	// Agent，因此显式负责人路径无需按位置索引即可组装其同级 Agent（悬空的成员 ID
	// 过去会导致切片偏移，并造成错误分配或 panic）。
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

	// 远程 A2A 成员作为协调器可转接到的子 Agent 加入。
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

	// 显式负责人：将该 Agent 用作根 Agent；其他成员（本地和远程）成为可转接的同级
	// Agent。
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

	// 自动协调器：合成的团队负责人 Agent。
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

// searchOptions 根据当前设置组装知识工具选项：工作区加上已配置的嵌入后端（如果有）。
func (k *Kit) searchOptions(cfg config.Config) SearchOptions {
	opts := SearchOptions{Workspace: cfg.Settings.WorkspaceDir, Index: k.knowledge}
	if cfg.Settings.EmbeddingProviderID == "" || cfg.Settings.EmbeddingModel == "" {
		return opts // 仅关键字
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == cfg.Settings.EmbeddingProviderID {
			if cfg.Providers[i].Protocol == config.ProtocolAnthropic || cfg.Providers[i].Protocol == config.ProtocolGemini {
				return opts // embeddings 端点仅与 OpenAI 兼容
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

// skillToolset 将选中的技能物化为每个 Agent 暂存目录下的
// SKILL.md，从而构建一个覆盖这些技能的 ADK
// 技能工具集。写入技能失败会被上报，而不是静默运行一个缺少用户所挂载能力的
// Agent。
//
// 挂载的 ID 按生效的技能集解析：在 <workspace>/.agents/skills 和
// ~/.agents/skills 下发现的项目级/全局技能优先于同名的应用技能（issue
// #52），因此被遮蔽的应用技能仍会物化胜出的定义，而不是让构建失败。目录已消失（
// 切换工作区）的已发现技能会被静默跳过：外部根目录本质上是临时的。缺失的应用技
// 能仍保持原有的硬错误。
//
// 暂存目录按 Agent 划分作用域（skills/agents/<key>）：ADK
// 文件系统源会暴露其所指向的目录树中的每一个技能，因此共享目录会在 Agent
// 之间泄漏技能，并使未挂载或已删除的技能仍可加载。不在所需集合中的目录会在加载
// 前被清理，因此解除挂载或删除技能会在下一次构建时生效。
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
	// 与 MCP
	// 相同的不得静默降级约定：已挂载但不再存在的应用技能（导入的共享文件是常见来源
	// ）会被上报，而不是被悄悄丢弃 —— 否则 Agent 会声称拥有它从未具备的能力。
	var missing []string
	for _, id := range ids {
		s, ok := byID[id]
		if !ok {
			if _, discovered := config.DiscoveredSkillSource(id); discovered {
				// 根目录可能随工作区一起移动；如果仍能看到同名技能，则回退到该技能。
				s, ok = byName[config.DiscoveredSkillName(id)]
			} else if old, exists := cfgByID[id]; exists {
				// 应用技能被同名的已发现技能遮蔽：挂载胜出的定义（project > global >
				// app）。
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
		// 原子替换：并发的预加载必须绝不把半写入（O_TRUNC）的 SKILL.md
		// 读取为有效但为空的指令。
		tmp := filepath.Join(dirSkill, "SKILL.md.tmp")
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return nil, fmt.Errorf("write skill %q: %w", s.Name, err)
		}
		if err := os.Rename(tmp, filepath.Join(dirSkill, "SKILL.md")); err != nil {
			_ = os.Remove(tmp)
			return nil, fmt.Errorf("write skill %q: %w", s.Name, err)
		}
	}
	// 清理不再挂载到该 Agent 的技能（包括悬空
	// ID：它们不会物化任何内容，因此任何残留目录都是过期的）。
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
	_ = cleanup // sources 在进程的整个生命周期内保持存活
	ts, err := skilltoolset.New(context.Background(), skilltoolset.Config{Source: preloaded})
	if err != nil {
		return nil, fmt.Errorf("create skill toolset: %w", err)
	}
	return ts, nil
}

// agentSkillKey 是每个 Agent 的技能暂存目录名。优先使用稳定的 Agent
// ID；经过清理的名称用于覆盖没有 ID 的配置（测试、临时构建）。
func agentSkillKey(ac config.AgentConfig) string {
	if ac.ID != "" {
		return ac.ID
	}
	return sanitizeAgentName(ac.Name)
}

// MaterializedSkillName 返回技能在磁盘上的目录名。导出该函数是为了让配置校验能够拒绝会与现有技能的物
// 化目录发生冲突的名称。
func MaterializedSkillName(name string) string { return sanitizeSkillName(name) }

// SkillContextStateKey 将回合上下文——通过斜杠调用的技能的 SKILL.md 正文，以及 @
// 引用的工作区文件的内联内容——带入一次运行。"temp:" 前缀是 ADK
// 的临时状态约定：该值会到达正在运行的调用（因此指令提供方可以将其追加），但在
// 被写回会话之前会被清除，因此下一回合绝不会继承上一回合的上下文。
const SkillContextStateKey = session.KeyPrefixTemp + "agentmate.skill_context"

// skillAwareInstruction 包装基础系统指令，使以 "/<skill-name>" 命令（或 @
// 引用的文件）启动的回合在该次运行期间追加该回合上下文。这是将技能挂载到 Agent
// 上的逐回合对应机制：模型会看到完整指令，而技能正文不会作为用户聊天历史的一部
// 分被存储。
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

// RenderSkillMD 完全按照技能在磁盘上的物化形式（frontmatter +
// body）渲染已配置的技能，因此通过斜杠调用的技能和挂载到 Agent
// 上的技能会收到完全相同的指令。
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

// RemoveMaterializedSkill 删除某个技能已物化的 SKILL.md 目录：每个 Agent
// 的暂存目录和遗留的共享布局。这是尽力而为——下一次 Agent
// 构建无论如何都会清理过时条目。该操作在 skillMu
// 下运行，因此并发构建的写入/清理/预加载序列绝不会被打断。
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

// applyPromptVars 解析系统提示词中的模板变量：{{date}}、{{time}}、{{datetime}}
// 和 {{workspace}}（在每次 Agent
// 构建时解析，即每回合解析，因此长时间运行的应用能保持最新）。
func applyPromptVars(prompt, workspace string) string {
	now := time.Now()
	return strings.NewReplacer(
		"{{datetime}}", now.Format("2006-01-02 15:04"),
		"{{date}}", now.Format("2006-01-02"),
		"{{time}}", now.Format("15:04"),
		"{{workspace}}", workspace,
	).Replace(prompt)
}

// SanitizeAgentName 生成安全的 ADK Agent
// 名称（类标识符）。将其导出，是为了让调用方（测试、工具链）能够预测团队的协调
// 器将会转交到的子 Agent 名称。
func SanitizeAgentName(name string) string { return sanitizeAgentName(name) }

// sanitizeAgentName 生成安全的 ADK Agent 名称（类标识符）。
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
		// 针对非 ASCII 名称的确定性回退方案。
		h := fnv.New32a()
		_, _ = h.Write([]byte(name))
		out = fmt.Sprintf("agent_%x", h.Sum32())
	}
	if out == "user" {
		out = "user_agent"
	}
	return out
}

// sanitizeSkillName 生成符合 agentskills.io
// 规范的技能名称：仅使用小写字母数字和连字符，首尾不得有连字符。共享实现位于
// config 中，以便发现与物化对哪些名称会冲突保持一致。
func sanitizeSkillName(name string) string { return config.NormalizeSkillName(name) }

// GuardrailsEnabled 报告某个 Agent 的护栏是否开启（默认开启）。
func GuardrailsEnabled(ac config.AgentConfig) bool {
	return ac.EnableGuardrails == nil || *ac.EnableGuardrails
}

// confirmsTool 报告某个内置工具类别是否已被标记为需要该 Agent
// 进行人工确认（HITL）。
func confirmsTool(ac config.AgentConfig, category string) bool {
	for _, c := range ac.ConfirmTools {
		if c == category {
			return true
		}
	}
	return false
}

// mcpToolsetForApproval 保持管理器的服务器 ID 缓存不带策略，并为本次 Agent 构建
// 的短生命周期包装器应用会话审批策略。MCP 工具没有可靠的风险检测器，因此除
// "never" 外的所有审批策略都会保留确认门禁。
func mcpToolsetForApproval(ts tool.Toolset, approvalPolicy string) tool.Toolset {
	requireConfirmation := config.NormalizeApprovalPolicy(approvalPolicy) != config.ApprovalPolicyNever
	return tool.WithConfirmation(ts, requireConfirmation, nil)
}

// builtinToolsFor 组装 Agent
// 运行所用的内置工具。收窄列表为空表示完整的内置工具集（config.AllBuiltinTools
// ）：Agent 默认功能齐全，除非用户显式移除了某些类别。
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

// SessionPolicy 构建内置工具与聊天服务的只读确认预览共享的执行策略。它是唯一将独立于审批的沙
// 箱配置 + 网络设置映射到 sandbox.Policy
// 的位置，因此提示词绝不会与将要执行该调用的边界不一致。
func SessionPolicy(workspace, profile, network string, protectedDirs ...string) sandbox.Policy {
	policy := sandbox.DefaultPolicy(workspace)
	switch config.NormalizeSandboxProfile(profile) {
	case config.SandboxProfileDangerFullAccess:
		policy.Mode = sandbox.ModeDangerFullAccess
		// 直接后端没有 AppContainer 能力门控；应报告有效网络状态，而不是留下误导性的
		// "off"。
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
		// 测试与特殊部署可能将配置存储放在所选工作区之上；保护祖先目录会阻止每一次写
		// 入。
		if !sandbox.Within(dir, policy.Workspace) {
			policy = policy.WithProtected(dir)
		}
	}
	return policy
}

// PreviewGuard 是文件与 shell
// 工具所使用的同一工作区策略的只读视图。确认提供方与聊天服务的提示构建器共享它
// ：预检绝不会创建、更改或删除文件。
type PreviewGuard struct {
	workspace string
	policy    sandbox.Policy
}

// NewPreviewGuard 为工作区和沙箱配置构建护栏。在每个回合调用都是安全的；不会写入任何状态。
func NewPreviewGuard(workspace, profile, network string, protectedDirs ...string) PreviewGuard {
	return PreviewGuard{
		workspace: workspace,
		policy:    SessionPolicy(workspace, profile, network, protectedDirs...),
	}
}

// NewPreviewGuardWithPolicy 复用已为工具构建的策略，避免进行第二次可能不一致的解析。
func NewPreviewGuardWithPolicy(workspace string, policy sandbox.Policy) PreviewGuard {
	return PreviewGuard{workspace: workspace, policy: policy}
}

// CheckWritePath 报告工作区相对路径是否可以写入。它会解析路径穿越和符号链接，但绝不接触目标文
// 件。
func (g PreviewGuard) CheckWritePath(rel string) (bool, string, string) {
	root, err := safeWorkspacePath(g.workspace, rel)
	if err != nil {
		return false, "outside_workspace", err.Error()
	}
	return g.policy.CheckWrite(root)
}

// CheckCommand 在不启动进程的情况下运行 shell 预检。
func (g PreviewGuard) CheckCommand(command string) (bool, string, string) {
	return g.policy.CheckCommand(command)
}

// ---------------------------------------------------------------------------
// 内置工具

type timeArgs struct {
	Timezone string `json:"timezone,omitempty" jsonschema:"IANA timezone such as Asia/Shanghai; empty for local time"`
}
type timeResult struct {
	Now      string `json:"now"`
	Timezone string `json:"timezone"`
}

// workspaceFileTools 为 "files" 类别构建 list/read/write/edit 工具集。
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

// TimeTool 报告当前时间。
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

// listArgs 保持 Path 为可选：列出工作区根目录是常见的首次调用，且不应使
// JSON-schema 校验失败。
type listArgs struct {
	Path string `json:"path,omitempty" jsonschema:"relative path inside the workspace; omit for the workspace root"`
}

// SafeWorkspacePath 在 workspace 内解析 rel，并阻止路径穿越。
func SafeWorkspacePath(workspace, rel string) (string, error) {
	return safeWorkspacePath(workspace, rel)
}

const (
	maxReadBytes  = 64 * 1024
	maxWriteBytes = 6 << 20 // 单次写入上限，与图片附件同量级
)

// ReadBoundedFile 从 path 最多读取 limit
// 字节，一旦达到上限就停止（超大文件不得仅为截断而加载到内存中）。返回的 bool
// 报告是否发生截断；截断后的内容会回退到最近的 UTF-8 rune
// 边界，并带有尾部标记。
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
		// 回退到最后一个完整 rune：截断可能落在多字节序列内部（裸前导字节也是
		// RuneStart）。
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

// WriteBoundedFile 会预先拒绝超过 limit
// 字节的载荷，因此失控的生成无法占满磁盘；被拒绝时不会创建文件。
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
	// 上述词法检查无法识别符号链接：工作区内指向 ~/.ssh
	// 的链接会通过该检查。解析两侧路径并再次比较。尚不存在的路径（写入目标，可能带
	// 有新的父目录）会通过其最深的现存祖先进行校验。
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
			// 对于尚不存在的目标，使用解析后的根目录拼写，使 Windows
			// 8.3（短）路径和长路径作为同一个工作区进行比较。
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

// isWithin 报告 target 是 root 本身还是位于其下。
func isWithin(root, target string) bool {
	return target == root || strings.HasPrefix(target, root+string(filepath.Separator))
}

// ListFilesTool 列出工作区文件。Path 为可选参数，因此模型无需猜测 "."
// 就能请求工作区根目录（issue #21：首次 "look at the project"
// 调用通常不带参数）。
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

// ReadFileTool 读取工作区内的文件。
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

// WriteFileTool 写入工作区内的文件。当 requireConfirmation 为 true
// 时，该工具会先请求人工操作员审批每次调用（ADK HITL）：运行会在
// adk_request_confirmation 中断处暂停，并且仅在收到带有 {"confirmed": true} 的
// FunctionResponse 后才继续。
func WriteFileTool(workspace string, requireConfirmation bool) (tool.Tool, error) {
	return WriteFileToolWithPolicy(workspace, requireConfirmation, sandbox.DefaultPolicy(workspace))
}

// WriteFileToolWithPolicy 将会话的受保护路径策略加入进程内写入路径。操作系统沙箱覆盖子进程；此检查是文
// 件工具的等效边界，因为这些工具不会生成进程。
func WriteFileToolWithPolicy(workspace string, requireConfirmation bool, policy sandbox.Policy) (tool.Tool, error) {
	guard := NewPreviewGuardWithPolicy(workspace, policy)
	return functiontool.New(functiontool.Config{
		Name:                "write_workspace_file",
		Description:         "Create or overwrite a text file inside the user's workspace (single write up to 6MB).",
		RequireConfirmation: requireConfirmation,
		RequireConfirmationProvider: func(args writeArgs) bool {
			// 处理程序会拒绝的路径不得以死胡同式审批打断用户：跳过
			// HITL，让处理程序返回错误。
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

// EditFileTool 通过将 oldText 替换为 newText 来编辑现有工作区文件。它刻意与
// write_workspace_file 分开：定向替换会保持无关字节（尤其是 CRLF
// 行尾）不变，并为模型提供 diff，而不是强制完整重写。
func EditFileTool(workspace string, requireConfirmation bool) (tool.Tool, error) {
	return EditFileToolWithPolicy(workspace, requireConfirmation, sandbox.DefaultPolicy(workspace))
}

// EditFileToolWithPolicy 将会话的受保护路径策略添加到进程内编辑路径，其边界与
// write_workspace_file 使用的相同。
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

// writeFilePreservingMode 通过同目录的临时文件写入，并将其重命名覆盖目标文件。由于目录相同，重命名在同
// 一卷上是原子操作，且被中断的写入绝不会留下写了一半的文件。
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
	kind byte // ' ' 表示上下文，'-' 表示删除，'+' 表示新增
	text string
}

// unifiedWorkspaceDiff 使用小规模的行级 LCS 渲染有界的统一
// diff。病态的大文件对会回退到粗粒度的删除/新增块：仍是有效的
// diff，但不会消耗平方级内存。
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
		// 除非这是最后一行，否则为截断标记预留空间，这样恰好放得下的 diff
		// 绝不会被标记为已截断。
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
// 会话计划（待办列表）工具

// todosStateKey 将计划存储在 ADK
// 会话状态中，使其在重启后仍然存在，并保持在对话范围内。
const todosStateKey = "agentmate.todos"

type todoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

type todosArgs struct {
	// Todos 是完整替换列表。省略 => 读取当前计划但不写入；为空 => 清除它。
	Todos []todoItem `json:"todos,omitempty"`
}

type todosResult struct {
	OK    bool       `json:"ok"`
	Todos []todoItem `json:"todos"`
}

// TodosTool 为模型提供一个会话范围内的计划（待办列表），用于多步骤工作：随着步骤创建、开
// 始和完成，它会重写该列表，用户可在工具结果卡片上跟进。它对应本应用所效仿的编
// 码 Agent 桌面端的计划工具。
func TodosTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "plan_todos",
		Description: "Maintain the session's task plan (todo list). Pass the FULL list of steps with status pending/in_progress/completed each time — mark a step in_progress before starting it and completed right after finishing it, creating new steps as work emerges. Omit 'todos' to read the current plan. Use it for any multi-step task so the user can follow progress.",
	}, func(ctx agent.Context, args todosArgs) (todosResult, error) {
		if ctx.State() == nil {
			return todosResult{}, fmt.Errorf("会话状态不可用")
		}
		if args.Todos == nil {
			// 只读：返回已存储的计划。
			cur, err := ctx.State().Get(todosStateKey)
			if err != nil {
				return todosResult{OK: true}, nil // 尚无计划。
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
