package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"google.golang.org/adk/v2/agent"
	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/compaction"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/sandbox"
)

// compactionEventRetentionSize 在尾部保留压缩触发时，会将当前回合和最近的一小段尾部保持为原始未压缩状态。只
// 要启用了 TokenThreshold，ADK 就要求该值为正值。
const compactionEventRetentionSize = 10

const (
	defaultMaxToolCalls   = 120
	defaultMaxLLMCalls    = 200
	defaultMaxTurnTokens  = int64(200_000)
	repeatedToolCallLimit = 5
)

func addUsage(total, u *UsageInfo) *UsageInfo {
	if u == nil {
		return total
	}
	if total == nil {
		cp := *u
		return &cp
	}
	m := *total
	m.PromptTokens += u.PromptTokens
	m.CompletionTokens += u.CompletionTokens
	m.TotalTokens += u.TotalTokens
	// 上下文占用绝不累积：最后一次调用的提示词为准。
	m.ContextTokens = u.ContextTokens
	// 该提示词的缓存份额同理——它描述的是当前上下文，而不是累计总量（重复累加会
	// 报告从未发生过的缓存命中）。
	m.CachedTokens = u.CachedTokens
	return &m
}

// Send 针对会话的目标 Agent/团队运行一个用户回合。它会阻塞直到运行完成，并通过
// chat:stream 事件流式传输进度。
func (c *ChatService) Send(sessionID, text string, attachments []AttachmentIn) error {
	return c.sendTurn(sessionID, text, attachments, "")
}

// SendWithReasoning 使用每回合的思考级别运行一个用户回合（空值会保留会话默认值，随后采用模型自身
// 的默认值）。它正是输入框的逐消息选择器所调用的方法：该级别仅应用于本回合，绝
// 不会写回会话或供应商配置。
func (c *ChatService) SendWithReasoning(sessionID, text string, attachments []AttachmentIn, reasoningLevel string) error {
	return c.sendTurn(sessionID, text, attachments, reasoningLevel)
}

// trimLastTurn 从最后一条用户消息开始（含该消息）移除所有已存储事件，并返回该消息的文本、图
// 片附件和思考级别，以便 Regenerate/EditAndResend
// 忠实重新运行该回合——包括其最初使用的级别。HITL 恢复事件（由 ConfirmToolCall
// 存储在用户作者下的
// FunctionResponse）不是用户消息，绝不开启回合边界——将它们视作用户消息曾导致工
// 具确认后的每次重新生成都失败。
func (c *ChatService) trimLastTurn(sessionID string) (string, []AttachmentIn, string, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return "", nil, "", err
	}
	if resp.Session == nil {
		return "", nil, "", fmt.Errorf("会话 %q 不存在", sessionID)
	}
	text := ""
	reasoning := ""
	var atts []AttachmentIn
	var evIDs []string
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil {
			continue
		}
		if ev.Author == "user" && !hasFunctionResponse(ev) {
			evIDs = []string{ev.ID}
			var sb strings.Builder
			atts = nil
			reasoning = eventReasoning(ev)
			for _, p := range ev.Content.Parts {
				if p == nil {
					continue
				}
				switch {
				case p.Text != "" && !p.Thought:
					sb.WriteString(p.Text)
				case p.InlineData != nil && len(p.InlineData.Data) > 0:
					atts = append(atts, AttachmentIn{
						Name: "attachment-" + strconv.Itoa(len(atts)+1),
						MIME: p.InlineData.MIMEType,
						Data: base64.StdEncoding.EncodeToString(p.InlineData.Data),
					})
				}
			}
			text = sb.String()
			continue
		}
		if text != "" || len(atts) > 0 {
			evIDs = append(evIDs, ev.ID)
		}
	}
	if text == "" && len(atts) == 0 {
		return "", nil, "", fmt.Errorf("没有可重新生成的用户消息")
	}
	if c.db != nil && len(evIDs) > 0 {
		if err := c.deleteEvents(sessionID, evIDs); err != nil {
			return "", nil, "", fmt.Errorf("裁剪历史失败: %w", err)
		}
	}
	return text, atts, reasoning, nil
}

// hasFunctionResponse 报告事件是否携带 FunctionResponse 部分（HITL
// 恢复和工具结果），即它并非普通用户消息。
func hasFunctionResponse(ev *session.Event) bool {
	for _, p := range ev.Content.Parts {
		if p != nil && p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

var _ = context.Background

// Regenerate 重做最后一个用户回合：它裁剪末尾对话（从最后一条用户消息开始，含该消息），并
// 使用该消息的原始附件重新运行同一条消息。
func (c *ChatService) Regenerate(sessionID string) error {
	// 与 DeleteSession
	// 相同的约定：先停止进行中的运行，再触碰已存储的事件，否则旧运行会持续向已裁剪
	// 的时间线追加内容。
	c.cancelAndWaitBounded(sessionID)
	text, atts, reasoning, err := c.trimLastTurn(sessionID)
	if err != nil {
		return err
	}
	return c.sendTurn(sessionID, text, atts, reasoning)
}

// EditAndResend 替换最后一个用户回合的文本并重新运行该回合，保留该回合原有的附件。
func (c *ChatService) EditAndResend(sessionID, newText string) error {
	if strings.TrimSpace(newText) == "" {
		return fmt.Errorf("消息内容不能为空")
	}
	c.cancelAndWaitBounded(sessionID) // 参见 Regenerate：仅在旧运行停止后才裁剪
	_, atts, reasoning, err := c.trimLastTurn(sessionID)
	if err != nil {
		return err
	}
	return c.sendTurn(sessionID, newText, atts, reasoning)
}

// deleteEvents 按 ID 删除已存储的事件（由 Regenerate 使用）。
func (c *ChatService) deleteEvents(sessionID string, ids []string) error {
	return c.db.WithContext(context.Background()).Exec(
		"DELETE FROM events WHERE app_name = ? AND user_id = ? AND session_id = ? AND id IN ?",
		c.appName, c.userID, sessionID, ids,
	).Error
}

// resolveSessionTarget 查找会话 ID 对应的 ChatSession 元数据。
func (c *ChatService) resolveSessionTarget(cfg config.Config, sessionID string) *config.ChatSession {
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sessionID {
			return &cfg.Sessions[i]
		}
	}
	return nil
}

// emitChat 将流式事件投递到测试钩子或 Wails 事件总线。
func (c *ChatService) emitChat(sessionID string, ev ChatStreamEvent) {
	ev.SessionID = sessionID
	if ev.Version == 0 {
		ev.Version = 2
	}
	if ev.Seq == 0 {
		ev.Seq = c.eventSeq.Add(1)
	}
	if ev.TurnID == "" {
		c.turnMu.Lock()
		ev.TurnID = c.sessionTurns[sessionID]
		c.turnMu.Unlock()
	}
	if appEventHook != nil {
		appEventHook(ev)
		return
	}
	if app := application.Get(); app != nil {
		app.Event.Emit(EventChatStream, ev)
	}
}

func (c *ChatService) setSessionTurn(sessionID, turnID string) {
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	if turnID == "" {
		delete(c.sessionTurns, sessionID)
		return
	}
	c.sessionTurns[sessionID] = turnID
}

// sendTurn 对用户输入做净化处理（护栏），构建消息内容并将该回合交给
// runTurn。turnReasoning 是按回合覆盖的思考级别（为空则保留会话默认值）。
func (c *ChatService) sendTurn(sessionID, text string, attachments []AttachmentIn, turnReasoning string) error {
	return c.sendTurnWithManifest(sessionID, text, attachments, turnReasoning, nil)
}

func (c *ChatService) sendTurnWithManifest(sessionID, text string, attachments []AttachmentIn, turnReasoning string, manifest *workflowTurn) error {
	lock := c.sessionWorkspaceLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}
	if manifest == nil {
		c.supersedePendingApprovals(sessionID)
	}
	releaseRepo := func() {}
	if strings.TrimSpace(target.ProjectID) != "" {
		releaseRepo = c.lockRepoForTurn(target.EffectiveWorkspaceFor(cfg))
	}
	defer releaseRepo()

	// 开头的 @ 提及仅路由当前回合；护栏遵循该回合的有效目标，因此被 @ 的 Agent
	// 自身的护栏设置会生效。
	turnTarget := turnTargetFor(cfg, target, text)
	// 以 "/<skill-name>" 开头的命令仅将对应技能的 SKILL.md 附加到本回合（见
	// runTurn）。消息文本本身保持不变：历史记录所渲染的就是它，而
	// Regenerate/EditAndResend
	// 会逐字重新解析它。发现逻辑会基于此会话的有效工作区进行解析，因此项目中的
	// SKILL.md 文件绝不需要先导入 config.json。
	workspace := target.EffectiveWorkspaceFor(cfg)
	skillCfg, skillArgs := resolveSkillCommand(effectiveSkillsFor(cfg, workspace), text)
	turnContext := joinTurnContexts(
		buildSkillTurnContext(skillCfg, skillArgs),
		buildFileMentionContext(workspace, text),
	)
	// 分层护栏：清理用户输入；标记注入尝试。
	agentCfg := c.targetAgentConfig(cfg, turnTarget)
	guardOn := agentCfg == nil || agentkit.GuardrailsEnabled(*agentCfg)
	userText := text
	if guardOn {
		gr := agentkit.ApplyInputGuardrail(text)
		userText = gr.Text
		if gr.InjectRisk {
			c.emitChat(sessionID, ChatStreamEvent{Kind: "warning", Author: "guardrail",
				Text: "⚠️ 检测到疑似提示注入内容（“" + gr.InjectMatch + "”），已原样发送但请谨慎对待模型输出。"})
		}
		if gr.Redacted {
			c.emitChat(sessionID, ChatStreamEvent{Kind: "warning", Author: "guardrail",
				Text: "🔒 已自动遮蔽消息中的疑似密钥/凭据。"})
		}
	}

	msg, err := buildUserContent(userText, attachments)
	if err != nil {
		return err
	}
	return c.runTurn(turnTarget, guardOn, msg, agentkit.SanitizeSecrets(text), turnContext, turnReasoning, manifest)
}

