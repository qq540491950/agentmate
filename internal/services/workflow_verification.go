package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/agentkit"
)

func codeTaskNeedsVerification(workspace string, mutating, planCompleted, verifyOK bool, verifyFailures int, exempt bool) bool {
	if verifyOK || exempt {
		return false
	}
	if verifyFailures >= 2 {
		return true
	}
	return agentkit.IsCodeWorkspace(workspace) && (mutating || planCompleted)
}

func (c *ChatService) verificationExempt(turnID string) bool {
	turn := c.turnByID(turnID)
	return turn != nil && turn.VerificationExempt
}

func (c *ChatService) writeVerificationArtifact(turnID, callID, output string) string {
	if c == nil || c.S == nil || c.S.Store == nil {
		return ""
	}
	dir := filepath.Join(c.S.Store.Dir(), "tool-results")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	safeTurn := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(turnID)
	safeCall := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(callID)
	full := filepath.Join(dir, fmt.Sprintf("verify-%s-%s.log", safeTurn, safeCall))
	if err := os.WriteFile(full, []byte(agentkit.SanitizeSecrets(output)), 0o600); err != nil {
		return ""
	}
	return full
}

func verificationOutput(resp map[string]any) string {
	if len(resp) == 0 {
		return ""
	}
	var sb strings.Builder
	results, _ := resp["results"].([]any)
	for _, raw := range results {
		item, _ := raw.(map[string]any)
		command, _ := item["command"].(string)
		exitCode, _ := item["exitCode"].(float64)
		output, _ := item["output"].(string)
		sb.WriteString(fmt.Sprintf("$ %s\n(exit %d)\n%s\n", command, int(exitCode), output))
	}
	if sb.Len() == 0 {
		data, _ := json.Marshal(resp)
		return string(data)
	}
	return sb.String()
}

func planUpdateFromResponse(resp map[string]any) *PlanUpdate {
	if resp == nil {
		return nil
	}
	raw, ok := resp["todos"]
	if !ok {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var steps []PlanStepView
	if json.Unmarshal(data, &steps) != nil {
		return nil
	}
	if steps == nil {
		steps = []PlanStepView{}
	}
	return &PlanUpdate{Version: hashBytes(data), Steps: steps}
}

func (c *ChatService) readPlanState(sessionID string) ([]agentkit.PlanStep, string, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return nil, "", err
	}
	if resp.Session == nil || resp.Session.State() == nil {
		return nil, "", fmt.Errorf("会话状态不可用")
	}
	value, err := resp.Session.State().Get(agentkit.TodosStateKey)
	if err != nil {
		return nil, "", nil
	}
	data, _ := json.Marshal(value)
	var steps []agentkit.PlanStep
	if json.Unmarshal(data, &steps) != nil {
		return nil, "", nil
	}
	normalized, _ := json.Marshal(steps)
	return steps, hashBytes(normalized), nil
}

func (c *ChatService) savePlanState(sessionID string, steps []agentkit.PlanStep) (string, error) {
	if err := agentkit.ValidatePlanSteps(steps); err != nil {
		return "", err
	}
	for _, step := range steps {
		if step.Status == "completed" && len(step.Evidence) == 0 {
			return "", fmt.Errorf("步骤「%s」标记 completed 前必须提供 evidence", step.Content)
		}
	}
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return "", err
	}
	if resp.Session == nil {
		return "", fmt.Errorf("会话 %q 不存在", sessionID)
	}
	ev := &session.Event{
		ID: newID("ev_plan_"), Author: "system", Timestamp: time.Now().UTC(),
		Actions: session.EventActions{StateDelta: map[string]any{agentkit.TodosStateKey: steps}},
	}
	if err := c.sessions.AppendEvent(context.Background(), resp.Session, ev); err != nil {
		return "", err
	}
	data, _ := json.Marshal(steps)
	version := hashBytes(data)
	c.emitPlanUpdate(sessionID, steps, version)
	return version, nil
}

func (c *ChatService) emitPlanUpdate(sessionID string, steps []agentkit.PlanStep, version string) {
	views := make([]PlanStepView, 0, len(steps))
	for _, step := range steps {
		views = append(views, PlanStepView{
			ID: step.ID, Content: step.Content, Status: step.Status,
			DependsOn:  append([]string(nil), step.DependsOn...),
			Acceptance: append([]string(nil), step.Acceptance...),
			Evidence:   append([]string(nil), step.Evidence...),
		})
	}
	c.emitChat(sessionID, ChatStreamEvent{Kind: "plan", Plan: &PlanUpdate{Version: version, Steps: views}})
}

