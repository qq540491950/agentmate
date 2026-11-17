package agentkit

import (
	"context"
	"iter"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

type delegateTestState struct {
	mu     sync.Mutex
	values map[string]any
}

func newDelegateTestState() *delegateTestState {
	return &delegateTestState{values: map[string]any{}}
}

func (s *delegateTestState) Get(key string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return value, nil
}

func (s *delegateTestState) Set(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *delegateTestState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		s.mu.Lock()
		values := make(map[string]any, len(s.values))
		for key, value := range s.values {
			values[key] = value
		}
		s.mu.Unlock()
		for key, value := range values {
			if !yield(key, value) {
				return
			}
		}
	}
}

type delegateTestContext struct {
	agent.ContextMock
	ctx       context.Context
	state     *delegateTestState
	sessionID string
	userID    string
}

func (c *delegateTestContext) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c *delegateTestContext) Done() <-chan struct{}       { return c.ctx.Done() }
func (c *delegateTestContext) Err() error                  { return c.ctx.Err() }
func (c *delegateTestContext) Value(key any) any           { return c.ctx.Value(key) }
func (c *delegateTestContext) State() session.State        { return c.state }
func (c *delegateTestContext) SessionID() string           { return c.sessionID }
func (c *delegateTestContext) UserID() string              { return c.userID }
func (c *delegateTestContext) Actions() *session.EventActions {
	return &session.EventActions{}
}

