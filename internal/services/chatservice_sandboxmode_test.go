package services

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/config"
)

func TestNewSessionSnapshotsDefaultSandboxMode(t *testing.T) {
	chat, store, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {})
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.ApprovalPolicy = config.ApprovalPolicyAutoApprove
		cfg.Settings.SandboxProfile = config.SandboxProfileWorkspaceWrite
		cfg.Settings.SandboxNetwork = config.SandboxNetworkOn
	}); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.NewSession("agent", "agent_ws", "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ApprovalPolicy != config.ApprovalPolicyAutoApprove ||
		sess.SandboxProfile != config.SandboxProfileWorkspaceWrite ||
		sess.SandboxMode != config.SandboxModeAutoApprove ||
		sess.SandboxNetwork != config.SandboxNetworkOn {
		t.Fatalf("new session snapshot = %+v", sess)
	}

	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.ApprovalPolicy = config.ApprovalPolicyRequestApproval
		cfg.Settings.SandboxProfile = config.SandboxProfileWorkspaceWrite
		cfg.Settings.SandboxNetwork = config.SandboxNetworkOff
	}); err != nil {
		t.Fatal(err)
	}
	for _, got := range store.Get().Sessions {
		if got.ID == sess.ID &&
			(got.ApprovalPolicy != config.ApprovalPolicyAutoApprove ||
				got.SandboxProfile != config.SandboxProfileWorkspaceWrite ||
				got.SandboxMode != config.SandboxModeAutoApprove ||
				got.SandboxNetwork != config.SandboxNetworkOn) {
			t.Fatalf("settings rewrite changed existing session: %+v", got)
		}
	}
}

func TestForkSessionInheritsSandboxMode(t *testing.T) {
	chat, _, _ := newWorkspaceTestChat(t, func(w http.ResponseWriter, r *http.Request) {})
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_ws", "source", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyRequestApproval,
		config.SandboxProfileDangerFullAccess,
		"composer",
	); err != nil {
		t.Fatal(err)
	}
	branch, err := chat.ForkSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ApprovalPolicy != config.ApprovalPolicyRequestApproval ||
		branch.SandboxProfile != config.SandboxProfileDangerFullAccess ||
		branch.SandboxMode != config.SandboxModeDangerFullAccess ||
		branch.SandboxNetwork != config.SandboxNetworkOff {
		t.Fatalf("fork execution policy = %+v", branch)
	}
	if got := branch.EffectiveSandboxNetwork(chat.S.Store.Get()); got != config.SandboxNetworkOn {
		t.Fatalf("danger fork effective network = %q, want on", got)
	}
}

func TestSessionSandboxModePersistsIndependentlyAndAudits(t *testing.T) {
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

	if got, err := chat.SessionSandboxMode(sess.ID); err != nil || got != config.SandboxModeAutoApprove {
		t.Fatalf("initial mode = %q err=%v", got, err)
	}
	if err := chat.SetSessionSandboxMode(sess.ID, config.SandboxModeDangerFullAccess, "composer"); err != nil {
		t.Fatal(err)
	}
	if got, err := chat.SessionSandboxMode(sess.ID); err != nil || got != config.SandboxModeDangerFullAccess {
		t.Fatalf("changed mode = %q err=%v", got, err)
	}
	if got := chat.S.Store.Get().Settings.SandboxMode; got == config.SandboxModeDangerFullAccess {
		t.Fatal("session change must not rewrite the new-session default")
	}
	if got, err := chat.SessionSandboxMode(other.ID); err != nil || got != config.SandboxModeAutoApprove {
		t.Fatalf("mode leaked to another session: %q err=%v", got, err)
	}
	if err := chat.SetSessionSandboxMode(sess.ID, "nonsense", "composer"); err == nil {
		t.Fatal("unknown mode must be rejected")
	}
	if _, err := chat.SessionSandboxMode("sess_missing"); err == nil {
		t.Fatal("unknown session must be rejected")
	}

	reopened, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range reopened.Get().Sessions {
		if got.ID == sess.ID {
			if got.ApprovalPolicy != config.ApprovalPolicyNever ||
				got.SandboxProfile != config.SandboxProfileDangerFullAccess ||
				got.SandboxMode != config.SandboxModeDangerFullAccess ||
				got.SandboxNetwork != config.SandboxNetworkOff {
				t.Fatalf("mode not persisted: %+v", got)
			}
		}
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		`"phase":"session_sandbox_mode_set"`,
		`"mode":"danger-full-access"`,
		`"approval":"never"`,
		`"sandbox":"danger-full-access"`,
		`"source":"composer"`,
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("audit missing %s:\n%s", want, doc)
		}
	}
}

