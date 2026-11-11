package services

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/config"
)

func TestReadOnlyFileToolsReadListAndRejectWriteWithoutHITL(t *testing.T) {
	turn := 0
	chat, _, workspace, _ := newConfirmTestChat(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turn++
		switch turn {
		case 1:
			_, _ = w.Write([]byte(sseToolCall(
				"list_1",
				"list_workspace_files",
				policyToolArgs(map[string]string{"path": ""}),
			) + sseDone))
		case 2:
			_, _ = w.Write([]byte(sseToolCall(
				"read_1",
				"read_workspace_file",
				policyToolArgs(map[string]string{"path": "seed.txt"}),
			) + sseDone))
		case 3:
			_, _ = w.Write([]byte(sseToolCall(
				"write_1",
				"write_workspace_file",
				policyToolArgs(map[string]string{"path": "blocked.txt", "content": "nope"}),
			) + sseDone))
		default:
			if body := readAll(r); !strings.Contains(body, "read_only") {
				t.Errorf("write result missing read_only error: %s", body)
			}
			_, _ = w.Write([]byte(sseText("只读完成") + sseDone))
		}
	})
	if err := os.WriteFile(filepath.Join(workspace, "seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := hookEvents(t)

	sess, err := chat.newLegacySession("agent", "agent_c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SetSessionExecutionPolicy(
		sess.ID,
		config.ApprovalPolicyRequestApproval,
		config.SandboxProfileReadOnly,
		"composer",
	); err != nil {
		t.Fatal(err)
	}
	policy, err := chat.SessionExecutionPolicy(sess.ID)
	if err != nil || policy.SandboxProfile != config.SandboxProfileReadOnly {
		t.Fatalf("read-only session policy = %+v err=%v", policy, err)
	}

	if err := chat.Send(sess.ID, "读取并尝试写入", nil); err != nil {
		t.Fatal(err)
	}
	for _, ev := range *events {
		if ev.Kind == "confirm_request" {
			t.Fatalf("read-only write unexpectedly asked for approval: %+v", ev)
		}
	}
	var sawList, sawRead bool
	for _, ev := range *events {
		if ev.Kind != "tool_result" {
			continue
		}
		switch ev.ToolName {
		case "list_workspace_files":
			entries, _ := ev.ToolResp["entries"].(string)
			sawList = strings.Contains(entries, "seed.txt")
		case "read_workspace_file":
			sawRead = ev.ToolResp["content"] == "seed"
		}
	}
	if !sawList || !sawRead {
		t.Fatalf("read/list results missing: list=%v read=%v events=%+v", sawList, sawRead, *events)
	}
	if _, err := os.Stat(filepath.Join(workspace, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only write created a file: %v", err)
	}
}
