package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/config"
)

// TestGetConfigMergesDiscoveredSkillsReadOnly 固定了 issue #52 的约定：
// 项目级/全局 .agents/skills 会出现在 GetConfig 中（因此 Skills 页面和 Agent
// 技能选择器会显示它们），但绝不会泄漏进 config.json，并且
// 这些只读条目无法通过应用编辑或删除。
func TestGetConfigMergesDiscoveredSkillsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 上的 os.UserHomeDir

	workspace := t.TempDir()
	writeDiscoveredSkill(t, filepath.Join(workspace, ".agents", "skills"), "project-skill", "项目技能")
	writeDiscoveredSkill(t, filepath.Join(home, ".agents", "skills"), "global-skill", "全局技能")

	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
	}); err != nil {
		t.Fatal(err)
	}
	svc := &ConfigService{S: &Services{Store: store}}

	cfg := svc.GetConfig()
	projectID := config.DiscoveredSkillID(config.SkillSourceProject, "project-skill")
	globalID := config.DiscoveredSkillID(config.SkillSourceGlobal, "global-skill")
	found := map[string]config.SkillConfig{}
	for _, s := range cfg.Skills {
		found[s.ID] = s
	}
	if s, ok := found[projectID]; !ok || s.Description != "项目技能" || !strings.Contains(s.Content, "项目技能正文") {
		t.Fatalf("project skill missing from GetConfig: %+v", found)
	}
	if _, ok := found[globalID]; !ok {
		t.Fatalf("global skill missing from GetConfig: %+v", found)
	}

	// 只读：发现的条目不会被持久化。
	for _, s := range store.Get().Skills {
		if _, discovered := config.DiscoveredSkillSource(s.ID); discovered {
			t.Fatalf("discovered skill leaked into config.json: %+v", s)
		}
	}
	if err := svc.DeleteSkill(projectID); err == nil {
		t.Fatal("DeleteSkill must reject a discovered skill")
	}
	if err := svc.SaveSkill(config.SkillConfig{ID: globalID, Name: "global-skill"}); err == nil {
		t.Fatal("SaveSkill must reject a discovered skill")
	}
}

func writeDiscoveredSkill(t *testing.T, root, name, desc string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + desc + "正文\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