func TestDelegateToolRunsMemberAndReturnsResult(t *testing.T) {
	worker, err := agent.New(agent.Config{
		Name:        "worker",
		Description: "test worker",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				ev := session.NewEvent(ctx, ctx.InvocationID())
				ev.Author = "worker"
				ev.LLMResponse.Content = genai.NewContentFromText("delegated result", genai.RoleModel)
				ev.LLMResponse.TurnComplete = true
				yield(ev, nil)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	delegate, err := DelegateTool([]agent.Agent{worker})
	if err != nil {
		t.Fatal(err)
	}
	res, err := delegate.(runnableShellTool).Run(
		&shellToolTestContext{ctx: context.Background()},
		map[string]any{"agent": "worker", "objective": "say hello"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if res["ok"] != true || !strings.Contains(res["text"].(string), "delegated result") {
		t.Fatalf("result = %+v", res)
	}
}

func TestDelegateToolForwardsMemberProgress(t *testing.T) {
	worker, err := agent.New(agent.Config{
		Name:        "worker",
		Description: "test worker",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				ev := session.NewEvent(ctx, ctx.InvocationID())
				ev.Author = "worker"
				ev.LLMResponse.Content = genai.NewContentFromText("进度结果", genai.RoleModel)
				ev.LLMResponse.TurnComplete = true
				yield(ev, nil)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	delegate, err := DelegateToolWithOptions([]agent.Agent{worker}, DelegateOptions{
		OnProgress: func(progress DelegationProgress) {
			phases = append(phases, progress.Phase)
			if progress.Member != "worker" {
				t.Errorf("progress member = %q", progress.Member)
			}
		},
		Budgets: map[string]DelegationBudget{"worker": {MaxToolCalls: 5, MaxTokens: 10000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := delegate.(runnableShellTool).Run(
		&shellToolTestContext{ctx: context.Background()},
		map[string]any{"agent": "worker", "objective": "say hello"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if res["ok"] != true {
		t.Fatalf("result = %+v", res)
	}
	if len(phases) < 2 || phases[0] != "stage" || phases[len(phases)-1] != "completed" {
		t.Fatalf("progress phases = %v", phases)
	}
}

func delegateBudgetWorker(t *testing.T, name string, toolCalls int, tokens int64) agent.Agent {
	t.Helper()
	worker, err := agent.New(agent.Config{
		Name:        name,
		Description: "budget worker",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				ev := session.NewEvent(ctx, ctx.InvocationID())
				ev.Author = name
				parts := make([]*genai.Part, 0, toolCalls+1)
				for i := 0; i < toolCalls; i++ {
					parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
						ID: "call_" + name + "_" + string(rune('a'+i)), Name: "noop",
					}})
				}
				parts = append(parts, &genai.Part{Text: "member result"})
				ev.LLMResponse.Content = &genai.Content{Role: genai.RoleModel, Parts: parts}
				ev.LLMResponse.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{TotalTokenCount: int32(tokens)}
				ev.LLMResponse.TurnComplete = true
				yield(ev, nil)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestDelegateToolEnforcesMemberBudgets(t *testing.T) {
	toolWorker := delegateBudgetWorker(t, "tool_budget", 2, 10)
	delegate, err := DelegateToolWithOptions([]agent.Agent{toolWorker}, DelegateOptions{
		Budgets: map[string]DelegationBudget{"tool_budget": {MaxToolCalls: 1, MaxTokens: 1000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := delegate.(runnableShellTool).Run(
		&delegateTestContext{ctx: context.Background(), state: newDelegateTestState()},
		map[string]any{"agent": "tool_budget", "objective": "run"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if res["ok"] != false || !strings.Contains(res["error"].(string), "工具调用超过独立预算") {
		t.Fatalf("tool budget result = %+v", res)
	}

	tokenWorker := delegateBudgetWorker(t, "token_budget", 0, 120)
	delegate, err = DelegateToolWithOptions([]agent.Agent{tokenWorker}, DelegateOptions{
		Budgets: map[string]DelegationBudget{"token_budget": {MaxToolCalls: 10, MaxTokens: 50}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err = delegate.(runnableShellTool).Run(
		&delegateTestContext{ctx: context.Background(), state: newDelegateTestState()},
		map[string]any{"agent": "token_budget", "objective": "run"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if res["ok"] != false || !strings.Contains(res["error"].(string), "token 超过独立预算") {
		t.Fatalf("token budget result = %+v", res)
	}
}

func TestDelegateToolEnforcesDepthAndFanout(t *testing.T) {
	worker := delegateBudgetWorker(t, "bounded_worker", 0, 1)
	delegate, err := DelegateTool([]agent.Agent{worker})
	if err != nil {
		t.Fatal(err)
	}
	deepState := newDelegateTestState()
	_ = deepState.Set(delegateDepthKey, maxDelegateDepth)
	_, err = delegate.(runnableShellTool).Run(
		&delegateTestContext{ctx: context.Background(), state: deepState},
		map[string]any{"agent": "bounded_worker", "objective": "run"},
	)
	if err == nil || !strings.Contains(err.Error(), "委派深度超过上限") {
		t.Fatalf("depth error = %v", err)
	}

	state := newDelegateTestState()
	_ = state.Set(TurnIDStateKey, "turn_fanout")
	ctx := &delegateTestContext{ctx: context.Background(), state: state}
	for i := 0; i < maxDelegatesPerTurn; i++ {
		if _, err := delegate.(runnableShellTool).Run(ctx, map[string]any{
			"agent": "bounded_worker", "objective": "run",
		}); err != nil {
			t.Fatalf("delegation %d failed: %v", i, err)
		}
	}
	_, err = delegate.(runnableShellTool).Run(ctx, map[string]any{
		"agent": "bounded_worker", "objective": "run",
	})
	if err == nil || !strings.Contains(err.Error(), "委派次数超过上限") {
		t.Fatalf("fanout error = %v", err)
	}
}

func TestDelegateArtifactSanitizesLargeMemberResult(t *testing.T) {
	secret := "sk-" + strings.Repeat("b", 28)
	data := []byte(`{"text":"` + strings.Repeat("member-result", 400_000) + `\n` + secret + `"}`)
	if len(data) < 5*1024*1024 {
		data = []byte(`{"text":"` + strings.Repeat("x", 5*1024*1024) + `\n` + secret + `"}`)
	}
	path := writeDelegateArtifact(t.TempDir(), data)
	if path == "" {
		t.Fatal("large member result was not written to an artifact")
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), secret) || !strings.Contains(string(saved), "[REDACTED]") {
		t.Fatal("delegate artifact leaked a secret")
	}
	if len(saved) < 4*1024*1024 {
		t.Fatalf("delegate artifact unexpectedly small: %d", len(saved))
	}
}
