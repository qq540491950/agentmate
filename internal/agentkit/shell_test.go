package agentkit

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
