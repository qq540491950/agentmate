package agentkit

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"
)

type delegateArgs struct {
	Agent      string   `json:"agent" jsonschema:"team member name"`
	Objective  string   `json:"objective" jsonschema:"what the member must accomplish"`
	Context    string   `json:"context,omitempty" jsonschema:"relevant context and constraints"`
	Acceptance []string `json:"acceptance,omitempty" jsonschema:"acceptance criteria the result must satisfy"`
}

type delegationResult struct {
	OK           bool       `json:"ok"`
	Agent        string     `json:"agent"`
	Text         string     `json:"text,omitempty"`
	ToolCalls    int        `json:"toolCalls,omitempty"`
	PlanProposal []PlanStep `json:"planProposal,omitempty"`
	Error        string     `json:"error,omitempty"`
}

// DelegationBudget 是单个成员的独立预算。
type DelegationBudget struct {
	MaxToolCalls int   `json:"maxToolCalls,omitempty"`
	MaxTokens    int64 `json:"maxTokens,omitempty"`
}

// DelegationProgress 是转发到父时间线的成员阶段/工具进度。
type DelegationProgress struct {
	SessionID string
	TurnID    string
	Member    string
	Phase     string
	ToolName  string
	CallID    string
	Text      string
}

// DelegateOptions 由聊天运行时注入：进度桥接与成员预算。
type DelegateOptions struct {
	OnProgress  func(DelegationProgress)
	Budgets     map[string]DelegationBudget
	ArtifactDir string
}

const (
	maxDelegatesPerTurn = 8
	maxDelegateDepth    = 2
	delegateDepthKey    = "agentmate.delegate.depth"
	delegateCountPrefix = "agentmate.delegate.count."
	planProposalKey     = "agentmate.plan_proposal"
)

// DelegateTool 让协调员在不放弃根节点控制权的情况下运行一个团队成员。
// 成员在独立的内存会话中执行，最终结果以结构化工具有效载荷返回。
func DelegateTool(members []agent.Agent) (tool.Tool, error) {
	return DelegateToolWithOptions(members, DelegateOptions{})
}

