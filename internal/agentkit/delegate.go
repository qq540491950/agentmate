package agentkit

import (
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
	OK        bool   `json:"ok"`
	Agent     string `json:"agent"`
	Text      string `json:"text,omitempty"`
	ToolCalls int    `json:"toolCalls,omitempty"`
	Error     string `json:"error,omitempty"`
}

const (
	maxDelegatesPerTurn = 8
	maxDelegateDepth    = 2
	delegateDepthKey    = "agentmate.delegate.depth"
)

// DelegateTool 让协调员在不放弃根节点控制权的情况下运行一个团队成员。
// 成员在独立的内存会话中执行，最终结果以结构化工具有效载荷返回。
func DelegateTool(members []agent.Agent) (tool.Tool, error) {
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
		countMu.Lock()
		if delegated >= maxDelegatesPerTurn {
			countMu.Unlock()
			return delegationResult{}, fmt.Errorf("单回合委派次数超过上限（%d）", maxDelegatesPerTurn)
		}
		delegated++
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
		rn, err := runner.New(runner.Config{
			AppName:        "delegate_" + member.Name(),
			Agent:          member,
			SessionService: sessions,
			MemoryService:  memory.InMemoryService(),
		})
		if err != nil {
			return delegationResult{}, err
		}
		prompt := buildDelegatePrompt(objective, args.Context, args.Acceptance)
		var text strings.Builder
		toolCalls := 0
		for ev, runErr := range rn.Run(ctx, userID, sess.Session.ID(),
			genai.NewContentFromText(prompt, genai.RoleUser),
			agent.RunConfig{StreamingMode: agent.StreamingModeSSE, MaxLLMCalls: 50}) {
			if runErr != nil {
				return delegationResult{Agent: member.Name(), Error: runErr.Error()}, nil
			}
			if ev == nil || ev.Content == nil {
				continue
			}
			for _, part := range ev.Content.Parts {
				if part == nil {
					continue
				}
				if part.FunctionCall != nil {
					toolCalls++
					if part.FunctionCall.Name == "adk_request_confirmation" {
						return delegationResult{
							Agent: member.Name(),
							Error: "成员需要人工确认；请改用 transfer 或调整成员工具",
						}, nil
					}
				}
				if part.Text != "" && !part.Thought {
					text.WriteString(part.Text)
				}
			}
		}
		out := delegationResult{OK: true, Agent: member.Name(), Text: strings.TrimSpace(text.String()), ToolCalls: toolCalls}
		if out.Text == "" {
			out.OK = false
			out.Error = "成员没有返回文本结果"
		}
		return out, nil
	})
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