func TestSetSessionExecutionPolicyPersistsAndAudits(t *testing.T) {
	chat, store, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {})
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", "split", "")
	if err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	chat.SetToolAuditPath(auditPath)
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}

	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyNever,
		config.SandboxProfileWorkspaceWrite,
		"composer",
	); err != nil {
		t.Fatal(err)
	}
	got, err := chat.SessionExecutionPolicy(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovalPolicy != config.ApprovalPolicyNever ||
		got.SandboxProfile != config.SandboxProfileWorkspaceWrite {
		t.Fatalf("session execution policy = %+v", got)
	}
	stored := store.Get()
	for _, session := range stored.Sessions {
		if session.ID == sess.ID {
			if session.SandboxNetwork != config.SandboxNetworkOff {
				t.Fatalf("network preference rewritten: %+v", session)
			}
			if session.EffectiveSandboxNetwork(stored) != config.SandboxNetworkOff {
				t.Fatalf("workspace effective network = %q", session.EffectiveSandboxNetwork(stored))
			}
		}
	}
	if legacy, err := chat.SessionSandboxMode(sess.ID); err != nil || legacy != config.SandboxModeDangerFullAccess {
		t.Fatalf("legacy projection = %q err=%v", legacy, err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyRequestApproval,
		config.SandboxProfileDangerFullAccess,
		"composer",
	); err != nil {
		t.Fatal(err)
	}
	stored = store.Get()
	for _, session := range stored.Sessions {
		if session.ID == sess.ID {
			if session.SandboxNetwork != config.SandboxNetworkOff {
				t.Fatalf("danger wrote network on: %+v", session)
			}
			if session.EffectiveSandboxNetwork(stored) != config.SandboxNetworkOn {
				t.Fatalf("danger effective network = %q", session.EffectiveSandboxNetwork(stored))
			}
		}
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyRequestApproval,
		config.SandboxProfileWorkspaceWrite,
		"composer",
	); err != nil {
		t.Fatal(err)
	}
	stored = store.Get()
	for _, session := range stored.Sessions {
		if session.ID == sess.ID && session.EffectiveSandboxNetwork(stored) != config.SandboxNetworkOff {
			t.Fatalf("workspace switch did not restore persisted network: %+v", session)
		}
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		`"phase":"session_execution_policy_set"`,
		`"approval":"never"`,
		`"sandbox":"workspace-write"`,
		`"approval":"request-approval"`,
		`"sandbox":"danger-full-access"`,
		`"source":"composer"`,
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("audit missing %s:\n%s", want, doc)
		}
	}
}

func TestRequestModeConfirmsFileWrite(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"request.txt\",\"content\":\"ok\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("done") + sseDone))
	})
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionSandboxMode(sess.ID, config.SandboxModeRequestApproval, "composer"); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "write", nil); err != nil {
		t.Fatal(err)
	}
	var callID string
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if callID == "" {
		t.Fatal("request mode did not ask for confirmation")
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "request.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("write after approval = %q err=%v", data, err)
	}
}

func TestFullAccessModeSkipsConfirmation(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("call_1", "write_workspace_file", `"{\"path\":\"full.txt\",\"content\":\"ok\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("done") + sseDone))
	})
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionSandboxMode(sess.ID, config.SandboxModeDangerFullAccess, "composer"); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "write", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("full access unexpectedly asked for confirmation: %+v", ev)
		}
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "full.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("full access write = %q err=%v", data, err)
	}
}