// resumeTurnPrompt 以检查点继续一个逻辑回合：不裁剪历史，复用暂停时的运行清单。
func (c *ChatService) resumeTurnPrompt(sessionID, prompt string, manifest *workflowTurn) error {
	return c.sendTurnWithManifest(sessionID, prompt, nil, manifest.ReasoningLevel, manifest)
}

// ConfirmToolCall 响应待处理的 ADK 工具确认中断（HITL）：approved=true
// 会恢复原始工具调用，false 会拒绝该调用，使模型看到拒绝并能够调整。
func (c *ChatService) ConfirmToolCall(sessionID, callID string, approved bool) error {
	if strings.TrimSpace(callID) == "" {
		return fmt.Errorf("缺少确认请求 ID")
	}
	approval, approvalErr := c.validateApproval(sessionID, callID)
	if approvalErr != nil && c.approvalByCallID(callID) != nil {
		return approvalErr
	}
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	if resp.Session == nil {
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}
	pending := false
	var pendingCall *genai.FunctionCall
	lastUserText := ""
	lastUserReasoning := ""
	for ev := range resp.Session.Events().All() {
		if ev == nil || ev.Content == nil || ev.Partial {
			continue
		}
		if ev.Content.Role == genai.RoleUser {
			if !hasFunctionResponse(ev) {
				lastUserReasoning = eventReasoning(ev)
			}
			for _, p := range ev.Content.Parts {
				if p != nil && p.Text != "" {
					lastUserText = p.Text
				}
			}
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			if isConfirmationInterrupt(ev, p.FunctionCall) && p.FunctionCall.ID == callID {
				pending = true
				if orig, _ := confirmRequestInfo(p.FunctionCall.Args); orig != nil {
					pendingCall = orig
				}
			}
			if p.FunctionResponse != nil && p.FunctionResponse.Name == toolconfirmation.FunctionCallName && p.FunctionResponse.ID == callID {
				return fmt.Errorf("该确认请求已被处理")
			}
		}
	}
	if !pending {
		return fmt.Errorf("未找到待确认的工具调用 %s", callID)
	}
	confirmPayload := map[string]any{
		"callID":   callID,
		"decision": map[bool]string{true: "approved", false: "rejected"}[approved],
	}
	for key, value := range escalationAuditFields(pendingCall) {
		confirmPayload[key] = value
	}
	c.audit(sessionID, "confirm", "user", toolconfirmation.FunctionCallName, mustJSON(confirmPayload), "")

	msg := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID:       callID,
			Name:     toolconfirmation.FunctionCallName,
			Response: map[string]any{"confirmed": approved},
		}}},
	}
	lock := c.sessionWorkspaceLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}
	releaseRepo := func() {}
	if strings.TrimSpace(target.ProjectID) != "" {
		releaseRepo = c.lockRepoForTurn(target.EffectiveWorkspaceFor(cfg))
	}
	defer releaseRepo()
	// HITL 恢复必须重建被中断回合所运行的完全相同的 Agent
	// 树——包括由原始用户消息中开头的 @mention 路由的回合。
	turnTarget := turnTargetFor(cfg, target, lastUserText)
	agentCfg := c.targetAgentConfig(cfg, turnTarget)
	guardOn := agentCfg == nil || agentkit.GuardrailsEnabled(*agentCfg)
	// 恢复的回合会从同一条用户消息重新计算回合上下文（斜杠技能和 @
	// 引用的文件）：临时状态不会被持久化，因此确认往返必须重新附加被中断回合开始时
	// 已有的内容。
	workspace := target.EffectiveWorkspaceFor(cfg)
	skillCfg, skillArgs := resolveSkillCommand(effectiveSkillsFor(cfg, workspace), lastUserText)
	turnContext := joinTurnContexts(
		buildSkillTurnContext(skillCfg, skillArgs),
		buildFileMentionContext(workspace, lastUserText),
	)
	// 按被中断回合所使用的级别恢复：一次 HITL
	// 往返是一个逻辑回合，因此续跑不得在中途切换思考预算。
	var manifest *workflowTurn
	if approval != nil {
		manifest = c.turnByID(approval.TurnID)
		if approved {
			c.setApprovalStatus(callID, "approved")
			c.approveToolInvocation(callID)
		} else {
			c.setApprovalStatus(callID, "rejected")
		}
		if manifest != nil {
			c.recordTrace(workflowTrace{
				SessionID: sessionID, TurnID: manifest.ID, Kind: "approval",
				Status: map[bool]string{true: "approved", false: "rejected"}[approved],
				Name:   approval.ToolName, CallID: callID,
				DurationMs: time.Since(approval.CreatedAt).Milliseconds(),
				DataJSON:   sanitizedJSON(map[string]any{"callId": callID}),
			})
		}
	} else if pending := c.latestWaitingTurn(sessionID); pending != nil {
		manifest = pending
	}
	return c.runTurn(turnTarget, guardOn, msg, "", turnContext, lastUserReasoning, manifest)
}