func DelegateToolWithOptions(members []agent.Agent, opts DelegateOptions) (tool.Tool, error) {
	byName := make(map[string]agent.Agent, len(members))
	names := make([]string, 0, len(members))
	for _, member := range members {
		if member == nil {
			continue
		}
		byName[strings.ToLower(member.Name())] = member
		names = append(names, member.Name())
	}
	if len(byName) == 0 {
		return nil, fmt.Errorf("团队没有可委派的成员")
	}
	var countMu sync.Mutex
	delegated := 0
	return functiontool.New(functiontool.Config{
		Name: "delegate_task",
		Description: "Delegate a bounded task to one team member and return its result. " +
			"Available members: " + strings.Join(names, ", ") + ". " +
			"Use this instead of transfer when you need to collect results and synthesize the final answer.",
	}, func(ctx agent.Context, args delegateArgs) (delegationResult, error) {
		member := byName[strings.ToLower(strings.TrimSpace(args.Agent))]
		if member == nil {
			return delegationResult{}, fmt.Errorf("未知团队成员 %q", args.Agent)
		}
		objective := strings.TrimSpace(args.Objective)
		if objective == "" {
			return delegationResult{}, fmt.Errorf("objective 不能为空")
		}
		userID := strings.TrimSpace(ctx.UserID())
		if userID == "" {
			userID = "local"
		}
		depth := 0
		if ctx.State() != nil {
			if value, err := ctx.State().Get(delegateDepthKey); err == nil {
				switch v := value.(type) {
				case int:
					depth = v
				case int64:
					depth = int(v)
				case float64:
					depth = int(v)
				}
			}
		}
		if depth >= maxDelegateDepth {
			return delegationResult{}, fmt.Errorf("委派深度超过上限（%d）", maxDelegateDepth)
		}
		turnID := stateString(ctx, TurnIDStateKey)
		countKey := delegateCountPrefix + turnID
		countMu.Lock()
		used := delegated
		stateful := ctx.State() != nil && turnID != ""
		if stateful {
			used = 0
		}
		if ctx.State() != nil && turnID != "" {
			if value, err := ctx.State().Get(countKey); err == nil {
				switch v := value.(type) {
				case int:
					used = v
				case int64:
					used = int(v)
				case float64:
					used = int(v)
				}
			}
		}
		if used >= maxDelegatesPerTurn {
			countMu.Unlock()
			return delegationResult{}, fmt.Errorf("单回合委派次数超过上限（%d）", maxDelegatesPerTurn)
		}
		used++
		if stateful {
			if err := ctx.State().Set(countKey, used); err != nil {
				countMu.Unlock()
				return delegationResult{}, err
			}
		} else {
			delegated = used
		}
		countMu.Unlock()
		state := stateSnapshot(ctx)
		state[delegateDepthKey] = depth + 1
		sessions := session.InMemoryService()
		sess, err := sessions.Create(ctx, &session.CreateRequest{
			AppName: "delegate_" + member.Name(),
			UserID:  userID,
			State:   state,
		})
		if err != nil {
			return delegationResult{}, err
		}
		delegateSessions := newDelegateSessionService(sessions, opts.ArtifactDir)
		rn, err := runner.New(runner.Config{
			AppName:        "delegate_" + member.Name(),
			Agent:          member,
			SessionService: delegateSessions,
			MemoryService:  memory.InMemoryService(),
		})
		if err != nil {
			return delegationResult{}, err
		}
		prompt := buildDelegatePrompt(objective, args.Context, args.Acceptance)
		var text strings.Builder
		toolCalls := 0
		var tokens int64
		budget := opts.Budgets[member.Name()]
		emitProgress := func(phase, toolName, callID, text string) {
			if opts.OnProgress == nil {
				return
			}
			opts.OnProgress(DelegationProgress{
				SessionID: ctx.SessionID(), TurnID: stateString(ctx, TurnIDStateKey),
				Member: member.Name(), Phase: phase, ToolName: toolName,
				CallID: callID, Text: SanitizeSecrets(text),
			})
		}
		emitProgress("stage", "", "", "成员开始执行委派任务")
		for ev, runErr := range rn.Run(ctx, userID, sess.Session.ID(),
			genai.NewContentFromText(prompt, genai.RoleUser),
			agent.RunConfig{StreamingMode: agent.StreamingModeSSE, MaxLLMCalls: 50}) {
			if runErr != nil {
				return delegationResult{Agent: member.Name(), Error: runErr.Error()}, nil
			}
			if ev == nil || ev.Content == nil {
				continue
			}
			if ev.LLMResponse.UsageMetadata != nil {
				tokens += int64(ev.LLMResponse.UsageMetadata.TotalTokenCount)
			}
			for _, part := range ev.Content.Parts {
				if part == nil {
					continue
				}
				if part.FunctionCall != nil {
					toolCalls++
					emitProgress("tool_call", part.FunctionCall.Name, part.FunctionCall.ID, "")
					if budget.MaxToolCalls > 0 && toolCalls > budget.MaxToolCalls {
						return delegationResult{
							Agent: member.Name(), ToolCalls: toolCalls,
							Error: fmt.Sprintf("成员工具调用超过独立预算（%d）", budget.MaxToolCalls),
						}, nil
					}
					if part.FunctionCall.Name == "adk_request_confirmation" {
						return delegationResult{
							Agent: member.Name(),
							Error: "成员需要人工确认；请改用 transfer 或调整成员工具",
						}, nil
					}
				}
				if part.FunctionResponse != nil {
					emitProgress("tool_result", part.FunctionResponse.Name, part.FunctionResponse.ID, resultErrorOfMap(part.FunctionResponse.Response))
				}
				if part.Text != "" && !part.Thought {
					text.WriteString(part.Text)
					emitProgress("stage", "", "", part.Text)
				}
			}
			if budget.MaxTokens > 0 && tokens > budget.MaxTokens {
				return delegationResult{
					Agent: member.Name(), ToolCalls: toolCalls,
					Error: fmt.Sprintf("成员 token 超过独立预算（%d）", budget.MaxTokens),
				}, nil
			}
		}
		proposal := planProposalFromSession(sess.Session)
		out := delegationResult{
			OK: true, Agent: member.Name(), Text: strings.TrimSpace(text.String()),
			ToolCalls: toolCalls, PlanProposal: proposal,
		}
		if out.Text == "" {
			out.OK = false
			out.Error = "成员没有返回文本结果"
		}
		emitProgress("completed", "", "", "成员完成委派任务")
		return out, nil
	})
}

