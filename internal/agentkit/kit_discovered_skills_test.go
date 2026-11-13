package agentkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool/skilltoolset/skill"

	"agentmate/internal/config"
)

func writeAgentSkillMD(t *testing.T, root, name, desc, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readMaterializedSkill(t *testing.T, store interface{ SkillsDir() string }, agentID, name string) (*skill.Frontmatter, string) {
	t.Helper()
	path := filepath.Join(store.SkillsDir(), "agents", agentID, name, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read materialized %s: %v", path, err)
	}
	fm, body, err := skill.ParseBytes(data)
	if err != nil {
		t.Fatalf("materialized SKILL.md invalid: %v", err)
	}
	return fm, string(body)
}

// TestDiscoveredSkillMaterialization 锁定 issue #52 验收项 5：在
// <workspace>/.agents/skills 下发现的项目技能，可通过其被发现时的 ID
// 挂载，并落到每个 Agent 的暂存目录中。
func TestDiscoveredSkillMaterialization(t *testing.T) {
	kit, store := newTestKit(t)
	workspace := t.TempDir()
	writeAgentSkillMD(t, filepath.Join(workspace, ".agents", "skills"), "project-skill", "项目技能", "# 项目正文")

	ac := config.AgentConfig{
		ID:       "agent_disc",
		Name:     "Disc",
		SkillIDs: []string{config.DiscoveredSkillID(config.SkillSourceProject, "project-skill")},
	}
	ts, err := kit.skillToolset(ac, store.Get(), workspace)
	if err != nil {
		t.Fatalf("skillToolset: %v", err)
	}
	if ts == nil {
		t.Fatal("expected skill toolset")
	}
	fm, body := readMaterializedSkill(t, store, ac.ID, "project-skill")
	if fm.Name != "project-skill" || fm.Description != "项目技能" {
		t.Fatalf("frontmatter = %+v", fm)
	}
	if !strings.Contains(body, "项目正文") {
		t.Fatalf("body = %q", body)
	}
}

// TestDiscoveredSkillShadowsAppSkill 校验 project > global > app 的优先级不会
// 破坏挂载了应用技能 ID 的 Agent——它会物化胜出的定义，而不是让构建失败。
func TestDiscoveredSkillShadowsAppSkill(t *testing.T) {
	kit, store := newTestKit(t)
	workspace := t.TempDir()
	writeAgentSkillMD(t, filepath.Join(workspace, ".agents", "skills"), "weekly-report", "项目周报", "# 项目版本")
	if err := store.Update(func(cfg *config.Config) {
		cfg.Skills = append(cfg.Skills, config.SkillConfig{
			ID: "app-weekly", Name: "Weekly Report", Description: "应用周报", Content: "# 应用版本",
		})
	}); err != nil {
		t.Fatal(err)
	}

	ac := config.AgentConfig{ID: "agent_shadow", Name: "Shadow", SkillIDs: []string{"app-weekly"}}
	if _, err := kit.skillToolset(ac, store.Get(), workspace); err != nil {
		t.Fatalf("shadowed app skill must not break the build: %v", err)
	}
	fm, body := readMaterializedSkill(t, store, ac.ID, "weekly-report")
	if fm.Description != "项目周报" || !strings.Contains(body, "项目版本") {
		t.Fatalf("expected project definition, got fm=%+v body=%q", fm, body)
	}
}

func TestGlobalSkillMaterialization(t *testing.T) {
	kit, store := newTestKit(t)
	writeAgentSkillMD(t, kit.globalSkillRoot, "global-skill", "全局技能", "# 全局正文")

	ac := config.AgentConfig{
		ID:       "agent_global",
		Name:     "Global",
		SkillIDs: []string{config.DiscoveredSkillID(config.SkillSourceGlobal, "global-skill")},
	}
	if _, err := kit.skillToolset(ac, store.Get(), t.TempDir()); err != nil {
		t.Fatalf("skillToolset: %v", err)
	}
	if fm, _ := readMaterializedSkill(t, store, ac.ID, "global-skill"); fm.Description != "全局技能" {
		t.Fatalf("frontmatter = %+v", fm)
	}
}

// TestDiscoveredSkillMaterializesResources 固定官方技能目录的附带资源：ADK
// 的 load_skill_resource 只读取物化目录，因此 references/ 等文件必须一起复制。
func TestDiscoveredSkillMaterializesResources(t *testing.T) {
	kit, store := newTestKit(t)
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	writeAgentSkillMD(t, root, "resource-skill", "带资源的技能", "见 references/REFERENCE.md")
	refDir := filepath.Join(root, "resource-skill", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "REFERENCE.md"), []byte("参考内容"), 0o644); err != nil {
		t.Fatal(err)
	}

	ac := config.AgentConfig{
		ID:       "agent_resource",
		Name:     "Resource",
		SkillIDs: []string{config.DiscoveredSkillID(config.SkillSourceProject, "resource-skill")},
	}
	if _, err := kit.skillToolset(ac, store.Get(), workspace); err != nil {
		t.Fatalf("skillToolset: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(store.SkillsDir(), "agents", ac.ID, "resource-skill", "references", "REFERENCE.md"))
	if err != nil {
		t.Fatalf("materialized resource missing: %v", err)
	}
	if string(data) != "参考内容" {
		t.Fatalf("resource = %q", data)
	}
}

// TestAppSkillWithoutDescriptionStillBuilds 校验旧配置/标题型技能不会因空
// description 在物化时让整个 Agent 构建失败。
func TestAppSkillWithoutDescriptionStillBuilds(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Skills = append(cfg.Skills, config.SkillConfig{
			ID: "s-empty-desc", Name: "Empty Desc", Content: "# 正文标题\n\n正文",
		})
	}); err != nil {
		t.Fatal(err)
	}
	ac := config.AgentConfig{ID: "agent_empty_desc", Name: "Empty", SkillIDs: []string{"s-empty-desc"}}
	if _, err := kit.skillToolset(ac, store.Get(), t.TempDir()); err != nil {
		t.Fatalf("skillToolset: %v", err)
	}
	fm, _ := readMaterializedSkill(t, store, ac.ID, "empty-desc")
	if fm.Description != "正文标题" {
		t.Fatalf("description = %q", fm.Description)
	}
}

// TestVanishedDiscoveredSkillIsSkipped：切换到不包含已挂载项目技能的工作区，不
// 得使 Agent 无法构建（issue #52 #7）；缺失的 *app* 技能仍保留现有的硬错误。
func TestVanishedDiscoveredSkillIsSkipped(t *testing.T) {
	kit, store := newTestKit(t)
	ac := config.AgentConfig{
		ID:       "agent_vanished",
		Name:     "Vanished",
		SkillIDs: []string{config.DiscoveredSkillID(config.SkillSourceProject, "gone")},
	}
	if _, err := kit.skillToolset(ac, store.Get(), t.TempDir()); err != nil {
		t.Fatalf("vanished discovered skill must be skipped, got %v", err)
	}

	missingApp := config.AgentConfig{ID: "agent_missing", Name: "Missing", SkillIDs: []string{"nope"}}
	if _, err := kit.skillToolset(missingApp, store.Get(), t.TempDir()); err == nil {
		t.Fatal("missing app skill must still error")
	}
}
