package services

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
)

func renderEvent(cfg config.Config, ev *session.Event, seq int) []ChatMessage {
	if ev.Content == nil {
		return nil
	}
	role := "assistant"
	if ev.Author == "user" || ev.Content.Role == genai.RoleUser {
		if ev.Author == "user" {
			role = "user"
		}
	}
	var out []ChatMessage
	base := ChatMessage{
		ID:        fmt.Sprintf("%s-%d", ev.ID, seq),
		Author:    displayAuthor(cfg, ev.Author),
		Timestamp: ev.Timestamp,
	}
	var textBuf strings.Builder
	var thoughtBuf strings.Builder
	var attachments []AttachmentOut
	flushText := func() {
		if textBuf.Len() == 0 && len(attachments) == 0 {
			return
		}
		m := base
		m.Kind = role
		m.Text = textBuf.String()
		if role == "user" {
			// The level rides on the user message's state delta; older
			// turns recorded none and simply show none.
			m.Reasoning = eventReasoning(ev)
			// Inline images are parts of this same user event. Keep them on
			// one message: splitting each image into its own user message
			// duplicated the event ID and made the frontend render phantom
			// turns that all highlighted together in the turn rail.
			m.Attachments = attachments
		}
		out = append(out, m)
		textBuf.Reset()
		attachments = nil
	}
	flushThought := func() {
		if thoughtBuf.Len() > 0 {
			m := base
			m.Kind = "thinking"
			m.Text = thoughtBuf.String()
			out = append(out, m)
			thoughtBuf.Reset()
		}
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
		case p.FunctionCall != nil && p.FunctionCall.Name == toolconfirmation.FunctionCallName:
			flushText()
			m := base
			m.Kind = "tool_call"
			m.ToolName = p.FunctionCall.Name
			m.ToolArgs = p.FunctionCall.Args
			m.CallID = p.FunctionCall.ID
			if orig, hint := confirmRequestInfo(p.FunctionCall.Args); orig != nil {
				m.Kind = "confirm_request"
				m.ToolName = orig.Name
				m.ToolArgs = orig.Args
				m.Hint = confirmRequestHint(orig, hint)
			}
			out = append(out, m)
		case p.FunctionCall != nil:
			flushText()
			m := base
			m.Kind = "tool_call"
			m.ToolName = p.FunctionCall.Name
			m.ToolArgs = p.FunctionCall.Args
			m.CallID = p.FunctionCall.ID
			out = append(out, m)
		case p.FunctionResponse != nil && p.FunctionResponse.Name == toolconfirmation.FunctionCallName:
			flushText()
			m := base
			m.Kind = "confirm_result"
			m.CallID = p.FunctionResponse.ID
			m.ToolName = p.FunctionResponse.Name
			m.ToolResp = p.FunctionResponse.Response
			out = append(out, m)
		case p.FunctionResponse != nil:
			flushText()
			m := base
			m.Kind = "tool_result"
			m.ToolName = p.FunctionResponse.Name
			m.ToolResp = p.FunctionResponse.Response
			m.CallID = p.FunctionResponse.ID
			out = append(out, m)
		case p.InlineData != nil && p.InlineData.Data != nil:
			if role == "user" {
				attachments = append(attachments, AttachmentOut{
					Name:    fmt.Sprintf("attachment-%d", len(attachments)+1),
					MIME:    p.InlineData.MIMEType,
					DataURL: "data:" + p.InlineData.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(p.InlineData.Data),
				})
			} else {
				// Assistant inline data is not rendered in chat history; keep
				// the existing text-boundary behavior for those events.
				flushText()
			}
		case p.Text != "" && !p.Thought:
			textBuf.WriteString(p.Text)
		}
	}
	flushThought()
	flushText()
	return out
}