// runTurn 为会话目标构建 Agent
// 树，用给定消息内容运行一个回合，并流式输出进度。它是 Send（用户文本）与
// ConfirmToolCall（HITL FunctionResponse 恢复）共用的引擎；titleFromText
// 仅对真正的用户消息非空（驱动用户气泡事件与自动标题）。turnReasoning
// 是每回合的级别覆盖（为空则保持会话默认值），在此处统一解析一次，使请求体与记
// 录的徽标不会互相矛盾。
//
// 回合上下文携带斜杠调用的技能正文以及 @
// 引用的工作区文件的内联内容。它依附于临时会话状态，因此仅本次调用会到达模型。
//
// agent/tool 栈中任何位置的 panic 都会被转换为错误事件：一个行为异常的 MCP
// 服务器或工具不得让整个桌面进程连同用户未保存的工作一起崩溃。
func (c *ChatService) runTurn(target *config.ChatSession, guardOn bool, msg *genai.Content, titleFromText, turnContext, turnReasoning string, manifest *workflowTurn) (err error) {
	sessionID := target.ID
	if c.workflowErr != nil {
		return fmt.Errorf("工作流存储不可用: %w", c.workflowErr)
	}
	if c.S != nil && c.S.Kit != nil {
		c.S.Kit.SetWorkflowAnchors(c.buildWorkflowAnchors)
	}
	// 在任何设置之前注册。IsRunning 为前端的会话校准提供支持，并且必须在 Agent
	// 树构建期间就已报告本回合；如果在高开销设置之后（或在用户事件之后）才注册，就
	// 会让并发探测看到错误的空闲状态。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := c.registerRun(sessionID, cancel)
	defer release()
	cfg := c.S.Store.Get()
	var savedSnapshot *turnAgentSnapshot
	if manifest != nil && strings.TrimSpace(manifest.AgentSnapshotJSON) != "" {
		var serr error
		cfg, savedSnapshot, serr = applyAgentSnapshot(cfg, manifest.AgentSnapshotJSON)
		if serr != nil {
			return serr
		}
	}
	sessionTerminal := c.terminalSettingsFor(sessionID, cfg.Settings.Terminal)
	workspace := target.EffectiveWorkspaceFor(cfg)
	// 每回合选择 > 会话默认 >
	// 模型默认。在没有可解析的模型（远程目标）时，没有可注入或记录的内容。
	applyLevel, recordedLevel := "", ""
	modelInfo, hasModel := c.effectiveTurnModel(cfg, target)
	if hasModel {
		applyLevel, recordedLevel = resolveTurnReasoning(modelInfo, turnReasoning, target.ReasoningLevel)
	}
	// 每个 Agent 的工具护栏（0 = 不限）。回合没有墙钟截止时间：允许长时间运行的
	// Agent 任务持续运行，直到完成、失败或被取消。停滞的模型流式由 LLM
	// 适配器的空闲看门狗单独限界。
	maxToolCalls := defaultMaxToolCalls
	maxModelCalls := defaultMaxLLMCalls
	maxTurnTokens := defaultMaxTurnTokens
	var maxSessionTokens int64
	if agentCfg := c.targetAgentConfig(cfg, target); agentCfg != nil {
		if agentCfg.MaxToolCalls > 0 {
			maxToolCalls = agentCfg.MaxToolCalls
		} else if agentCfg.MaxToolCalls < 0 {
			maxToolCalls = 0
		}
		maxSessionTokens = agentCfg.MaxSessionTokens
		if agentCfg.MaxTurnTokens > 0 {
			maxTurnTokens = agentCfg.MaxTurnTokens
		}
	}
	if hasModel && modelInfo.ContextWindow > 0 {
		contextCap := int64(modelInfo.ContextWindow) * 4 / 5
		if maxTurnTokens == defaultMaxTurnTokens || maxTurnTokens > contextCap {
			maxTurnTokens = contextCap
		}
	}
	var budgetBaseline int64
	budgetExceeded := false
	if maxSessionTokens > 0 {
		if st, err := c.SessionStats(sessionID); err == nil {
			budgetBaseline = st.TokensTotal
			budgetExceeded = budgetBaseline > maxSessionTokens
		}
	}

	// 根据当前配置构建 Agent
	// 树。会话的审批策略与沙箱配置是相互独立的维度；直接聊天、@
	// 路由的回合以及每个本地团队成员都共享这两者。
	approvalPolicy := target.EffectiveApprovalPolicy(cfg)
	sandboxProfile := target.EffectiveSandboxProfile(cfg)
	sandboxNetwork := target.EffectiveSandboxNetwork(cfg)
	if manifest != nil {
		if strings.TrimSpace(manifest.Workspace) != "" {
			workspace = manifest.Workspace
		}
		if manifest.ApprovalPolicy != "" {
			approvalPolicy = manifest.ApprovalPolicy
		}
		if manifest.SandboxProfile != "" {
			sandboxProfile = manifest.SandboxProfile
		}
		if manifest.SandboxNetwork != "" {
			sandboxNetwork = manifest.SandboxNetwork
		}
		if manifest.ReasoningLevel != "" && turnReasoning == "" {
			turnReasoning = manifest.ReasoningLevel
			if hasModel {
				applyLevel, recordedLevel = resolveTurnReasoning(modelInfo, turnReasoning, target.ReasoningLevel)
			}
		}
	}
	modelID := ""
	if hasModel {
		modelID = modelInfo.ID
	}
	snapshot := ""
	parentTurnID := ""
	budgetOverride := false
	if manifest != nil {
		snapshot = manifest.AgentSnapshotJSON
		parentTurnID = manifest.ID
		budgetOverride = manifest.Status == turnStatusBudgetPaused
	}
	if snapshot == "" {
		snapshot, _ = snapshotForTurn(cfg, target)
	}
	turn, terr := c.beginTurn(target, workspace, approvalPolicy, sandboxProfile, sandboxNetwork, modelID, recordedLevel, parentTurnID, snapshot, budgetOverride)
	if terr != nil {
		return terr
	}
	c.setSessionTurn(sessionID, turn.ID)
	defer c.setSessionTurn(sessionID, "")
	emit := func(ev ChatStreamEvent) { c.emitChat(sessionID, ev) }
	doneEmitted := false
	errorSent := false
	stateEmitted := false
	var sessionUsage *UsageInfo
	var turnUsage *UsageInfo
	finishReason := ""
	turnStatus := turnStatusQueued
	emitDone := func() {
		if doneEmitted {
			return
		}
		doneEmitted = true
		emit(ChatStreamEvent{Kind: "done", SessionUsage: sessionUsage})
	}
	emitError := func(text string) {
		text = agentkit.SanitizeSecrets(text)
		if text == "" {
			return
		}
		errorSent = true
		emit(ChatStreamEvent{Kind: "error", Text: text})
	}
	emitTurnState := func(status string) {
		if turnStatus == status && stateEmitted {
			return
		}
		turnStatus = status
		stateEmitted = true
		c.updateTurnStatus(turn.ID, status)
		ev := ChatStreamEvent{Kind: "turn_state", TurnStatus: status}
		if isTerminalTurnStatus(status) {
			ev.SessionUsage = sessionUsage
		}
		emit(ev)
		c.recordTrace(workflowTrace{
			SessionID: sessionID, TurnID: turn.ID, Kind: "state", Status: status,
			DataJSON: sanitizedJSON(map[string]any{"reason": finishReason}),
		})
	}
	// 先注册最终态兜底，再注册 panic 恢复：panic 恢复会先设置 failed，最终态
	// 兜底只负责持久化与补齐 done/turn_state。
	defer func() {
		if !isTerminalTurnStatus(turnStatus) && turnStatus != turnStatusWaitingApproval && turnStatus != turnStatusBudgetPaused {
			switch {
			case err != nil && errors.Is(err, context.Canceled):
				emitTurnState(turnStatusCancelled)
			case err != nil:
				emitTurnState(turnStatusFailed)
			default:
				emitTurnState(turnStatusCompleted)
			}
		} else if !stateEmitted {
			emitTurnState(turnStatus)
		}
		if err != nil && !errorSent {
			emitError(err.Error())
		}
		if err != nil || finishReason == "verification_blocked" {
			code := finishReason
			if code == "" {
				code = "turn_failed"
			}
			message := ""
			if err != nil {
				message = err.Error()
			}
			c.recordTrace(workflowTrace{
				SessionID: sessionID, TurnID: turn.ID, Kind: "error", Status: "failed",
				ErrorCode: code, ErrorMessage: message,
			})
		}
		emitDone()
		c.finishTurn(turn.ID, &turnStatus, &err, finishReason, sessionUsage)
	}()
	defer c.recoverTurnRun(sessionID, turn.ID, &turnStatus, &err)
	emitTurnState(turnStatusQueued)
	emitTurnState(turnStatusPreparing)
	var protectedDirs []string
	if c.S != nil && c.S.Store != nil {
		protectedDirs = append(protectedDirs, c.S.Store.Dir())
	}
	previewGuard := agentkit.NewPreviewGuard(workspace, sandboxProfile, sandboxNetwork, protectedDirs...)
	var root agent.Agent
	switch target.TargetType {
	case "":
		// 直接聊天：没有配置目标时，使用通用提示词运行默认供应商的模型。按会话的模型
		// 覆盖仍然生效，因此头部选择器的工作方式与 Agent 会话相同。
		ac := config.AgentConfig{}
		if savedSnapshot != nil && savedSnapshot.Direct != nil {
			ac = *savedSnapshot.Direct
		} else {
			var derr error
			ac, derr = directAgentConfig(cfg)
			if derr != nil {
				return derr
			}
		}
		ac.SandboxNetwork = sandboxNetwork
		ac.ApplyExecutionPolicy(approvalPolicy, sandboxProfile)
		ac.Terminal = &sessionTerminal
		root, err = c.S.Kit.BuildAgentWithModelWorkspaceAndReasoning(ac, target.ModelOverride, workspace, applyLevel)
	case "team":
		for _, t := range cfg.Teams {
			if t.ID == target.TargetID {
				t.ApprovalPolicy = approvalPolicy
				t.SandboxProfile = sandboxProfile
				t.SandboxMode = config.LegacySandboxMode(approvalPolicy, sandboxProfile)
				t.SandboxNetwork = sandboxNetwork
				t.Terminal = &sessionTerminal
				root, _, err = c.S.Kit.BuildTeamWithWorkspaceAndReasoning(t, workspace, applyLevel)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("团队 %q 不存在", target.TargetID)
		}
	case "remote":
		for _, r := range cfg.RemoteAgents {
			if r.ID == target.TargetID {
				root, err = agentkit.BuildRemoteAgent(r)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("远程 Agent %q 不存在", target.TargetID)
		}
	default:
		for _, a := range cfg.Agents {
			if a.ID == target.TargetID {
				// 按会话的模型覆盖（聊天头部选择器）：为空时保留 Agent 已配置的模型。
				a.SandboxNetwork = sandboxNetwork
				a.ApplyExecutionPolicy(approvalPolicy, sandboxProfile)
				a.Terminal = &sessionTerminal
				root, err = c.S.Kit.BuildAgentWithModelWorkspaceAndReasoning(a, target.ModelOverride, workspace, applyLevel)
				break
			}
		}
		if root == nil {
			return fmt.Errorf("Agent %q 不存在", target.TargetID)
		}
	}
	if err != nil {
		return err
	}

	runnerCfg := runner.Config{
		AppName:           c.appName,
		Agent:             root,
		SessionService:    c.sessions,
		MemoryService:     c.S.Memory,
		AutoCreateSession: true,
	}
	// 上下文压缩：在 token
	// 阈值下，对已完成的回合使用滑动窗口和/或保留尾部。仅限以本地 LLM
	// 为根的目标——默认摘要器沿用根 Agent 的模型，而远程 A2A Agent 不具备该模型。
	if target.TargetType != "remote" {
		se := cfg.Settings
		tokenThreshold := 0
		if hasModel {
			tokenThreshold = compactionTokenThreshold(modelInfo.ContextWindow, se.CompactionPercent())
		}
		if se.CompactionInterval > 0 || tokenThreshold > 0 {
			cc := &compaction.Config{CompactionInterval: se.CompactionInterval}
			if se.CompactionInterval > 0 {
				cc.OverlapSize = 1
			}
			if tokenThreshold > 0 {
				cc.TokenThreshold = tokenThreshold
				// 保留当前回合和一小段尾部为原始内容；只要启用了 TokenThreshold，ADK
				// 就要求保留大小为正数。
				cc.EventRetentionSize = compactionEventRetentionSize
			}
			runnerCfg.Compaction = cc
		}
	}
	rn, err := runner.New(runnerCfg)
	if err != nil {
		return err
	}

	emitTurnState(turnStatusRunning)

	toolCallsSeen := 0
	modelCallsSeen := 0
	turnTokens := int64(0)
	budgetWarned := false
	pauseBudget := func(reason string, limit, used int64) {
		finishReason = "budget_" + reason
		emit(ChatStreamEvent{Kind: "budget_pause", Budget: &BudgetPauseInfo{
			Reason: reason, Limit: limit, Used: used,
			ToolCalls: toolCallsSeen, ModelCalls: modelCallsSeen, TurnTokens: turnTokens,
		}})
		c.recordTrace(workflowTrace{
			SessionID: sessionID, TurnID: turn.ID, Kind: "budget", Status: "paused",
			DataJSON: sanitizedJSON(map[string]any{
				"reason": reason, "limit": limit, "used": used,
				"toolCalls": toolCallsSeen, "modelCalls": modelCallsSeen, "turnTokens": turnTokens,
			}),
		})
		emitTurnState(turnStatusBudgetPaused)
		emitDone()
		cancel()
	}
	if titleFromText != "" {
		emit(ChatStreamEvent{Kind: "user", Text: titleFromText, Attachments: attachmentsOut(msg), Reasoning: recordedLevel})
	}
	if budgetExceeded && titleFromText != "" && !budgetOverride {
		// 软预算不硬杀：用户可继续一次或停止。
		pauseBudget("session_tokens", maxSessionTokens, budgetBaseline)
		return nil
	}

	// 将级别记录在用户消息事件本身，以便历史渲染、导出以及 Regenerate/confirm
	// 恢复时能看到本回合实际使用的级别。一次 HITL 恢复会追加一个 FunctionResponse
	// 事件，而不是一条新的用户消息，因此它不会记录任何内容。
	// 状态增量按顺序应用，且最后一个 RunOption 生效，因此这两个键要放入同一个
	// map。技能键带有 "temp:" 前缀：ADK
	// 会在本次调用中应用它，但在持久化用户事件之前会将其裁剪掉。
	stateDelta := map[string]any{}
	// 临时状态把当前回合/工作区带给工具网关；ADK 会在持久化事件前剥离
	// temp: 前缀，因此不会污染会话历史。
	stateDelta[agentkit.TurnIDStateKey] = turn.ID
	stateDelta[agentkit.WorkspaceStateKey] = workspace
	if titleFromText != "" && recordedLevel != "" {
		stateDelta[turnReasoningStateKey] = recordedLevel
	}
	if turnContext != "" {
		stateDelta[agentkit.SkillContextStateKey] = turnContext
	}
	var runOpts []runner.RunOption
	if len(stateDelta) > 0 {
		runOpts = append(runOpts, runner.WithStateDelta(stateDelta))
	}
	// 远程 A2A Agent
	// 没有本地指令供应商，因此技能命令或文件上下文必须随消息本身一起传递，才能在那
	// 里生效。
	if turnContext != "" && target.TargetType == "remote" && msg != nil {
		msg.Parts = append([]*genai.Part{genai.NewPartFromText(turnContext + "\n\n用户输入：")}, msg.Parts...)
	}

	lastReply := ""
	var transferPath []string
	verifyFailures := 0
	verifyOK := false
	verifyCallIDs := map[string]bool{}
	lastVerifyOutput := ""
	mutatingSeen := false
	toolStarts := map[string]time.Time{}
	budgetPendingReason := ""
	var budgetPendingLimit, budgetPendingUsed int64
	budgetPendingCalls := 0
	budgetPendingResponses := 0
	requestBudgetPause := func(reason string, limit, used int64, calls int) {
		if calls <= 0 {
			return
		}
		budgetPendingReason = reason
		budgetPendingLimit = limit
		budgetPendingUsed = used
		budgetPendingCalls = calls
		budgetPendingResponses = 0
	}
	checkpointSeq := 0
	lastModelAt := time.Now()
	loopToolCallKey := ""
	loopToolCallCount := 0
	// 默认给模型调用一个可诊断的上限；长任务可在 Agent 配置中显式调整。
	adkMaxLLMCalls := maxModelCalls + 2
	if maxModelCalls <= 0 {
		adkMaxLLMCalls = -1
	}
	for ev, err := range rn.Run(ctx, c.userID, sessionID, msg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
		MaxLLMCalls:   adkMaxLLMCalls,
	}, runOpts...) {
		if ctx.Err() != nil {
			if turnStatus == turnStatusBudgetPaused {
				return nil
			}
			emitError("已取消")
			return context.Canceled
		}
		if err != nil {
			// 失败仅会流式发送到 UI；没有这一行，磁盘上不会留下任何痕迹（供应商 429 /
			// 配额错误此前在事后无法诊断）。
			log.Printf("会话 %s 轮次失败: %v", sessionID, err)
			if errors.Is(err, agent.ErrLLMCallsLimitExceeded) {
				pauseBudget("model_calls", int64(maxModelCalls), int64(modelCallsSeen))
				return nil
			}
			emitError(err.Error())
			// 将失败暴露给调用方，而不是报告成功。
			return fmt.Errorf("run turn: %w", err)
		}
		if ev == nil {
			continue
		}
		if ev.ErrorCode != "" || ev.ErrorMessage != "" {
			msg := strings.TrimSpace(ev.ErrorMessage)
			if msg == "" {
				msg = ev.ErrorCode
			}
			if msg == "" {
				msg = "模型返回了错误"
			}
			emitError("模型调用失败：" + msg)
			return fmt.Errorf("model error: %s", msg)
		}
		if ev.Content == nil || ev.Author == "user" {
			continue
		}
		author := displayAuthor(cfg, ev.Author)
		if ev.Partial {
			for _, p := range ev.Content.Parts {
				if p != nil && p.Text != "" {
					text := p.Text
					// 护栏还必须覆盖流式路径：增量会在最终聚合消息被净化之前到达
					// UI。拆分到两个增量中的机密仅会被最终消息事件捕获，而该事件会替换已流式输
					// 出的文本。
					if guardOn {
						text = agentkit.ApplyOutputGuardrail(text)
					}
					kind := "delta"
					if p.Thought {
						kind = "thinking_delta"
					}
					emit(ChatStreamEvent{Kind: kind, Author: author, Text: text})
				}
			}
			continue
		}
		// 本次模型回合的最终事件。
		modelCallsSeen++
		modelDuration := time.Since(lastModelAt)
		lastModelAt = time.Now()
		if u := usageOf(ev); u != nil {
			// 一个带工具循环 /
			// 团队转交的回合会进行多次模型调用；应累加，而不是仅保留最后一次调用的数字（
			// 这会少报多步骤回合）。
			turnUsage = addUsage(turnUsage, u)
			sessionUsage = addUsage(sessionUsage, u)
			turnTokens += u.TotalTokens
		}
		modelTrace := workflowTrace{
			SessionID: sessionID, TurnID: turn.ID, Kind: "model", Status: "succeeded",
			Name: author, DurationMs: modelDuration.Milliseconds(),
		}
		if usage := usageOf(ev); usage != nil {
			modelTrace.PromptTokens = usage.PromptTokens
			modelTrace.CompletionTokens = usage.CompletionTokens
			modelTrace.TotalTokens = usage.TotalTokens
			if hasModel {
				modelTrace.Cost = float64(usage.PromptTokens)*modelInfo.PriceIn/1_000_000 +
					float64(usage.CompletionTokens)*modelInfo.PriceOut/1_000_000
			}
			modelTrace.DataJSON = sanitizedJSON(usage)
		}
		c.recordTrace(modelTrace)
		if maxSessionTokens > 0 && !budgetExceeded && budgetBaseline+sessionUsageTotal(sessionUsage) > maxSessionTokens {
			budgetExceeded = true
		}
		var textBuf strings.Builder
		var thoughtBuf strings.Builder
		toolCallsThisTurn := 0
		// 同一次模型响应内，文本是过程旁白还是最终答复，由该响应是否还包含工具
		// 调用决定。提前扫描比依赖循环顺序更可靠：文本部件通常先于工具调用到达。
		responsePhase := responseMessagePhase(ev.Content.Parts)
		// flushThought/flushText 保持聚合部分的事件顺序：
		// 推理先于答案到达，因此其块必须在助手气泡开始之前关闭。
		flushThought := func() {
			if thoughtBuf.Len() == 0 {
				return
			}
			text := thoughtBuf.String()
			thoughtBuf.Reset()
			if guardOn {
				text = agentkit.ApplyOutputGuardrail(text)
			}
			emit(ChatStreamEvent{Kind: "thinking", Author: author, Text: text})
		}
		flushText := func() {
			if textBuf.Len() == 0 {
				return
			}
			outText := textBuf.String()
			textBuf.Reset()
			if guardOn {
				outText = agentkit.ApplyOutputGuardrail(outText)
			}
			if responsePhase == messagePhaseCommentary {
				lastReply = ""
			} else {
				lastReply = outText
			}
			emit(ChatStreamEvent{Kind: "message", Phase: responsePhase, Author: author, Text: outText, Usage: turnUsage})
			checkpointSeq++
			c.recordCheckpoint(turn.ID, sessionID, "", checkpointSeq)
			turnUsage = nil
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			if p.Text != "" && p.Thought {
				flushText()
				thoughtBuf.WriteString(p.Text)
				continue
			}
			flushThought()
			switch {
			case p.FunctionCall != nil && isConfirmationInterrupt(ev, p.FunctionCall):
				lastReply = ""
				// HITL 暂停点：呈现可操作的确认请求，并让回合就此结束；答案通过
				// ConfirmToolCall 以 FunctionResponse 恢复的形式到达。
				name, args := p.FunctionCall.Name, p.FunctionCall.Args
				hint := ""
				if orig, h := confirmRequestInfo(p.FunctionCall.Args); orig != nil {
					name, args, hint = orig.Name, orig.Args, h
					hint = confirmRequestReason(previewGuard, orig, hint)
				}
				risk, diff := agentkit.ReviewToolCall(workspace, sandboxProfile, sandboxNetwork, name, args)
				diff = agentkit.SanitizeSecrets(diff)
				policy := agentkit.FormatPolicySnapshot(workspace, approvalPolicy, sandboxProfile, sandboxNetwork, modelID, recordedLevel)
				emit(ChatStreamEvent{Kind: "confirm_request", Author: author,
					ToolName: name, ToolArgs: redactMapCopy(args), CallID: p.FunctionCall.ID, Hint: hint,
					Risk: risk, Diff: diff, Policy: policy})
				c.recordApproval(turn.ID, sessionID, p.FunctionCall.ID, name, args, hint)
				emitTurnState(turnStatusWaitingApproval)
			case p.FunctionCall != nil:
				toolCallsThisTurn++
				lastReply = ""
				callKey := p.FunctionCall.Name + ":" + hashBytes([]byte(mustJSON(p.FunctionCall.Args)))
				if callKey == loopToolCallKey {
					loopToolCallCount++
				} else {
					loopToolCallKey = callKey
					loopToolCallCount = 1
				}
				if loopToolCallCount >= repeatedToolCallLimit {
					emitError(fmt.Sprintf("检测到重复工具调用环（%s 连续 %d 次），本轮已停止。", p.FunctionCall.Name, loopToolCallCount))
					emitTurnState(turnStatusFailed)
					emitDone()
					cancel()
					return nil
				}
				if p.FunctionCall.Name == "verify_work" && verifyFailures >= 2 {
					reason := "自动修复 2 轮后验证仍失败，计划已标为 blocked。"
					artifact := c.writeVerificationArtifact(turn.ID, "gate", lastVerifyOutput)
					emit(ChatStreamEvent{Kind: "verification", Verification: &VerificationInfo{
						OK: false, Status: "blocked", Attempts: verifyFailures,
						Artifact: artifact, Output: truncateRunes(lastVerifyOutput, 4000), Reason: reason,
					}})
					c.markVerificationBlocked(sessionID, turn.ID, reason, lastVerifyOutput)
					finishReason = "verification_blocked"
					emitError(reason)
					emitTurnState(turnStatusFailed)
					emitDone()
					cancel()
					return nil
				}
				if p.FunctionCall.Name == "verify_work" {
					verifyCallIDs[p.FunctionCall.ID] = true
					emitTurnState(turnStatusVerifying)
				}
				switch p.FunctionCall.Name {
				case "write_workspace_file", "edit_workspace_file", "run_command", "verify_work", "delegate_task":
					mutatingSeen = true
				}
				toolStarts[p.FunctionCall.ID] = time.Now()
				c.auditTool(sessionID, p.FunctionCall.ID, "call", author, p.FunctionCall.Name, mustJSON(auditToolCallArgs(p.FunctionCall)), "")
				c.recordTrace(workflowTrace{
					SessionID: sessionID, TurnID: turn.ID, Kind: "tool", Status: "started",
					Name: p.FunctionCall.Name, CallID: p.FunctionCall.ID, Author: author,
					DataJSON: sanitizedJSON(map[string]any{"args": p.FunctionCall.Args}),
				})
				emit(ChatStreamEvent{Kind: "tool_call", Author: author,
					ToolName: p.FunctionCall.Name, ToolArgs: redactMapCopy(p.FunctionCall.Args), CallID: p.FunctionCall.ID})
				if p.FunctionCall.Name == "transfer_to_agent" {
					next, _ := p.FunctionCall.Args["agent_name"].(string)
					if transferWouldLoop(transferPath, next) {
						emitError("检测到团队转交循环，本轮已停止。")
						emitTurnState(turnStatusFailed)
						emitDone()
						cancel()
						return nil
					}
					transferPath = append(transferPath, next)
				}
			case p.FunctionResponse != nil:
				if budgetPendingReason != "" {
					budgetPendingResponses++
				}
				started := toolStarts[p.FunctionResponse.ID]
				delete(toolStarts, p.FunctionResponse.ID)
				c.auditTool(sessionID, p.FunctionResponse.ID, "result", author, p.FunctionResponse.Name,
					mustJSON(p.FunctionResponse.Response), resultErrorOf(p.FunctionResponse.Response))
				toolStatus := "succeeded"
				toolCode := ""
				if errText := resultErrorOf(p.FunctionResponse.Response); errText != "" {
					toolStatus = "failed"
					toolCode = "tool_failed"
				}
				duration := time.Duration(0)
				if !started.IsZero() {
					duration = time.Since(started)
				}
				c.recordTrace(workflowTrace{
					SessionID: sessionID, TurnID: turn.ID, Kind: "tool", Status: toolStatus,
					Name: p.FunctionResponse.Name, CallID: p.FunctionResponse.ID, Author: author,
					ErrorCode: toolCode, DurationMs: duration.Milliseconds(),
					DataJSON: sanitizedJSON(map[string]any{"result": p.FunctionResponse.Response}),
				})
				emit(ChatStreamEvent{Kind: "tool_result", Author: author,
					ToolName: p.FunctionResponse.Name, ToolResp: p.FunctionResponse.Response, CallID: p.FunctionResponse.ID})
				if p.FunctionResponse.Name == "plan_todos" {
					if update := planUpdateFromResponse(p.FunctionResponse.Response); update != nil {
						emit(ChatStreamEvent{Kind: "plan", Plan: update})
					}
				}
				if verifyCallIDs[p.FunctionResponse.ID] {
					delete(verifyCallIDs, p.FunctionResponse.ID)
					ok, _ := p.FunctionResponse.Response["ok"].(bool)
					verifyOK = ok
					if !ok {
						verifyFailures++
					}
					lastVerifyOutput = verificationOutput(p.FunctionResponse.Response)
					artifact := ""
					if !ok {
						artifact = c.writeVerificationArtifact(turn.ID, p.FunctionResponse.ID, lastVerifyOutput)
					}
					status := "passed"
					if !ok {
						status = "failed"
					}
					emit(ChatStreamEvent{Kind: "verification", Verification: &VerificationInfo{
						OK: ok, Status: status, Attempts: verifyFailures, Artifact: artifact,
						Output: truncateRunes(lastVerifyOutput, 4000),
					}})
					c.recordTrace(workflowTrace{
						SessionID: sessionID, TurnID: turn.ID, Kind: "verification", Status: status,
						Name: "verify_work", ErrorCode: map[bool]string{true: "", false: "verification_failed"}[ok],
						DataJSON: sanitizedJSON(map[string]any{"output": lastVerifyOutput, "artifact": artifact}),
					})
					emitTurnState(turnStatusRunning)
				}
				checkpointSeq++
				c.recordCheckpoint(turn.ID, sessionID, ev.ID, checkpointSeq)
			case p.Text != "" && !p.Thought:
				textBuf.WriteString(p.Text)
			}
		}
		flushThought()
		flushText()
		if maxToolCalls > 0 && toolCallsThisTurn > 0 {
			toolCallsSeen += toolCallsThisTurn
			if !budgetWarned && toolCallsSeen >= maxToolCalls*8/10 {
				budgetWarned = true
				emit(ChatStreamEvent{Kind: "warning", Text: fmt.Sprintf("已使用 %d/%d 次工具调用，接近单轮上限。", toolCallsSeen, maxToolCalls)})
			}
			if toolCallsSeen >= maxToolCalls {
				requestBudgetPause("tool_calls", int64(maxToolCalls), int64(toolCallsSeen), toolCallsThisTurn)
			}
		}
		if maxModelCalls > 0 && modelCallsSeen >= maxModelCalls && lastReply == "" {
			requestBudgetPause("model_calls", int64(maxModelCalls), int64(modelCallsSeen), toolCallsThisTurn)
		}
		if maxTurnTokens > 0 && turnTokens >= maxTurnTokens && lastReply == "" {
			requestBudgetPause("turn_tokens", maxTurnTokens, turnTokens, toolCallsThisTurn)
		}
		if maxSessionTokens > 0 && !budgetOverride && budgetExceeded && lastReply == "" {
			requestBudgetPause("session_tokens", maxSessionTokens, budgetBaseline+sessionUsageTotal(sessionUsage), toolCallsThisTurn)
		}
		if budgetPendingReason != "" && turnStatus == turnStatusRunning && budgetPendingResponses >= budgetPendingCalls {
			pauseBudget(budgetPendingReason, budgetPendingLimit, budgetPendingUsed)
			return nil
		}
	}
	if turnStatus == turnStatusRunning && strings.TrimSpace(lastReply) == "" {
		finishReason = "empty_response"
		emitError("模型没有返回最终回答，本轮已失败。")
		emitTurnState(turnStatusFailed)
	} else if turnStatus == turnStatusRunning {
		if codeTaskNeedsVerification(workspace, mutatingSeen, c.planHasCompleted(sessionID), verifyOK, verifyFailures, c.verificationExempt(turn.ID)) {
			artifact := c.writeVerificationArtifact(turn.ID, "gate", lastVerifyOutput)
			reason := "代码任务未通过验证，不能标记 completed。"
			if verifyFailures >= 2 {
				reason = "自动修复 2 轮后验证仍失败，计划已标为 blocked。"
			}
			emit(ChatStreamEvent{Kind: "verification", Verification: &VerificationInfo{
				OK: false, Status: "blocked", Attempts: verifyFailures,
				Artifact: artifact, Output: truncateRunes(lastVerifyOutput, 4000),
				Reason: reason,
			}})
			c.markVerificationBlocked(sessionID, turn.ID, reason, lastVerifyOutput)
			finishReason = "verification_blocked"
			emitError(reason)
			emitTurnState(turnStatusFailed)
		} else {
			if !verifyOK && verifyFailures > 0 {
				emit(ChatStreamEvent{Kind: "warning", Text: "验证命令失败；若任务仍需完成，请修复后重新运行 verify_work。"})
			}
			finishReason = "completed"
			emitTurnState(turnStatusCompleted)
		}
	}
	emitDone()

	// 自动记忆：按需选择将已完成的回合写入长期
	// 记忆。尽力而为——失败不得导致回合失败。
	if c.S.Store.Get().Settings.AutoRemember && c.S.Memory != nil {
		if resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
			AppName: c.appName, UserID: c.userID, SessionID: sessionID,
		}); err == nil && resp.Session != nil && c.S.Memory != nil {
			_ = c.S.Memory.AddSessionToMemory(context.Background(), resp.Session)
		}
	}

	// 侧边栏预览 + 基于首条用户消息的自动标题。
	autoFallback := ""
	if lastReply != "" || titleFromText != "" {
		c.S.Store.Update(func(cfg *config.Config) {
			for i := range cfg.Sessions {
				if cfg.Sessions[i].ID != sessionID {
					continue
				}
				cfg.Sessions[i].UpdatedAt = nowUTC()
				if lastReply != "" {
					cfg.Sessions[i].Preview = truncateRunes(agentkit.SanitizeSecrets(lastReply), 80)
				}
				if titleFromText != "" && cfg.Sessions[i].Title == "新对话" {
					cfg.Sessions[i].Title = truncateRunes(agentkit.SanitizeSecrets(titleFromText), 30)
					autoFallback = cfg.Sessions[i].Title
				}
			}
		})
	}
	// 第一轮交互完成后，模型生成的标题会替换基于截断文本的回退标题
	// （尽力而为、异步——绝不阻塞回合）。 该 goroutine 自带 recover：它运行在
	// runTurn 的 deferred recoverRun 之外，此处的 panic 会导致整个进程崩溃。
	if autoFallback != "" && cfg.Settings.AutoTitleEnabled() {
		autoTitleStart(func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("autoTitle panicked (kept fallback title): %v", r)
				}
			}()
			c.autoTitle(sessionID, cfg, autoFallback, titleFromText, lastReply)
		})
	}
	return nil
}

