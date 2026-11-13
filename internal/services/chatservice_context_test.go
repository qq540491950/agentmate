package services

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/config"
)

// newContextTestChat 构造一个
// ChatService，其模型服务器会记录每个请求体，因此测试可以断言实际到达模型的内容
// 。在构建套件之前重定向 HOME，从而使用户级技能发现保持隔离。
func newContextTestChat(t *testing.T) (*ChatService, <-chan string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 上的 os.UserHomeDir

	bodies := make(chan string, 8)
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		bodies <- readAll(r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	})
	return chat, bodies
}

// TestProjectDiscoveredSkillSlashCommand 锁定斜杠菜单中项目技能这一部分：对于绑定到该项目的聊天，位于
// <project>/.agents/skills 的技能必须能被解析，即使它绝不会进入 config.json。
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

// TestFileMentionInjectsWorkspaceContent 固定 Composer 的「@ 添加文件上下文」
// 契约：文本引用按会话的有效工作区解析，并且仅在该回合内联。
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

	// 原始引用保留在存储的用户消息中；仅内联正文是回合作用域的。
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

	// 后续回合不得继承上一回合的文件上下文。
	if err := chat.Send(sess.ID, "继续", nil); err != nil {
		t.Fatal(err)
	}
	second := <-bodies
	if strings.Contains(second, "蓝鲸计划") {
		t.Fatalf("file context leaked into the following turn: %s", second)
	}
}