// confirmRequestInfo extracts the original tool call and human-readable hint
// from an adk_request_confirmation FunctionCall's args. The args survive a
// JSON roundtrip once persisted, so the originalFunctionCall field may be a
// *genai.FunctionCall (live) or a generic map (reloaded history).
func confirmRequestInfo(args map[string]any) (*genai.FunctionCall, string) {
	orig, err := toolconfirmation.OriginalCallFrom(&genai.FunctionCall{Args: args})
	if err != nil {
		return nil, ""
	}
	hint := ""
	if tc, ok := args["toolConfirmation"].(map[string]any); ok {
		hint, _ = tc["hint"].(string)
	}
	return orig, hint
}

// confirmRequestHint replaces ADK's generic English prompt for run_command.
// The card needs an unambiguous boundary label: an escalated call must say it
// leaves the sandbox and carry the model's justification; an ordinary call
// must not look like an escalation.
func confirmRequestHint(call *genai.FunctionCall, fallback string) string {
	if call == nil || call.Name != "run_command" {
		return fallback
	}
	escalate, _ := call.Args["escalate"].(bool)
	if !escalate {
		return "执行终端命令。"
	}
	justification, _ := call.Args["justification"].(string)
	justification = strings.TrimSpace(justification)
	if justification == "" {
		return "脱离沙箱执行终端命令。"
	}
	return "脱离沙箱执行终端命令。理由：" + justification
}

// escalationAuditFields returns the stable fields the tool-audit trail uses
// for a per-call sandbox escape. A nil result means the call is not an
// escalated run_command.
func escalationAuditFields(call *genai.FunctionCall) map[string]any {
	if call == nil || call.Name != "run_command" {
		return nil
	}
	escalate, _ := call.Args["escalate"].(bool)
	if !escalate {
		return nil
	}
	fields := map[string]any{"escalated": true}
	if command, ok := call.Args["command"].(string); ok {
		fields["command"] = command
	}
	if justification, ok := call.Args["justification"].(string); ok {
		fields["justification"] = justification
	}
	return fields
}

// auditToolCallArgs preserves the original tool arguments and adds the
// normalized escalation fields (notably escalated=true) for the call record.
func auditToolCallArgs(call *genai.FunctionCall) map[string]any {
	if call == nil {
		return nil
	}
	fields := escalationAuditFields(call)
	if fields == nil {
		return call.Args
	}
	merged := make(map[string]any, len(call.Args)+len(fields))
	for key, value := range call.Args {
		merged[key] = value
	}
	for key, value := range fields {
		merged[key] = value
	}
	return merged
}

// isConfirmationInterrupt reports whether the part is the adk_request_confirmation
// interrupt declared long-running on this event (ADK HITL pause point).
func isConfirmationInterrupt(ev *session.Event, fc *genai.FunctionCall) bool {
	if fc == nil {
		return false
	}
	return fc.Name == toolconfirmation.FunctionCallName && slices.Contains(ev.LongRunningToolIDs, fc.ID)
}

// targetAgentConfig returns the AgentConfig behind a session (nil for teams
// or when the target no longer exists).
func (c *ChatService) targetAgentConfig(cfg config.Config, target *config.ChatSession) *config.AgentConfig {
	if target.TargetType != "agent" {
		return nil
	}
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == target.TargetID {
			return &cfg.Agents[i]
		}
	}
	return nil
}

