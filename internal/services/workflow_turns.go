package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
)

// workflowTurn 是一次运行的最小持久化清单。它不替代 ADK 事件历史，
// 只记录这次运行使用的目标、工作区、执行策略和生命周期状态。
type workflowTurn struct {
	ID                 string     `gorm:"primaryKey" json:"id"`
	SessionID          string     `gorm:"index" json:"sessionId"`
	Status             string     `gorm:"index" json:"status"`
	TargetType         string     `json:"targetType"`
	TargetID           string     `json:"targetId"`
	Workspace          string     `json:"workspace"`
	ApprovalPolicy     string     `json:"approvalPolicy"`
	SandboxProfile     string     `json:"sandboxProfile"`
	SandboxNetwork     string     `json:"sandboxNetwork"`
	Model              string     `json:"model"`
	ReasoningLevel     string     `json:"reasoningLevel"`
	ParentTurnID       string     `json:"parentTurnId"`
	AgentSnapshotJSON  string     `gorm:"type:text" json:"agentSnapshotJson,omitempty"`
	PlanVersion        string     `json:"planVersion,omitempty"`
	BudgetOverride     bool       `json:"budgetOverride,omitempty"`
	VerificationExempt bool       `json:"verificationExempt,omitempty"`
	FinishReason       string     `json:"finishReason,omitempty"`
	ErrorMessage       string     `json:"errorMessage,omitempty"`
	UsageJSON          string     `gorm:"type:text" json:"usageJson,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	EndedAt            *time.Time `json:"endedAt,omitempty"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

func (workflowTurn) TableName() string { return "workflow_turns" }

type workflowApproval struct {
	ID         string `gorm:"primaryKey"`
	TurnID     string `gorm:"index"`
	SessionID  string `gorm:"index"`
	ToolName   string
	ArgsJSON   string `gorm:"type:text"`
	Hint       string `gorm:"type:text"`
	Risk       string `json:"risk,omitempty"`
	Diff       string `gorm:"type:text" json:"diff,omitempty"`
	PolicyJSON string `gorm:"type:text" json:"policyJson,omitempty"`
	Status     string `gorm:"index"`
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

func (workflowApproval) TableName() string { return "workflow_approvals" }

type workflowCheckpoint struct {
	ID               string `gorm:"primaryKey"`
	TurnID           string `gorm:"index"`
	SessionID        string `gorm:"index"`
	Seq              int
	EventID          string
	PlanVersion      string
	PlanJSON         string `gorm:"type:text"`
	PendingApprovals string `gorm:"type:text"`
	SideEffectsJSON  string `gorm:"type:text"`
	LastUserText     string `gorm:"type:text"`
	ResumePrompt     string `gorm:"type:text"`
	CreatedAt        time.Time
}

func (workflowCheckpoint) TableName() string { return "workflow_checkpoints" }

const (
	turnStatusQueued          = "queued"
	turnStatusPreparing       = "preparing"
	turnStatusRunning         = "running"
	turnStatusWaitingApproval = "waiting_approval"
	turnStatusVerifying       = "verifying"
	turnStatusCompleted       = "completed"
	turnStatusFailed          = "failed"
	turnStatusCancelled       = "cancelled"
	turnStatusInterrupted     = "interrupted"
	turnStatusBudgetPaused    = "budget_paused"
)

var terminalTurnStatuses = map[string]bool{
	turnStatusCompleted: true, turnStatusFailed: true, turnStatusCancelled: true, turnStatusInterrupted: true,
}

var allowedTurnTransitions = map[string]map[string]bool{
	turnStatusQueued: {
		turnStatusPreparing: true, turnStatusFailed: true, turnStatusCancelled: true,
	},
	turnStatusPreparing: {
		turnStatusRunning: true, turnStatusFailed: true, turnStatusCancelled: true,
	},
	turnStatusRunning: {
		turnStatusWaitingApproval: true, turnStatusVerifying: true, turnStatusBudgetPaused: true,
		turnStatusCompleted: true, turnStatusFailed: true, turnStatusCancelled: true,
	},
	turnStatusWaitingApproval: {
		turnStatusRunning: true, turnStatusPreparing: true, turnStatusFailed: true,
		turnStatusCancelled: true, turnStatusBudgetPaused: true,
	},
	turnStatusVerifying: {
		turnStatusRunning: true, turnStatusFailed: true, turnStatusCancelled: true,
	},
	turnStatusBudgetPaused: {
		turnStatusRunning: true, turnStatusPreparing: true, turnStatusCancelled: true, turnStatusFailed: true,
	},
}

func isTerminalTurnStatus(status string) bool { return terminalTurnStatuses[status] }

// turnAgentSnapshot 是不可变运行清单中与 Agent 构建相关的部分。HITL、重试与
// 恢复优先使用它，而不是暂停后可能已变化的配置。
type turnAgentSnapshot struct {
	Agent        *config.AgentConfig       `json:"agent,omitempty"`
	Team         *config.TeamConfig        `json:"team,omitempty"`
	Remote       *config.RemoteAgentConfig `json:"remote,omitempty"`
	Direct       *config.AgentConfig       `json:"direct,omitempty"`
	TeamAgents   []config.AgentConfig      `json:"teamAgents,omitempty"`
	BuiltinTools []string                  `json:"builtinTools,omitempty"`
	Skills       []string                  `json:"skills,omitempty"`
	MCPServers   []string                  `json:"mcpServers,omitempty"`
	PromptHash   string                    `json:"promptHash,omitempty"`
}

func snapshotForTurn(cfg config.Config, target *config.ChatSession) (string, error) {
	snap := turnAgentSnapshot{}
	switch target.TargetType {
	case "":
		ac, err := directAgentConfig(cfg)
		if err != nil {
			return "", err
		}
		acCopy := ac
		snap.Direct = &acCopy
		snap.BuiltinTools = append([]string(nil), ac.EffectiveBuiltinTools()...)
		snap.Skills = append([]string(nil), ac.SkillIDs...)
		snap.MCPServers = append([]string(nil), ac.MCPServerIDs...)
		snap.PromptHash = hashPrompt(ac.SystemPrompt)
	case "team":
		for i := range cfg.Teams {
			if cfg.Teams[i].ID != target.TargetID {
				continue
			}
			team := cfg.Teams[i]
			team.MemberAgentIDs = append([]string(nil), team.MemberAgentIDs...)
			team.RemoteMemberIDs = append([]string(nil), team.RemoteMemberIDs...)
			snap.Team = &team
			snap.PromptHash = hashPrompt(team.SystemPrompt)
			for _, id := range team.MemberAgentIDs {
				for _, ac := range cfg.Agents {
					if ac.ID == id {
						acCopy := ac
						acCopy.SkillIDs = append([]string(nil), ac.SkillIDs...)
						acCopy.MCPServerIDs = append([]string(nil), ac.MCPServerIDs...)
						acCopy.BuiltinTools = append([]string(nil), ac.BuiltinTools...)
						snap.TeamAgents = append(snap.TeamAgents, acCopy)
						snap.BuiltinTools = append(snap.BuiltinTools, ac.EffectiveBuiltinTools()...)
						snap.Skills = append(snap.Skills, ac.SkillIDs...)
						snap.MCPServers = append(snap.MCPServers, ac.MCPServerIDs...)
					}
				}
			}
		}
	case "remote":
		for i := range cfg.RemoteAgents {
			if cfg.RemoteAgents[i].ID == target.TargetID {
				remote := cfg.RemoteAgents[i]
				snap.Remote = &remote
			}
		}
	default:
		for i := range cfg.Agents {
			if cfg.Agents[i].ID != target.TargetID {
				continue
			}
			ac := cfg.Agents[i]
			ac.SkillIDs = append([]string(nil), ac.SkillIDs...)
			ac.MCPServerIDs = append([]string(nil), ac.MCPServerIDs...)
			ac.BuiltinTools = append([]string(nil), ac.BuiltinTools...)
			snap.Agent = &ac
			snap.BuiltinTools = append([]string(nil), ac.EffectiveBuiltinTools()...)
			snap.Skills = append([]string(nil), ac.SkillIDs...)
			snap.MCPServers = append([]string(nil), ac.MCPServerIDs...)
			snap.PromptHash = hashPrompt(ac.SystemPrompt)
		}
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func hashPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func applyAgentSnapshot(cfg config.Config, raw string) (config.Config, *turnAgentSnapshot, error) {
	if strings.TrimSpace(raw) == "" {
		return cfg, nil, nil
	}
	var snap turnAgentSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return cfg, nil, fmt.Errorf("回合 Agent 清单损坏: %w", err)
	}
	replaceAgent := func(next config.AgentConfig) {
		for i := range cfg.Agents {
			if cfg.Agents[i].ID == next.ID {
				cfg.Agents[i] = next
				return
			}
		}
		cfg.Agents = append(cfg.Agents, next)
	}
	if snap.Agent != nil {
		replaceAgent(*snap.Agent)
	}
	for _, agent := range snap.TeamAgents {
		replaceAgent(agent)
	}
	if snap.Team != nil {
		found := false
		for i := range cfg.Teams {
			if cfg.Teams[i].ID == snap.Team.ID {
				cfg.Teams[i] = *snap.Team
				found = true
				break
			}
		}
		if !found {
			cfg.Teams = append(cfg.Teams, *snap.Team)
		}
	}
	if snap.Remote != nil {
		found := false
		for i := range cfg.RemoteAgents {
			if cfg.RemoteAgents[i].ID == snap.Remote.ID {
				cfg.RemoteAgents[i] = *snap.Remote
				found = true
				break
			}
		}
		if !found {
			cfg.RemoteAgents = append(cfg.RemoteAgents, *snap.Remote)
		}
	}
	return cfg, &snap, nil
}

func (c *ChatService) beginTurn(target *config.ChatSession, workspace, approval, profile, network, model, reasoning, parent, snapshot string, budgetOverride bool) (*workflowTurn, error) {
	turn := &workflowTurn{
		ID:                newID("turn_"),
		SessionID:         target.ID,
		Status:            turnStatusQueued,
		TargetType:        target.TargetType,
		TargetID:          target.TargetID,
		Workspace:         workspace,
		ApprovalPolicy:    approval,
		SandboxProfile:    profile,
		SandboxNetwork:    network,
		Model:             model,
		ReasoningLevel:    reasoning,
		ParentTurnID:      parent,
		AgentSnapshotJSON: snapshot,
		BudgetOverride:    budgetOverride,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	if c.db == nil {
		return turn, nil
	}
	if err := c.db.Create(turn).Error; err != nil {
		return nil, err
	}
	return turn, nil
}

func (c *ChatService) updateTurnStatus(turnID, status string) bool {
	if c.db == nil || turnID == "" {
		return true
	}
	var turn workflowTurn
	if err := c.db.Where("id = ?", turnID).First(&turn).Error; err != nil {
		return false
	}
	if turn.Status == status {
		return true
	}
	if !allowedTurnTransitions[turn.Status][status] && !terminalTurnStatuses[turn.Status] {
		return false
	}
	updates := map[string]any{
		"status": status, "updated_at": time.Now().UTC(),
	}
	if terminalTurnStatuses[status] {
		updates["ended_at"] = time.Now().UTC()
	}
	return c.db.Model(&workflowTurn{}).Where("id = ?", turnID).Updates(updates).Error == nil
}

func (c *ChatService) finishTurn(turnID string, status *string, runErr *error, finishReason string, usage *UsageInfo) {
	if status == nil || *status == "" {
		return
	}
	final := *status
	if final == turnStatusRunning || final == turnStatusPreparing || final == turnStatusQueued {
		switch {
		case runErr != nil && errors.Is(*runErr, context.Canceled):
			final = turnStatusCancelled
		case runErr != nil && *runErr != nil:
			final = turnStatusFailed
		default:
			final = turnStatusCompleted
		}
	}
	if c.db != nil && turnID != "" {
		fields := map[string]any{"status": final, "updated_at": time.Now().UTC()}
		if terminalTurnStatuses[final] {
			fields["ended_at"] = time.Now().UTC()
		}
		if runErr != nil && *runErr != nil {
			fields["error_message"] = sanitizeForPersistence((*runErr).Error())
		}
		if finishReason != "" {
			fields["finish_reason"] = finishReason
		}
		if usage != nil {
			fields["usage_json"] = sanitizedJSON(usage)
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
	turn := c.turnByID(turnID)
	risk, diff := "", ""
	policy := ""
	if turn != nil {
		risk, diff = agentkit.ReviewToolCall(turn.Workspace, turn.SandboxProfile, turn.SandboxNetwork, toolName, args)
		diff = agentkit.SanitizeSecrets(diff)
		policy = agentkit.FormatPolicySnapshot(turn.Workspace, turn.ApprovalPolicy, turn.SandboxProfile, turn.SandboxNetwork, turn.Model, turn.ReasoningLevel)
	}
	cleanArgs := string(sanitizedJSON(args))
	_ = c.db.Create(&workflowApproval{
		ID: callID, TurnID: turnID, SessionID: sessionID, ToolName: toolName,
		ArgsJSON: cleanArgs, Hint: agentkit.SanitizeSecrets(hint), Risk: risk,
		Diff: diff, PolicyJSON: policy, Status: "pending", CreatedAt: time.Now().UTC(),
	}).Error
	workspace := ""
	if turn != nil {
		workspace = turn.Workspace
	}
	c.autoConfirmIfGranted(sessionID, callID, toolName, risk, workspace)
	c.recordTrace(workflowTrace{
		SessionID: sessionID, TurnID: turnID, Kind: "approval", Status: "pending",
		Name: toolName, CallID: callID, ErrorCode: risk, DataJSON: sanitizedJSON(map[string]any{
			"hint": hint, "policy": policy, "diff": diff,
		}),
	})
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
	now := time.Now().UTC()
	_ = c.db.Model(&workflowApproval{}).Where("id = ?", callID).Updates(map[string]any{
		"status": status, "resolved_at": &now,
	}).Error
}

func (c *ChatService) recordCheckpoint(turnID, sessionID, eventID string, seq int) {
	if c.db == nil || turnID == "" {
		return
	}
	planJSON, planVersion := c.planJSONForSession(sessionID)
	pending := c.pendingApprovalsJSON(turnID)
	sideEffects := c.sideEffectsSummaryJSON(turnID, 50, 12*1024)
	lastUser := c.lastUserText(sessionID)
	turn := c.turnByID(turnID)
	resumePrompt := c.buildResumePrompt(turn, lastUser, planJSON, pending, sideEffects, "", eventID, seq)
	_ = c.db.Create(&workflowCheckpoint{
		ID: newID("chk_"), TurnID: turnID, SessionID: sessionID, Seq: seq,
		EventID: eventID, PlanVersion: planVersion, PlanJSON: planJSON,
		PendingApprovals: pending, SideEffectsJSON: sideEffects,
		LastUserText: lastUser, ResumePrompt: resumePrompt, CreatedAt: time.Now().UTC(),
	}).Error
	if turn != nil && planVersion != "" && turn.PlanVersion != planVersion {
		_ = c.db.Model(&workflowTurn{}).Where("id = ?", turnID).Update("plan_version", planVersion).Error
	}
}

func (c *ChatService) recoverInterruptedTurns() {
	if c.db == nil {
		return
	}
	_ = c.db.Model(&workflowTurn{}).
		Where("status IN ?", []string{turnStatusQueued, turnStatusPreparing, turnStatusRunning}).
		Updates(map[string]any{
			"status": turnStatusInterrupted, "updated_at": time.Now().UTC(),
			"ended_at": time.Now().UTC(), "finish_reason": "startup_recovery",
		}).Error
}

// ListTurns 返回会话最近的运行记录，供诊断/恢复 UI 使用。
func (c *ChatService) ListTurns(sessionID string) ([]workflowTurn, error) {
	if c.db == nil {
		return nil, fmt.Errorf("会话数据库不可用")
	}
	var turns []workflowTurn
	err := c.db.Where("session_id = ?", sessionID).Order("created_at DESC").Limit(50).Find(&turns).Error
	for i := range turns {
		// AgentSnapshotJSON 只供恢复重建，不通过诊断 RPC 暴露系统提示词与策略副本。
		turns[i].AgentSnapshotJSON = ""
	}
	return turns, err
}

// ResumeTurn 从最近一次失败/中断/取消/预算暂停的检查点继续：它带着事件游标、
// 计划版本、未决审批和副作用摘要重新进入模型，不裁剪整轮历史。
func (c *ChatService) ResumeTurn(sessionID string) error {
	if c.db == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	var turn workflowTurn
	err := c.db.Where("session_id = ? AND status IN ?", sessionID,
		[]string{turnStatusInterrupted, turnStatusFailed, turnStatusCancelled, turnStatusBudgetPaused}).
		Order("created_at DESC").First(&turn).Error
	if err != nil {
		return fmt.Errorf("没有可恢复的回合")
	}
	return c.resumeTurnFrom(sessionID, &turn, "")
}

func (c *ChatService) resumeTurnFrom(sessionID string, turn *workflowTurn, extraInstruction string) error {
	var checkpoint workflowCheckpoint
	err := c.db.Where("turn_id = ?", turn.ID).Order("created_at DESC").First(&checkpoint).Error
	_ = err
	lastUser := strings.TrimSpace(checkpoint.LastUserText)
	if lastUser == "" {
		lastUser = c.lastUserText(sessionID)
	}
	if lastUser == "" {
		return fmt.Errorf("没有可恢复的用户请求")
	}
	prompt := strings.TrimSpace(checkpoint.ResumePrompt)
	if prompt == "" {
		prompt = c.buildResumePrompt(turn, lastUser, checkpoint.PlanJSON,
			checkpoint.PendingApprovals, checkpoint.SideEffectsJSON, extraInstruction,
			checkpoint.EventID, checkpoint.Seq)
	} else if extraInstruction != "" {
		prompt += "\n\n补充指令：\n" + extraInstruction
	}
	return c.resumeTurnPrompt(sessionID, prompt, turn)
}

func (c *ChatService) planJSONForSession(sessionID string) (string, string) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil || resp.Session == nil || resp.Session.State() == nil {
		return "", ""
	}
	value, err := resp.Session.State().Get(agentkit.TodosStateKey)
	if err != nil {
		return "", ""
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return "", ""
	}
	var steps []agentkit.PlanStep
	if json.Unmarshal(data, &steps) != nil {
		return "", ""
	}
	normalized, _ := json.Marshal(steps)
	return string(normalized), hashBytes(normalized)
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (c *ChatService) pendingApprovalsJSON(turnID string) string {
	turn := c.turnByID(turnID)
	if turn == nil {
		return ""
	}
	ids := c.turnAncestorIDs(turn)
	var approvals []workflowApproval
	_ = c.db.Where("turn_id IN ? AND status = ?", ids, "pending").
		Order("created_at ASC").Limit(50).Find(&approvals).Error
	if len(approvals) == 0 {
		return ""
	}
	type approvalSummary struct {
		CallID string `json:"callId"`
		TurnID string `json:"turnId"`
		Tool   string `json:"tool"`
		Risk   string `json:"risk"`
		Hint   string `json:"hint,omitempty"`
		Policy string `json:"policy,omitempty"`
	}
	rows := make([]approvalSummary, 0, len(approvals))
	for _, approval := range approvals {
		rows = append(rows, approvalSummary{
			CallID: approval.ID, TurnID: approval.TurnID, Tool: approval.ToolName,
			Risk: approval.Risk, Hint: truncateRunes(agentkit.SanitizeSecrets(approval.Hint), 400),
			Policy: truncateRunes(agentkit.SanitizeSecrets(approval.PolicyJSON), 800),
		})
	}
	for len(rows) > 0 {
		data := sanitizedJSON(rows)
		if len(data) <= 8*1024 {
			return data
		}
		rows = rows[:len(rows)-1]
	}
	return ""
}

func (c *ChatService) lastUserText(sessionID string) string {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil || resp.Session == nil {
		return ""
	}
	last := ""
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil || ev.Author != "user" || hasFunctionResponse(ev) {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part != nil && part.Text != "" && !part.Thought {
				last = part.Text
			}
		}
	}
	return agentkit.SanitizeSecrets(strings.TrimSpace(last))
}

func (c *ChatService) buildResumePrompt(turn *workflowTurn, lastUser, planJSON, pendingJSON, sideEffectsJSON, extra, eventID string, seq int) string {
	var sb strings.Builder
	sb.WriteString("从持久化检查点继续上次未完成的任务。不要重跑整轮，也不要重复已完成的副作用。\n\n")
	sb.WriteString("上次用户请求：\n")
	sb.WriteString(lastUser)
	sb.WriteString("\n\n恢复约束：\n")
	sb.WriteString("- 先读取当前计划、工作区和副作用台账，再决定下一步。\n")
	sb.WriteString("- 只处理 pending/blocked/failed 步骤；已 completed 的步骤必须保留证据并跳过。\n")
	sb.WriteString("- 恢复后 shell/MCP 调用和冲突文件写入必须重新获得人工确认。\n")
	sb.WriteString("- 代码任务完成前必须调用 verify_work；验证失败最多自动修复 2 轮。\n")
	if turn != nil {
		sb.WriteString(fmt.Sprintf("- 检查点回合：%s；计划版本：%s。\n", turn.ID, turn.PlanVersion))
	}
	if seq > 0 || eventID != "" {
		sb.WriteString(fmt.Sprintf("- 事件游标：seq=%d，event=%s；只追加游标之后的新证据。\n", seq, eventID))
	}
	if strings.TrimSpace(planJSON) != "" {
		sb.WriteString("\n当前计划（JSON）：\n")
		sb.WriteString(truncateRunes(agentkit.SanitizeSecrets(planJSON), 12000))
		sb.WriteString("\n")
	}
	if strings.TrimSpace(pendingJSON) != "" {
		sb.WriteString("\n未决审批（JSON）：\n")
		sb.WriteString(truncateRunes(agentkit.SanitizeSecrets(pendingJSON), 6000))
		sb.WriteString("\n")
	}
	if strings.TrimSpace(sideEffectsJSON) != "" {
		sb.WriteString("\n副作用台账摘要（JSON）：\n")
		sb.WriteString(truncateRunes(agentkit.SanitizeSecrets(sideEffectsJSON), 10000))
		sb.WriteString("\n")
	}
	if strings.TrimSpace(extra) != "" {
		sb.WriteString("\n补充指令：\n")
		sb.WriteString(extra)
		sb.WriteString("\n")
	}
	return sb.String()
}
