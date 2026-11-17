package services

import (
	"fmt"
	"strings"
	"time"

	"agentmate/internal/agentkit"
)

// ListApprovals 返回会话中仍未处理的确认请求，供审批队列逐项处理。
func (c *ChatService) ListApprovals(sessionID string) ([]workflowApproval, error) {
	if c.db == nil {
		return nil, fmt.Errorf("会话数据库不可用")
	}
	var approvals []workflowApproval
	err := c.db.Where("session_id = ? AND status = ?", sessionID, "pending").
		Order("created_at ASC").Find(&approvals).Error
	if err != nil {
		return nil, err
	}
	for i := range approvals {
		approvals[i].ArgsJSON = agentkit.SanitizeSecrets(approvals[i].ArgsJSON)
		approvals[i].Diff = agentkit.SanitizeSecrets(approvals[i].Diff)
		approvals[i].Hint = agentkit.SanitizeSecrets(approvals[i].Hint)
	}
	return approvals, nil
}

// ConfirmApprovals 批量批准/拒绝同一批待处理请求。实现按项调用 ConfirmToolCall：
// 每个确认恢复一次 ADK 运行，未处理项保持 pending。
func (c *ChatService) ConfirmApprovals(sessionID string, callIDs []string, approved bool) error {
	if len(callIDs) == 0 {
		return fmt.Errorf("缺少确认请求 ID")
	}
	seen := map[string]bool{}
	for _, callID := range callIDs {
		callID = strings.TrimSpace(callID)
		if callID == "" || seen[callID] {
			continue
		}
		seen[callID] = true
		if err := c.ConfirmToolCall(sessionID, callID, approved); err != nil {
			return fmt.Errorf("确认 %s 失败: %w", callID, err)
		}
	}
	return nil
}

// GrantApprovalScope 是有作用域的批量授权：只匹配同一会话、同一工具和同一风险
// 等级，24 小时有效。高危调用始终逐次确认，不允许被批量授权覆盖。
func (c *ChatService) GrantApprovalScope(sessionID, callID string) error {
	if c.db == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	approval := c.approvalByCallID(callID)
	if approval == nil || approval.SessionID != sessionID {
		return fmt.Errorf("确认请求不存在")
	}
	if approval.Status != "pending" {
		return fmt.Errorf("确认请求已被处理")
	}
	if approval.Risk == "high" {
		return fmt.Errorf("高危操作不支持批量授权，请逐次确认")
	}
	workspace := ""
	if turn := c.turnByID(approval.TurnID); turn != nil {
		workspace = turn.Workspace
	}
	grant := workflowApprovalGrant{
		ID: newID("grant_"), SessionID: sessionID, TurnID: approval.TurnID,
		ToolName: approval.ToolName, Risk: approval.Risk, Workspace: workspace,
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
	if err := c.db.Create(&grant).Error; err != nil {
		return err
	}
	c.recordTrace(workflowTrace{
		SessionID: sessionID, TurnID: approval.TurnID, Kind: "approval", Status: "granted",
		Name: approval.ToolName, DataJSON: sanitizedJSON(map[string]any{
			"scope": "session_tool_risk", "risk": approval.Risk, "workspace": workspace,
		}),
	})
	return nil
}

func (c *ChatService) approvalGranted(sessionID, toolName, risk, workspace string) bool {
	if c.db == nil || strings.TrimSpace(risk) == "" || risk == "high" {
		return false
	}
	var count int64
	query := c.db.Model(&workflowApprovalGrant{}).
		Where("session_id = ? AND tool_name = ? AND risk = ? AND expires_at > ?",
			sessionID, toolName, risk, time.Now().UTC())
	if strings.TrimSpace(workspace) != "" {
		query = query.Where("workspace = ? OR workspace = ''", workspace)
	}
	_ = query.Count(&count).Error
	return count > 0
}

func (c *ChatService) autoConfirmIfGranted(sessionID, callID, toolName, risk, workspace string) {
	if !c.approvalGranted(sessionID, toolName, risk, workspace) {
		return
	}
	// 当前 runTurn 还持有会话工作区锁；goroutine 会等它释放后恢复。
	go func() {
		_ = c.ConfirmToolCall(sessionID, callID, true)
	}()
}

func (c *ChatService) latestWaitingTurnID(sessionID string) string {
	turn := c.latestWaitingTurn(sessionID)
	if turn == nil {
		return ""
	}
	return turn.ID
}

func (c *ChatService) validateApproval(sessionID, callID string) (*workflowApproval, error) {
	approval := c.approvalByCallID(callID)
	if approval == nil || approval.SessionID != sessionID {
		return nil, fmt.Errorf("未找到待确认的工具调用 %s", callID)
	}
	if approval.Status != "pending" {
		return nil, fmt.Errorf("该确认请求已被处理")
	}
	owner := c.turnByID(approval.TurnID)
	if owner == nil || owner.SessionID != sessionID {
		return nil, fmt.Errorf("确认请求不属于当前会话")
	}
	if isTerminalTurnStatus(owner.Status) {
		return nil, fmt.Errorf("确认请求已过期（回合 %s）", owner.Status)
	}
	latest := c.latestWaitingTurnID(sessionID)
	if latest != "" && latest != approval.TurnID {
		// 同一会话允许同一暂停回合的多个并行确认；旧回合不得确认新回合。
		parent := c.turnByID(owner.ParentTurnID)
		if parent == nil || parent.ID != latest {
			return nil, fmt.Errorf("确认请求属于旧回合，不能跨回合确认")
		}
	}
	return approval, nil
}

// supersedePendingApprovals 在新用户回合开始时废弃旧的待确认卡片，避免跨回合
// 确认把上一轮的调用带进新回合。
func (c *ChatService) supersedePendingApprovals(sessionID string) {
	if c.db == nil {
		return
	}
	var turnIDs []string
	_ = c.db.Model(&workflowApproval{}).
		Where("session_id = ? AND status = ?", sessionID, "pending").
		Distinct().Pluck("turn_id", &turnIDs).Error
	now := time.Now().UTC()
	_ = c.db.Model(&workflowApproval{}).
		Where("session_id = ? AND status = ?", sessionID, "pending").
		Updates(map[string]any{"status": "superseded", "resolved_at": &now}).Error
	if len(turnIDs) > 0 {
		_ = c.db.Model(&workflowTurn{}).
			Where("id IN ? AND status = ?", turnIDs, turnStatusWaitingApproval).
			Updates(map[string]any{
				"status": turnStatusCancelled, "finish_reason": "approval_superseded",
				"ended_at": &now, "updated_at": now,
			}).Error
		for _, turnID := range turnIDs {
			c.recordTrace(workflowTrace{
				SessionID: sessionID, TurnID: turnID, Kind: "recovery", Status: "superseded",
			})
			c.emitChat(sessionID, ChatStreamEvent{
				Kind: "turn_state", TurnID: turnID, TurnStatus: turnStatusCancelled,
			})
			c.emitChat(sessionID, ChatStreamEvent{Kind: "done", TurnID: turnID})
		}
	}
}
