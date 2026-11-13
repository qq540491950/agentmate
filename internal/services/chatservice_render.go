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
			// 该级别承载于用户消息的状态增量上；较早的回合未记录任何级别，因此直接不显示
			// 。
			m.Reasoning = eventReasoning(ev)
			// 内联图像是同一个用户事件的一部分。将它们保留在一条消息中：把每个图像拆分成
			// 各自的用户消息会重复事件
			// ID，并使前端渲染出幽灵回合，而这些回合都会在回合栏中一起高亮。
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
				// 助手内联数据不会在聊天历史中渲染；对这些事件保留现有的文本边界行为。
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

// confirmRequestInfo 从 adk_request_confirmation FunctionCall 的 args
// 中提取原始工具调用和人类可读提示。args 在持久化后经过 JSON 往返仍会保留，因此
// originalFunctionCall 字段可能是 *genai.FunctionCall（实时）或通用
// map（重新加载的历史）。
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

// confirmRequestHint 替换 ADK 针对 run_command
// 的通用英文提示。卡片需要一个无歧义的边界标签：升级后的调用必须说明其会离开沙
// 箱，并携带模型的理由；普通调用不得看起来像一次升级。
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

// escalationAuditFields 返回工具审计追踪用于单次调用沙箱逃逸的稳定字段。结果为
// nil 表示该调用不是升级后的 run_command。
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

// auditToolCallArgs 保留原始工具参数，并为调用记录添加规范化后的升级字段（尤其是
// escalated=true）。
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

// isConfirmationInterrupt 报告该 part 是否是在此事件上声明为长时间运行的
// adk_request_confirmation 中断（ADK HITL 暂停点）。
func isConfirmationInterrupt(ev *session.Event, fc *genai.FunctionCall) bool {
	if fc == nil {
		return false
	}
	return fc.Name == toolconfirmation.FunctionCallName && slices.Contains(ev.LongRunningToolIDs, fc.ID)
}

// targetAgentConfig 返回会话背后的 AgentConfig（团队或目标已不存在时为 nil）。
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

// directAgentConfig 构建直接对话背后的隐式目标：默认供应商的第一个模型、感知
// 工作区的提示词、工作区文件工具和终端工具。不会持久化任何内容——直接会话只在
// 每回合临时物化一个 Agent。没有文件工具时，项目对话只能承诺「我来查看项目」
// 然后停住：模型无法读取被问到的目录（issue #21）。没有 Shell 工具时，默认
// Agent 会否认自己具备终端能力并拒绝执行「git pull」（issue #40）——配置的
// Agent 自 #36 起已有 run_command，但这条硬编码路径仍把自己限制在文件操作上。
// 写入和命令仍经过 HITL 确认，与新 Agent 的默认设置一致。
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
		// 审批和操作系统边界来自会话独立的执行策略。直接聊天有意不添加按 Agent
		// 的覆盖配置，因此自动批准仅对检测到风险的命令请求确认。
	}, nil
}

// mentionMatches 报告 body 是否以提及名称开头，且其后为 token
// 边界（结尾、空格、换行或全角空格）。
func mentionMatches(body, name string) bool {
	if !strings.HasPrefix(body, name) {
		return false
	}
	rest := body[len(name):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || strings.HasPrefix(rest, "　")
}

// resolveMention 解析开头的「@名称」提及，并返回它指向的回合级目标。候选包括
// Agent、团队和远程 Agent；最长匹配优先，因此「写手」不会遮蔽「写手助手」。
// 无法解析的提及会让该回合继续使用会话自身的目标。
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

// turnTargetFor 解析每回合目标：用户文本开头的 @ 提及仅将本回合路由到指定的
// Agent/团队/远程目标，其他内容仍留在会话自身的目标上。该提及会保留在消息文本中
// ——历史记录、Regenerate 和 EditAndResend 会重新解析它并一致地重新路由。
func turnTargetFor(cfg config.Config, target *config.ChatSession, userText string) *config.ChatSession {
	turn := *target
	if userText != "" {
		if typ, id, ok := resolveMention(cfg, userText); ok {
			turn.TargetType, turn.TargetID = typ, id
		}
	}
	return &turn
}

// effectiveSkillsFor 是工作区中聊天所用技能集的斜杠命令视图：项目/全局发现优先于同名应用技能，与
// agent-build 层（config.EffectiveSkills）完全一致。
func effectiveSkillsFor(cfg config.Config, workspace string) []config.SkillConfig {
	return config.EffectiveSkills(cfg, workspace, config.GlobalSkillRoot())
}

// resolveSkillCommand 会针对有效技能（应用配置加上项目/全局 .agents/skills
// 发现）解析开头的 "/<skill-name>" 命令。匹配使用 ADK
// 会暴露的物化名称（小写、连字符形式），并且不区分大小写，因此 "/Weekly_Report"
// 能找到名为 "Weekly Report"
// 的技能。该行的剩余部分作为本回合的用户输入返回。仅考虑第一个
// token：该文本兼作聊天历史，因此 Regenerate 和 HITL 恢复可以逐字重新解析它。
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

// buildSkillTurnContext 渲染注入到由 "/<skill-name>"
// 命令启动的回合中的指令块。args
// 是命令之后的文本，因此即使存储的用户消息保留完整命令行，模型也能获得明确说明
// 的任务。该块由 agentkit.skillAwareInstruction
// 追加到系统指令中——绝不会追加到存储的用户消息——因此历史记录和导出内容始终显示
// 用户实际输入的内容。
func buildSkillTurnContext(s *config.SkillConfig, args string) string {
	if s == nil {
		return ""
	}
	md, err := agentkit.RenderSkillMD(s.Name, s.Description, s.Content)
	if err != nil {
		// 回退到原始正文：frontmatter 渲染的小故障不得悄悄使调用变成空操作。
		md = s.Content
	}
	out := "本轮用户消息以 /" + agentkit.MaterializedSkillName(s.Name) + " 命令开头；" +
		"请严格按以下技能「" + s.Name + "」的 SKILL.md 执行：\n\n" + md
	if args != "" {
		out += "\n\n本轮实际任务（命令后的用户输入）：\n" + args
	}
	return out
}

// renderTodoList 将 plan_todos
// 工具结果渲染为供导出渲染器使用的紧凑清单行集合（原始 JSON
// 载荷在导出内容中不可读）。escape 对模型提供的内容应用 HTML 转义：HTML
// 导出会将其直接渲染进页面，而 todo
// 内容会回显工具参数（由模型控制）——不转义它们就会成为存储型 XSS 的攻击向量。
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

// displayNameOf 从配置中解析会话目标的显示名称（team / agent）；未知 ID
// 回退为原始 id。由两个导出渲染器共享，因此 Markdown 与 HTML 绝不会出现不一致。
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
