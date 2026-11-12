package services

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agentmate/internal/config"
	"agentmate/internal/sandbox"
)

type auditTestRecord struct {
	Phase   string         `json:"phase"`
	Tool    string         `json:"tool"`
	Payload map[string]any `json:"payload"`
}

func readAuditTestRecords(t *testing.T, path string) []auditTestRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var records []auditTestRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec auditTestRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("bad audit line: %v\n%s", err, scanner.Text())
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func escalationToolArgs(command, justification string) string {
	return strconv.Quote(string(mustJSON(map[string]any{
		"command":       command,
		"escalate":      true,
		"justification": justification,
	})))
}

func configureShellEscalationAgent(t *testing.T, store *config.Store) {
	t.Helper()
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.Terminal = testTerminalSettings()
		for i := range cfg.Agents {
			cfg.Agents[i].BuiltinTools = []string{"shell"}
			cfg.Agents[i].ConfirmTools = nil
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func firstConfirmationRequest(events []ChatStreamEvent) *ChatStreamEvent {
	for i := range events {
		if events[i].Kind == "confirm_request" {
			return &events[i]
		}
	}
	return nil
}

func TestEscalationAutoApproveApprovedRunsOutsideForOneCall(t *testing.T) {
	const justification = "构建脚本必须写入工作区外的共享缓存"
	turn := 0
	chat, store, globalWorkspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall(
				"esc_ok", "run_command",
				escalationToolArgs(policyShellCommand("../../escalated.txt"), justification),
			) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("已越界写入。") + sseDone))
	})
	configureShellEscalationAgent(t, store)
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	chat.SetToolAuditPath(auditPath)
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)

	sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", "escalation approved", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyAutoApprove,
		config.SandboxProfileWorkspaceWrite,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(globalWorkspace, "escalated.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "写工作区外的文件", nil); err != nil {
		t.Fatal(err)
	}
	req := firstConfirmationRequest(*events)
	if req == nil || req.CallID == "" {
		t.Fatalf("auto-approve escalation did not ask for confirmation: %+v", *events)
	}
	if req.ToolName != "run_command" {
		t.Fatalf("confirmation tool = %q, want run_command", req.ToolName)
	}
	if !strings.Contains(req.Hint, "脱离沙箱") || !strings.Contains(req.Hint, justification) {
		t.Fatalf("confirmation hint must call out the sandbox escape and justification: %q", req.Hint)
	}
	if escalate, _ := req.ToolArgs["escalate"].(bool); !escalate {
		t.Fatalf("confirmation lost escalate flag: %+v", req.ToolArgs)
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if string(data) != "original" {
		t.Fatalf("escalated command ran before approval: %q", data)
	}

	if err := chat.ConfirmToolCall(sess.ID, req.CallID, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if strings.TrimSpace(string(data)) != "changed" {
		t.Fatalf("approved escalation did not write outside the workspace: %q", data)
	}
	policy, err := chat.SessionExecutionPolicy(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.SandboxProfile != config.SandboxProfileWorkspaceWrite {
		t.Fatalf("one-call escalation changed the session profile to %q", policy.SandboxProfile)
	}

	records := readAuditTestRecords(t, auditPath)
	var sawCall, sawConfirm bool
	for _, rec := range records {
		switch rec.Phase {
		case "call":
			if rec.Tool != "run_command" {
				continue
			}
			if rec.Payload["escalated"] != true ||
				rec.Payload["justification"] != justification ||
				rec.Payload["command"] != policyShellCommand("../../escalated.txt") {
				t.Fatalf("escalated call audit missing fields: %+v", rec.Payload)
			}
			sawCall = true
		case "confirm":
			if rec.Tool != "adk_request_confirmation" {
				continue
			}
			if rec.Payload["decision"] != "approved" ||
				rec.Payload["escalated"] != true ||
				rec.Payload["justification"] != justification {
				t.Fatalf("escalated confirm audit missing fields: %+v", rec.Payload)
			}
			sawConfirm = true
		}
	}
	if !sawCall || !sawConfirm {
		t.Fatalf("audit missing escalation call/decision: %+v", records)
	}
}

func TestEscalationAutoApproveRejectedDoesNotExecute(t *testing.T) {
	const justification = "需要读取工作区外的构建缓存"
	turn := 0
	chat, store, globalWorkspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall(
				"esc_reject", "run_command",
				escalationToolArgs(policyShellCommand("../../rejected.txt"), justification),
			) + sseDone))
			return
		}
		body := readAll(r)
		if !strings.Contains(body, "call is rejected") {
			t.Errorf("rejected escalation missing from resume request: %s", body)
		}
		_, _ = w.Write([]byte(sseText("已拒绝。") + sseDone))
	})
	configureShellEscalationAgent(t, store)
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	chat.SetToolAuditPath(auditPath)
	if err := store.Update(func(cfg *config.Config) { cfg.Settings.ToolsAudit = true }); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)

	sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", "escalation rejected", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyAutoApprove,
		config.SandboxProfileWorkspaceWrite,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(globalWorkspace, "rejected.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "写工作区外的文件", nil); err != nil {
		t.Fatal(err)
	}
	req := firstConfirmationRequest(*events)
	if req == nil || req.CallID == "" {
		t.Fatalf("auto-approve escalation did not ask for confirmation: %+v", *events)
	}
	if err := chat.ConfirmToolCall(sess.ID, req.CallID, false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if string(data) != "original" {
		t.Fatalf("rejected escalation executed the command: %q", data)
	}

	records := readAuditTestRecords(t, auditPath)
	var sawRejected bool
	for _, rec := range records {
		if rec.Phase != "confirm" || rec.Tool != "adk_request_confirmation" {
			continue
		}
		if rec.Payload["decision"] != "rejected" ||
			rec.Payload["escalated"] != true ||
			rec.Payload["justification"] != justification {
			t.Fatalf("rejected escalation audit missing fields: %+v", rec.Payload)
		}
		sawRejected = true
	}
	if !sawRejected {
		t.Fatalf("audit missing rejected escalation decision: %+v", records)
	}
}

