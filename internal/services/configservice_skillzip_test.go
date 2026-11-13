package services

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/config"
)

func writeServiceSkillZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skill.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportSkillZipToGlobalAndProject(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	projectRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
		cfg.Projects = append(cfg.Projects, config.Project{ID: "proj_demo", Name: "demo", RootDir: projectRoot})
	}); err != nil {
		t.Fatal(err)
	}
	svc := &ConfigService{S: &Services{Store: store}}

	globalZip := writeServiceSkillZip(t, map[string]string{
		"diagnosing-bugs/SKILL.md":                "---\nname: diagnosing-bugs\ndescription: Diagnosis loop\n---\n\n# Diagnosing\n",
		"diagnosing-bugs/references/checklist.md": "checklist",
	})
	got, err := svc.ImportSkillZip(globalZip, SkillImportScopeGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "diagnosing-bugs" || got.Scope != SkillImportScopeGlobal {
		t.Fatalf("result = %+v", got)
	}
	wantGlobal := filepath.Join(home, ".agents", "skills", "diagnosing-bugs")
	if got.TargetDir != wantGlobal {
		t.Fatalf("target = %q, want %q", got.TargetDir, wantGlobal)
	}
	if _, err := os.Stat(filepath.Join(wantGlobal, "references", "checklist.md")); err != nil {
		t.Fatal(err)
	}

	projectZip := writeServiceSkillZip(t, map[string]string{
		"project-skill/SKILL.md": "---\nname: project-skill\ndescription: Project only\n---\n\nBody",
	})
	got, err = svc.ImportSkillZip(projectZip, SkillImportScopeProject, "proj_demo")
	if err != nil {
		t.Fatal(err)
	}
	wantProject := filepath.Join(projectRoot, ".agents", "skills", "project-skill")
	if got.TargetDir != wantProject {
		t.Fatalf("target = %q, want %q", got.TargetDir, wantProject)
	}
	discovered := config.DiscoveredSkills(projectRoot, "")
	if len(discovered) != 1 || discovered[0].Name != "project-skill" {
		t.Fatalf("discovered = %+v", discovered)
	}
}

func TestImportSkillZipUsesDefaultWorkspaceAndValidatesTargets(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
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
	zipPath := writeServiceSkillZip(t, map[string]string{
		"SKILL.md": "---\nname: default-workspace-skill\ndescription: Default workspace\n---\n",
	})

	got, err := svc.ImportSkillZip(zipPath, SkillImportScopeProject, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetDir != filepath.Join(workspace, ".agents", "skills", "default-workspace-skill") {
		t.Fatalf("target = %q", got.TargetDir)
	}

	if _, err := svc.ImportSkillZip(zipPath, SkillImportScopeProject, "missing-project"); err == nil || !strings.Contains(err.Error(), "项目不存在") {
		t.Fatalf("unknown project error = %v", err)
	}
	if _, err := svc.ImportSkillZip(zipPath, "invalid", ""); err == nil || !strings.Contains(err.Error(), "未知") {
		t.Fatalf("invalid scope error = %v", err)
	}
}
