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

// compactionEventRetentionSize keeps the current turn and a short recent tail
// raw when tail-retention compaction fires. ADK requires a positive value
// whenever TokenThreshold is enabled.
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
	// Context occupancy never accumulates: the last call's prompt wins.
	m.ContextTokens = u.ContextTokens
	// Same for the cached share of that prompt — it describes the current
	// context, not a cumulative total (and re-summing it would report cache
	// hits that never happened).
	m.CachedTokens = u.CachedTokens
	return &m
}

// Send runs one user turn against the session's target agent/team. It blocks
// until the run completes and streams progress via chat:stream events.
func (c *ChatService) Send(sessionID, text string, attachments []AttachmentIn) error {
	return c.sendTurn(sessionID, text, attachments, "")
}

// SendWithReasoning runs one user turn with a per-turn thinking level (empty
// keeps the session default, then the model's own default). It is what the
// composer's per-message picker calls: the level applies to this turn only and
// is never written back to the session or provider configuration.
func (c *ChatService) SendWithReasoning(sessionID, text string, attachments []AttachmentIn, reasoningLevel string) error {
	return c.sendTurn(sessionID, text, attachments, reasoningLevel)
}

// trimLastTurn removes all stored events from the last user message onward
// (inclusive) and returns that message's text, image attachments and thinking
// level, so Regenerate/EditAndResend re-run the turn faithfully — including
// the level it originally used. HITL resume events (a FunctionResponse stored
// under the user author by ConfirmToolCall) are not user messages and never
// start a turn boundary — treating them as one made every regenerate after a
// tool confirmation fail.
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

