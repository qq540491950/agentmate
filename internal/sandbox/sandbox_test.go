package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyPolicyDeniesEveryWrite(t *testing.T) {
	workspace := t.TempDir()
	policy := DefaultPolicy(workspace)
	policy.Mode = ModeReadOnly
	if err := policy.validate(); err != nil {
		t.Fatalf("read-only policy rejected: %v", err)
	}
	for _, path := range []string{
		filepath.Join(workspace, "inside.txt"),
		filepath.Join(workspace, ".git", "config"),
		filepath.Join(filepath.Dir(workspace), "outside.txt"),
	} {
		ok, code, reason := policy.CheckWrite(path)
		if ok || code != "read_only" {
			t.Fatalf("CheckWrite(%s) = %v (%s), want read_only", path, ok, code)
		}
		if !strings.Contains(reason, "只读") {
			t.Fatalf("CheckWrite(%s) reason = %q", path, reason)
		}
	}
}

func TestReadOnlyRunRejectsDirectoryOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy(workspace)
	policy.Mode = ModeReadOnly
	res, err := Run(context.Background(), Request{
		Command: "definitely-not-run",
		Dir:     outside,
		Timeout: time.Second,
	}, policy)
	if err != nil {
		t.Fatalf("outside dir should be a policy denial, got error: %v", err)
	}
	if !res.Denied || res.DeniedCode != "outside_workspace" {
		t.Fatalf("outside dir result = %+v, want outside_workspace denial", res)
	}
}
