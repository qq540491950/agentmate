package agentkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolconfirmation"

	"agentmate/internal/config"
	"agentmate/internal/sandbox"
)

func TestShellToolRejectsEmptyWorkspace(t *testing.T) {
	if _, err := ShellTool("", false); err == nil {
		t.Fatal("expected error for empty workspace")
	}
}

func TestShellToolBuildsRunCommand(t *testing.T) {
	tl, err := ShellTool(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Name() != "run_command" {
		t.Fatalf("tool name = %q, want run_command", tl.Name())
	}
}

// TestRunShellCommandInWorkspace pins the core contract (issue #36): the
// command actually runs and its working directory is the task workspace.
func TestRunShellCommandInWorkspace(t *testing.T) {
	dir := t.TempDir()
	command := "pwd"
	if runtime.GOOS == "windows" {
		command = "cd"
	}
	res, err := runShellCommand(context.Background(), dir, command, 10*time.Second, maxShellOutput)
	if err != nil {
		t.Fatalf("runShellCommand: %v", err)
	}
	if res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("unexpected result: %+v", res)
	}
	// Compare directory identity instead of spelling: Windows t.TempDir()
	// returns an 8.3 short path (RUNNER~1) that `cd` may print in long form.
	got := strings.TrimSpace(res.Output)
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat command cwd %q: %v", got, err)
	}
	wantInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("cwd = %q, want %q", got, dir)
	}
}