func resultErrorOfMap(resp map[string]any) string {
	if resp == nil {
		return ""
	}
	if text, ok := resp["error"].(string); ok {
		return text
	}
	if text, ok := resp["summary"].(string); ok {
		return text
	}
	return ""
}

type planProposalArgs struct {
	Steps []PlanStep `json:"steps"`
}

// PlanProposalTool 让团队成员提交计划建议，而不直接写协调员的计划。
func PlanProposalTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "submit_plan_proposal",
		Description: "Submit a proposed plan for the coordinator. Members must not write the session plan directly.",
	}, func(ctx agent.Context, args planProposalArgs) (map[string]any, error) {
		if len(args.Steps) == 0 {
			return nil, fmt.Errorf("计划建议不能为空")
		}
		if len(args.Steps) > 50 {
			return nil, fmt.Errorf("计划建议最多 50 步")
		}
		clean := make([]PlanStep, 0, len(args.Steps))
		for i, step := range args.Steps {
			step.Content = strings.TrimSpace(step.Content)
			if step.Content == "" {
				continue
			}
			if step.ID == "" {
				step.ID = fmt.Sprintf("proposal-%d", i+1)
			}
			if step.Status == "" {
				step.Status = "pending"
			}
			clean = append(clean, step)
		}
		if err := ValidatePlanSteps(clean); err != nil {
			return nil, err
		}
		if ctx.State() != nil {
			if err := ctx.State().Set(planProposalKey, clean); err != nil {
				return nil, err
			}
		}
		return map[string]any{"ok": true, "steps": clean}, nil
	})
}

func planProposalFromSession(sess session.Session) []PlanStep {
	if sess == nil || sess.State() == nil {
		return nil
	}
	value, err := sess.State().Get(planProposalKey)
	if err != nil {
		return nil
	}
	data, _ := json.Marshal(value)
	var steps []PlanStep
	if json.Unmarshal(data, &steps) != nil {
		return nil
	}
	return steps
}

func stateSnapshot(ctx agent.Context) map[string]any {
	out := map[string]any{}
	if ctx == nil || ctx.State() == nil {
		return out
	}
	for key, value := range ctx.State().All() {
		if strings.HasPrefix(key, "_adk") {
			continue
		}
		out[key] = value
	}
	return out
}

func buildDelegatePrompt(objective, context string, acceptance []string) string {
	var sb strings.Builder
	sb.WriteString("You are executing a bounded task delegated by the team coordinator.\n\nObjective:\n")
	sb.WriteString(objective)
	if strings.TrimSpace(context) != "" {
		sb.WriteString("\n\nContext and constraints:\n")
		sb.WriteString(strings.TrimSpace(context))
	}
	if len(acceptance) > 0 {
		sb.WriteString("\n\nAcceptance criteria:\n")
		for _, item := range acceptance {
			if item = strings.TrimSpace(item); item != "" {
				sb.WriteString("- ")
				sb.WriteString(item)
				sb.WriteString("\n")
			}
		}
	}
	sb.WriteString("\nReturn a concise result with evidence, files or commands you used, and any open questions.")
	return sb.String()
}