// confirmRequestReason 为 HITL 卡片构建面向用户的理由。它
// 在后端计算，因此前端仅渲染 ChatStreamEvent.Hint； 策略本身保留在
// agentkit（PreviewGuard）中，绝不复制到这里。
func confirmRequestReason(guard agentkit.PreviewGuard, call *genai.FunctionCall, fallback string) string {
	hint := strings.TrimSpace(fallback)
	// ADK 始终提供一个通用的英文提示。将其（以及空的
	// hint）视为“无应用提示”，并据此推导出可操作的理由。
	if hint != "" && !isGenericConfirmationHint(hint) {
		return hint
	}
	if call == nil {
		return "该工具调用需要确认。"
	}
	switch call.Name {
	case "run_command":
		escalate, _ := call.Args["escalate"].(bool)
		if escalate {
			justification, _ := call.Args["justification"].(string)
			justification = strings.TrimSpace(justification)
			if justification == "" {
				return "脱离沙箱执行终端命令。"
			}
			return "脱离沙箱执行终端命令。理由：" + justification
		}
		command, _ := call.Args["command"].(string)
		if risky, reason := sandbox.AssessCommand(command); risky {
			return "该命令需要确认：" + reason + "。"
		}
		return "该命令需要确认。"
	case "write_workspace_file", "edit_workspace_file":
		path, _ := call.Args["path"].(string)
		if ok, code, _ := guard.CheckWritePath(path); !ok {
			switch code {
			case "read_only":
				return "只读模式不允许写入：" + path
			case "protected_path":
				return "受保护路径不可写：" + path
			case "outside_workspace":
				return "工作区外路径不可写：" + path
			default:
				return "写入预检失败：" + path
			}
		}
		if call.Name == "edit_workspace_file" {
			return "修改工作区文件：" + path
		}
		return "写入工作区文件：" + path
	default:
		return "外部工具可能有副作用。"
	}
}