// hasFunctionResponse reports whether the event carries a FunctionResponse
// part (HITL resumes and tool results), i.e. it is not a plain user message.
func hasFunctionResponse(ev *session.Event) bool {
	for _, p := range ev.Content.Parts {
		if p != nil && p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

var _ = context.Background

// Regenerate redoes the last user turn: it trims the trailing conversation
// (from the last user message onward, inclusive) and re-runs the same message
// with its original attachments.
func (c *ChatService) Regenerate(sessionID string) error {
	// Same contract as DeleteSession: stop the in-flight run BEFORE touching
	// stored events, or the old run keeps appending to the trimmed timeline.
	c.cancelAndWaitBounded(sessionID)
	text, atts, reasoning, err := c.trimLastTurn(sessionID)
	if err != nil {
		return err
	}
	return c.sendTurn(sessionID, text, atts, reasoning)
}

// EditAndResend replaces the text of the last user turn and re-runs it,
// keeping the turn's original attachments.
func (c *ChatService) EditAndResend(sessionID, newText string) error {
	if strings.TrimSpace(newText) == "" {
		return fmt.Errorf("消息内容不能为空")
	}
	c.cancelAndWaitBounded(sessionID) // see Regenerate: trim only after the old run stopped
	_, atts, reasoning, err := c.trimLastTurn(sessionID)
	if err != nil {
		return err
	}
	return c.sendTurn(sessionID, newText, atts, reasoning)
}

// deleteEvents removes stored events by ID (used by Regenerate).
func (c *ChatService) deleteEvents(sessionID string, ids []string) error {
	return c.db.WithContext(context.Background()).Exec(
		"DELETE FROM events WHERE app_name = ? AND user_id = ? AND session_id = ? AND id IN ?",
		c.appName, c.userID, sessionID, ids,
	).Error
}

// resolveSessionTarget finds the ChatSession metadata for a session ID.
func (c *ChatService) resolveSessionTarget(cfg config.Config, sessionID string) *config.ChatSession {
	for i := range cfg.Sessions {
		if cfg.Sessions[i].ID == sessionID {
			return &cfg.Sessions[i]
		}
	}
	return nil
}

// emitChat delivers a stream event to the test hook or the Wails event bus.
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

// sendTurn sanitizes the user input (guardrails), builds the message content
// and hands the turn to runTurn. turnReasoning is a per-turn thinking level
// override (empty keeps the session default).
func (c *ChatService) sendTurn(sessionID, text string, attachments []AttachmentIn, turnReasoning string) error {
	lock := c.sessionWorkspaceLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	cfg := c.S.Store.Get()
	target := c.resolveSessionTarget(cfg, sessionID)
	if target == nil {
		return fmt.Errorf("会话 %q 不存在", sessionID)
	}

	// A leading @mention routes just this turn; guardrails follow the turn's
	// effective target so an @'d agent's own guardrail setting applies.
	turnTarget := turnTargetFor(cfg, target, text)
	// A leading "/<skill-name>" command attaches that skill's SKILL.md to this
	// turn only (see runTurn). The message text itself stays untouched: it is
	// what history renders, and Regenerate/EditAndResend re-parse it verbatim.
	// Discovery is resolved against this chat's effective workspace so project
	// SKILL.md files never have to be imported into config.json first.
	workspace := target.EffectiveWorkspaceFor(cfg)
	skillCfg, skillArgs := resolveSkillCommand(effectiveSkillsFor(cfg, workspace), text)
	turnContext := joinTurnContexts(
		buildSkillTurnContext(skillCfg, skillArgs),
		buildFileMentionContext(workspace, text),
	)
	// Layered guardrails: sanitize user input; flag injection attempts.
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

// ConfirmToolCall answers a pending ADK tool-confirmation interrupt (HITL):
// approved=true resumes the original tool call, false rejects it so the model
// sees the rejection and can adjust.
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
	// A HITL resume must rebuild the SAME agent tree the interrupted turn ran
	// on — including a turn that was routed by a leading @mention in the
	// originating user message.
	turnTarget := turnTargetFor(cfg, target, lastUserText)
	agentCfg := c.targetAgentConfig(cfg, turnTarget)
	guardOn := agentCfg == nil || agentkit.GuardrailsEnabled(*agentCfg)
	// A resumed turn recomputes the turn context (slash skill and @-referenced
	// files) from the same user message: temporary state is not persisted, so
	// the confirmation round-trip must re-attach what the interrupted turn
	// started with.
	workspace := target.EffectiveWorkspaceFor(cfg)
	skillCfg, skillArgs := resolveSkillCommand(effectiveSkillsFor(cfg, workspace), lastUserText)
	turnContext := joinTurnContexts(
		buildSkillTurnContext(skillCfg, skillArgs),
		buildFileMentionContext(workspace, lastUserText),
	)
	// Resume on the level the interrupted turn used: a HITL round-trip is one
	// logical turn, so the continuation must not switch thinking budgets
	// halfway through.
	return c.runTurn(turnTarget, guardOn, msg, "", turnContext, lastUserReasoning)
}

// runTurn builds the agent tree for the session target, runs one turn with
// the given message content and streams progress. It is the shared engine
// behind Send (user text) and ConfirmToolCall (HITL FunctionResponse resume);
// titleFromText is non-empty only for genuine user messages (drives the
// user-bubble event and auto-title). turnReasoning is a per-turn level
// override (empty keeps the session default), resolved once here so the
// request body and the recorded badge cannot disagree.
//
// The turn context carries the slash-invoked skill body and the inlined
// content of @-referenced workspace files. It rides on temporary session
// state, so it reaches the model for this invocation only.
//
// A panic anywhere in the agent/tool stack is converted into an error event:
// one misbehaving MCP server or tool must not take the whole desktop process
// down with the user's unsaved work in it.
func (c *ChatService) runTurn(target *config.ChatSession, guardOn bool, msg *genai.Content, titleFromText, turnContext, turnReasoning string) (err error) {
	sessionID := target.ID
	defer c.recoverRun(sessionID, &err)
	// Register before any setup. IsRunning powers the frontend's session
	// calibration and must already report this turn while the agent tree is
	// being built; registering after the expensive setup (or after the user
	// event) lets a concurrent probe see a false idle state.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := c.registerRun(sessionID, cancel)
	defer release()
	cfg := c.S.Store.Get()
	sessionTerminal := c.terminalSettingsFor(sessionID, cfg.Settings.Terminal)
	workspace := target.EffectiveWorkspaceFor(cfg)
	// Per-turn pick > session default > model default. With no resolvable
	// model (remote targets) there is nothing to inject or record.
	applyLevel, recordedLevel := "", ""
	modelInfo, hasModel := c.effectiveTurnModel(cfg, target)
	if hasModel {
		applyLevel, recordedLevel = resolveTurnReasoning(modelInfo, turnReasoning, target.ReasoningLevel)
	}
	// Per-agent tool guardrail (0 = unlimited). Turns have no wall-clock
	// deadline: long agent tasks are allowed to run until they finish, fail,
	// or are cancelled. A stalled model stream is bounded separately by the
	// LLM adapter's idle watchdog.
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

	// Build the agent tree from the current configuration. The session's
	// approval policy and sandbox profile are independent dimensions; direct
	// chats, @-routed turns and every local team member share both.
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
		// Direct chat: no configured target, run the default provider's
		// model with a generic prompt. The per-session model override still
		// applies so the header picker works the same as agent sessions.
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
				// Per-session model override (chat-header picker): empty keeps
				// the agent's configured model.
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
	// Context compaction: sliding window over completed turns and/or tail
	// retention at a token threshold. Local LLM-rooted targets only — the
	// default summarizer rides the root agent's model, which remote A2A
	// agents do not have.
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
				// Keep the current turn and a small tail raw; ADK requires a
				// positive retention size whenever TokenThreshold is enabled.
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
		// Refuse new user turns only; pending HITL confirm resumes must still
		// run so an in-flight tool round-trip can finish.
		emit(ChatStreamEvent{Kind: "error", Text: fmt.Sprintf("会话 token 预算已满（%d），本轮未执行。", maxSessionTokens)})
		emit(ChatStreamEvent{Kind: "done"})
		return nil
	}
	if titleFromText != "" {
		emit(ChatStreamEvent{Kind: "user", Text: titleFromText, Attachments: attachmentsOut(msg), Reasoning: recordedLevel})
	}

	// Record the level on the user message event itself so history rendering,
	// exports and Regenerate/confirm resume see the level this turn actually
	// used. A HITL resume appends a FunctionResponse event, not a new user
	// message, so it records nothing.
	// State deltas apply in order and the last RunOption wins, so both keys go
	// into ONE map. The skill key is "temp:"-prefixed: ADK applies it to this
	// invocation but trims it before persisting the user event.
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
	// Remote A2A agents have no local instruction provider, so a skill command
	// or file context must ride on the message itself to take effect there.
	if turnContext != "" && target.TargetType == "remote" && msg != nil {
		msg.Parts = append([]*genai.Part{genai.NewPartFromText(turnContext + "\n\n用户输入：")}, msg.Parts...)
	}

	var turnUsage *UsageInfo
	var sessionUsage *UsageInfo
	toolCallsSeen := 0
	lastReply := ""
	// MaxLLMCalls is pinned to "no limit" on purpose. ADK v2.7.0 introduced a
	// per-invocation model-call budget whose zero value resolves to 500 (or to
	// ADK_MAX_LLM_CALLS when that env var parses), and reaching it aborts the
	// whole run with ErrLLMCallsLimitExceeded. That contradicts this app's
	// "a turn has no wall-clock limit" contract: a long task that churns
	// through tool calls (plan_todos rewrites, many MCP tools, team members)
	// would otherwise die at an invisible, framework-chosen threshold that the
	// user can only discover by hitting it. Ending a turn stays the model's or
	// the user's decision.
	for ev, err := range rn.Run(ctx, c.userID, sessionID, msg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
		MaxLLMCalls:   -1,
	}, runOpts...) {
		if ctx.Err() != nil {
			emit(ChatStreamEvent{Kind: "error", Text: "已取消"})
			return context.Canceled
		}
		if err != nil {
			// The failure is only streamed to the UI; without this line it
			// leaves no trace on disk (provider 429s / quota errors were
			// previously undiagnosable after the fact).
			log.Printf("会话 %s 轮次失败: %v", sessionID, err)
			emit(ChatStreamEvent{Kind: "error", Text: err.Error()})
			// Surface the failure to the caller instead of reporting success.
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
					// Guardrails must cover the streaming path too: deltas reach
					// the UI before the final aggregated message is sanitized.
					// A secret split across two deltas is only caught by the
					// final message event, which replaces the streamed text.
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
		// Final event for this model turn.
		if u := usageOf(ev); u != nil {
			// A turn with a tool loop / team transfer makes several model
			// calls; accumulate instead of keeping only the last call's
			// numbers (which under-reported multi-step turns).
			turnUsage = addUsage(turnUsage, u)
			sessionUsage = addUsage(sessionUsage, u)
		}
		if maxSessionTokens > 0 && !budgetExceeded && budgetBaseline+sessionUsageTotal(sessionUsage) > maxSessionTokens {
			budgetExceeded = true
		}
		var textBuf strings.Builder
		var thoughtBuf strings.Builder
		toolCallsThisTurn := 0
		// flushThought/flushText keep the event order of the aggregated parts:
		// reasoning arrives before the answer, so its block must close before
		// the assistant bubble starts.
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
				// HITL pause point: surface an actionable confirmation
				// request and let the turn end; the answer arrives via
				// ConfirmToolCall as a FunctionResponse resume.
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
					ToolName: p.FunctionCall.Name, ToolArgs: p.FunctionCall.Args})
			case p.FunctionResponse != nil:
				c.audit(sessionID, "result", author, p.FunctionResponse.Name,
					mustJSON(p.FunctionResponse.Response), resultErrorOf(p.FunctionResponse.Response))
				emit(ChatStreamEvent{Kind: "tool_result", Author: author,
					ToolName: p.FunctionResponse.Name, ToolResp: p.FunctionResponse.Response})
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

	// Auto-remember: opt-in ingestion of the finished turn into long-term
	// memory. Best-effort — failures must not fail the turn.
	if c.S.Store.Get().Settings.AutoRemember && c.S.Memory != nil {
		if resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
			AppName: c.appName, UserID: c.userID, SessionID: sessionID,
		}); err == nil && resp.Session != nil && c.S.Memory != nil {
			_ = c.S.Memory.AddSessionToMemory(context.Background(), resp.Session)
		}
	}

	// Sidebar preview + auto-title from the first user message.
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
	// Model-generated title replaces the truncated-text fallback once the
	// first exchange completes (best-effort, async — never blocks the turn).
	// The goroutine carries its own recover: it runs outside runTurn's
	// deferred recoverRun, and a panic here would take the whole process down.
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

