package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

func newMCPApprovalServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "approval-test", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "mcp_echo",
		Description: "echoes a message",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "mcp-ok"}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, &calls
}

func enableMCPApprovalServer(t *testing.T, store *config.Store, endpoint string) {
	t.Helper()
	if err := store.Update(func(cfg *config.Config) {
		cfg.MCPServers = append(cfg.MCPServers, config.MCPServerConfig{
			ID: "mcp_approval", Name: "审批测试 MCP", Transport: config.MCPStreamableHTTP, URL: endpoint,
		})
	}); err != nil {
		t.Fatal(err)
	}
}

func newMCPApprovalChat(t *testing.T, handler http.HandlerFunc) (*ChatService, *config.Store, *mcpmgr.Manager) {
	t.Helper()
	chat, store, _, _ := newConfirmTestChat(t, handler)
	manager := mcpmgr.New()
	chat.S.Kit = agentkit.NewKit(store, manager)
	chat.S.MCP = manager
	return chat, store, manager
}

func mcpCallOrDone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	if strings.Contains(readAll(r), "mcp-ok") {
		_, _ = w.Write([]byte(sseText("完成") + sseDone))
		return
	}
	_, _ = w.Write([]byte(sseToolCall("call_mcp", "mcp_echo", `"{\"message\":\"hello\"}"`) + sseDone))
}

func pendingMCPConfirm(events []ChatStreamEvent) *ChatStreamEvent {
	for i := range events {
		if events[i].Kind == "confirm_request" {
			return &events[i]
		}
	}
	return nil
}

func TestMCPToolApprovalFollowsSessionMode(t *testing.T) {
	tests := []struct {
		name        string
		approval    string
		sandbox     string
		wantConfirm bool
	}{
		{
			name: "request approval", approval: config.ApprovalPolicyRequestApproval,
			sandbox: config.SandboxProfileWorkspaceWrite, wantConfirm: true,
		},
		{
			name: "auto approve", approval: config.ApprovalPolicyAutoApprove,
			sandbox: config.SandboxProfileWorkspaceWrite, wantConfirm: true,
		},
		{
			name: "danger full access", approval: config.ApprovalPolicyNever,
			sandbox: config.SandboxProfileDangerFullAccess, wantConfirm: false,
		},
		{
			name: "request + danger", approval: config.ApprovalPolicyRequestApproval,
			sandbox: config.SandboxProfileDangerFullAccess, wantConfirm: true,
		},
		{
			name: "never + workspace", approval: config.ApprovalPolicyNever,
			sandbox: config.SandboxProfileWorkspaceWrite, wantConfirm: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat, store, manager := newMCPApprovalChat(t, mcpCallOrDone)
			mcpSrv, calls := newMCPApprovalServer(t)
			t.Cleanup(manager.CloseAll)
			enableMCPApprovalServer(t, store, mcpSrv.URL)
			events := hookEvents(t)

			sess, err := chat.newLegacySession("agent", "agent_c", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := chat.SetSessionExecutionPolicy(sess.ID, tt.approval, tt.sandbox, "test"); err != nil {
				t.Fatal(err)
			}
			if err := chat.Send(sess.ID, "调用 MCP 工具", nil); err != nil {
				t.Fatal(err)
			}

			req := pendingMCPConfirm(*events)
			if tt.wantConfirm {
				if req == nil || req.ToolName != "mcp_echo" || req.CallID == "" {
					t.Fatalf("expected MCP confirmation, got %+v", *events)
				}
				if req.ToolArgs["message"] != "hello" {
					t.Fatalf("confirmation must show original MCP args: %+v", req.ToolArgs)
				}
				if !strings.Contains(req.Hint, "外部工具可能有副作用") {
					t.Fatalf("MCP confirmation should warn about external side effects: %q", req.Hint)
				}
				if calls.Load() != 0 {
					t.Fatal("MCP tool ran before approval")
				}
				return
			}
			if req != nil {
				t.Fatalf("danger-full-access unexpectedly asked for confirmation: %+v", req)
			}
			if calls.Load() != 1 {
				t.Fatalf("danger-full-access MCP calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestMCPApprovalIsolatedBetweenSessions(t *testing.T) {
	chat, store, manager := newMCPApprovalChat(t, mcpCallOrDone)
	mcpSrv, calls := newMCPApprovalServer(t)
	t.Cleanup(manager.CloseAll)
	enableMCPApprovalServer(t, store, mcpSrv.URL)
	events := hookEvents(t)

	autoSess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	dangerSess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionSandboxMode(autoSess.ID, config.SandboxModeAutoApprove, "test"); err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionSandboxMode(dangerSess.ID, config.SandboxModeDangerFullAccess, "test"); err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(autoSess.ID, "自动批准会话", nil); err != nil {
		t.Fatal(err)
	}
	req := pendingMCPConfirm(*events)
	if req == nil || req.ToolName != "mcp_echo" {
		t.Fatalf("auto session did not request MCP confirmation: %+v", *events)
	}
	if calls.Load() != 0 {
		t.Fatal("auto session ran the MCP tool before approval")
	}

	*events = nil
	if err := chat.Send(dangerSess.ID, "完全访问会话", nil); err != nil {
		t.Fatal(err)
	}
	if got := pendingMCPConfirm(*events); got != nil {
		t.Fatalf("danger session inherited another session's approval policy: %+v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("danger session MCP calls = %d, want 1", calls.Load())
	}

	if err := chat.ConfirmToolCall(autoSess.ID, req.CallID, true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("approved auto session MCP calls = %d, want 2", calls.Load())
	}
}