func TestRequestApprovalConfirmsNormalAndEscalatedWithDistinctCards(t *testing.T) {
	if !sandbox.Available() {
		t.Skipf("OS sandbox unavailable: %s", sandbox.BackendStatus().Error)
	}
	const justification = "生成产物需要写到共享目录"
	turn := 0
	chat, store, globalWorkspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall(
				"normal_1", "run_command",
				policyToolArgs(map[string]string{"command": "echo normal>normal.txt"}),
			) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseToolCall(
				"escalated_1", "run_command",
				escalationToolArgs(policyShellCommand("../../request-escalated.txt"), justification),
			) + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("两步都完成。") + sseDone))
		}
	})
	configureShellEscalationAgent(t, store)
	events := hookEvents(t)

	sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", "request approval escalation", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyRequestApproval,
		config.SandboxProfileWorkspaceWrite,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(globalWorkspace, "request-escalated.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "先普通再越界", nil); err != nil {
		t.Fatal(err)
	}
	normal := firstConfirmationRequest(*events)
	if normal == nil || normal.CallID == "" {
		t.Fatalf("request-approval normal command did not confirm: %+v", *events)
	}
	if strings.Contains(normal.Hint, "脱离沙箱") {
		t.Fatalf("normal command card must not look like an escalation: %q", normal.Hint)
	}
	if err := chat.ConfirmToolCall(sess.ID, normal.CallID, true); err != nil {
		t.Fatal(err)
	}

	var escalated *ChatStreamEvent
	for i := range *events {
		ev := &(*events)[i]
		if ev.Kind == "confirm_request" && ev.CallID != normal.CallID {
			escalated = ev
		}
	}
	if escalated == nil || escalated.CallID == "" {
		t.Fatalf("request-approval escalation did not confirm: %+v", *events)
	}
	if !strings.Contains(escalated.Hint, "脱离沙箱") || !strings.Contains(escalated.Hint, justification) {
		t.Fatalf("escalated card must name the boundary and justification: %q", escalated.Hint)
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if string(data) != "original" {
		t.Fatalf("escalated command ran before the second approval: %q", data)
	}
	if err := chat.ConfirmToolCall(sess.ID, escalated.CallID, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if strings.TrimSpace(string(data)) != "changed" {
		t.Fatalf("approved request-approval escalation did not write outside: %q", data)
	}
	if data, err := os.ReadFile(filepath.Join(sess.WorkspaceDir, "normal.txt")); err != nil || strings.TrimSpace(string(data)) != "normal" {
		t.Fatalf("normal command result missing: data=%q err=%v", data, err)
	}
}

func TestDangerFullAccessIgnoresEscalationWithoutExtraConfirmation(t *testing.T) {
	const justification = "已经完全访问"
	turn := 0
	chat, store, globalWorkspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall(
				"danger_esc", "run_command",
				escalationToolArgs(policyShellCommand("../../danger-escalated.txt"), justification),
			) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("直接执行完成。") + sseDone))
	})
	configureShellEscalationAgent(t, store)
	events := hookEvents(t)

	sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", "danger escalation", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyAutoApprove,
		config.SandboxProfileDangerFullAccess,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(globalWorkspace, "danger-escalated.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "完全访问下写文件", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("danger-full-access added a confirmation for escalate: %+v", ev)
		}
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if strings.TrimSpace(string(data)) != "changed" {
		t.Fatalf("danger-full-access did not execute: %q", data)
	}
}
