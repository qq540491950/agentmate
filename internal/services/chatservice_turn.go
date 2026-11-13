package services

import (
	"context"
	"encoding/base64"
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
	if appEventHook != nil {
		appEventHook(ev)
		return
	}
	if app := application.Get(); app != nil {
		app.Event.Emit(EventChatStream, ev)
	}
}

// sendTurn 对用户输入做净化处理（护栏），构建消息内容并将该回合交给
// runTurn。turnReasoning 是按回合覆盖的思考级别（为空则保留会话默认值）。
func (c *ChatService) sendTurn(sessionID, text string, attachments []AttachmentIn, turnReasoning string) error {
	lock := c.sessionWorkspaceLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}

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
			c.emitChat(sessionID, ChatStreamEvent{Kind: "error", Author: "guardrail",
				Text: "⚠️ 检测到疑似提示注入内容（“" + gr.InjectMatch + "”），已原样发送但请谨慎对待模型输出。"})
		}
		if gr.Redacted {
			c.emitChat(sessionID, ChatStreamEvent{Kind: "error", Author: "guardrail",
				Text: "🔒 已自动遮蔽消息中的疑似密钥/凭据。"})
		}
	}

	msg, err := buildUserContent(userText, attachments)
	if err != nil {
		return err
	}
	return c.runTurn(turnTarget, guardOn, msg, text, turnContext, turnReasoning)
}