func (c *ChatService) markVerificationBlocked(sessionID, turnID, reason, output string) {
	steps, _, err := c.readPlanState(sessionID)
	if err != nil {
		return
	}
	if len(steps) == 0 {
		steps = []agentkit.PlanStep{{
			ID: "verification", Content: "验证工作区变更", Status: "blocked",
			Acceptance: []string{"测试/构建通过，或用户显式豁免"},
		}}
	} else {
		blocked := false
		for i := range steps {
			if steps[i].Status == "in_progress" || steps[i].Status == "failed" {
				steps[i].Status = "blocked"
				blocked = true
				break
			}
		}
		if !blocked {
			for i := range steps {
				if steps[i].Status != "completed" && steps[i].Status != "skipped" {
					steps[i].Status = "blocked"
					blocked = true
					break
				}
			}
		}
	}
	for i := range steps {
		if steps[i].Status == "blocked" {
			evidence := reason
			if strings.TrimSpace(output) != "" {
				evidence += "；输出：" + truncateRunes(output, 500)
			}
			steps[i].Evidence = append(steps[i].Evidence, evidence)
			break
		}
	}
	hasBlocked := false
	for _, step := range steps {
		if step.Status == "blocked" {
			hasBlocked = true
			break
		}
	}
	if !hasBlocked {
		steps = append(steps, agentkit.PlanStep{
			ID: "verification-blocked", Content: "验证失败，需要用户介入", Status: "blocked",
			Acceptance: []string{"修复后 verify_work 通过，或用户显式豁免"},
			Evidence:   []string{reason},
		})
	}
	if _, err := c.savePlanState(sessionID, steps); err != nil {
		return
	}
	c.recordTrace(workflowTrace{
		SessionID: sessionID, TurnID: turnID, Kind: "verification", Status: "blocked",
		ErrorCode: "verification_blocked", ErrorMessage: reason,
		DataJSON: sanitizedJSON(map[string]any{"output": truncateRunes(output, 4000)}),
	})
}

// SavePlan 允许用户在计划编辑器中修改依赖、验收标准和证据。
func (c *ChatService) SavePlan(sessionID string, steps []PlanStepView) (string, error) {
	converted := make([]agentkit.PlanStep, 0, len(steps))
	for _, step := range steps {
		converted = append(converted, agentkit.PlanStep{
			ID: step.ID, Content: step.Content, Status: step.Status,
			DependsOn:  append([]string(nil), step.DependsOn...),
			Acceptance: append([]string(nil), step.Acceptance...),
			Evidence:   append([]string(nil), step.Evidence...),
		})
	}
	return c.savePlanState(sessionID, converted)
}

// ExemptVerification 记录用户显式豁免；只允许对验证门禁阻塞的最新回合使用。
func (c *ChatService) ExemptVerification(sessionID, reason string) error {
	if c.db == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	var turn workflowTurn
	if err := c.db.Where("session_id = ? AND finish_reason = ?", sessionID, "verification_blocked").
		Order("created_at DESC").First(&turn).Error; err != nil {
		return fmt.Errorf("没有等待验证豁免的回合")
	}
	reason = strings.TrimSpace(agentkit.SanitizeSecrets(reason))
	if reason == "" {
		reason = "用户显式豁免验证"
	}
	if steps, _, err := c.readPlanState(sessionID); err == nil {
		changed := false
		for i := range steps {
			if steps[i].Status != "blocked" {
				continue
			}
			steps[i].Status = "completed"
			steps[i].Evidence = append(steps[i].Evidence, "用户显式豁免验证："+reason)
			changed = true
		}
		if changed {
			_, _ = c.savePlanState(sessionID, steps)
		}
	}
	now := time.Now().UTC()
	err := c.db.Model(&workflowTurn{}).Where("id = ?", turn.ID).Updates(map[string]any{
		"status": turnStatusCompleted, "verification_exempt": true,
		"finish_reason": "verification_exempt", "ended_at": &now,
		"error_message": "", "updated_at": now,
	}).Error
	if err != nil {
		return err
	}
	c.recordTrace(workflowTrace{
		SessionID: sessionID, TurnID: turn.ID, Kind: "verification", Status: "exempt",
		DataJSON: sanitizedJSON(map[string]any{"reason": reason}),
	})
	c.emitChat(sessionID, ChatStreamEvent{Kind: "verification", TurnID: turn.ID, Verification: &VerificationInfo{
		OK: true, Status: "exempt", Exempt: true, Reason: reason,
	}})
	c.emitChat(sessionID, ChatStreamEvent{Kind: "turn_state", TurnID: turn.ID, TurnStatus: turnStatusCompleted})
	c.emitChat(sessionID, ChatStreamEvent{Kind: "done", TurnID: turn.ID})
	return nil
}
