package services

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"agentmate/internal/config"
)

func policyToolArgs(args map[string]string) string {
	return strconv.Quote(string(mustJSON(args)))
}

func policyShellCommand(relativePath string) string {
	if runtime.GOOS == "windows" {
		return "echo changed>" + strings.ReplaceAll(relativePath, "/", `\`)
	}
	return "echo changed>" + relativePath
}

func TestNeverWorkspaceWriteSkipsConfirmationButKeepsSandbox(t *testing.T) {
	turn := 0
	chat, store, globalWorkspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall(
				"write_1",
				"write_workspace_file",
				policyToolArgs(map[string]string{"path": "never.txt", "content": "ok"}),
			) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseToolCall(
				"shell_1",
				"run_command",
				policyToolArgs(map[string]string{"command": policyShellCommand("../../outside.txt")}),
			) + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("done") + sseDone))
		}
	})
	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.Agents {
			cfg.Agents[i].BuiltinTools = []string{"files", "shell"}
			cfg.Agents[i].ConfirmTools = []string{"files", "shell"}
		}
	}); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", "never + workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyNever,
		config.SandboxProfileWorkspaceWrite,
		"composer",
	); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(globalWorkspace, "outside.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "write inside and outside", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("never policy unexpectedly asked for confirmation: %+v", ev)
		}
	}
	if data, err := os.ReadFile(filepath.Join(sess.WorkspaceDir, "never.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("inside write = %q err=%v", data, err)
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if string(data) != "original" {
		t.Fatalf("workspace-write sandbox did not block outside write: %q", data)
	}
}

func TestRequestApprovalDangerFullAccessConfirmsWithoutSandbox(t *testing.T) {
	turn := 0
	chat, store, globalWorkspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall(
				"shell_danger",
				"run_command",
				policyToolArgs(map[string]string{"command": policyShellCommand("../../outside.txt")}),
			) + sseDone))
		default:
			_, _ = w.Write([]byte(sseText("done") + sseDone))
		}
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
	sess, err := chat.NewSessionWithWorkspace("agent", "agent_c", "request + danger", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyRequestApproval,
		config.SandboxProfileDangerFullAccess,
		"composer",
	); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(globalWorkspace, "outside.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := chat.Send(sess.ID, "write outside", nil); err != nil {
		t.Fatal(err)
	}
	var req *ChatStreamEvent
	for i := range *events {
		if (*events)[i].Kind == "confirm_request" {
			req = &(*events)[i]
			break
		}
	}
	if req == nil || req.CallID == "" {
		t.Fatalf("request approval unexpectedly skipped confirmation: %+v", *events)
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if string(data) != "original" {
		t.Fatalf("danger tool ran before approval: %q", data)
	}
	if err := chat.ConfirmToolCall(sess.ID, req.CallID, true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil {
		t.Fatal(err)
	} else if strings.TrimSpace(string(data)) != "changed" {
		t.Fatalf("danger sandbox still blocked outside write: %q", data)
	}
}
