package services

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-workbuddy/internal/agentkit"
	"go-workbuddy/internal/config"
)

// TestSessionAllowAllToolsPersistsAndAudits covers the session-level grant:
// the flag survives a config reload (restart), other sessions stay untouched,
// and both switch locations land in the audit trail with their source.
func TestSessionAllowAllToolsPersistsAndAudits(t *testing.T) {
	chat, store, _, dir := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	chat.SetToolAuditPath(auditPath)
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}

	if got, err := chat.SessionAllowAllTools(sess.ID); err != nil || got {
		t.Fatalf("new session must default to off, got %v err=%v", got, err)
	}
	if err := chat.SetSessionAllowAllTools(sess.ID, true, "confirm_card"); err != nil {
		t.Fatal(err)
	}
	if got, err := chat.SessionAllowAllTools(sess.ID); err != nil || !got {
		t.Fatalf("grant not visible, got %v err=%v", got, err)
	}
	if got, err := chat.SessionAllowAllTools(other.ID); err != nil || got {
		t.Fatalf("grant leaked to another session, got %v err=%v", got, err)
	}

	// "Restart": a fresh store over the same directory must load the grant.
	reopened, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range reopened.Get().Sessions {
		if s.ID == sess.ID && !s.AllowAllTools {
			t.Fatal("allow-all flag was not persisted to config.json")
		}
	}

	if err := chat.SetSessionAllowAllTools(sess.ID, false, "composer"); err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionAllowAllTools(sess.ID, true, "unexpected-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.SessionAllowAllTools("sess_missing"); err == nil {
		t.Fatal("expected an error for an unknown session")
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		`"phase":"session_allow_all_tools_set"`,
		`"source":"confirm_card"`,
		`"source":"composer"`,
		`"source":"unknown"`,
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("audit missing %s:\n%s", want, doc)
		}
	}
}

// TestAllowAllSessionSkipsToolConfirmation: once the session grants complete
// access, a side-effectful write runs directly — no confirm_request event and
// no waiting on the human.
func TestAllowAllSessionSkipsToolConfirmation(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"granted.txt\",\"content\":\"直接写入\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("已直接写入。") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionAllowAllTools(sess.ID, true, "composer"); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 granted.txt", nil); err != nil {
		t.Fatal(err)
	}

	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("allow-all session must not ask for confirmation: %+v", ev)
		}
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "granted.txt")); err != nil || string(data) != "直接写入" {
		t.Fatalf("expected direct write, err=%v data=%q", err, data)
	}
}

// TestAllowAllFromConfirmCardAppliesToLaterTurns mirrors the card action: the
// grant and the approval happen together, the pending call resumes, and the
// next turn's write no longer asks.
func TestAllowAllFromConfirmCardAppliesToLaterTurns(t *testing.T) {
	turn := 0
	var callID string
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"first.txt\",\"content\":\"一\"}"`) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseText("第一次完成。") + sseDone))
		case 3:
			_, _ = w.Write([]byte(sseToolCall("call_2", "write_workspace_file", `"{\"path\":\"second.txt\",\"content\":\"二\"}"`) + sseDone))
		case 4:
			_, _ = w.Write([]byte(sseText("第二次完成。") + sseDone))
		default:
			t.Errorf("unexpected model turn %d", turn)
			_, _ = w.Write([]byte(sseDone))
		}
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写 first.txt", nil); err != nil {
		t.Fatal(err)
	}
	for i := range *events {
		if (*events)[i].Kind == "confirm_request" {
			callID = (*events)[i].CallID
		}
	}
	if callID == "" {
		t.Fatal("expected a first confirm_request")
	}

	// The card action order: grant the session, then approve this call.
	if err := chat.SetSessionAllowAllTools(sess.ID, true, "confirm_card"); err != nil {
		t.Fatal(err)
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "first.txt")); err != nil || string(data) != "一" {
		t.Fatalf("expected the pending write to resume, err=%v data=%q", err, data)
	}

	if err := chat.Send(sess.ID, "再写 second.txt", nil); err != nil {
		t.Fatal(err)
	}
	confirmations := 0
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			confirmations++
		}
	}
	if confirmations != 1 {
		t.Fatalf("later turns must not ask again, got %d confirmations", confirmations)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "second.txt")); err != nil || string(data) != "二" {
		t.Fatalf("expected the second write to run directly, err=%v data=%q", err, data)
	}
}

// TestAllowAllAppliesToMentionRoutedTurn: an @-mentioned agent turn is built
// with the same session permission as the chat's own target.
func TestAllowAllAppliesToMentionRoutedTurn(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("call_m", "write_workspace_file", `"{\"path\":\"mention.txt\",\"content\":\"路由\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("已写入。") + sseDone))
	})
	events := hookEvents(t)

	sess, err := chat.newLegacySession("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionAllowAllTools(sess.ID, true, "composer"); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "@写手 写 mention.txt", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("mention-routed turn ignored the session grant: %+v", ev)
		}
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "mention.txt")); err != nil || string(data) != "路由" {
		t.Fatalf("expected the routed write, err=%v data=%q", err, data)
	}
}

// TestAllowAllAppliesToTeamMembers: a team session's grant reaches the local
// member agent that actually calls the write tool.
func TestAllowAllAppliesToTeamMembers(t *testing.T) {
	turn := 0
	chat, store, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		body := readAll(r)
		switch {
		case strings.Contains(body, "You are the coordinator"):
			target := agentkit.SanitizeAgentName("写手")
			args := `"{\"agent_name\":\"` + target + `\"}"`
			_, _ = w.Write([]byte(sseToolCall("t1", "transfer_to_agent", args) + sseDone))
		case strings.Contains(body, "你负责写文件") && !strings.Contains(body, `\"ok\":true`):
			_, _ = w.Write([]byte(sseToolCall("call_t", "write_workspace_file", `"{\"path\":\"team.txt\",\"content\":\"团队\"}"`) + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("团队已写入。") + sseDone))
		}
	})
	events := hookEvents(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Teams = append(cfg.Teams, config.TeamConfig{
			ID: "team_c", Name: "写手团队", ProviderID: "prov_c", Model: "fake-mini",
			AutoCoordinate: true, MemberAgentIDs: []string{"agent_c"},
		})
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := chat.newLegacySession("team", "team_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionAllowAllTools(sess.ID, true, "composer"); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "请团队写文件", nil); err != nil {
		t.Fatal(err)
	}

	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("team member ignored the session grant: %+v", ev)
		}
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "team.txt")); err != nil || string(data) != "团队" {
		t.Fatalf("expected the team member write, err=%v data=%q", err, data)
	}
}