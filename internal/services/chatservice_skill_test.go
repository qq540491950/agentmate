package services

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// newSkillTestChat 将 ChatService 接入一个按预先脚本响应的 SSE 模型服务器。
// 返回的通道按回合顺序接收每个请求体。
func newSkillTestChat(t *testing.T) (*ChatService, <-chan string) {
	t.Helper()
	bodies := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodies <- readAll(r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: srv.URL, Models: []config.ModelInfo{{ID: "m"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{
			ID: "a1", Name: "助手", ProviderID: "p1", Model: "m",
		})
		// 使用一个独立的技能名称，避免与其他测试夹具冲突。
		cfg.Skills = append(cfg.Skills, config.SkillConfig{
			ID: "s1", Name: "Slash Test Skill", Description: "测试技能",
			Content: "# 斜杠技能规则\n只输出一张表格。",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	db, sessions, err := newTestSessionService(t, filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewChatService(svc, sessions, db), bodies
}

// TestSkillSlashCommandInjectsSkill 断言 "/<skill>" 命令会作为注入的 SKILL.md
// 指令到达模型，同时存储的用户消息与用户输入的内容完全一致，并且该注入不会泄漏
// 到下一回合。
func TestSkillSlashCommandInjectsSkill(t *testing.T) {
	chat, bodies := newSkillTestChat(t)
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "/slash-test-skill 本周进展：完成登录页", nil); err != nil {
		t.Fatal(err)
	}
	first := <-bodies
	if !strings.Contains(first, "只输出一张表格") {
		t.Fatalf("skill body must be injected into the request, got: %s", first)
	}
	if !strings.Contains(first, "/slash-test-skill 本周进展：完成登录页") {
		t.Fatalf("raw command must stay the user message, got: %s", first)
	}
	if !strings.Contains(first, "本周进展：完成登录页") || !strings.Contains(first, "本轮实际任务") {
		t.Fatalf("command arguments must reach the model as the actual task, got: %s", first)
	}

	// 历史记录会显示该命令，绝不显示注入的 SKILL.md 正文。
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
		if m.Text != "/slash-test-skill 本周进展：完成登录页" {
			t.Fatalf("user history must keep the typed text, got %q", m.Text)
		}
		if strings.Contains(m.Text, "只输出一张表格") {
			t.Fatal("skill body leaked into the stored user message")
		}
	}
	if !sawUser {
		t.Fatalf("user message missing from history: %+v", msgs)
	}

	// 下一个普通回合不得继承技能指令（"temp:" 状态按每次调用应用，不持久化）。
	if err := chat.Send(sess.ID, "继续", nil); err != nil {
		t.Fatal(err)
	}
	second := <-bodies
	if strings.Contains(second, "只输出一张表格") {
		t.Fatalf("skill instruction leaked into the following turn: %s", second)
	}
}

// TestResolveSkillCommand 覆盖命令匹配：物化名称规范化、大小写不敏感、参数以及未命中的情况。
func TestResolveSkillCommand(t *testing.T) {
	cfg := config.Config{Skills: []config.SkillConfig{
		{ID: "s1", Name: "Weekly Report"},
		{ID: "s2", Name: "翻译"},
	}}
	cases := []struct {
		text    string
		wantID  string
		wantArg string
	}{
		{"/weekly-report 写周报", "s1", "写周报"},
		{"/Weekly_Report", "s1", ""},
		{"/WEEKLY-REPORT   多个 空白", "s1", "多个 空白"},
		{"  /weekly-report\t参数", "s1", "参数"},
		{"/skill 任意", "s2", "任意"},
		{"/export x", "", ""},
		{"hello /weekly-report", "", ""},
		{"/", "", ""},
	}
	for _, c := range cases {
		got, args := resolveSkillCommand(cfg.Skills, c.text)
		gotID := ""
		if got != nil {
			gotID = got.ID
		}
		if gotID != c.wantID {
			t.Errorf("resolveSkillCommand(%q) id = %q, want %q", c.text, gotID, c.wantID)
		}
		if c.wantID != "" && args != c.wantArg {
			t.Errorf("resolveSkillCommand(%q) args = %q, want %q", c.text, args, c.wantArg)
		}
	}
}
