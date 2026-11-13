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

// TestDiscoveredSkillShadowsAppSkill: precedence project > global > app must
// not break an agent that mounted the app skill's ID — it materializes the
// winning definition instead of failing the build.
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