// directAgentConfig builds the implicit target behind a direct chat: the
// default provider's first model, a workspace-aware prompt, the workspace
// file tools and the terminal tool. Nothing is persisted — direct sessions
// only materialize an agent per turn. Without the file tools a project chat
// could only promise "我来查看项目" and stop: the model had no way to read the
// directory it was asked about (issue #21). Without the shell tool the default
// agent denied having any terminal capability and refused "git pull" (issue
// #40) — the configured agents had run_command since #36, but this hardcoded
// path still narrowed itself to files. Writes and commands stay behind HITL
// confirmation, matching a new agent's defaults.
func directAgentConfig(cfg config.Config) (config.AgentConfig, error) {
	var provider *config.ModelProvider
	for i := range cfg.Providers {
		if cfg.Providers[i].IsDefault {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil && len(cfg.Providers) > 0 {
		provider = &cfg.Providers[0]
	}
	if provider == nil {
		return config.AgentConfig{}, fmt.Errorf("尚未配置模型供应商，请先在「模型配置」页添加并填入 API Key")
	}
	model := ""
	if len(provider.Models) > 0 {
		model = provider.Models[0].ID
	}
	if model == "" {
		return config.AgentConfig{}, fmt.Errorf("默认供应商 %s 尚未配置模型，请先在「模型配置」页添加模型", provider.Name)
	}
	return config.AgentConfig{
		ID:         "direct",
		Name:       directAssistantName,
		ProviderID: provider.ID,
		Model:      model,
		SystemPrompt: "你是 AgentMate 桌面助手。用中文简洁、准确地回答用户问题。\n" +
			"当前任务工作区：{{workspace}}。当问题涉及项目或文件内容时，先调用 " +
			"list_workspace_files 查看目录结构，再按需调用 read_workspace_file 读取文件，" +
			"基于实际内容回答，不要凭空猜测。\n" +
			"多步任务先用 plan_todos 形成简短计划；同一轮把互不依赖的检索、读取和只读命令一起发起，" +
			"不要一次只读一个文件。每完成一批工具或切换阶段，用一句中文说明当前进展。" +
			"只要求分析、排查或解释时默认保持只读，除非用户明确要求修改文件、升级依赖或执行有副作用的操作。" +
			"证据已经足够就先给结论；连续两轮没有获得新证据时停止继续检索。\n" +
			"你具备终端命令工具 run_command，可在工作区内执行 git、构建、测试、安装依赖等命令；" +
			"需要拉取代码、运行命令时直接调用 run_command，不要声称没有终端或 Shell 能力。" +
			"写入文件和执行命令会先请用户确认。",
		BuiltinTools: []string{"files", "shell", "todos"},
		// Approval and the OS boundary come from the session's independent
		// execution policy. Direct chats deliberately add no per-agent
		// override, so auto-approve only asks for risk-detected commands.
	}, nil
}

// mentionMatches reports whether body starts with the mention name followed
// by a token boundary (end, space, newline or full-width space).
func mentionMatches(body, name string) bool {
	if !strings.HasPrefix(body, name) {
		return false
	}
	rest := body[len(name):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || strings.HasPrefix(rest, "　")
}

// resolveMention parses a leading "@名称" mention and returns the turn-scoped
// target it names. Candidates cover agents, teams and remote agents; the
// longest matching name wins so "写手" does not shadow "写手助手". Unresolvable
// mentions leave the turn on the session's own target.
func resolveMention(cfg config.Config, text string) (targetType, targetID string, ok bool) {
	if !strings.HasPrefix(text, "@") || len(text) < 2 {
		return "", "", false
	}
	body := text[1:]
	bestLen := 0
	for _, a := range cfg.Agents {
		if a.Name != "" && mentionMatches(body, a.Name) && len(a.Name) > bestLen {
			bestLen, targetType, targetID = len(a.Name), "agent", a.ID
		}
	}
	for _, t := range cfg.Teams {
		if t.Name != "" && mentionMatches(body, t.Name) && len(t.Name) > bestLen {
			bestLen, targetType, targetID = len(t.Name), "team", t.ID
		}
	}
	for _, r := range cfg.RemoteAgents {
		if r.Name != "" && mentionMatches(body, r.Name) && len(r.Name) > bestLen {
			bestLen, targetType, targetID = len(r.Name), "remote", r.ID
		}
	}
	return targetType, targetID, bestLen > 0
}

// turnTargetFor resolves the per-turn target: a leading @mention in the user
// text routes just this turn to the named agent/team/remote, everything else
// stays on the session's own target. The mention stays in the message text —
// history, Regenerate and EditAndResend re-parse it and re-route consistently.
func turnTargetFor(cfg config.Config, target *config.ChatSession, userText string) *config.ChatSession {
	turn := *target
	if userText != "" {
		if typ, id, ok := resolveMention(cfg, userText); ok {
			turn.TargetType, turn.TargetID = typ, id
		}
	}
	return &turn
}

// effectiveSkillsFor is the slash-command view of the skill set for a chat
// working in workspace: project/global discovery wins over same-named app
// skills, exactly like the agent-build layer (config.EffectiveSkills).
func effectiveSkillsFor(cfg config.Config, workspace string) []config.SkillConfig {
	return config.EffectiveSkills(cfg, workspace, config.GlobalSkillRoot())
}

// resolveSkillCommand parses a leading "/<skill-name>" command against the
// effective skills (app config plus project/global .agents/skills discovery).
// Matching uses the materialized name ADK would expose (lowercase, hyphenated)
// and is case-insensitive, so "/Weekly_Report" finds a skill named
// "Weekly Report". The remainder of the line is returned as the turn's user
// input. Only the first token is considered: the text doubles as chat history,
// so Regenerate and HITL resume can re-parse it verbatim.
func resolveSkillCommand(skills []config.SkillConfig, text string) (*config.SkillConfig, string) {
	t := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(t, "/") {
		return nil, ""
	}
	rest := t[1:]
	cut := strings.IndexAny(rest, " \t\r\n")
	token, args := rest, ""
	if cut >= 0 {
		token, args = rest[:cut], strings.TrimSpace(rest[cut:])
	}
	if token == "" {
		return nil, ""
	}
	cmd := agentkit.MaterializedSkillName(token)
	for i := range skills {
		s := &skills[i]
		if agentkit.MaterializedSkillName(s.Name) == cmd {
			return s, args
		}
	}
	return nil, ""
}

// buildSkillTurnContext renders the instruction block injected into a turn
// started by a "/<skill-name>" command. args is the text after the command, so
// the model gets the task spelled out even though the stored user message
// keeps the full command line. The block is appended to the system
// instruction by agentkit.skillAwareInstruction — never to the stored user
// message — so history and exports keep showing what the user actually typed.
func buildSkillTurnContext(s *config.SkillConfig, args string) string {
	if s == nil {
		return ""
	}
	md, err := agentkit.RenderSkillMD(s.Name, s.Description, s.Content)
	if err != nil {
		// Fall back to the raw body: a frontmatter rendering hiccup must not
		// silently turn the invocation into a no-op.
		md = s.Content
	}
	out := "本轮用户消息以 /" + agentkit.MaterializedSkillName(s.Name) + " 命令开头；" +
		"请严格按以下技能「" + s.Name + "」的 SKILL.md 执行：\n\n" + md
	if args != "" {
		out += "\n\n本轮实际任务（命令后的用户输入）：\n" + args
	}
	return out
}

// renderTodoList renders a plan_todos tool result as a compact checklist line
// set for the export renderers (raw JSON payloads are unreadable in exports).
// escape applies HTML escaping to the model-provided content: the HTML export
// renders this straight into the page, and todo contents echo tool arguments
// (model-controlled) — unescaped they would be a stored-XSS vector.
func renderTodoList(resp map[string]any, escape bool) string {
	todos, ok := resp["todos"].([]any)
	if !ok || len(todos) == 0 {
		return ""
	}
	done := 0
	var lines []string
	for _, raw := range todos {
		td, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		content, _ := td["content"].(string)
		status, _ := td["status"].(string)
		if strings.TrimSpace(content) == "" {
			continue
		}
		if escape {
			content = htmlEscapeString(content)
		}
		mark := "○"
		switch status {
		case "completed":
			mark = "✓"
			done++
		case "in_progress":
			mark = "▶"
		}
		lines = append(lines, "- "+mark+" "+content)
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("**任务计划**（%d/%d 完成）\n\n%s", done, len(lines), strings.Join(lines, "\n"))
}

// displayNameOf resolves a session target's display name from the config
// (team / agent); unknown IDs fall back to the raw id. Shared by both export
// renderers so Markdown and HTML never disagree.
func displayNameOf(cfg config.Config, targetType, id string) string {
	if targetType == "team" {
		for _, t := range cfg.Teams {
			if t.ID == id {
				return t.Name
			}
		}
		return id
	}
	for _, a := range cfg.Agents {
		if a.ID == id {
			return a.Name
		}
	}
	return id
}
