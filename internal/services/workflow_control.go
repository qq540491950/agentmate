package services

import (
	"fmt"
	"strings"
	"time"
)

// ResolveBudgetPause 处理软预算暂停：continue 从检查点继续，stop 标记取消。
// 两条路径都不会把预算暂停直接当成 failed。
func (c *ChatService) ResolveBudgetPause(sessionID, decision string) error {
	if c.db == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "continue" && decision != "stop" {
		return fmt.Errorf("无效的预算决定：%s", decision)
	}
	var turn workflowTurn
	if err := c.db.Where("session_id = ? AND status = ?", sessionID, turnStatusBudgetPaused).
		Order("created_at DESC").First(&turn).Error; err != nil {
		return fmt.Errorf("没有等待处理的预算暂停")
	}
	if decision == "stop" {
		now := time.Now().UTC()
		if err := c.db.Model(&workflowTurn{}).Where("id = ?", turn.ID).Updates(map[string]any{
			"status": turnStatusCancelled, "finish_reason": "budget_stopped",
			"ended_at": &now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		c.recordTrace(workflowTrace{
			SessionID: sessionID, TurnID: turn.ID, Kind: "budget", Status: "stopped",
			DataJSON: sanitizedJSON(map[string]any{"decision": "stop"}),
		})
		c.emitChat(sessionID, ChatStreamEvent{Kind: "turn_state", TurnID: turn.ID, TurnStatus: turnStatusCancelled})
		c.emitChat(sessionID, ChatStreamEvent{Kind: "done", TurnID: turn.ID})
		return nil
	}
	// 继续使用原回合清单；runTurn 会为新回合设置 BudgetOverride，避免同一
	// 会话 token 阈值立即再次暂停。
	return c.resumeTurnFrom(sessionID, &turn, "用户选择继续本轮预算。允许本次越过软阈值，但仍保留硬安全和验证门禁。")
}

// AbandonTurn 放弃恢复半完成回合，但保留工作区文件与副作用台账供查看。
func (c *ChatService) AbandonTurn(sessionID string) error {
	if c.db == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	var turn workflowTurn
	if err := c.db.Where("session_id = ? AND status IN ?", sessionID,
		[]string{turnStatusInterrupted, turnStatusFailed, turnStatusCancelled, turnStatusBudgetPaused}).
		Order("created_at DESC").First(&turn).Error; err != nil {
		return fmt.Errorf("没有可放弃的回合")
	}
	now := time.Now().UTC()
	if err := c.db.Model(&workflowTurn{}).Where("id = ?", turn.ID).Updates(map[string]any{
		"status": turnStatusCancelled, "finish_reason": "abandoned",
		"ended_at": &now, "updated_at": now,
	}).Error; err != nil {
		return err
	}
	c.recordTrace(workflowTrace{SessionID: sessionID, TurnID: turn.ID, Kind: "recovery", Status: "abandoned"})
	c.emitChat(sessionID, ChatStreamEvent{Kind: "turn_state", TurnID: turn.ID, TurnStatus: turnStatusCancelled})
	c.emitChat(sessionID, ChatStreamEvent{Kind: "done", TurnID: turn.ID})
	return nil
}

// RetryStep 只重试失败或 blocked 的计划步骤，不裁剪历史。已完成步骤与其
// 副作用由台账和计划状态保护。
func (c *ChatService) RetryStep(sessionID, stepID string) error {
	if c.db == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	var turn workflowTurn
	if err := c.db.Where("session_id = ? AND status IN ?", sessionID,
		[]string{turnStatusInterrupted, turnStatusFailed, turnStatusCancelled, turnStatusBudgetPaused}).
		Order("created_at DESC").First(&turn).Error; err != nil {
		return fmt.Errorf("没有可重试的失败回合")
	}
	steps, _, err := c.readPlanState(sessionID)
	if err != nil || len(steps) == 0 {
		return fmt.Errorf("当前会话没有可重试的计划")
	}
	idx := -1
	for i := range steps {
		if stepID != "" && steps[i].ID == stepID {
			idx = i
			break
		}
		if stepID == "" && (steps[i].Status == "failed" || steps[i].Status == "blocked") {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("未找到失败或 blocked 的计划步骤")
	}
	if steps[idx].Status != "failed" && steps[idx].Status != "blocked" {
		return fmt.Errorf("只能重试 failed 或 blocked 步骤（当前 %s）", steps[idx].Status)
	}
	steps[idx].Status = "in_progress"
	if _, err := c.savePlanState(sessionID, steps); err != nil {
		return err
	}
	retry := &RetryInfo{StepID: steps[idx].ID, Reason: steps[idx].Content, Attempt: 1}
	c.emitChat(sessionID, ChatStreamEvent{Kind: "retry", TurnID: turn.ID, Retry: retry})
	c.recordTrace(workflowTrace{
		SessionID: sessionID, TurnID: turn.ID, Kind: "retry", Status: "started",
		Name: steps[idx].ID, DataJSON: sanitizedJSON(retry),
	})
	prompt := fmt.Sprintf("只重试计划步骤 %s：「%s」。\n"+
		"不要重跑已完成步骤或重复已应用的写操作。先检查当前计划、工作区和副作用台账，再执行该步骤；完成后运行 verify_work 并记录证据。",
		steps[idx].ID, steps[idx].Content)
	return c.resumeTurnFrom(sessionID, &turn, prompt)
}
