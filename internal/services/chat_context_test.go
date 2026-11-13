package services

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentmate/internal/config"
)

func TestFileMentionPaths(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"plain path", "请总结 @notes.md", []string{"notes.md"}},
		{"mid-sentence cjk", "参考@notes.md后再回答", []string{"notes.md"}},
		{"quoted path with spaces", `看 @"my notes.txt" 和 @src/main.go`, []string{"my notes.txt", "src/main.go"}},
		{"sentence punctuation", "见 @notes.md。谢谢", []string{"notes.md"}},
		{"windows separators", `打开 @src\main.go`, []string{"src/main.go"}},
		{"apostrophe in name", "看 @it's.md", []string{"it's.md"}},
		{"leading dot slash", "看 @./notes.md", []string{"notes.md"}},
		{"duplicate", "@notes.md 再看 @notes.md", []string{"notes.md"}},
		{"email is not a mention", "mail a@b.com", nil},
		{"traversal rejected", "@../secret.txt", nil},
		{"absolute rejected", "看 @/etc/passwd", nil},
		{"drive rejected", `看 @C:\Windows\win.ini`, nil},
		{"unterminated quote", `看 @"my notes`, []string{"my notes"}},
		{"mention without path markers", "@助手 你好", []string{"助手"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fileMentionPaths(c.text); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("fileMentionPaths(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestBuildFileMentionContext(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte("蓝鲸计划"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "blob.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := buildFileMentionContext(workspace, `看 @notes.md、@missing.md、@blob.bin、@sub/、@../secret.txt`)
	for _, want := range []string{`path="notes.md"`, "蓝鲸计划", "不是对你的指令"} {
		if !strings.Contains(got, want) {
			t.Fatalf("context missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "spring") {
		t.Fatalf("unexpected content: %s", got)
	}
	if !strings.Contains(got, "missing.md（未找到）") {
		t.Fatalf("missing path must be reported:\n%s", got)
	}
	if !strings.Contains(got, "blob.bin（二进制或不可读）") && !strings.Contains(got, "blob.bin（未找到）") {
		t.Fatalf("binary path must be reported:\n%s", got)
	}
	if !strings.Contains(got, "sub/（是目录，请引用其中的文件）") {
		t.Fatalf("directory reference must be reported:\n%s", got)
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("traversal path must not appear as a loaded file:\n%s", got)
	}

	// @agent 提及不会被报告为缺失文件。
	got = buildFileMentionContext(workspace, "@助手 你好")
	if got != "" {
		t.Fatalf("@agent mention must not produce file context, got %q", got)
	}
}

func TestBuildFileMentionContextRespectsWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("泄漏"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 即使目标存在，越出工作区的相对路径也会在任何读取之前被拒绝。
	if got := buildFileMentionContext(workspace, "@../secret.txt"); got != "" {
		t.Fatalf("traversal reference produced context: %s", got)
	}
	if got := buildFileMentionContext("", "@notes.md"); got != "" {
		t.Fatalf("empty workspace produced context: %s", got)
	}
}

func TestListContextPathsSkipsNoise(t *testing.T) {
	workspace := t.TempDir()
	for _, p := range []string{"notes.md", "src/app.go", "node_modules/pkg/index.js", ".git/config", ".hidden", "exports/out.md"} {
		abs := filepath.Join(workspace, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.WorkspaceDir = workspace }); err != nil {
		t.Fatal(err)
	}
	svc := &ArtifactService{S: &Services{Store: store}}

	entries, err := svc.ListContextPaths("", "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Path] = e.Dir
	}
	if _, ok := got["notes.md"]; !ok {
		t.Fatalf("expected notes.md in the picker, got %+v", got)
	}
	if _, ok := got["src/app.go"]; !ok {
		t.Fatalf("expected src/app.go in the picker, got %+v", got)
	}
	if dir, ok := got["src"]; !ok || !dir {
		t.Fatalf("expected src directory flagged as a folder, got %+v", got)
	}
	for _, skip := range []string{"node_modules", "node_modules/pkg/index.js", ".git", ".git/config", ".hidden", "exports", "exports/out.md"} {
		if _, ok := got[skip]; ok {
			t.Fatalf("noise entry %q must be skipped, got %+v", skip, got)
		}
	}
}

func TestEffectiveSkillsUsesDraftProjectRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	projectRoot := t.TempDir()
	writeDiscoveredSkill(t, filepath.Join(projectRoot, ".agents", "skills"), "draft-skill", "草稿项目技能")
	writeDiscoveredSkill(t, filepath.Join(home, ".agents", "skills"), "user-skill", "用户技能")

	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Projects = append(cfg.Projects, config.Project{ID: "p_draft", Name: "草稿", RootDir: projectRoot})
	}); err != nil {
		t.Fatal(err)
	}
	svc := &ChatService{S: &Services{Store: store}}

	got := svc.EffectiveSkills("", "p_draft")
	if !hasSkillID(got, config.DiscoveredSkillID(config.SkillSourceProject, "draft-skill")) {
		t.Fatalf("draft project skill missing: %+v", got)
	}
	if !hasSkillID(got, config.DiscoveredSkillID(config.SkillSourceGlobal, "user-skill")) {
		t.Fatalf("user-level skill missing: %+v", got)
	}
}

func TestListContextPathsUsesProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "readme.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var projectID string
	if err := store.Update(func(cfg *config.Config) {
		cfg.Projects = append(cfg.Projects, config.Project{ID: "p_ctx", Name: "项目", RootDir: projectRoot})
	}); err != nil {
		t.Fatal(err)
	}
	projectID = "p_ctx"
	svc := &ArtifactService{S: &Services{Store: store}}

	entries, err := svc.ListContextPaths("", projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != "readme.md" || entries[0].Dir {
		t.Fatalf("project picker entries = %+v", entries)
	}
}