// confirmRequestReason builds the human-facing reason for a HITL card. It is
// computed on the backend so the frontend only renders ChatStreamEvent.Hint;
// the policy itself stays in agentkit (PreviewGuard) and is never copied here.
func confirmRequestReason(guard agentkit.PreviewGuard, call *genai.FunctionCall, fallback string) string {
	hint := strings.TrimSpace(fallback)
	// ADK always supplies a generic English prompt. Treat that (and an empty
	// hint) as "no application hint" and derive the actionable reason.
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

// isGenericConfirmationHint reports whether ADK supplied its built-in prompt
// instead of an application-specific explanation.
func isGenericConfirmationHint(hint string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(hint)), "please approve or reject")
}

// effectiveTurnModel resolves the ModelInfo of the model this session's root
// agent will run, mirroring the build path: the direct chat's default model
// (with the per-chat override), an agent's model (override included), or the
// team's explicit lead / auto coordinator (teams ignore the chat override,
// exactly like BuildTeamWithWorkspaceAndReasoning). Remote targets have no
// local model, so they report false.
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
		// The build path resolves level fields through FindModel too: a model
		// the provider no longer lists gets no table, so report no table here.
		return config.ModelInfo{}, false
	}
	return mi, true
}

// compactionTokenThreshold converts a context-window percentage into the
// absolute prompt-token threshold ADK expects. Unknown context windows disable
// this strategy rather than guessing a window that could summarize too early.
func compactionTokenThreshold(contextWindow, percent int) int {
	if contextWindow <= 0 || percent <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	return contextWindow * percent / 100
}

