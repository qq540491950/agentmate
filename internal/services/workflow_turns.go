package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/config"
)

// workflowTurn 是一次运行的最小持久化清单。它不替代 ADK 事件历史，
// 只记录这次运行使用的目标、工作区、执行策略和生命周期状态。
type workflowTurn struct {
	ID             string    `gorm:"primaryKey" json:"id"`
	SessionID      string    `gorm:"index" json:"sessionId"`
	Status         string    `gorm:"index" json:"status"`
	TargetType     string    `json:"targetType"`
	TargetID       string    `json:"targetId"`
	Workspace      string    `json:"workspace"`
	ApprovalPolicy string    `json:"approvalPolicy"`
	SandboxProfile string    `json:"sandboxProfile"`
	SandboxNetwork string    `json:"sandboxNetwork"`
	Model          string    `json:"model"`
	ReasoningLevel string    `json:"reasoningLevel"`
	ParentTurnID   string    `json:"parentTurnId"`
	ErrorMessage   string    `json:"errorMessage,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func (workflowTurn) TableName() string { return "workflow_turns" }

type workflowApproval struct {
	ID        string `gorm:"primaryKey"`
	TurnID    string `gorm:"index"`
	SessionID string `gorm:"index"`
	ToolName  string
	ArgsJSON  string `gorm:"type:text"`
	Hint      string `gorm:"type:text"`
	Status    string `gorm:"index"`
	CreatedAt time.Time
}

func (workflowApproval) TableName() string { return "workflow_approvals" }

type workflowCheckpoint struct {
	ID        string `gorm:"primaryKey"`
	TurnID    string `gorm:"index"`
	SessionID string `gorm:"index"`
	Seq       int
	EventID   string
	CreatedAt time.Time
}

func (workflowCheckpoint) TableName() string { return "workflow_checkpoints" }

func (c *ChatService) beginTurn(target *config.ChatSession, workspace, approval, profile, network, model, reasoning, parent string) (*workflowTurn, error) {
	turn := &workflowTurn{
		ID:             newID("turn_"),
		SessionID:      target.ID,
		Status:         "running",
		TargetType:     target.TargetType,
		TargetID:       target.TargetID,
		Workspace:      workspace,
		ApprovalPolicy: approval,
		SandboxProfile: profile,
		SandboxNetwork: network,
		Model:          model,
		ReasoningLevel: reasoning,
		ParentTurnID:   parent,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if c.db == nil {
		return turn, nil
	}
	if err := c.db.Create(turn).Error; err != nil {
		return nil, err
	}
	return turn, nil
}

func (c *ChatService) updateTurnStatus(turnID, status string) {
	if c.db == nil || turnID == "" {
		return
	}
	_ = c.db.Model(&workflowTurn{}).Where("id = ?", turnID).Updates(map[string]any{
		"status": status, "updated_at": time.Now().UTC(),
	}).Error
}

func (c *ChatService) finishTurn(turnID string, status *string, runErr *error) {
	if status == nil || *status == "" {
		return
	}
	final := *status
	if final == "running" {
		switch {
		case runErr != nil && errors.Is(*runErr, context.Canceled):
			final = "cancelled"
		case runErr != nil && *runErr != nil:
			final = "failed"
		default:
			final = "completed"
		}
	}
	if c.db != nil && turnID != "" {
		fields := map[string]any{"status": final, "updated_at": time.Now().UTC()}
		if runErr != nil && *runErr != nil {
			fields["error_message"] = (*runErr).Error()
		}
		_ = c.db.Model(&workflowTurn{}).Where("id = ?", turnID).Updates(fields).Error
	}
	*status = final
}

// latestWaitingTurn 为 HITL 恢复提供暂停时的策略/工作区快照。
func (c *ChatService) latestWaitingTurn(sessionID string) *workflowTurn {
	if c.db == nil {
		return nil
	}
	var turn workflowTurn
	err := c.db.Where("session_id = ? AND status = ?", sessionID, "waiting_approval").
		Order("created_at DESC").First(&turn).Error
	if err != nil {
		return nil
	}
	return &turn
}

func (c *ChatService) turnByID(turnID string) *workflowTurn {
	if c.db == nil || turnID == "" {
		return nil
	}
	var turn workflowTurn
	if err := c.db.Where("id = ?", turnID).First(&turn).Error; err != nil {
		return nil
	}
	return &turn
}

func (c *ChatService) recordApproval(turnID, sessionID, callID, toolName string, args map[string]any, hint string) {
	if c.db == nil || callID == "" {
		return
	}
	_ = c.db.Create(&workflowApproval{
		ID: callID, TurnID: turnID, SessionID: sessionID, ToolName: toolName,
		ArgsJSON: string(mustJSON(args)), Hint: hint, Status: "pending", CreatedAt: time.Now().UTC(),
	}).Error
}

func (c *ChatService) approvalByCallID(callID string) *workflowApproval {
	if c.db == nil || callID == "" {
		return nil
	}
	var approval workflowApproval
	if err := c.db.Where("id = ?", callID).First(&approval).Error; err != nil {
		return nil
	}
	return &approval
}

func (c *ChatService) setApprovalStatus(callID, status string) {
	if c.db == nil || callID == "" {
		return
	}
	_ = c.db.Model(&workflowApproval{}).Where("id = ?", callID).Update("status", status).Error
}

func (c *ChatService) recordCheckpoint(turnID, sessionID, eventID string, seq int) {
	if c.db == nil || turnID == "" {
		return
	}
	_ = c.db.Create(&workflowCheckpoint{
		ID: newID("chk_"), TurnID: turnID, SessionID: sessionID, Seq: seq,
		EventID: eventID, CreatedAt: time.Now().UTC(),
	}).Error
}

func (c *ChatService) recoverInterruptedTurns() {
	if c.db == nil {
		return
	}
	_ = c.db.Model(&workflowTurn{}).
		Where("status = ?", "running").
		Updates(map[string]any{"status": "interrupted", "updated_at": time.Now().UTC()}).Error
}

// ListTurns 返回会话最近的运行记录，供诊断/恢复 UI 使用。
func (c *ChatService) ListTurns(sessionID string) ([]workflowTurn, error) {
	if c.db == nil {
		return nil, fmt.Errorf("会话数据库不可用")
	}
	var turns []workflowTurn
	err := c.db.Where("session_id = ?", sessionID).Order("created_at DESC").Limit(50).Find(&turns).Error
	return turns, err
}

// ResumeTurn 从最近一次失败/中断的回合继续：它不重放旧工具调用，而是把上次
// 用户请求与当前计划交回模型，让模型先检查实际状态，再从未完成步骤继续。
func (c *ChatService) ResumeTurn(sessionID string) error {
	if c.db == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	var turn workflowTurn
	err := c.db.Where("session_id = ? AND status IN ?", sessionID, []string{"interrupted", "failed", "cancelled"}).
		Order("created_at DESC").First(&turn).Error
	if err != nil {
		return fmt.Errorf("没有可恢复的回合")
	}
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil || resp.Session == nil {
		return fmt.Errorf("无法读取会话历史")
	}
	lastUser := ""
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil || ev.Author != "user" || hasFunctionResponse(ev) {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part != nil && part.Text != "" && !part.Thought {
				lastUser = part.Text
			}
		}
	}
	if strings.TrimSpace(lastUser) == "" {
		return fmt.Errorf("没有可恢复的用户请求")
	}
	c.updateTurnStatus(turn.ID, "resumed")
	prompt := "继续上次未完成的任务。上次请求：\n" + lastUser +
		"\n\n请先检查当前计划和实际工作区，已完成步骤不要重复执行；从第一个 pending/blocked 步骤继续，并在结束前运行 verify_work。"
	return c.sendTurn(sessionID, prompt, nil, turn.ReasoningLevel)
}
