package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/sandbox"
)

func eventStatuses(events []ChatStreamEvent) []string {
	var out []string
	for _, ev := range events {
		if ev.Kind == "turn_state" {
			out = append(out, ev.TurnStatus)
		}
	}
	return out
}

func containsStatus(events []ChatStreamEvent, status string) bool {
	for _, got := range eventStatuses(events) {
		if got == status {
			return true
		}
	}
	return false
}

func TestTurnLifecycleEmitsQueuedPreparingRunningCompleted(t *testing.T) {
	chat, events, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "开始", nil); err != nil {
		t.Fatal(err)
	}
	statuses := strings.Join(eventStatuses(*events), ",")
	for _, want := range []string{turnStatusQueued, turnStatusPreparing, turnStatusRunning, turnStatusCompleted} {
		if !containsStatus(*events, want) {
			t.Fatalf("status %s missing: %s", want, statuses)
		}
	}
	if strings.Index(statuses, turnStatusQueued) > strings.Index(statuses, turnStatusPreparing) ||
		strings.Index(statuses, turnStatusPreparing) > strings.Index(statuses, turnStatusRunning) {
		t.Fatalf("state order = %s", statuses)
	}
}

func TestRepeatedToolCallLoopStops(t *testing.T) {
	var modelCalls atomic.Int64
	chat, events, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		call := modelCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseToolCall(fmt.Sprintf("repeat_%d", call), "get_current_time", `"{}"`) + sseDone))
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "反复查询时间", nil); err != nil {
		t.Fatal(err)
	}
	if modelCalls.Load() > repeatedToolCallLimit {
		t.Fatalf("模型调用未被环检测截断: %d", modelCalls.Load())
	}
	var sawLoopError, sawFailed bool
	for _, ev := range *events {
		if ev.Kind == "error" && strings.Contains(ev.Text, "重复工具调用环") {
			sawLoopError = true
		}
		if ev.Kind == "turn_state" && ev.TurnStatus == turnStatusFailed {
			sawFailed = true
		}
	}
	if !sawLoopError || !sawFailed {
		t.Fatalf("repeated tool loop did not terminate cleanly: %+v", *events)
	}
}

