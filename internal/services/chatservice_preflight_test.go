package services

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
)

func TestAutoApproveRiskyCommandHintExplainsRisk(t *testing.T) {
	turn := 0
	chat, store, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		if turn == 1 {
			_, _ = w.Write([]byte(sseToolCall(
				"shell_risk",
				"run_command",
				policyToolArgs(map[string]string{"command": "rm -rf build"}),
			) + sseDone))
			return
		}
		_, _ = w.Write([]byte(sseText("done") + sseDone))
	})
	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.Agents {
			cfg.Agents[i].BuiltinTools = []string{"shell"}
			cfg.Agents[i].ConfirmTools = nil
		}
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)
	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyAutoApprove,
		config.SandboxProfileWorkspaceWrite,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	if err := chat.Send(sess.ID, "清理构建目录", nil); err != nil {
		t.Fatal(err)
	}
	req := pendingMCPConfirm(*events)
	if req == nil {
		t.Fatalf("risky command did not ask for confirmation: %+v", *events)
	}
	if !strings.Contains(req.Hint, "该命令需要确认") ||
		!strings.Contains(req.Hint, "会删除文件或目录") {
		t.Fatalf("risk hint missing the AssessCommand reason: %q", req.Hint)
	}
}

func TestRequestApprovalSkipsConfirmationForRejectedPaths(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantText string
	}{
		{name: "outside workspace", path: "../escaped.txt", wantText: "路径越出工作区范围"},
		{name: "protected path", path: ".git/config", wantText: "protected_path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			turn := 0
			chat, _, _, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				turn++
				if turn == 1 {
					_, _ = w.Write([]byte(sseToolCall(
						"write_rejected",
						"write_workspace_file",
						policyToolArgs(map[string]string{"path": tt.path, "content": "x"}),
					) + sseDone))
					return
				}
				if body := readAll(r); !strings.Contains(body, tt.wantText) {
					t.Errorf("tool result missing %q: %s", tt.wantText, body)
				}
				_, _ = w.Write([]byte(sseText("done") + sseDone))
			})
			events := hookEvents(t)
			sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", tt.name, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := chat.SetSessionExecutionPolicy(
				sess.ID,
				config.ApprovalPolicyRequestApproval,
				config.SandboxProfileWorkspaceWrite,
				"test",
			); err != nil {
				t.Fatal(err)
			}
			if err := chat.Send(sess.ID, "写一个会被拒绝的文件", nil); err != nil {
				t.Fatal(err)
			}
			for _, ev := range *events {
				if ev.Kind == "confirm_request" {
					t.Fatalf("preflight failure must not ask for confirmation: %+v", ev)
				}
			}
			if _, err := os.Stat(filepath.Join(sess.WorkspaceDir, filepath.FromSlash(tt.path))); !os.IsNotExist(err) {
				t.Fatalf("rejected path was created: %v", err)
			}
		})
	}
}

func TestConfirmRequestReason(t *testing.T) {
	root := t.TempDir()
	guard := agentkit.NewPreviewGuard(
		root, config.SandboxProfileWorkspaceWrite, config.SandboxNetworkOff,
	)
	readOnly := agentkit.NewPreviewGuard(
		root, config.SandboxProfileReadOnly, config.SandboxNetworkOff,
	)
	tests := []struct {
		name string
		g    agentkit.PreviewGuard
		call *genai.FunctionCall
		want []string
	}{
		{
			name: "safe command",
			g:    guard,
			call: &genai.FunctionCall{Name: "run_command", Args: map[string]any{"command": "git status"}},
			want: []string{"该命令需要确认"},
		},
		{
			name: "risky command",
			g:    guard,
			call: &genai.FunctionCall{Name: "run_command", Args: map[string]any{"command": "rm -rf build"}},
			want: []string{"该命令需要确认", "会删除文件或目录"},
		},
		{
			name: "sandbox escape",
			g:    guard,
			call: &genai.FunctionCall{Name: "run_command", Args: map[string]any{
				"command": "echo x", "escalate": true, "justification": "需要写共享缓存",
			}},
			want: []string{"脱离沙箱", "需要写共享缓存"},
		},
		{
			name: "write inside",
			g:    guard,
			call: &genai.FunctionCall{Name: "write_workspace_file", Args: map[string]any{"path": "a.txt"}},
			want: []string{"写入工作区文件", "a.txt"},
		},
		{
			name: "edit inside",
			g:    guard,
			call: &genai.FunctionCall{Name: "edit_workspace_file", Args: map[string]any{"path": "a.txt"}},
			want: []string{"修改工作区文件", "a.txt"},
		},
		{
			name: "outside workspace",
			g:    guard,
			call: &genai.FunctionCall{Name: "write_workspace_file", Args: map[string]any{"path": "../a.txt"}},
			want: []string{"工作区外"},
		},
		{
			name: "protected path",
			g:    guard,
			call: &genai.FunctionCall{Name: "edit_workspace_file", Args: map[string]any{"path": ".git/config"}},
			want: []string{"受保护路径"},
		},
		{
			name: "read only",
			g:    readOnly,
			call: &genai.FunctionCall{Name: "write_workspace_file", Args: map[string]any{"path": "a.txt"}},
			want: []string{"只读模式"},
		},
		{
			name: "external tool",
			g:    guard,
			call: &genai.FunctionCall{Name: "mcp_echo", Args: map[string]any{"message": "hi"}},
			want: []string{"外部工具可能有副作用"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := confirmRequestReason(tt.g, tt.call, "Please approve or reject the tool call")
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("reason = %q, want substring %q", got, want)
				}
			}
		})
	}

	custom := &genai.FunctionCall{Name: "mcp_echo"}
	if got := confirmRequestReason(guard, custom, "自定义原因"); got != "自定义原因" {
		t.Fatalf("custom hint overwritten: %q", got)
	}
}