// isGenericConfirmationHint 判断 ADK 提供的是否为其内置提示，
// 而非应用专属说明。
func isGenericConfirmationHint(hint string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(hint)), "please approve or reject")
}

// effectiveTurnModel 解析本会话根 Agent 将要运行的模型的
// ModelInfo，与构建路径保持一致：直接聊天时的默认模型（含按会话的覆盖）、Agent
// 自身的模型（同样包含覆盖），或者团队显式指定的 lead
// 或自动协调者（团队会忽略会话级覆盖，与 BuildTeamWithWorkspaceAndReasoning
// 完全一致）。远程目标没有本地模型，因此返回 false。
func (c *ChatService) effectiveTurnModel(cfg config.Config, target *config.ChatSession) (config.ModelInfo, bool) {
	providerID, modelID := "", ""
	applyOverride := true
	switch target.TargetType {
	case "":
		ac, err := directAgentConfig(cfg)
		if err != nil {
			return config.ModelInfo{}, false
		}
		providerID, modelID = cfg.ResolveAgentModel(ac)
	case "team":
		applyOverride = false
		var team *config.TeamConfig
		for i := range cfg.Teams {
			if cfg.Teams[i].ID == target.TargetID {
				team = &cfg.Teams[i]
				break
			}
		}
		if team == nil {
			return config.ModelInfo{}, false
		}
		lead := findAgentConfig(cfg, team.LeadAgentID)
		if !team.AutoCoordinate && lead != nil {
			providerID, modelID = cfg.ResolveAgentModel(*lead)
		}
		if providerID == "" {
			providerID, modelID = team.ProviderID, team.Model
		}
	case "remote":
		return config.ModelInfo{}, false
	default:
		ac := findAgentConfig(cfg, target.TargetID)
		if ac == nil {
			return config.ModelInfo{}, false
		}
		providerID, modelID = cfg.ResolveAgentModel(*ac)
	}
	provider := cfg.FindProvider(providerID)
	if provider == nil {
		return config.ModelInfo{}, false
	}
	override := ""
	if applyOverride {
		override = target.ModelOverride
	}
	modelID = agentkit.EffectiveModel(cfg, providerID, modelID, override)
	mi, ok := provider.FindModel(modelID)
	if !ok {
		// 构建路径同样通过 FindModel
		// 解析等级字段：供应商已不再列出的模型没有对应的表，因此这里同样报告为无表。
		return config.ModelInfo{}, false
	}
	return mi, true
}

