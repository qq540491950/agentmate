package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/config"
)

// TestGetConfigMergesDiscoveredSkillsReadOnly pins issue #52's contract:
// project/global .agents/skills appear in GetConfig (so the Skills page and
// the Agent skill picker show them) but never leak into config.json, and the
// read-only entries cannot be edited or deleted through the app.
func TestGetConfigMergesDiscoveredSkillsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows

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

	// Read-only: the discovered entries are not persisted.
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