// findAgentConfig returns the configured agent with the given ID.
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

// resolveTurnReasoning picks a turn's thinking level from the model's table:
// the per-turn pick first, then the session default, then the table's own
// default. apply is what the request builder receives ("" lets a configured
// table resolve its default); recorded is the level shown on the message.
// A built-in fallback table records nothing unless someone picked a level,
// because nothing is sent in that case.
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

// eventReasoning reads the thinking level recorded on a stored user message
// event (see turnReasoningStateKey).
func eventReasoning(ev *session.Event) string {
	if ev == nil || ev.Actions.StateDelta == nil {
		return ""
	}
	if v, ok := ev.Actions.StateDelta[turnReasoningStateKey].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// autoTitleStart schedules the best-effort title side call. Tests replace it
// with a synchronous runner so scripted fake-model expectations stay stable.
var autoTitleStart = func(run func()) { go run() }

// autoTitleModel resolves the provider/model used for title refinement:
// explicit title settings first, then the global default provider's first
// model ("main model"). The conversation target and its model override are
// intentionally ignored: a weak agent model or a remote target must not
// degrade the sidebar title.
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

// autoTitle asks the configured title model for a short conversation title
// and replaces the truncated-text fallback. Failures keep the fallback
// silently.
func (c *ChatService) autoTitle(sessionID string, cfg config.Config, fallback, userText, reply string) {
	clean, ok := c.generateTitle(cfg, userText, reply)
	if !ok {
		return
	}
	err := c.S.Store.Update(func(cfg2 *config.Config) {
		for i := range cfg2.Sessions {
			// Only replace OUR fallback: a manual rename in between wins.
			if cfg2.Sessions[i].ID == sessionID && cfg2.Sessions[i].Title == fallback {
				cfg2.Sessions[i].Title = clean
			}
		}
	})
	if err != nil {
		return
	}
	// Nudge the sidebar via the dedicated metadata event (a synthetic done
	// would clobber the live usage/context indicators).
	if app := application.Get(); app != nil {
		app.Event.Emit("sessions:updated", sessionID)
	}
}

// generateTitle performs the non-streaming title side call. It returns false
// when no usable title model is configured or the model produced no usable
// text, so callers can keep their existing title/fallback.
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
	// Guardrail the quoted user content just like a normal turn.
	prompt := "根据下面的对话开头，概括一个不超过 12 个汉字的中文标题。只输出标题本身，一行，不要引号、书名号、句号、Markdown 或任何解释；不要把用户消息里的斜杠命令或代码原样作为标题。\n\n用户: " +
		agentkit.ApplyOutputGuardrail(truncateRunes(userText, 200)) +
		"\n助手: " + agentkit.ApplyOutputGuardrail(truncateRunes(reply, 200))
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		// Thinking models spend output tokens on reasoning_content first; 40
		// tokens can be consumed entirely before the title is emitted. The
		// title itself is still capped below, so a larger budget is cheap.
		Config: &genai.GenerateContentConfig{MaxOutputTokens: 512, Temperature: ptrFloat32(0.3)},
	}
	var title strings.Builder
	for resp, err := range m.GenerateContent(ctx, req, false) {
		if err != nil || ctx.Err() != nil {
			return "", false
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				// Reasoning is display-only. Some providers return it before
				// the answer; concatenating both made the first thought line
				// the sidebar title.
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
		return "", false // empty, non-text, or raw command output
	}
	return clean, true
}

// RefineSessionTitle regenerates an existing session's title from its stored
// first user message and latest assistant reply. It is the explicit action
// for chats created before auto-title existed, or whose fallback was a raw
// slash command; new sessions refine automatically after their first turn.
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

// truncateRunes caps a string at maxRunes without splitting a UTF-8 rune.
// C0 control bytes and DEL are stripped first: model output can carry them
// (e.g. U+0001) and they render as garbage in the sidebar preview and titles.
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
