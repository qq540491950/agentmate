//go:build !windows

package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceWriteFailsClosedWithoutOSBackend(t *testing.T) {
	res, err := Run(context.Background(), Request{
		Command: "echo",
		Args:    []string{"must-not-run"},
		Timeout: 5 * time.Second,
	}, DefaultPolicy(t.TempDir()))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("workspace-write error = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "当前平台未实现 OS 沙箱") {
		t.Fatalf("workspace-write error missing platform message: %v", err)
	}
	if res.ExitCode != 0 || res.Output != "" {
		t.Fatalf("workspace-write must not execute the command: %+v", res)
	}
}

func TestReadOnlyFailsClosedWithoutOSBackend(t *testing.T) {
	policy := DefaultPolicy(t.TempDir())
	policy.Mode = ModeReadOnly
	res, err := Run(context.Background(), Request{
		Command: "echo",
		Args:    []string{"must-not-run"},
		Timeout: 5 * time.Second,
	}, policy)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("read-only error = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "当前平台未实现 OS 沙箱") {
		t.Fatalf("read-only error missing platform message: %v", err)
	}
	if res.ExitCode != 0 || res.Output != "" {
		t.Fatalf("read-only must not execute the command: %+v", res)
	}
}

func TestDangerFullAccessRunsDirectWithoutOSBackend(t *testing.T) {
	policy := DefaultPolicy(t.TempDir())
	policy.Mode = ModeDangerFullAccess
	res, err := Run(context.Background(), Request{
		Command: "echo",
		Args:    []string{"direct-ok"},
		Timeout: 5 * time.Second,
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Output) != "direct-ok" {
		t.Fatalf("danger-full-access result = %+v", res)
	}
}