// compactionTokenThreshold 把上下文窗口的百分比转换为 ADK 期望的绝对提示词
// token 阈值。遇到未知的上下文窗口时会禁用该策略，而不是猜测一个可能导致过早压缩的窗
// 口。
func compactionTokenThreshold(contextWindow, percent int) int {
	if contextWindow <= 0 || percent <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	return contextWindow * percent / 100
}

// findAgentConfig 返回具有给定 ID 的已配置 Agent。
func findAgentConfig(cfg config.Config, id string) *config.AgentConfig {
	if id == "" {
		return nil
	}
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == id {
			return &cfg.Agents[i]
		}
	}
	return nil
}

// resolveTurnReasoning 从模型的表中挑选某个回合的思考等级：先取该回合的选择，再取会话默认值，最后取
// 表自身的默认值。apply 是请求构建器实际收到的值（""
// 表示由已配置的表自行解析默认值）；recorded
// 是显示在消息上的等级。内置回退表不会记录任何等级，除非有人显式选择了等级，因
// 为这种情况下不会发送任何内容。
func resolveTurnReasoning(mi config.ModelInfo, explicit, session string) (apply, recorded string) {
	table := mi.ReasoningTable()
	if table == nil {
		return "", ""
	}
	for _, want := range []string{explicit, session} {
		if name, ok := table.MatchLevel(want); ok {
			return name, name
		}
	}
	if mi.ReasoningConfigured() {
		if name := table.DefaultLevelName(); name != "" {
			return "", name
		}
	}
	return "", ""
}

