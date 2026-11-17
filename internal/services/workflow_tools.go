package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"agentmate/internal/agentkit"
)

// toolInvocation 是副作用工具的执行台账。它记录执行前的预期哈希、执行后的
// 实际哈希和结果状态，用于崩溃恢复时判断“已应用、可重跑、需人工选择”。
type toolInvocation struct {
	ID               string `gorm:"primaryKey"`
	SessionID        string `gorm:"index"`
	TurnID           string `gorm:"index"`
	ToolName         string `gorm:"index"`
	Source           string
	EffectsJSON      string `gorm:"type:text"`
	ArgsJSON         string `gorm:"type:text"`
	ArgsHash         string `gorm:"index"`
	Status           string `gorm:"index"`
	ErrorCode        string
	ErrorMessage     string `gorm:"type:text"`
	BeforeHash       string
	AfterHash        string
	PlannedAfterHash string
	ResultJSON       string `gorm:"type:text"`
	ResultBytes      int
	Artifact         string
	DurationMs       int64
	CreatedAt        time.Time `gorm:"index"`
	UpdatedAt        time.Time
}

func (toolInvocation) TableName() string { return "tool_invocations" }

type workflowApprovalGrant struct {
	ID        string `gorm:"primaryKey"`
	SessionID string `gorm:"index"`
	TurnID    string `gorm:"index"`
	ToolName  string `gorm:"index"`
	Source    string
	Risk      string
	Workspace string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (workflowApprovalGrant) TableName() string { return "workflow_approval_grants" }

// workflowTrace 是默认脱敏的回合 trace，覆盖模型调用、工具、审批、重试、
// 验证和状态迁移。
type workflowTrace struct {
	ID               string `gorm:"primaryKey"`
	SessionID        string `gorm:"index"`
	TurnID           string `gorm:"index"`
	Seq              int64
	Kind             string `gorm:"index"`
	Status           string
	Name             string
	CallID           string `gorm:"index"`
	Author           string
	ErrorCode        string
	ErrorMessage     string `gorm:"type:text"`
	DataJSON         string `gorm:"type:text"`
	DurationMs       int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Cost             float64
	CreatedAt        time.Time `gorm:"index"`
}

func (workflowTrace) TableName() string { return "turn_traces" }

func (c *ChatService) autoMigrateWorkflow() error {
	if c.db == nil {
		return nil
	}
	return c.db.AutoMigrate(
		&workflowTurn{}, &workflowApproval{}, &workflowCheckpoint{},
		&toolInvocation{}, &workflowApprovalGrant{}, &workflowTrace{},
	)
}

// BeginToolInvocation 写入执行前台账，并在恢复回合中消费父回合的副作用摘要。
func (c *ChatService) BeginToolInvocation(req agentkit.ToolInvocationBegin) (agentkit.ToolInvocationDecision, error) {
	if c.db == nil || strings.TrimSpace(req.SessionID) == "" {
		return agentkit.ToolInvocationDecision{}, nil
	}
	if strings.TrimSpace(req.TurnID) == "" {
		req.TurnID = c.activeTurnID(req.SessionID)
	}
	if strings.TrimSpace(req.TurnID) == "" {
		return agentkit.ToolInvocationDecision{}, nil
	}
	now := time.Now().UTC()
	var existing toolInvocation
	if req.CallID != "" {
		err := c.db.Where("id = ?", req.CallID).First(&existing).Error
		if err == nil {
			// 用户已经批准了恢复确认：允许本次执行，并为写工具注入当前哈希作为
			// 人工选择后的覆盖依据。
			if existing.Status == "approved" {
				decision := agentkit.ToolInvocationDecision{}
				if req.ToolName == "write_workspace_file" || req.ToolName == "edit_workspace_file" {
					if req.BeforeHash != "" {
						decision.ArgsOverride = map[string]any{"expectedSha256": req.BeforeHash}
					}
				}
				_ = c.db.Model(&toolInvocation{}).Where("id = ?", req.CallID).Updates(map[string]any{
					"status": "started", "updated_at": now,
				}).Error
				return decision, nil
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return agentkit.ToolInvocationDecision{}, err
		}
	}

	if decision, ok, err := c.resumeInvocationDecision(req); err != nil || ok {
		if err != nil {
			return agentkit.ToolInvocationDecision{}, err
		}
		if decision.Skip {
			c.upsertToolInvocation(req, "skipped", now)
			if decision.Result != nil {
				_ = c.db.Model(&toolInvocation{}).Where("id = ?", req.CallID).Updates(map[string]any{
					"result_json": sanitizedJSON(decision.Result),
					"after_hash":  resultStringForLedger(decision.Result, "sha256"),
					"updated_at":  now,
				}).Error
			}
		} else if decision.NeedConfirmation {
			status := "awaiting_confirmation"
			c.upsertToolInvocation(req, status, now)
		} else if decision.ErrorCode != "" {
			c.upsertToolInvocation(req, "failed", now)
		}
		return decision, nil
	}

	c.upsertToolInvocation(req, "started", now)
	return agentkit.ToolInvocationDecision{}, nil
}

func (c *ChatService) upsertToolInvocation(req agentkit.ToolInvocationBegin, status string, now time.Time) {
	if c.db == nil || req.CallID == "" {
		return
	}
	effects, _ := json.Marshal(req.Effects)
	row := toolInvocation{
		ID: req.CallID, SessionID: req.SessionID, TurnID: req.TurnID,
		ToolName: req.ToolName, Source: req.Source, EffectsJSON: string(effects),
		ArgsJSON: sanitizedJSON(req.Args), ArgsHash: req.ArgsHash, Status: status,
		BeforeHash: req.BeforeHash, PlannedAfterHash: req.PlannedAfterHash,
		CreatedAt: now, UpdatedAt: now,
	}
	var existing toolInvocation
	if err := c.db.Where("id = ?", req.CallID).First(&existing).Error; err == nil {
		row.CreatedAt = existing.CreatedAt
		_ = c.db.Model(&existing).Updates(map[string]any{
			"session_id": row.SessionID, "turn_id": row.TurnID, "tool_name": row.ToolName,
			"source": row.Source, "effects_json": row.EffectsJSON, "args_json": row.ArgsJSON,
			"args_hash": row.ArgsHash, "status": row.Status, "before_hash": row.BeforeHash,
			"planned_after_hash": row.PlannedAfterHash, "updated_at": row.UpdatedAt,
		}).Error
		return
	}
	_ = c.db.Create(&row).Error
}

func (c *ChatService) resumeInvocationDecision(req agentkit.ToolInvocationBegin) (agentkit.ToolInvocationDecision, bool, error) {
	current := c.turnByID(req.TurnID)
	if current == nil || strings.TrimSpace(current.ParentTurnID) == "" {
		return agentkit.ToolInvocationDecision{}, false, nil
	}
	ids := c.turnAncestorIDs(current)
	if len(ids) == 0 {
		return agentkit.ToolInvocationDecision{}, false, nil
	}
	var prior toolInvocation
	err := c.db.Where("turn_id IN ? AND tool_name = ? AND args_hash = ? AND status IN ?",
		ids, req.ToolName, req.ArgsHash, []string{"succeeded", "skipped", "started", "awaiting_confirmation"}).
		Order("created_at DESC").First(&prior).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return agentkit.ToolInvocationDecision{}, false, nil
	}
	if err != nil {
		return agentkit.ToolInvocationDecision{}, false, err
	}
	switch req.ToolName {
	case "write_workspace_file", "edit_workspace_file":
		appliedHash := prior.AfterHash
		if appliedHash == "" {
			appliedHash = prior.PlannedAfterHash
		}
		if appliedHash == "" && prior.ResultJSON != "" {
			var priorResult map[string]any
			if json.Unmarshal([]byte(prior.ResultJSON), &priorResult) == nil {
				appliedHash = resultStringForLedger(priorResult, "sha256")
			}
		}
		switch {
		case req.BeforeHash != "" && appliedHash != "" && strings.EqualFold(req.BeforeHash, appliedHash):
			var result map[string]any
			_ = json.Unmarshal([]byte(prior.ResultJSON), &result)
			if result == nil {
				result = map[string]any{}
			}
			result["ok"] = true
			result["skipped"] = true
			result["code"] = "already_applied"
			result["note"] = "恢复检查点：该旁路副作用已应用，本次跳过重复写入。"
			return agentkit.ToolInvocationDecision{Skip: true, Result: result}, true, nil
		case req.BeforeHash != "" && prior.BeforeHash != "" && req.BeforeHash == prior.BeforeHash:
			// 文件仍是执行前内容：上次写入没有生效，可安全重跑。
			return agentkit.ToolInvocationDecision{}, false, nil
		case req.BeforeHash == "" && prior.BeforeHash == "" && appliedHash == "":
			// 新建目标尚未出现，且没有可证明已应用的内容，可安全重跑。
			return agentkit.ToolInvocationDecision{}, false, nil
		default:
			return agentkit.ToolInvocationDecision{
				NeedConfirmation:    true,
				ConfirmationHint:    "恢复检查点：文件内容与暂停时不一致，请确认是否覆盖。",
				ConfirmationPayload: map[string]any{"code": "side_effect_conflict", "path": prior.ArgsJSON},
			}, true, nil
		}
	default:
		if containsEffect(req.Effects, agentkit.EffectCommand) || containsEffect(req.Effects, agentkit.EffectExternal) {
			return agentkit.ToolInvocationDecision{
				NeedConfirmation: true,
				ConfirmationHint: "恢复检查点：该命令或外部 MCP 调用可能有副作用，必须人工确认后才能重放。",
				ConfirmationPayload: map[string]any{
					"code": "resume_side_effect_confirmation", "tool": req.ToolName,
				},
			}, true, nil
		}
	}
	return agentkit.ToolInvocationDecision{}, false, nil
}

// FinishToolInvocation 持久化执行结果；超长结果只保存摘要，完整结果由网关落盘。
func (c *ChatService) FinishToolInvocation(res agentkit.ToolInvocationResult) error {
	if c.db == nil || res.CallID == "" {
		return nil
	}
	if strings.TrimSpace(res.TurnID) == "" {
		res.TurnID = c.activeTurnID(res.SessionID)
	}
	result := sanitizedJSON(res.Result)
	if len(result) > 64*1024 {
		result = result[:64*1024] + `…`
	}
	status := res.Status
	if status == "" {
		status = "succeeded"
	}
	updates := map[string]any{
		"status": status, "error_code": res.ErrorCode, "error_message": agentkit.SanitizeSecrets(res.ErrorMessage),
		"before_hash": res.BeforeHash, "after_hash": res.AfterHash,
		"planned_after_hash": res.PlannedAfterHash, "result_json": result,
		"result_bytes": res.ResultBytes, "artifact": res.Artifact,
		"duration_ms": res.Duration.Milliseconds(), "updated_at": time.Now().UTC(),
	}
	return c.db.Model(&toolInvocation{}).Where("id = ?", res.CallID).Updates(updates).Error
}

func (c *ChatService) activeTurnID(sessionID string) string {
	if c.db == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	var turn workflowTurn
	err := c.db.Where("session_id = ? AND status IN ?", sessionID,
		[]string{turnStatusQueued, turnStatusPreparing, turnStatusRunning, turnStatusVerifying}).
		Order("created_at DESC").First(&turn).Error
	if err != nil {
		return ""
	}
	return turn.ID
}

func (c *ChatService) approveToolInvocation(callID string) {
	if c.db == nil || callID == "" {
		return
	}
	_ = c.db.Model(&toolInvocation{}).
		Where("id = ? AND status IN ?", callID, []string{"started", "waiting_approval", "awaiting_confirmation"}).
		Updates(map[string]any{"status": "approved", "updated_at": time.Now().UTC()}).Error
}

func (c *ChatService) turnAncestorIDs(turn *workflowTurn) []string {
	if c.db == nil || turn == nil {
		return nil
	}
	ids := []string{turn.ID}
	current := turn
	for depth := 0; depth < 8 && strings.TrimSpace(current.ParentTurnID) != ""; depth++ {
		parent := c.turnByID(current.ParentTurnID)
		if parent == nil {
			break
		}
		ids = append(ids, parent.ID)
		current = parent
	}
	return ids
}

// toolInvocationSummary 是写入检查点和恢复提示的最小副作用摘要。完整结果可能
// 达到 64KB，绝不进入检查点；恢复判断只需要调用身份、状态和前后哈希。
type toolInvocationSummary struct {
	CallID           string    `json:"callId"`
	ToolName         string    `json:"tool"`
	Source           string    `json:"source,omitempty"`
	Status           string    `json:"status"`
	ErrorCode        string    `json:"errorCode,omitempty"`
	ArgsHash         string    `json:"argsHash,omitempty"`
	BeforeHash       string    `json:"beforeHash,omitempty"`
	AfterHash        string    `json:"afterHash,omitempty"`
	PlannedAfterHash string    `json:"plannedAfterHash,omitempty"`
	Artifact         string    `json:"artifact,omitempty"`
	ResultBytes      int       `json:"resultBytes,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func (c *ChatService) sideEffectsSummary(turnID string, limit int) []toolInvocationSummary {
	if c.db == nil || strings.TrimSpace(turnID) == "" {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	var rows []toolInvocation
	_ = c.db.Where("turn_id = ? AND status IN ?", turnID,
		[]string{"started", "awaiting_confirmation", "succeeded", "failed", "skipped", "approved"}).
		Order("created_at DESC").Limit(limit).Find(&rows).Error
	out := make([]toolInvocationSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, toolInvocationSummary{
			CallID: row.ID, ToolName: row.ToolName, Source: row.Source,
			Status: row.Status, ErrorCode: row.ErrorCode, ArgsHash: row.ArgsHash,
			BeforeHash: row.BeforeHash, AfterHash: row.AfterHash,
			PlannedAfterHash: row.PlannedAfterHash, Artifact: row.Artifact,
			ResultBytes: row.ResultBytes, UpdatedAt: row.UpdatedAt,
		})
	}
	return out
}

// sideEffectsSummaryJSON 在摘要按时间倒序累积时持续检查大小，避免单条大结果或
// 多轮工具调用把检查点撑大。超出预算时保留最近且更重要的尾部证据。
func (c *ChatService) sideEffectsSummaryJSON(turnID string, limit, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 12 * 1024
	}
	rows := c.sideEffectsSummary(turnID, limit)
	for len(rows) > 0 {
		data := sanitizedJSON(rows)
		if len(data) <= maxBytes {
			return data
		}
		rows = rows[:len(rows)-1]
	}
	return ""
}

func sanitizedJSON(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return agentkit.SanitizeSecrets(string(data))
}

func sanitizeForPersistence(text string) string {
	return agentkit.SanitizeSecrets(text)
}

func containsEffect(effects []agentkit.Effect, want agentkit.Effect) bool {
	for _, effect := range effects {
		if effect == want {
			return true
		}
	}
	return false
}

func resultStringForLedger(result map[string]any, key string) string {
	if result == nil {
		return ""
	}
	value, _ := result[key].(string)
	return value
}

func (c *ChatService) recordTrace(trace workflowTrace) {
	if c.db == nil {
		return
	}
	if c.S != nil && c.S.Store != nil && !c.S.Store.Settings().TraceEnabledValue() {
		return
	}
	if trace.ID == "" {
		trace.ID = newID("trace_")
	}
	if trace.CreatedAt.IsZero() {
		trace.CreatedAt = time.Now().UTC()
	}
	trace.DataJSON = agentkit.SanitizeSecrets(trace.DataJSON)
	trace.ErrorMessage = agentkit.SanitizeSecrets(trace.ErrorMessage)
	_ = c.db.Create(&trace).Error
}

// ListTurnTraces 返回单回合 trace 时间线，供运行详情面板使用。
func (c *ChatService) ListTurnTraces(sessionID, turnID string) ([]workflowTrace, error) {
	if c.db == nil {
		return nil, fmt.Errorf("会话数据库不可用")
	}
	var traces []workflowTrace
	query := c.db.Where("session_id = ?", sessionID)
	if strings.TrimSpace(turnID) != "" {
		query = query.Where("turn_id = ?", turnID)
	}
	err := query.Order("created_at ASC").Limit(500).Find(&traces).Error
	return traces, err
}
