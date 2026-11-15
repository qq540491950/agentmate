package agentkit

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

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