func TestBudgetPauseCanContinueWithoutFailed(t *testing.T) {
	var calls atomic.Int64
	chat, events, _ := newGuardrailTestChat(t, 1, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(sseToolCall("c1", "get_current_time", `"{}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("继续完成") + sseDone))
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "查询并继续", nil); err != nil {
		t.Fatal(err)
	}
	var paused *ChatStreamEvent
	for i := range *events {
		ev := &(*events)[i]
		if ev.Kind == "budget_pause" {
			paused = ev
		}
		if ev.Kind == "turn_state" && ev.TurnStatus == turnStatusFailed {
			t.Fatalf("soft budget must not fail: %+v", *events)
		}
	}
	if paused == nil || paused.Budget == nil || paused.Budget.Reason != "tool_calls" {
		t.Fatalf("budget pause missing: %+v", *events)
	}
	var sawCall, sawResult bool
	for _, ev := range *events {
		if paused != nil && ev.Seq >= paused.Seq {
			break
		}
		if ev.Kind == "tool_call" {
			sawCall = true
		}
		if ev.Kind == "tool_result" {
			sawResult = true
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("budget pause must happen after a complete tool pair: call=%v result=%v events=%+v",
			sawCall, sawResult, *events)
	}
	var turn workflowTurn
	if err := chat.db.Where("session_id = ?", sess.ID).Order("created_at DESC").First(&turn).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != turnStatusBudgetPaused {
		t.Fatalf("turn status = %s, want %s", turn.Status, turnStatusBudgetPaused)
	}
	if err := chat.ResolveBudgetPause(sess.ID, "continue"); err != nil {
		t.Fatal(err)
	}
	var latest workflowTurn
	if err := chat.db.Where("session_id = ?", sess.ID).Order("created_at DESC").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.Status != turnStatusCompleted {
		t.Fatalf("continued turn = %s", latest.Status)
	}
}

func TestToolInvocationLedgerRecordsWriteHashAndResumeSkips(t *testing.T) {
	turn := 0
	chat, store, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r)
		if strings.Contains(body, "根据下面的对话开头") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"标题"},"finish_reason":"stop"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1, 3:
			_, _ = w.Write([]byte(sseToolCall(fmt.Sprintf("c%d", turn), "write_workspace_file",
				`"{\"path\":\"ledger.txt\",\"content\":\"v\"}"`) + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("完成") + sseDone))
		}
	})
	if err := store.Update(func(cfg *config.Config) {
		cfg.Agents[0].ConfirmTools = nil
		disabled := false
		cfg.Settings.AutoTitle = &disabled
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写文件", nil); err != nil {
		t.Fatal(err)
	}
	var inv toolInvocation
	if err := chat.db.Where("session_id = ? AND tool_name = ?", sess.ID, "write_workspace_file").
		Order("created_at DESC").First(&inv).Error; err != nil {
		t.Fatal(err)
	}
	if inv.Status != "succeeded" || inv.AfterHash != sha256Hex([]byte("v")) || inv.PlannedAfterHash != inv.AfterHash {
		t.Fatalf("invocation ledger = %+v", inv)
	}
	if err := chat.db.Model(&workflowTurn{}).Where("session_id = ?", sess.ID).Update("status", turnStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	*events = nil
	if err := chat.ResumeTurn(sess.ID); err != nil {
		t.Fatal(err)
	}
	var skipped toolInvocation
	if err := chat.db.Where("session_id = ? AND status = ?", sess.ID, "skipped").
		Order("created_at DESC").First(&skipped).Error; err != nil {
		t.Fatalf("resume did not skip applied write: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(workspace, "ledger.txt")); string(data) != "v" {
		t.Fatalf("file changed after resume: %q", data)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestConfirmRequestCarriesRiskDiffPolicyAndRejectsCrossTurn(t *testing.T) {
	turn := 0
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("old", "write_workspace_file",
				`"{\"path\":\"old.txt\",\"content\":\"old\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("新回合") + sseDone))
	})
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写旧文件", nil); err != nil {
		t.Fatal(err)
	}
	var oldCall string
	var confirmation *ChatStreamEvent
	for i := range *events {
		if (*events)[i].Kind == "confirm_request" {
			confirmation = &(*events)[i]
			oldCall = confirmation.CallID
		}
	}
	if confirmation == nil || oldCall == "" {
		t.Fatalf("confirmation missing: %+v", *events)
	}
	if confirmation.Risk == "" || !strings.Contains(confirmation.Diff, "+old") || !strings.Contains(confirmation.Policy, "工作区=") {
		t.Fatalf("confirmation metadata = %+v", confirmation)
	}
	history, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var persisted bool
	for _, message := range history {
		if message.Kind == "confirm_request" && message.CallID == oldCall {
			persisted = message.Risk != "" && message.Diff != "" && message.Policy != ""
		}
	}
	if !persisted {
		t.Fatalf("confirmation metadata missing from history: %+v", history)
	}
	if err := chat.Send(sess.ID, "开始新回合", nil); err != nil {
		t.Fatal(err)
	}
	if err := chat.ConfirmToolCall(sess.ID, oldCall, true); err == nil {
		t.Fatal("old confirmation must be rejected after a new user turn")
	}
}

