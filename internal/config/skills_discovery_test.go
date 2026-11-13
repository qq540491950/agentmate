package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillMD(name, desc, body string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
}

func TestDiscoveredSkillsScansOneLevelFromBothRoots(t *testing.T) {
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, ".agents", "skills")
	globalRoot := t.TempDir()

	writeSkill(t, projectRoot, "alpha", skillMD("alpha", "项目技能", "# 项目"))
	writeSkill(t, projectRoot, "bad-name", skillMD("Bad_Name", "非法名称", "# x"))
	writeSkill(t, projectRoot, "no-desc", skillMD("no-desc", "", "# x"))
	writeSkill(t, projectRoot, "nested/deep", skillMD("deep", "嵌套不算", "# x"))
	writeSkill(t, globalRoot, "omega", skillMD("omega", "全局技能", "# 全局"))
	// 根目录下的散落文件和小写文件名会被忽略。
	if err := os.WriteFile(filepath.Join(projectRoot, "loose.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, "lower"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "lower", "skill.md"), []byte(skillMD("lower", "小写文件名", "# x")), 0o644); err != nil {
		t.Fatal(err)
	}

	got := DiscoveredSkills(workspace, globalRoot)
	if len(got) != 2 {
		t.Fatalf("discovered %d skills, want 2: %+v", len(got), got)
	}
	if got[0].ID != DiscoveredSkillID(SkillSourceProject, "alpha") || got[0].Name != "alpha" {
		t.Fatalf("project skill = %+v", got[0])
	}
	if got[0].Description != "项目技能" || got[0].Content != "# 项目" {
		t.Fatalf("project skill parse = %+v", got[0])
	}
	if source, ok := DiscoveredSkillSource(got[0].ID); !ok || source != SkillSourceProject {
		t.Fatalf("project source = %q ok=%v", source, ok)
	}
	if name := DiscoveredSkillName(got[0].ID); name != "alpha" {
		t.Fatalf("project name = %q", name)
	}
	if got[1].ID != DiscoveredSkillID(SkillSourceGlobal, "omega") {
		t.Fatalf("global skill = %+v", got[1])
	}
}

func TestDiscoveredSkillsMissingRootsAreSilent(t *testing.T) {
	got := DiscoveredSkills(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "nope2"))
	if len(got) != 0 {
		t.Fatalf("missing roots should be empty, got %+v", got)
	}
	// 空工作区仅跳过项目根目录；全局根目录仍然有效。
	globalRoot := t.TempDir()
	writeSkill(t, globalRoot, "omega", skillMD("omega", "全局", "# x"))
	if got := DiscoveredSkills("", globalRoot); len(got) != 1 || got[0].Name != "omega" {
		t.Fatalf("global-only discovery = %+v", got)
	}
}

func TestEffectiveSkillsPriorityProjectGlobalApp(t *testing.T) {
	workspace := t.TempDir()
	globalRoot := t.TempDir()
	projectRoot := filepath.Join(workspace, ".agents", "skills")
	writeSkill(t, projectRoot, "shared", skillMD("shared", "项目版本", "# 项目版本"))
	writeSkill(t, globalRoot, "shared", skillMD("shared", "全局版本", "# 全局版本"))
	writeSkill(t, globalRoot, "weekly-report", skillMD("weekly-report", "全局周报", "# 全局周报"))

	cfg := Config{Skills: []SkillConfig{
		{ID: "app-shared", Name: "shared", Description: "应用版本", Content: "# 应用版本"},
		{ID: "app-weekly", Name: "Weekly Report", Description: "应用周报", Content: "# 应用周报"},
		{ID: "app-only", Name: "only-app", Description: "仅应用内", Content: "# 仅应用内"},
	}}

	got := EffectiveSkills(cfg, workspace, globalRoot)
	var names, descs []string
	for _, s := range got {
		names = append(names, s.Name)
		descs = append(descs, s.Description)
	}
	wantNames := []string{"shared", "weekly-report", "only-app"}
	if len(got) != len(wantNames) {
		t.Fatalf("effective = %v, want %v", names, wantNames)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] {
			t.Fatalf("effective names = %v, want %v", names, wantNames)
		}
	}
	// 项目优先于全局，全局优先于应用；周名称在规范化后进行比较，因此 "Weekly
	// Report" 会被 "weekly-report" 遮蔽。
	if descs[0] != "项目版本" || descs[1] != "全局周报" {
		t.Fatalf("precedence lost: %v", descs)
	}
	if got[2].ID != "app-only" {
		t.Fatalf("app-only skill changed identity: %+v", got[2])
	}
}

func TestEffectiveSkillsWithoutDiscoveredRootsKeepsAppSkills(t *testing.T) {
	cfg := Config{Skills: []SkillConfig{{ID: "s1", Name: "API Review", Content: "x"}}}
	got := EffectiveSkills(cfg, filepath.Join(t.TempDir(), "missing"), "")
	if len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("effective = %+v", got)
	}
}

func TestParseSkillMarkdownBlockScalarDescription(t *testing.T) {
	content := "---\nname: alpha\ndescription: >-\n  第一行\n  第二行\n---\n\n正文\n"
	name, desc, body := ParseSkillMarkdown(content)
	if name != "alpha" {
		t.Fatalf("name = %q", name)
	}
	if desc != "第一行 第二行" {
		t.Fatalf("desc = %q", desc)
	}
	if body != "正文" {
		t.Fatalf("body = %q", body)
	}
}

func TestNormalizeSkillName(t *testing.T) {
	cases := map[string]string{
		"Weekly Report": "weekly-report",
		"api__review":   "api-review",
		"  --x--  ":     "x",
		"周报":            "skill",
	}
	for in, want := range cases {
		if got := NormalizeSkillName(in); got != want {
			t.Fatalf("NormalizeSkillName(%q) = %q, want %q", in, got, want)
		}
	}
}