// eventReasoning 读取已存储的用户消息事件上记录的思考等级（参见
// turnReasoningStateKey）。
func eventReasoning(ev *session.Event) string {
	if ev == nil || ev.Actions.StateDelta == nil {
		return ""
	}
	if v, ok := ev.Actions.StateDelta[turnReasoningStateKey].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// autoTitleStart 调度尽力而为的标题旁路调用。测试会把它替换为同步执行器，以便脚本化的假模型预
// 期保持稳定。
var autoTitleStart = func(run func()) { go run() }

// autoTitleModel 解析用于标题润色的供应商/模型：优先使用显式的标题设置，其次使用全局默认供应商
// 的第一个模型（"主模型"）。会话目标及其模型覆盖被有意忽略：弱小的 Agent
// 模型或远程目标不得使侧边栏标题降级。
func autoTitleModel(cfg config.Config) (config.ModelProvider, string, bool) {
	titleProviderID := strings.TrimSpace(cfg.Settings.TitleProviderID)
	titleModel := strings.TrimSpace(cfg.Settings.TitleModel)
	if titleProviderID != "" && titleModel != "" {
		if provider := cfg.FindProvider(titleProviderID); provider != nil {
			if _, ok := provider.FindModel(titleModel); ok {
				return *provider, titleModel, true
			}
		}
	}
	provider := cfg.DefaultProvider()
	if provider == nil {
		return config.ModelProvider{}, "", false
	}
	if len(provider.Models) == 0 {
		return config.ModelProvider{}, "", false
	}
	return *provider, provider.Models[0].ID, true
}

// autoTitle 向配置的标题模型请求一个简短的会话标题，并替换基于截断文本的回退。失败时静默
// 保留该回退。
func (c *ChatService) autoTitle(sessionID string, cfg config.Config, fallback, userText, reply string) {
	clean, ok := c.generateTitle(cfg, userText, reply)
	if !ok {
		return
	}
	err := c.S.Store.Update(func(cfg2 *config.Config) {
		for i := range cfg2.Sessions {
			// 仅替换我们自己的回退：期间发生的手动重命名优先。
			if cfg2.Sessions[i].ID == sessionID && cfg2.Sessions[i].Title == fallback {
				cfg2.Sessions[i].Title = clean
			}
		}
	})
	if err != nil {
		return
	}
	// 通过专用元数据事件通知侧边栏（合成的 done 会覆盖实时用量/上下文指示器）。
	if app := application.Get(); app != nil {
		app.Event.Emit("sessions:updated", sessionID)
	}
}

// generateTitle 执行非流式标题旁路调用。当未配置可用的标题模型，或模型未生成可用文本时，它返
// 回 false，因此调用方可保留其现有的标题/回退。
func (c *ChatService) generateTitle(cfg config.Config, userText, reply string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider, modelID, ok := autoTitleModel(cfg)
	if !ok {
		return "", false
	}
	m, err := agentkit.NewModelFromProvider(provider, modelID)
	if err != nil {
		return "", false
	}
	// 像普通回合一样，对引用的用户内容应用护栏。
	prompt := "根据下面的对话开头，概括一个不超过 12 个汉字的中文标题。只输出标题本身，一行，不要引号、书名号、句号、Markdown 或任何解释；不要把用户消息里的斜杠命令或代码原样作为标题。\n\n用户: " +
		agentkit.ApplyOutputGuardrail(truncateRunes(userText, 200)) +
		"\n助手: " + agentkit.ApplyOutputGuardrail(truncateRunes(reply, 200))
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		// 思考模型会先在 reasoning_content 上消耗输出 token；在标题输出前，40 个
		// token 可能被完全消耗。标题本身仍会在下方受到上限限制，因此更大的预算代价很小。
		Config: &genai.GenerateContentConfig{MaxOutputTokens: 512, Temperature: ptrFloat32(0.3)},
	}
	var title strings.Builder
	for resp, err := range m.GenerateContent(ctx, req, false) {
		if err != nil || ctx.Err() != nil {
			return "", false
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				// 推理内容仅用于显示。部分供应商会在答案之前返回它；将两者拼接曾使第一行思
				// 考内容成为侧边栏标题。
				if p != nil && p.Text != "" && !p.Thought {
					title.WriteString(p.Text)
				}
			}
		}
	}
	clean := strings.TrimSpace(title.String())
	if i := strings.IndexAny(clean, "\r\n"); i >= 0 {
		clean = clean[:i]
	}
	clean = strings.TrimSpace(strings.Trim(clean, " \"'「」『』`#。.!？?\t"))
	for _, prefix := range []string{"会话标题：", "会话标题:", "标题：", "标题:"} {
		clean = strings.TrimSpace(strings.TrimPrefix(clean, prefix))
	}
	clean = truncateRunes(clean, 16)
	if clean == "" || strings.HasPrefix(clean, "{") || strings.HasPrefix(clean, "/") {
		return "", false // 空内容、非文本或原始命令输出
	}
	return clean, true
}