func TestVerificationGateBlocksCompletionAndSupportsExemption(t *testing.T) {
	turn := 0
	chat, store, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("w1", "write_workspace_file",
				`"{\"path\":\"main.go\",\"content\":\"package main\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("已完成") + sseDone))
	})
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Agents[0].BuiltinTools = []string{"files", "verify"}
		cfg.Agents[0].ConfirmTools = nil
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "实现 Go 文件", nil); err != nil {
		t.Fatal(err)
	}
	var blocked *VerificationInfo
	for _, ev := range *events {
		if ev.Kind == "verification" && ev.Verification != nil && ev.Verification.Status == "blocked" {
			blocked = ev.Verification
		}
	}
	if blocked == nil || blocked.Artifact == "" {
		t.Fatalf("verification gate did not block: %+v", *events)
	}
	if _, err := os.Stat(blocked.Artifact); err != nil {
		t.Fatalf("verification artifact missing: %v", err)
	}
	var turnRow workflowTurn
	if err := chat.db.Where("session_id = ?", sess.ID).Order("created_at DESC").First(&turnRow).Error; err != nil {
		t.Fatal(err)
	}
	if turnRow.Status != turnStatusFailed || turnRow.FinishReason != "verification_blocked" {
		t.Fatalf("turn = %+v", turnRow)
	}
	if err := chat.ExemptVerification(sess.ID, "用户确认只做示例文件"); err != nil {
		t.Fatal(err)
	}
	if err := chat.db.Where("id = ?", turnRow.ID).First(&turnRow).Error; err != nil {
		t.Fatal(err)
	}
	if turnRow.Status != turnStatusCompleted || !turnRow.VerificationExempt {
		t.Fatalf("exempt turn = %+v", turnRow)
	}
	steps, _, err := chat.readPlanState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundExemptEvidence := false
	for _, step := range steps {
		if step.Status == "completed" {
			for _, evidence := range step.Evidence {
				if strings.Contains(evidence, "用户显式豁免验证") {
					foundExemptEvidence = true
				}
			}
		}
	}
	if !foundExemptEvidence {
		t.Fatalf("exempted plan missing completion evidence: %+v", steps)
	}
}

func TestRetryStepTargetsBlockedStepWithoutTrimmingHistory(t *testing.T) {
	var prompts []string
	var responses atomic.Int64
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		prompts = append(prompts, readAll(r))
		w.Header().Set("Content-Type", "text/event-stream")
		if responses.Add(1) == 1 {
			_, _ = w.Write([]byte(sseText("初次失败") + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("步骤已重试") + sseDone))
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "做任务", nil); err != nil {
		t.Fatal(err)
	}
	before, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.savePlanState(sess.ID, []agentkit.PlanStep{
		{ID: "done", Content: "已完成步骤", Status: "completed", Evidence: []string{"evidence.txt"}},
		{ID: "bad", Content: "失败步骤", Status: "blocked", Acceptance: []string{"测试通过"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := chat.db.Model(&workflowTurn{}).Where("session_id = ?", sess.ID).
		Updates(map[string]any{"status": turnStatusFailed, "finish_reason": "verification_blocked"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := chat.RetryStep(sess.ID, "bad"); err != nil {
		t.Fatal(err)
	}
	after, err := chat.GetSessionMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) <= len(before) {
		t.Fatalf("retry trimmed history: before=%d after=%d", len(before), len(after))
	}
	if len(prompts) < 2 || !strings.Contains(prompts[len(prompts)-1], "只重试计划步骤 bad") {
		t.Fatalf("retry prompt missing target: %+v", prompts)
	}
	var steps []agentkit.PlanStep
	steps, _, err = chat.readPlanState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[1].Status != "in_progress" {
		t.Fatalf("plan after retry = %+v", steps)
	}
}

func TestWorkflowAnchorsKeepPlanApprovalEvidenceAndToolPairs(t *testing.T) {
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.savePlanState(sess.ID, []agentkit.PlanStep{
		{ID: "one", Content: "保留当前计划", Status: "in_progress", Acceptance: []string{"计划可见"}},
		{ID: "two", Content: "有证据的步骤", Status: "completed", Evidence: []string{"artifact:test.log"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := chat.db.Create(&workflowApproval{
		ID: "call_pending", TurnID: "turn_pending", SessionID: sess.ID,
		ToolName: "run_command", Hint: "需要确认", Status: "pending", CreatedAt: nowUTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := chat.db.Create(&toolInvocation{
		ID: "call_done", SessionID: sess.ID, TurnID: "turn_pending",
		ToolName: "write_workspace_file", Status: "succeeded",
		BeforeHash: "before", AfterHash: "after", ArgsHash: "args", CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	resp, err := chat.sessions.Get(t.Context(), &session.GetRequest{AppName: chat.appName, UserID: chat.userID, SessionID: sess.ID})
	if err != nil {
		t.Fatal(err)
	}
	evCall := &session.Event{
		ID: "ev_call", Author: "agent",
		LLMResponse: adkmodel.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{
			FunctionCall: &genai.FunctionCall{ID: "pair_1", Name: "read_workspace_file", Args: map[string]any{"path": "notes.md"}},
		}}}},
	}
	evResult := &session.Event{
		ID: "ev_result", Author: "agent",
		LLMResponse: adkmodel.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{
			FunctionResponse: &genai.FunctionResponse{ID: "pair_1", Name: "read_workspace_file", Response: map[string]any{"ok": true, "text": "资料"}},
		}}}},
	}
	if err := chat.sessions.AppendEvent(t.Context(), resp.Session, evCall); err != nil {
		t.Fatal(err)
	}
	if err := chat.sessions.AppendEvent(t.Context(), resp.Session, evResult); err != nil {
		t.Fatal(err)
	}
	anchors := chat.buildWorkflowAnchors(sess.ID)
	for _, want := range []string{
		"保留当前计划", "artifact:test.log", "call_pending", "run_command",
		"最近副作用证据", "call pair_1", "<untrusted_data>", "notes.md",
	} {
		if !strings.Contains(anchors, want) {
			t.Fatalf("anchors missing %q:\n%s", want, anchors)
		}
	}
}

func TestAuditRedactsSecretsBeforeWrite(t *testing.T) {
	secret := "sk-" + strings.Repeat("a", 24)
	turn := 0
	chat, store, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			args := fmt.Sprintf(`"{\"path\":\"secret.txt\",\"content\":\"%s\"}"`, secret)
			_, _ = w.Write([]byte(sseToolCall("s1", "write_workspace_file", args) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("done") + sseDone))
	})
	if err := store.Update(func(cfg *config.Config) {
		disabled := false
		cfg.Settings.AutoTitle = &disabled
		cfg.Settings.ToolsAudit = true
		cfg.Agents[0].ConfirmTools = nil
	}); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	chat.SetToolAuditPath(auditPath)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写一个文件", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("audit leaked secret:\n%s", data)
	}
}

func TestRecoveryLedgerSkipsAppliedWriteAndConfirmsConflictAndShell(t *testing.T) {
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	oldTurn := workflowTurn{
		ID: "turn_old", SessionID: "sess_recovery", Status: turnStatusInterrupted,
		Workspace: t.TempDir(), CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	newTurn := workflowTurn{
		ID: "turn_new", SessionID: "sess_recovery", Status: turnStatusRunning,
		ParentTurnID: oldTurn.ID, Workspace: oldTurn.Workspace,
		CreatedAt: nowUTC().Add(1), UpdatedAt: nowUTC().Add(1),
	}
	if err := chat.db.Create(&oldTurn).Error; err != nil {
		t.Fatal(err)
	}
	if err := chat.db.Create(&newTurn).Error; err != nil {
		t.Fatal(err)
	}
	base := agentkit.ToolInvocationBegin{
		SessionID: "sess_recovery", TurnID: newTurn.ID, CallID: "call_new",
		ToolName: "write_workspace_file", Source: "builtin",
		Effects: []agentkit.Effect{agentkit.EffectWriteWorkspace},
		Args:    map[string]any{"path": "a.txt", "content": "v"}, ArgsHash: "same-args",
		BeforeHash: "after",
	}
	if err := chat.db.Create(&toolInvocation{
		ID: "call_old", SessionID: "sess_recovery", TurnID: oldTurn.ID,
		ToolName: "write_workspace_file", Source: "builtin", Status: "succeeded",
		ArgsHash: "same-args", BeforeHash: "before", AfterHash: "after",
		PlannedAfterHash: "after", ResultJSON: `{"ok":true,"sha256":"after"}`,
		CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	decision, err := chat.BeginToolInvocation(base)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Skip || decision.Result["code"] != "already_applied" {
		t.Fatalf("applied write decision = %+v", decision)
	}

	conflict := base
	conflict.CallID = "call_conflict"
	conflict.ArgsHash = "conflict-args"
	conflict.BeforeHash = "external"
	if err := chat.db.Create(&toolInvocation{
		ID: "call_old_conflict", SessionID: "sess_recovery", TurnID: oldTurn.ID,
		ToolName: "write_workspace_file", Status: "started",
		ArgsHash: "conflict-args", BeforeHash: "before", PlannedAfterHash: "planned",
		CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	decision, err = chat.BeginToolInvocation(conflict)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.NeedConfirmation || !strings.Contains(decision.ConfirmationHint, "不一致") {
		t.Fatalf("conflict decision = %+v", decision)
	}

	shell := base
	shell.CallID = "call_shell"
	shell.ToolName = "run_command"
	shell.ArgsHash = "shell-args"
	shell.Effects = []agentkit.Effect{agentkit.EffectCommand}
	if err := chat.db.Create(&toolInvocation{
		ID: "call_old_shell", SessionID: "sess_recovery", TurnID: oldTurn.ID,
		ToolName: "run_command", Status: "started", ArgsHash: "shell-args",
		CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	decision, err = chat.BeginToolInvocation(shell)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.NeedConfirmation || !strings.Contains(decision.ConfirmationHint, "外部 MCP") && !strings.Contains(decision.ConfirmationHint, "命令") {
		t.Fatalf("shell resume decision = %+v", decision)
	}
}

func TestCancelledAndPreparationFailuresEmitTerminalTurnState(t *testing.T) {
	chat, events, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- chat.Send(sess.ID, "慢任务", nil) }()
	deadline := time.Now().Add(2 * time.Second)
	for !chat.IsRunning(sess.ID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	chat.Cancel(sess.ID)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled run did not return")
	}
	if !containsStatus(*events, turnStatusCancelled) {
		t.Fatalf("cancelled state missing: %+v", *events)
	}
	var lastDone int
	for _, ev := range *events {
		if ev.Kind == "done" {
			lastDone++
		}
	}
	if lastDone == 0 {
		t.Fatalf("cancelled run missing done: %+v", *events)
	}

	*events = nil
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		cfg.Agents = nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "准备失败", nil); err == nil {
		t.Fatal("expected preparation failure")
	}
	if !containsStatus(*events, turnStatusFailed) {
		t.Fatalf("failed state missing: %+v", *events)
	}
}

func TestApprovalQueueListsPendingBulkConfirmsAndGrantsScope(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("queued_call", "write_workspace_file",
				`"{\"path\":\"queued.txt\",\"content\":\"ok\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("已批准") + sseDone))
	})
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写文件", nil); err != nil {
		t.Fatal(err)
	}
	queue, err := chat.ListApprovals(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 1 || queue[0].Risk == "" || queue[0].Diff == "" || queue[0].PolicyJSON == "" {
		t.Fatalf("approval queue = %+v", queue)
	}
	if err := chat.GrantApprovalScope(sess.ID, queue[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := chat.ConfirmApprovals(sess.ID, []string{queue[0].ID}, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "queued.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("approved write = %q, %v", data, err)
	}
	if !containsStatus(*events, turnStatusCompleted) {
		t.Fatalf("approved turn did not complete: %+v", *events)
	}
}

func TestApprovalScopeRejectsHighRiskAndMatchesToolRisk(t *testing.T) {
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("unused") + sseDone))
	})
	turn := workflowTurn{
		ID: "turn_scope", SessionID: "sess_scope", Status: turnStatusWaitingApproval,
		Workspace: t.TempDir(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := chat.db.Create(&turn).Error; err != nil {
		t.Fatal(err)
	}
	high := workflowApproval{
		ID: "call_high", TurnID: turn.ID, SessionID: turn.SessionID,
		ToolName: "run_command", Risk: "high", Status: "pending", CreatedAt: time.Now().UTC(),
	}
	if err := chat.db.Create(&high).Error; err != nil {
		t.Fatal(err)
	}
	if err := chat.GrantApprovalScope(turn.SessionID, high.ID); err == nil {
		t.Fatal("high-risk approval scope must be rejected")
	}
	medium := workflowApproval{
		ID: "call_medium", TurnID: turn.ID, SessionID: turn.SessionID,
		ToolName: "write_workspace_file", Risk: "medium", Status: "pending", CreatedAt: time.Now().UTC(),
	}
	if err := chat.db.Create(&medium).Error; err != nil {
		t.Fatal(err)
	}
	if err := chat.GrantApprovalScope(turn.SessionID, medium.ID); err != nil {
		t.Fatal(err)
	}
	if !chat.approvalGranted(turn.SessionID, "write_workspace_file", "medium", turn.Workspace) {
		t.Fatal("matching tool and risk should be granted")
	}
	if chat.approvalGranted(turn.SessionID, "edit_workspace_file", "medium", turn.Workspace) {
		t.Fatal("different tool must not be auto-approved")
	}
	if chat.approvalGranted(turn.SessionID, "write_workspace_file", "high", turn.Workspace) {
		t.Fatal("different risk must not be auto-approved")
	}
}

func TestSupersededApprovalEmitsTerminalState(t *testing.T) {
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("unused") + sseDone))
	})
	events := hookEvents(t)
	const sessionID = "sess_supersede"
	turn := workflowTurn{
		ID: "turn_supersede", SessionID: sessionID, Status: turnStatusWaitingApproval,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := chat.db.Create(&turn).Error; err != nil {
		t.Fatal(err)
	}
	if err := chat.db.Create(&workflowApproval{
		ID: "call_supersede", TurnID: turn.ID, SessionID: sessionID,
		ToolName: "write_workspace_file", Risk: "medium", Status: "pending",
		CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	chat.supersedePendingApprovals(sessionID)
	var cancelled, done bool
	for _, ev := range *events {
		if ev.TurnID != turn.ID {
			continue
		}
		if ev.Kind == "turn_state" && ev.TurnStatus == turnStatusCancelled {
			cancelled = true
		}
		if ev.Kind == "done" {
			done = true
		}
	}
	if !cancelled || !done {
		t.Fatalf("superseded turn did not emit terminal events: %+v", *events)
	}
}

func TestResumeWriteDecisionUsesCurrentHashToSkipOrConflict(t *testing.T) {
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("unused") + sseDone))
	})
	now := time.Now().UTC()
	parent := workflowTurn{
		ID: "turn_parent", SessionID: "sess_hash", Status: turnStatusFailed,
		CreatedAt: now, UpdatedAt: now,
	}
	child := workflowTurn{
		ID: "turn_child", SessionID: parent.SessionID, ParentTurnID: parent.ID,
		Status: turnStatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	for _, turn := range []workflowTurn{parent, child} {
		if err := chat.db.Create(&turn).Error; err != nil {
			t.Fatal(err)
		}
	}
	prior := toolInvocation{
		ID: "inv_hash", SessionID: parent.SessionID, TurnID: parent.ID,
		ToolName: "write_workspace_file", ArgsHash: "args-hash", Status: "succeeded",
		BeforeHash: "old-hash", AfterHash: "new-hash", PlannedAfterHash: "new-hash",
		ResultJSON: `{"ok":true,"sha256":"new-hash"}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := chat.db.Create(&prior).Error; err != nil {
		t.Fatal(err)
	}
	req := agentkit.ToolInvocationBegin{
		SessionID: parent.SessionID, TurnID: child.ID, ToolName: prior.ToolName,
		ArgsHash: prior.ArgsHash, BeforeHash: "new-hash",
	}
	decision, handled, err := chat.resumeInvocationDecision(req)
	if err != nil || !handled || !decision.Skip {
		t.Fatalf("applied write decision = %+v handled=%v err=%v", decision, handled, err)
	}

	req.BeforeHash = "old-hash"
	decision, handled, err = chat.resumeInvocationDecision(req)
	if err != nil || handled || decision.Skip {
		t.Fatalf("reverted write should be rerunnable: %+v handled=%v err=%v", decision, handled, err)
	}

	req.BeforeHash = "external-hash"
	decision, handled, err = chat.resumeInvocationDecision(req)
	if err != nil || !handled || !decision.NeedConfirmation {
		t.Fatalf("external write should require confirmation: %+v handled=%v err=%v", decision, handled, err)
	}
}

func TestCheckpointSideEffectSummaryIsCompactAndOmitsResults(t *testing.T) {
	chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("unused") + sseDone))
	})
	turnID, sessionID := "turn_checkpoint", "sess_checkpoint"
	now := time.Now().UTC()
	latest := toolInvocation{
		ID: "call_latest", SessionID: sessionID, TurnID: turnID,
		ToolName: "write_workspace_file", Source: "builtin", Status: "succeeded",
		BeforeHash: "before-hash", AfterHash: "after-hash", PlannedAfterHash: "after-hash",
		ArgsHash: "args-hash", ResultJSON: `{"text":"sk-` + strings.Repeat("x", 24) + `"}`,
		Artifact: "artifact.json", ResultBytes: 100 * 1024, CreatedAt: now, UpdatedAt: now,
	}
	if err := chat.db.Create(&latest).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		if err := chat.db.Create(&toolInvocation{
			ID: fmt.Sprintf("call_old_%02d", i), SessionID: sessionID, TurnID: turnID,
			ToolName: "shell", Status: "succeeded", ResultJSON: strings.Repeat("x", 2000),
			CreatedAt: now.Add(-time.Duration(i+1) * time.Minute), UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	got := chat.sideEffectsSummaryJSON(turnID, 100, 12*1024)
	if len(got) > 12*1024 {
		t.Fatalf("checkpoint summary = %d bytes, want <= 12KB", len(got))
	}
	for _, leaked := range []string{"ResultJSON", "sk-", strings.Repeat("x", 64)} {
		if strings.Contains(got, leaked) {
			t.Fatalf("checkpoint leaked %q: %s", leaked, got)
		}
	}
	for _, want := range []string{"call_latest", "write_workspace_file", "after-hash", "artifact.json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %s", want, got)
		}
	}
}

func TestApprovalArgsDiffAndHintAreSanitizedAtPersistence(t *testing.T) {
	chat, _, _, workspace := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("unused") + sseDone))
	})
	turn := workflowTurn{
		ID: "turn_secret_approval", SessionID: "sess_secret_approval",
		Status: turnStatusWaitingApproval, Workspace: workspace,
		CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
	}
	if err := chat.db.Create(&turn).Error; err != nil {
		t.Fatal(err)
	}
	secret := "sk-" + strings.Repeat("s", 28)
	chat.recordApproval(turn.ID, turn.SessionID, "call_secret_approval", "write_workspace_file",
		map[string]any{"path": "secret.txt", "content": secret}, "写入 "+secret)
	var approval workflowApproval
	if err := chat.db.Where("id = ?", "call_secret_approval").First(&approval).Error; err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{approval.ArgsJSON, approval.Hint, approval.Diff, approval.PolicyJSON} {
		if strings.Contains(field, secret) {
			t.Fatalf("approval persistence leaked secret: %s", field)
		}
	}
}

func TestVerifyWorkSuccessMarksVerifyingAndCompletes(t *testing.T) {
	if !sandbox.Available() {
		t.Skipf("OS sandbox unavailable: %s", sandbox.BackendStatus().Error)
	}
	turn := 0
	chat, store, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			args := `"{\"commands\":[\"echo ok\"]}"`
			_, _ = w.Write([]byte(sseToolCall("v1", "verify_work", args) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("验证通过") + sseDone))
	})
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Agents[0].BuiltinTools = []string{"files", "verify"}
		cfg.Agents[0].ConfirmTools = nil
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "验证项目", nil); err != nil {
		t.Fatal(err)
	}
	if !containsStatus(*events, turnStatusVerifying) {
		t.Fatalf("verifying state missing: %+v", *events)
	}
	var passed bool
	for _, ev := range *events {
		if ev.Kind == "verification" && ev.Verification != nil && ev.Verification.OK {
			passed = true
		}
	}
	if !passed || !containsStatus(*events, turnStatusCompleted) {
		t.Fatalf("verification success not completed: %+v", *events)
	}
}

func TestTwoFailedVerificationRoundsBlockPlan(t *testing.T) {
	turn := 0
	chat, store, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn <= 2 {
			args := `"{\"commands\":[\"exit 1\"]}"`
			_, _ = w.Write([]byte(sseToolCall(fmt.Sprintf("v%d", turn), "verify_work", args) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("修复后仍失败") + sseDone))
	})
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Agents[0].BuiltinTools = []string{"verify"}
		cfg.Agents[0].ConfirmTools = nil
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "运行修复验证", nil); err != nil {
		t.Fatal(err)
	}
	var blocked bool
	for _, ev := range *events {
		if ev.Kind == "verification" && ev.Verification != nil && ev.Verification.Status == "blocked" {
			blocked = true
		}
	}
	if !blocked || !containsStatus(*events, turnStatusFailed) {
		t.Fatalf("two failed rounds did not block: %+v", *events)
	}
	steps, _, err := chat.readPlanState(sess.ID)
	if err != nil || len(steps) == 0 || steps[0].Status != "blocked" {
		t.Fatalf("plan not blocked: %+v, %v", steps, err)
	}
}

func TestVerificationStopsBeforeThirdAttempt(t *testing.T) {
	turn := 0
	chat, store, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		args := `"{\"commands\":[\"exit 1\"]}"`
		_, _ = w.Write([]byte(sseToolCall(fmt.Sprintf("v%d", turn), "verify_work", args) + sseDone))
	})
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Agents[0].BuiltinTools = []string{"verify"}
		cfg.Agents[0].ConfirmTools = nil
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "持续验证", nil); err != nil {
		t.Fatal(err)
	}
	verifyResults := 0
	sawBlocked := false
	for _, ev := range *events {
		if ev.Kind == "tool_result" && ev.ToolName == "verify_work" {
			verifyResults++
		}
		if ev.Kind == "verification" && ev.Verification != nil && ev.Verification.Status == "blocked" {
			sawBlocked = true
		}
	}
	if verifyResults != 2 || !sawBlocked {
		t.Fatalf("verification attempted beyond the hard cap: results=%d blocked=%v events=%+v",
			verifyResults, sawBlocked, *events)
	}
}

func TestParallelConfirmationsRemainPendingUntilEachResolved(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(
				`data: {"choices":[{"delta":{"tool_calls":[` +
					`{"index":0,"id":"p1","function":{"name":"write_workspace_file","arguments":"{\"path\":\"one.txt\",\"content\":\"1\"}"}},` +
					`{"index":1,"id":"p2","function":{"name":"write_workspace_file","arguments":"{\"path\":\"two.txt\",\"content\":\"2\"}"}}` +
					`]}}]}` + "\n\n" + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("全部完成") + sseDone))
	})
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "并行写两个文件", nil); err != nil {
		t.Fatal(err)
	}
	var pending []string
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			pending = append(pending, ev.CallID)
		}
	}
	if len(pending) < 2 {
		var queue []workflowApproval
		queue, _ = chat.ListApprovals(sess.ID)
		t.Fatalf("parallel confirmations = %v, queue=%+v events=%+v", pending, queue, *events)
	}
	if err := chat.ConfirmApprovals(sess.ID, pending, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		if _, err := os.Stat(filepath.Join(workspace, name)); err != nil {
			t.Fatalf("%s missing after batch approval: %v", name, err)
		}
	}
}

func TestTurnTracePersistsModelToolAndStateSteps(t *testing.T) {
	turn := 0
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("t1", "get_current_time", `"{}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
	})
	if err := chat.S.Store.Update(func(cfg *config.Config) {
		disabled := false
		cfg.Settings.AutoTitle = &disabled
	}); err != nil {
		t.Fatal(err)
	}
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "查询", nil); err != nil {
		t.Fatal(err)
	}
	var latest workflowTurn
	if err := chat.db.Where("session_id = ?", sess.ID).Order("created_at DESC").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	traces, err := chat.ListTurnTraces(sess.ID, latest.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	toolFinished := false
	for _, trace := range traces {
		kinds[trace.Kind] = true
		if trace.Kind == "tool" && trace.CallID == "t1" && trace.Status == "succeeded" {
			toolFinished = true
		}
	}
	for _, want := range []string{"state", "model", "tool"} {
		if !kinds[want] {
			t.Fatalf("trace kind %s missing: %+v", want, traces)
		}
	}
	if !toolFinished {
		t.Fatalf("tool trace did not finish: %+v", traces)
	}

	disabled := false
	if err := chat.S.Store.Update(func(cfg *config.Config) { cfg.Settings.TraceEnabled = &disabled }); err != nil {
		t.Fatal(err)
	}
	before := len(traces)
	if err := chat.Send(sess.ID, "第二回合", nil); err != nil {
		t.Fatal(err)
	}
	traces, err = chat.ListTurnTraces(sess.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != before {
		t.Fatalf("trace disabled but wrote new rows: before=%d after=%d", before, len(traces))
	}
}

func TestHITLResumeUsesSnapshotAfterAgentDeletion(t *testing.T) {
	turn := 0
	chat, store, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall("snap1", "write_workspace_file",
				`"{\"path\":\"snapshot.txt\",\"content\":\"kept\"}"`) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("快照恢复成功") + sseDone))
	})
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "写快照文件", nil); err != nil {
		t.Fatal(err)
	}
	var callID string
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			callID = ev.CallID
		}
	}
	if callID == "" {
		t.Fatal("confirmation missing")
	}
	if err := store.Update(func(cfg *config.Config) { cfg.Agents = nil }); err != nil {
		t.Fatal(err)
	}
	if err := chat.ConfirmToolCall(sess.ID, callID, true); err != nil {
		t.Fatalf("resume after agent deletion failed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "snapshot.txt")); err != nil || string(data) != "kept" {
		t.Fatalf("snapshot write = %q, %v", data, err)
	}
}

