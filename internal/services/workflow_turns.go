package services

import (
	"context"
	"errors"
	"time"

	"agentmate/internal/config"
)

// workflowTurn 是一次运行的最小持久化清单。它不替代 ADK 事件历史，
// 只记录这次运行使用的目标、工作区、执行策略和生命周期状态。
type workflowTurn struct {
	ID             string `gorm:"primaryKey"`
	SessionID      string `gorm:"index"`
	Status         string `gorm:"index"`
	TargetType     string
	TargetID       string
	Workspace      string
	ApprovalPolicy string
	SandboxProfile string
	SandboxNetwork string
	Model          string
	ReasoningLevel string
	ParentTurnID   string
	ErrorMessage   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
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
