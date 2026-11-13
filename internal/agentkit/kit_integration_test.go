package agentkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"

	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// newTestKit 基于临时配置存储构建一个 Kit。
func newTestKit(t *testing.T) (*Kit, *config.Store) {
	t.Helper()
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit := NewKit(store, mcpmgr.New())
	// 保持发现流程封闭：绝不读取开发者真实的 ~/.agents/skills。
	kit.globalSkillRoot = t.TempDir()
	kit.globalAgentsFile = "" // 绝不读取开发者真实的 ~/.agents/AGENTS.md
	return kit, store
}

func TestBuildAgentWithBuiltinTools(t *testing.T) {
	kit, store := newTestKit(t)
	err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "a1", Name: "助手 A", ProviderID: "p1", Model: "m1",
			SystemPrompt: "你好 {不是状态占位符} 可以包含大括号",
			BuiltinTools: []string{"time", "shell"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	a, err := kit.BuildAgent(cfg.Agents[0])
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() == "" {
		t.Fatal("agent name empty")
	}
	if a.Description() != "助手 A" {
		t.Fatalf("unexpected description: %q", a.Description())
	}
}

func TestBuiltinToolsDefaultToFullSet(t *testing.T) {
	kit, store := newTestKit(t)
	cfg := store.Get()
	workspace := t.TempDir()

	names := func(tools []tool.Tool) map[string]bool {
		out := make(map[string]bool, len(tools))
		for _, tl := range tools {
			out[tl.Name()] = true
		}
		return out
	}

	// 一个绝不选择任何工具的自定义 Agent
	// 会继承每一个内置类别：文件访问、命令执行、知识、待办事项和时间。（长期记忆工
	// 具仅在接入记忆服务时才存在，因此此处没有。）
	all, err := kit.builtinToolsFor(config.AgentConfig{Name: "默认全量"}, cfg, workspace)
	if err != nil {
		t.Fatal(err)
	}
	got := names(all)
	for _, want := range []string{
		"get_current_time",
		"list_workspace_files",
		"read_workspace_file",
		"write_workspace_file",
		"edit_workspace_file",
		"search_knowledge",
		"plan_todos",
		"run_command",
	} {
		if !got[want] {
			t.Fatalf("empty builtinTools should mount %q, got %v", want, got)
		}
	}

	// 非空列表是显式收窄：仅包含所选的类别。
	narrowed, err := kit.builtinToolsFor(config.AgentConfig{
		Name: "仅时间", BuiltinTools: []string{"time"},
	}, cfg, workspace)
	if err != nil {
		t.Fatal(err)
	}
	got = names(narrowed)
	if len(got) != 1 || !got["get_current_time"] {
		t.Fatalf("explicit time-only agent should mount just get_current_time, got %v", got)
	}
}

func TestBuildTeamAutoCoordinator(t *testing.T) {
	kit, store := newTestKit(t)
	err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		for _, id := range []string{"m1", "m2"} {
			cfg.Agents = append(cfg.Agents, config.AgentConfig{
				ID: id, Name: "成员" + id, ProviderID: "p1", Model: "m1",
			})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	team := config.TeamConfig{
		ID: "t1", Name: "Dream Team", AutoCoordinate: true,
		MemberAgentIDs: []string{"m1", "m2"},
		ProviderID:     "p1", Model: "m1",
	}
	root, name, err := kit.BuildTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	if name != "dream_team" {
		t.Fatalf("team name = %q", name)
	}
	subs := root.SubAgents()
	if len(subs) != 2 {
		t.Fatalf("expected 2 sub-agents, got %d", len(subs))
	}

	// 显式 lead 模式：lead 为根，另一成员为对等成员。
	team2 := team
	team2.AutoCoordinate = false
	team2.LeadAgentID = "m1"
	root2, _, err := kit.BuildTeam(team2)
	if err != nil {
		t.Fatal(err)
	}
	if root2.Name() != sanitizeAgentName("成员m1") {
		t.Fatalf("lead should be root, got %q", root2.Name())
	}
	if len(root2.SubAgents()) != 1 {
		t.Fatalf("lead should have 1 peer, got %d", len(root2.SubAgents()))
	}
}

func TestBuildTeamMissingProvider(t *testing.T) {
	kit, store := newTestKit(t)
	team := config.TeamConfig{ID: "t", Name: "T", AutoCoordinate: true, ProviderID: "nope"}
	if _, _, err := kit.BuildTeam(team); err == nil {
		// store 有默认 Agent 列表，供应商缺失 -> 必须报错
		_ = store
		t.Fatal("expected error for missing provider")
	}
}

func TestSkillMaterialization(t *testing.T) {
	kit, store := newTestKit(t)
	err := store.Update(func(cfg *config.Config) {
		// 替换预置的默认值："Weekly Report" 规范化后与预置的 weekly-report
		// 技能的物化名称相同，且保存路径绝不允许同一配置中存在两个同名技能。
		cfg.Skills = []config.SkillConfig{
			{ID: "s1", Name: "Weekly Report", Description: "写周报", Content: "# 步骤\n1. 收集"},
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ac := config.AgentConfig{ID: "agent_x", Name: "Tester", SkillIDs: []string{"s1"}}
	ts, err := kit.skillToolset(ac, store.Get(), t.TempDir())
	if err != nil {
		t.Fatalf("skillToolset: %v", err)
	}
	if ts == nil {
		t.Fatal("expected skill toolset, got nil")
	}
	// 技能必须物化为有效的 SKILL.md，位于每个 Agent
	// 的暂存目录（agents/<id>/<skill>/SKILL.md）内，而不是共享根目录。
	path := filepath.Join(store.SkillsDir(), "agents", ac.ID, "weekly-report", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fm, _, err := skill.ParseBytes(data)
	if err != nil {
		t.Fatalf("materialized SKILL.md invalid: %v", err)
	}
	if fm.Name != "weekly-report" || fm.Description != "写周报" {
		t.Fatalf("unexpected frontmatter: %+v", fm)
	}
	if _, err := os.Stat(filepath.Join(store.SkillsDir(), "weekly-report")); !os.IsNotExist(err) {
		t.Fatalf("skill leaked into the shared skills root: %v", err)
	}
}

// TestSkillToolsetPrunesUnmountedSkills 固定每个 Agent 的隔离性：为一个 Agent
// 物化的技能不得出现在另一个 Agent
// 的工具集中，并且解除挂载技能会在下一次构建时移除其文件。
func TestSkillToolsetPrunesUnmountedSkills(t *testing.T) {
	kit, store := newTestKit(t)
	err := store.Update(func(cfg *config.Config) {
		cfg.Skills = append(cfg.Skills,
			config.SkillConfig{ID: "s1", Name: "Shared Skill", Description: "技能一", Content: "one"},
			config.SkillConfig{ID: "s2", Name: "Private Skill", Description: "技能二", Content: "two"},
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	mountAll := config.AgentConfig{ID: "agent_a", Name: "A", SkillIDs: []string{"s1", "s2"}}
	mountOne := config.AgentConfig{ID: "agent_b", Name: "B", SkillIDs: []string{"s1"}}
	if _, err := kit.skillToolset(mountAll, store.Get(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.skillToolset(mountOne, store.Get(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Agent B 的暂存目录必须仅包含已挂载的技能。
	entries, err := os.ReadDir(filepath.Join(store.SkillsDir(), "agents", "agent_b"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "shared-skill" {
		t.Fatalf("agent B staging dir = %v, want [shared-skill]", names)
	}
	// 从 Agent A 解除挂载 s2 会在下一次构建时清理它。
	mountAll.SkillIDs = []string{"s1"}
	if _, err := kit.skillToolset(mountAll, store.Get(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(filepath.Join(store.SkillsDir(), "agents", "agent_a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "shared-skill" {
		names = nil
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("agent A staging dir after detach = %v, want [shared-skill]", names)
	}
	// RemoveMaterializedSkill 会清除每个 Agent 中已删除技能的副本。
	kit.RemoveMaterializedSkill("Shared Skill")
	for _, id := range []string{"agent_a", "agent_b"} {
		if _, err := os.Stat(filepath.Join(store.SkillsDir(), "agents", id, "shared-skill")); !os.IsNotExist(err) {
			t.Fatalf("agent %s still has shared-skill after removal: %v", id, err)
		}
	}
}

func TestAgentNameUniquenessAcrossBuilds(t *testing.T) {
	a := sanitizeAgentName("周报助手")
	b := sanitizeAgentName("周报助手")
	if a != b {
		t.Fatalf("non-ascii names must map deterministically: %q vs %q", a, b)
	}
}

// TestBuildAgentReportsMCPFailures 固定不静默跳过的契约：传输层无法构建的服务器和悬空的服务器 ID
// 都会在构建错误中暴露，而不是静默运行一个缺少其工具的 Agent。
func TestBuildAgentReportsMCPFailures(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		cfg.MCPServers = append(cfg.MCPServers, config.MCPServerConfig{
			ID: "mcp_bad", Name: "坏服务器", Transport: config.MCPStdio, Command: "",
		})
	}); err != nil {
		t.Fatal(err)
	}
	ac := config.AgentConfig{ID: "agent_m", Name: "工具人", ProviderID: "p1", Model: "m1",
		MCPServerIDs: []string{"mcp_bad", "mcp_missing"}}
	_, err := kit.BuildAgent(ac)
	if err == nil {
		t.Fatal("expected aggregated MCP error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "坏服务器") {
		t.Fatalf("error missing broken server: %v", msg)
	}
	if !strings.Contains(msg, "mcp_missing") {
		t.Fatalf("error missing dangling server id: %v", msg)
	}
}

// TestBuildAgentReportsDanglingSkills：已挂载但不再存在的技能（导入的共享文件是
// 常见来源）必须被报告，而不是被静默丢弃——与 MCP 服务器相同的不静默降级契约。
func TestBuildAgentReportsDanglingSkills(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
	}); err != nil {
		t.Fatal(err)
	}
	ac := config.AgentConfig{ID: "agent_s", Name: "技能人", ProviderID: "p1", Model: "m1",
		SkillIDs: []string{"skill_gone"}}
	if _, err := kit.BuildAgent(ac); err == nil {
		t.Fatal("expected dangling skill error")
	} else if !strings.Contains(err.Error(), "skill_gone") {
		t.Fatalf("error missing skill id: %v", err)
	}
}

// TestBuildTeamLeadPeersIncludeRemoteMembers 固定显式负责人路径：负责人的可转移对等成员必须同时包含本地成员和远程成员（远
// 程成员过去会被静默丢弃），并跳过悬空的成员 ID，而不是移动位置索引。
func TestBuildTeamLeadPeersIncludeRemoteMembers(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		cfg.Agents = append(cfg.Agents,
			config.AgentConfig{ID: "a_lead", Name: "组长", ProviderID: "p1", Model: "m1"},
			config.AgentConfig{ID: "a_peer", Name: "本地队员", ProviderID: "p1", Model: "m1"},
		)
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{
			ID: "ra_1", Name: "远程队员", CardURL: "http://127.0.0.1:1/card", // 惰性：构建时绝不获取
		})
	}); err != nil {
		t.Fatal(err)
	}
	team := config.TeamConfig{
		ID: "team_x", Name: "混合队", LeadAgentID: "a_lead",
		MemberAgentIDs:  []string{"a_lead", "a_peer", "a_gone"},
		RemoteMemberIDs: []string{"ra_1"},
	}
	root, rootName, err := kit.BuildTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	if rootName != SanitizeAgentName("组长") {
		t.Fatalf("root = %s", rootName)
	}
	peers := root.SubAgents()
	if len(peers) != 2 {
		t.Fatalf("lead peers = %d, want 2 (local + remote, dangling skipped)", len(peers))
	}
	names := map[string]bool{}
	for _, p := range peers {
		names[p.Name()] = true
	}
	if !names[SanitizeAgentName("本地队员")] || !names[SanitizeAgentName("远程队员")] {
		t.Fatalf("peers missing expected members: %v", names)
	}
}

// TestBuildAgentInheritsDefaultModel：仅保存三个语义字段（name/description/syst
// em prompt）的 Agent
// 必须在全局默认供应商和模型上构建，且按会话的选择器必须仍然可用（#39）。
func TestBuildAgentInheritsDefaultModel(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI,
			Models: []config.ModelInfo{{ID: "m1"}, {ID: "m2"}}, IsDefault: true,
		})
	}); err != nil {
		t.Fatal(err)
	}

	ac := config.AgentConfig{Name: "语义 Agent", Description: "一句话说明", SystemPrompt: "用中文回答"}
	a, err := kit.BuildAgent(ac)
	if err != nil {
		t.Fatalf("agent without provider/model should inherit defaults: %v", err)
	}
	if a.Name() == "" {
		t.Fatal("agent name empty")
	}

	// 头部模型选择器以解析后的默认供应商为基准进行钳制，而不是以配置为空的供应商为
	// 基准。
	if _, err := kit.BuildAgentWithModel(ac, "m2"); err != nil {
		t.Fatalf("per-session override should resolve against the default provider: %v", err)
	}

	// 未配置供应商：错误必须告诉用户该做什么。
	emptyKit, _ := newTestKit(t)
	_, err = emptyKit.BuildAgent(ac)
	if err == nil || !strings.Contains(err.Error(), "尚未配置模型供应商") {
		t.Fatalf("expected actionable missing-provider error, got %v", err)
	}
}
