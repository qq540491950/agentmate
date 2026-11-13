package agentkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool"

	"agentmate/internal/config"
	"agentmate/internal/sandbox"
)

func TestPreviewGuardCheckWritePath(t *testing.T) {
	root := t.TempDir()
	guard := NewPreviewGuard(root, config.SandboxProfileWorkspaceWrite, config.SandboxNetworkOff)
	if ok, code, reason := guard.CheckWritePath("inside.txt"); !ok {
		t.Fatalf("inside path rejected: %s %s", code, reason)
	}
	if ok, code, _ := guard.CheckWritePath("../escape.txt"); ok || code != "outside_workspace" {
		t.Fatalf("outside path = %v (%s), want outside_workspace", ok, code)
	}
	if ok, code, _ := guard.CheckWritePath(".git/config"); ok || code != "protected_path" {
		t.Fatalf("protected path = %v (%s), want protected_path", ok, code)
	}

	readOnly := NewPreviewGuard(root, config.SandboxProfileReadOnly, config.SandboxNetworkOff)
	if ok, code, _ := readOnly.CheckWritePath("inside.txt"); ok || code != "read_only" {
		t.Fatalf("read-only path = %v (%s), want read_only", ok, code)
	}

	full := NewPreviewGuard(root, config.SandboxProfileDangerFullAccess, config.SandboxNetworkOff)
	if ok, code, reason := full.CheckWritePath(".git/config"); !ok {
		t.Fatalf("danger full access rejected protected path: %s %s", code, reason)
	}
	// 内置文件工具始终以任务工作区为根，即使 OS 沙箱已禁用；仅放宽受保护路径检查。
	if ok, code, _ := full.CheckWritePath("../escape.txt"); ok || code != "outside_workspace" {
		t.Fatalf("danger full access outside path = %v (%s)", ok, code)
	}
}

func TestPreviewGuardCheckCommand(t *testing.T) {
	root := t.TempDir()
	guard := NewPreviewGuard(root, config.SandboxProfileWorkspaceWrite, config.SandboxNetworkOff)
	if ok, _, reason := guard.CheckCommand("git status"); !ok {
		t.Fatalf("safe command rejected: %s", reason)
	}
	if ok, code, _ := guard.CheckCommand(`echo x>.git\config`); ok || code != "protected_path" {
		t.Fatalf("protected redirect = %v (%s), want protected_path", ok, code)
	}
	full := NewPreviewGuard(root, config.SandboxProfileDangerFullAccess, config.SandboxNetworkOff)
	if ok, code, reason := full.CheckCommand(`echo x>.git\config`); !ok {
		t.Fatalf("danger full access rejected command: %s %s", code, reason)
	}
}

func TestWriteToolConfirmationSkipsPreflightFailures(t *testing.T) {
	root := t.TempDir()
	policy := sandbox.DefaultPolicy(root)
	tl, err := WriteFileToolWithPolicy(root, true, policy)
	if err != nil {
		t.Fatal(err)
	}
	run := tl.(runnableShellTool)
	ctx := &shellToolTestContext{ctx: context.Background()}

	_, err = run.Run(ctx, map[string]any{"path": "../escape.txt", "content": "x"})
	if err == nil || errors.Is(err, tool.ErrConfirmationRequired) {
		t.Fatalf("outside write should fail without confirmation, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("outside write created a file: %v", statErr)
	}

	_, err = run.Run(ctx, map[string]any{"path": ".git/config", "content": "x"})
	if err == nil || errors.Is(err, tool.ErrConfirmationRequired) ||
		!strings.Contains(err.Error(), "protected_path") {
		t.Fatalf("protected write should fail without confirmation, got %v", err)
	}

	_, err = run.Run(ctx, map[string]any{"path": "ok.txt", "content": "x"})
	if !errors.Is(err, tool.ErrConfirmationRequired) {
		t.Fatalf("normal write should ask for confirmation, got %v", err)
	}
}

func TestEditToolConfirmationSkipsPreflightFailures(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := sandbox.DefaultPolicy(root)
	tl, err := EditFileToolWithPolicy(root, true, policy)
	if err != nil {
		t.Fatal(err)
	}
	run := tl.(runnableShellTool)
	ctx := &shellToolTestContext{ctx: context.Background()}

	_, err = run.Run(ctx, map[string]any{
		"path": "../escape.txt", "oldText": "x", "newText": "y",
	})
	if err == nil || errors.Is(err, tool.ErrConfirmationRequired) {
		t.Fatalf("outside edit should fail without confirmation, got %v", err)
	}

	_, err = run.Run(ctx, map[string]any{
		"path": "notes.txt", "oldText": "before", "newText": "after",
	})
	if !errors.Is(err, tool.ErrConfirmationRequired) {
		t.Fatalf("normal edit should ask for confirmation, got %v", err)
	}
}

func TestShellToolConfirmationSkipsPreflightFailure(t *testing.T) {
	root := t.TempDir()
	tl, err := ShellToolWithPolicy(root, true, sandbox.DefaultPolicy(root))
	if err != nil {
		t.Fatal(err)
	}
	run := tl.(runnableShellTool)
	ctx := &shellToolTestContext{ctx: context.Background()}
	_, err = run.Run(ctx, map[string]any{"command": `echo x>.git\config`})
	if err == nil || errors.Is(err, tool.ErrConfirmationRequired) ||
		!strings.Contains(err.Error(), "protected_path") {
		t.Fatalf("protected command should fail without confirmation, got %v", err)
	}
	_, err = run.Run(ctx, map[string]any{"command": "echo ok"})
	if !errors.Is(err, tool.ErrConfirmationRequired) {
		t.Fatalf("normal command should ask for confirmation, got %v", err)
	}
}
