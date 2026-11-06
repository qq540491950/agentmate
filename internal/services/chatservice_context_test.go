package services

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-workbuddy/internal/config"
)

// newContextTestChat wires a ChatService whose model server records every
// request body, so tests can assert what actually reached the model. HOME is
// redirected before the kit is built, keeping user-level skill discovery
// hermetic.
func newContextTestChat(t *testing.T) (*ChatService, <-chan string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows

	bodies := make(chan string, 8)
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		bodies <- readAll(r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	})
	return chat, bodies
}

// TestProjectDiscoveredSkillSlashCommand pins the project-skill half of the
// slash menu: a skill living in <project>/.agents/skills must resolve for a
// chat bound to that project, even though it never enters config.json.
func TestProjectDiscoveredSkillSlashCommand(t *testing.T) {
	chat, bodies := newContextTestChat(t)
	projectRoot := t.TempDir()
	writeDiscoveredSkill(t, filepath.Join(projectRoot, ".agents", "skills"), "project-skill", "项目技能")

	project, err := chat.SaveProject("", "", projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithProject("", "", "项目对话", project.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "/project-skill 写一份周报", nil); err != nil {
		t.Fatal(err)
	}
	body := <-bodies
	if !strings.Contains(body, "项目技能正文") {
		t.Fatalf("project skill body must reach the model, got: %s", body)
	}
	if !strings.Contains(body, "写一份周报") {
		t.Fatalf("command arguments must reach the model, got: %s", body)
	}
	wantID := config.DiscoveredSkillID(config.SkillSourceProject, "project-skill")
	if got := chat.EffectiveSkills(sess.ID, ""); !hasSkillID(got, wantID) {
		t.Fatalf("EffectiveSkills(%q) must list %q, got %+v", sess.ID, wantID, got)
	}
}

func hasSkillID(skills []config.SkillConfig, id string) bool {
	for _, s := range skills {
		if s.ID == id {
			return true
		}
	}
	return false
}

// TestFileMentionInjectsWorkspaceContent pins the composer's "@ 添加文件上下文"
// contract: text references are resolved against the chat's effective
// workspace and inlined for that turn only.
func TestFileMentionInjectsWorkspaceContent(t *testing.T) {
	chat, bodies := newContextTestChat(t)
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "notes.md"), []byte("蓝鲸计划：周三上线"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "my notes.txt"), []byte("带空格的笔记"), 0o644); err != nil {
		t.Fatal(err)
	}
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "secret.txt"), []byte("泄漏的秘密"), 0o644); err != nil {
		t.Fatal(err)
	}

	project, err := chat.SaveProject("", "", projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSessionWithProject("", "", "项目对话", project.ID)
	if err != nil {
		t.Fatal(err)
	}

	text := `请总结 @notes.md，并参考 @"my notes.txt"，忽略 @../secret.txt`
	if err := chat.Send(sess.ID, text, nil); err != nil {
		t.Fatal(err)
	}
	body := <-bodies
	if !strings.Contains(body, "蓝鲸计划：周三上线") {
		t.Fatalf("@notes.md content must reach the model, got: %s", body)
	}
	if !strings.Contains(body, "带空格的笔记") {
		t.Fatalf("quoted @\"my notes.txt\" content must reach the model, got: %s", body)
	}
	if strings.Contains(body, "泄漏的秘密") {
		t.Fatalf("a reference outside the workspace must never be read, got: %s", body)
	}

	// The raw reference stays in the stored user message; only the inlined
	// body is turn-scoped.
	msgs, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	sawUser := false
	for _, m := range msgs {
		if m.Kind != "user" {
			continue
		}
		sawUser = true
		if m.Text != text {
			t.Fatalf("stored user text = %q, want the typed text %q", m.Text, text)
		}
	}
	if !sawUser {
		t.Fatalf("user message missing from history: %+v", msgs)
	}

	// A follow-up turn must not inherit the previous turn's file context.
	if err := chat.Send(sess.ID, "继续", nil); err != nil {
		t.Fatal(err)
	}
	second := <-bodies
	if strings.Contains(second, "蓝鲸计划") {
		t.Fatalf("file context leaked into the following turn: %s", second)
	}
}