// ConfirmToolCall 响应待处理的 ADK 工具确认中断（HITL）：approved=true
// 会恢复原始工具调用，false 会拒绝该调用，使模型看到拒绝并能够调整。
func (c *ChatService) ConfirmToolCall(sessionID, callID string, approved bool) error {
	if strings.TrimSpace(callID) == "" {
		return fmt.Errorf("缺少确认请求 ID")
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
	return c.runTurn(turnTarget, guardOn, msg, "", turnContext, lastUserReasoning)
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
func (c *ChatService) runTurn(target *config.ChatSession, guardOn bool, msg *genai.Content, titleFromText, turnContext, turnReasoning string) (err error) {
	sessionID := target.ID
	defer c.recoverRun(sessionID, &err)
	// 在任何设置之前注册。IsRunning 为前端的会话校准提供支持，并且必须在 Agent
	// 树构建期间就已报告本回合；如果在高开销设置之后（或在用户事件之后）才注册，就
	// 会让并发探测看到错误的空闲状态。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := c.registerRun(sessionID, cancel)
	defer release()
	cfg := c.S.Store.Get()
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
	maxToolCalls := 0
	var maxSessionTokens int64
	if agentCfg := c.targetAgentConfig(cfg, target); agentCfg != nil {
		maxToolCalls = agentCfg.MaxToolCalls
		maxSessionTokens = agentCfg.MaxSessionTokens
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
		ac, derr := directAgentConfig(cfg)
		if derr != nil {
			return derr
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

	emit := func(ev ChatStreamEvent) { c.emitChat(sessionID, ev) }

	if budgetExceeded && titleFromText != "" {
		// 仅拒绝新的用户回合；待处理的 HITL
		// 确认恢复仍必须运行，以便进行中的工具往返能够完成。
		emit(ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("会话 token 预算已满（%d），本轮未执行。", maxSessionTokens)})
		emit(ChatStreamEvent{Kind: "done"})
		return nil
	}
	if titleFromText != "" {
		emit(ChatStreamEvent{Kind: "user", Text: titleFromText, Attachments: attachmentsOut(msg), Reasoning: recordedLevel})
	}

	// 将级别记录在用户消息事件本身，以便历史渲染、导出以及 Regenerate/confirm
	// 恢复时能看到本回合实际使用的级别。一次 HITL 恢复会追加一个 FunctionResponse
	// 事件，而不是一条新的用户消息，因此它不会记录任何内容。
	// 状态增量按顺序应用，且最后一个 RunOption 生效，因此这两个键要放入同一个
	// map。技能键带有 "temp:" 前缀：ADK
	// 会在本次调用中应用它，但在持久化用户事件之前会将其裁剪掉。
	stateDelta := map[string]any{}
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

	var turnUsage *UsageInfo
	var sessionUsage *UsageInfo
	toolCallsSeen := 0
	lastReply := ""
	// MaxLLMCalls 被有意固定为 "no limit"。ADK v2.7.0
	// 引入了按调用计的模型调用预算，其零值会解析为
	// 500（当该环境变量可解析时，也会解析为
	// ADK_MAX_LLM_CALLS），一旦达到该预算，就会以 ErrLLMCallsLimitExceeded
	// 中止整个运行。这与本应用 "一个回合没有实际耗时限制"
	// 的约定相矛盾：否则，一个不断产生工具调用（plan_todos 重写、大量 MCP
	// 工具、团队成员）的长任务，就会终止在一个不可见、由框架选择的阈值上，而用户只
	// 有撞上该阈值时才能发现它。结束一个回合仍然是模型或用户的决定。
	for ev, err := range rn.Run(ctx, c.userID, sessionID, msg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
		MaxLLMCalls:   -1,
	}, runOpts...) {
		if ctx.Err() != nil {
			emit(ChatStreamEvent{Kind: "error", Text: "已取消"})
			return context.Canceled
		}
		if err != nil {
			// 失败仅会流式发送到 UI；没有这一行，磁盘上不会留下任何痕迹（供应商 429 /
			// 配额错误此前在事后无法诊断）。
			log.Printf("会话 %s 轮次失败: %v", sessionID, err)
			emit(ChatStreamEvent{Kind: "error", Text: err.Error()})
			// 将失败暴露给调用方，而不是报告成功。
			return fmt.Errorf("run turn: %w", err)
		}
		if ev == nil || ev.Content == nil || ev.Author == "user" {
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
		if u := usageOf(ev); u != nil {
			// 一个带工具循环 /
			// 团队转交的回合会进行多次模型调用；应累加，而不是仅保留最后一次调用的数字（
			// 这会少报多步骤回合）。
			turnUsage = addUsage(turnUsage, u)
			sessionUsage = addUsage(sessionUsage, u)
		}
		if maxSessionTokens > 0 && !budgetExceeded && budgetBaseline+sessionUsageTotal(sessionUsage) > maxSessionTokens {
			budgetExceeded = true
		}
		var textBuf strings.Builder
		var thoughtBuf strings.Builder
		toolCallsThisTurn := 0
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
			lastReply = outText
			emit(ChatStreamEvent{Kind: "message", Author: author, Text: outText, Usage: turnUsage})
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
				// HITL 暂停点：呈现可操作的确认请求，并让回合就此结束；答案通过
				// ConfirmToolCall 以 FunctionResponse 恢复的形式到达。
				name, args := p.FunctionCall.Name, p.FunctionCall.Args
				hint := ""
				if orig, h := confirmRequestInfo(p.FunctionCall.Args); orig != nil {
					name, args, hint = orig.Name, orig.Args, h
					hint = confirmRequestReason(previewGuard, orig, hint)
				}
				emit(ChatStreamEvent{Kind: "confirm_request", Author: author,
					ToolName: name, ToolArgs: args, CallID: p.FunctionCall.ID, Hint: hint})
			case p.FunctionCall != nil:
				toolCallsThisTurn++
				c.audit(sessionID, "call", author, p.FunctionCall.Name, mustJSON(auditToolCallArgs(p.FunctionCall)), "")
				emit(ChatStreamEvent{Kind: "tool_call", Author: author,
					ToolName: p.FunctionCall.Name, ToolArgs: p.FunctionCall.Args, CallID: p.FunctionCall.ID})
			case p.FunctionResponse != nil:
				c.audit(sessionID, "result", author, p.FunctionResponse.Name,
					mustJSON(p.FunctionResponse.Response), resultErrorOf(p.FunctionResponse.Response))
				emit(ChatStreamEvent{Kind: "tool_result", Author: author,
					ToolName: p.FunctionResponse.Name, ToolResp: p.FunctionResponse.Response, CallID: p.FunctionResponse.ID})
			case p.Text != "" && !p.Thought:
				textBuf.WriteString(p.Text)
			}
		}
		flushThought()
		flushText()
		if maxToolCalls > 0 && toolCallsThisTurn > 0 {
			toolCallsSeen += toolCallsThisTurn
			if toolCallsSeen > maxToolCalls {
				emit(ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("已达单轮工具调用上限（%d 次），本轮已停止。", maxToolCalls)})
				emit(ChatStreamEvent{Kind: "done", SessionUsage: sessionUsage})
				cancel()
				return nil
			}
		}
	}
	if budgetExceeded {
		emit(ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("已达会话 token 预算（%d），后续轮次将被拒绝；请开新会话或调整预算。", maxSessionTokens)})
	}
	emit(ChatStreamEvent{Kind: "done", SessionUsage: sessionUsage})

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
					cfg.Sessions[i].Preview = truncateRunes(lastReply, 80)
				}
				if titleFromText != "" && cfg.Sessions[i].Title == "新对话" {
					cfg.Sessions[i].Title = truncateRunes(titleFromText, 30)
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