// RefineSessionTitle 根据已存储的首条用户消息和最新助手回复，重新生成现有会话的标题。对于自动标题
// 功能出现之前创建的会话，或回退为原始斜杠命令的会话，这是显式操作；新会话在首
// 个回合后会自动优化。
func (c *ChatService) RefineSessionTitle(sessionID string) (string, error) {
	cfg := c.S.Store.Get()
	if c.resolveSessionTarget(cfg, sessionID) == nil {
		return "", fmt.Errorf("会话 %q 不存在", sessionID)
	}
	msgs, err := c.GetSessionMessages(sessionID)
	if err != nil {
		return "", fmt.Errorf("读取会话历史失败: %w", err)
	}
	var userText, reply string
	for _, m := range msgs {
		switch m.Kind {
		case "user":
			if userText == "" {
				userText = strings.TrimSpace(m.Text)
			}
		case "assistant":
			if strings.TrimSpace(m.Text) != "" {
				reply = strings.TrimSpace(m.Text)
			}
		}
	}
	if userText == "" {
		return "", fmt.Errorf("会话还没有用户消息，无法提炼标题")
	}
	title, ok := c.generateTitle(cfg, userText, reply)
	if !ok {
		return "", fmt.Errorf("标题提炼失败，请检查标题模型配置后重试")
	}
	if err := c.mutateSession(sessionID, func(s *config.ChatSession) { s.Title = title }); err != nil {
		return "", err
	}
	c.audit(sessionID, "session_title_refined", "user", "session", mustJSON(map[string]string{"title": title}), "")
	if app := application.Get(); app != nil {
		app.Event.Emit("sessions:updated", sessionID)
	}
	return title, nil
}

// truncateRunes 在不拆分 UTF-8 rune 的前提下，将字符串截断至 maxRunes
// 长度。先剥离 C0 控制字节和 DEL：模型输出可能携带这些字符（例如
// U+0001），它们在侧边栏预览和标题中会显示为乱码。
func truncateRunes(s string, maxRunes int) string {
	s = strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

func ptrFloat32(f float32) *float32 { return &f }

func transferWouldLoop(path []string, next string) bool {
	if strings.TrimSpace(next) == "" {
		return false
	}
	if len(path) >= 8 {
		return true
	}
	for _, name := range path {
		if name == next {
			return true
		}
	}
	return false
}

func (c *ChatService) planHasCompleted(sessionID string) bool {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil || resp.Session == nil || resp.Session.State() == nil {
		return false
	}
	value, err := resp.Session.State().Get("agentmate.todos")
	if err != nil {
		return false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	var todos []struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &todos) != nil {
		return false
	}
	for _, todo := range todos {
		if todo.Status == "completed" {
			return true
		}
	}
	return false
}