// TestRunShellCommandNonZeroExit: a failed command is a normal tool result
// carrying the exit code, not a tool error — the model needs to see it to
// adjust course.
func TestRunShellCommandNonZeroExit(t *testing.T) {
	res, err := runShellCommand(context.Background(), t.TempDir(), "exit 3", 10*time.Second, maxShellOutput)
	if err != nil {
		t.Fatalf("unexpected tool error: %v", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3 (result %+v)", res.ExitCode, res)
	}
}

func TestRunShellCommandTruncatesOutput(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", maxShellOutput+4096)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	command := "cat big.txt"
	if runtime.GOOS == "windows" {
		command = "type big.txt"
	}
	res, err := runShellCommand(context.Background(), dir, command, 10*time.Second, maxShellOutput)
	if err != nil {
		t.Fatalf("runShellCommand: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated output")
	}
	body := strings.TrimSuffix(res.Output, "\n...[output truncated]")
	if len(body) != maxShellOutput {
		t.Fatalf("kept %d bytes, want %d", len(body), maxShellOutput)
	}
	if !strings.HasPrefix(body, "xxxx") || !strings.HasSuffix(body, "xxxx") {
		t.Fatal("output body does not look like the first 64KB of the file")
	}
}

func TestRunShellCommandTimeout(t *testing.T) {
	command := "sleep 5"
	if runtime.GOOS == "windows" {
		command = "ping -n 6 127.0.0.1 >nul"
	}
	start := time.Now()
	res, err := runShellCommand(context.Background(), t.TempDir(), command, 300*time.Millisecond, 1024)
	if err != nil {
		t.Fatalf("runShellCommand: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("expected timeout result, got %+v", res)
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", res.ExitCode)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("timeout took %s, want a prompt kill", elapsed)
	}
}

func TestRunShellCommandRejectsEmptyCommandAndMissingWorkspace(t *testing.T) {
	if _, err := runShellCommand(context.Background(), t.TempDir(), "   ", time.Second, 1024); err == nil {
		t.Fatal("expected error for empty command")
	}
	if _, err := runShellCommand(context.Background(), filepath.Join(t.TempDir(), "missing"), "echo hi", time.Second, 1024); err == nil {
		t.Fatal("expected error for missing workspace")
	}
}

func TestRunShellCommandWithPolicyBlocksProtectedRedirect(t *testing.T) {
	dir := t.TempDir()
	_, err := runShellCommandWithPolicy(
		context.Background(), dir, `echo x>.git\config`, 5*time.Second, 1024,
		sandbox.DefaultPolicy(dir),
	)
	if err == nil || !strings.Contains(err.Error(), "protected_path") {
		t.Fatalf("expected protected_path error, got %v", err)
	}
}

func TestRunShellCommandWithPolicyConfinesWorkspaceWrites(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("OS sandbox is Windows-only in P0")
	}
	base, _ := os.UserHomeDir()
	root, err := os.MkdirTemp(base, "agentmate-shell-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = runShellCommandWithPolicy(
		context.Background(), workspace, `echo changed>..\outside.txt`, 10*time.Second, 1024,
		sandbox.DefaultPolicy(workspace),
	)
	if err != nil {
		t.Fatalf("sandboxed run: %v", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("outside file changed to %q", string(data))
	}
}

// shellToolTestContext supplies the real context methods sandbox.Run needs
// while keeping the ADK confirmation state under test control.
type shellToolTestContext struct {
	agent.ContextMock
	ctx          context.Context
	confirmation *toolconfirmation.ToolConfirmation
}

func (c *shellToolTestContext) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c *shellToolTestContext) Done() <-chan struct{}       { return c.ctx.Done() }
func (c *shellToolTestContext) Err() error                  { return c.ctx.Err() }
func (c *shellToolTestContext) Value(key any) any           { return c.ctx.Value(key) }
func (c *shellToolTestContext) Actions() *session.EventActions {
	return &session.EventActions{}
}
func (c *shellToolTestContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return c.confirmation
}

type runnableShellTool interface {
	tool.Tool
	Run(agent.Context, any) (map[string]any, error)
}

func TestShellEscalationRejectedWhenApprovalNever(t *testing.T) {
	dir := t.TempDir()
	tl, err := ShellToolWithPolicyAndMode(
		dir, false, config.ApprovalPolicyNever, sandbox.DefaultPolicy(dir), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tl.(runnableShellTool).Run(&shellToolTestContext{ctx: context.Background()}, map[string]any{
		"command":       "echo should-not-run>never-escalation.txt",
		"escalate":      true,
		"justification": "需要写入工作区外的构建缓存",
	})
	if err == nil || !strings.Contains(err.Error(), "never") {
		t.Fatalf("escalation under never should be rejected, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "never-escalation.txt")); !os.IsNotExist(statErr) {
		t.Fatal("rejected escalation must not execute the command")
	}
}

func TestShellEscalationForcesConfirmationInAutoApprove(t *testing.T) {
	dir := t.TempDir()
	tl, err := ShellToolWithPolicyAndMode(
		dir, false, config.ApprovalPolicyAutoApprove, sandbox.DefaultPolicy(dir), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tl.(runnableShellTool).Run(&shellToolTestContext{ctx: context.Background()}, map[string]any{
		"command":       "echo pending>auto-escalation.txt",
		"escalate":      true,
		"justification": "需要写入工作区外的构建缓存",
	})
	if !errors.Is(err, tool.ErrConfirmationRequired) {
		t.Fatalf("escalation should require confirmation, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "auto-escalation.txt")); !os.IsNotExist(statErr) {
		t.Fatal("command must not run before confirmation")
	}
}

func TestShellEscalationIgnoredInDangerMode(t *testing.T) {
	dir := t.TempDir()
	policy := sandbox.DefaultPolicy(dir)
	policy.Mode = sandbox.ModeDangerFullAccess
	tl, err := ShellToolWithPolicyAndMode(
		dir, false, config.ApprovalPolicyAutoApprove, policy, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tl.(runnableShellTool).Run(&shellToolTestContext{ctx: context.Background()}, map[string]any{
		"command":       "echo direct>danger-escalation.txt",
		"escalate":      true,
		"justification": "完全访问模式已经不在沙箱内",
	})
	if err != nil {
		t.Fatalf("danger mode should ignore escalate: %v (result %+v)", err, res)
	}
	if data, readErr := os.ReadFile(filepath.Join(dir, "danger-escalation.txt")); readErr != nil || strings.TrimSpace(string(data)) != "direct" {
		t.Fatalf("danger command did not run, data=%q err=%v", data, readErr)
	}
}