func TestEmptyModelResponseFailsTurnWithTerminalState(t *testing.T) {
	chat, events, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseDone))
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = chat.Send(sess.ID, "空回答", nil)
	var sawError bool
	for _, ev := range *events {
		if ev.Kind == "error" && (strings.Contains(ev.Text, "没有返回最终回答") || strings.Contains(ev.Text, "空响应")) {
			sawError = true
		}
	}
	if !sawError || !containsStatus(*events, turnStatusFailed) {
		t.Fatalf("empty response was not failed: %+v", *events)
	}
}

func TestSavePlanPersistsDependenciesAcceptanceAndEvidence(t *testing.T) {
	chat, _, _ := newGuardrailTestChat(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseText("ok") + sseDone))
	})
	sess, err := chat.newLegacySession("agent", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	version, err := chat.SavePlan(sess.ID, []PlanStepView{
		{ID: "a", Content: "准备", Status: "completed", Acceptance: []string{"资料齐全"}, Evidence: []string{"file:notes.md"}},
		{ID: "b", Content: "实现", Status: "in_progress", DependsOn: []string{"a"}, Acceptance: []string{"测试通过"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if version == "" {
		t.Fatal("missing plan version")
	}
	steps, gotVersion, err := chat.readPlanState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotVersion != version || len(steps) != 2 || steps[1].DependsOn[0] != "a" ||
		steps[0].Acceptance[0] != "资料齐全" || steps[0].Evidence[0] != "file:notes.md" {
		t.Fatalf("saved plan = %+v version=%s want=%s", steps, gotVersion, version)
	}
	if _, err := chat.SavePlan(sess.ID, []PlanStepView{
		{ID: "x", Content: "无证据完成", Status: "completed"},
	}); err == nil {
		t.Fatal("completed without evidence must be rejected")
	}
}
