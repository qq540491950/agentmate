//go:build windows

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sandboxTestRoot(t *testing.T) string {
	t.Helper()
	base, err := os.UserHomeDir()
	if err != nil {
		return t.TempDir()
	}
	root, err := os.MkdirTemp(base, "agentmate-sandbox-test-")
	if err != nil {
		return t.TempDir()
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func TestWindowsWorkspaceWriteBlocksOutside(t *testing.T) {
	if !Available() {
		t.Skipf("AppContainer unavailable: %s", BackendStatus().Error)
	}
	defer Close()

	root := sandboxTestRoot(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy(workspace)
	res, err := Run(context.Background(), Request{
		Command: "cmd.exe",
		Args: []string{"/d", "/c",
			"echo inside>inside.txt & echo outside>..\\outside.txt"},
		Dir:         workspace,
		Timeout:     20 * time.Second,
		OutputLimit: 16 << 10,
	}, policy)
	if err != nil {
		t.Fatalf("sandboxed run: %v (status %+v)", err, BackendStatus())
	}
	if _, err := os.Stat(filepath.Join(workspace, "inside.txt")); err != nil {
		t.Fatalf("inside write missing: %v (result %+v)", err, res)
	}
	if _, err := os.Stat(outside); err == nil {
		data, _ := os.ReadFile(outside)
		if string(data) != "original" {
			t.Fatalf("outside write escaped sandbox: %s -> %q (result %+v)", outside, string(data), res)
		}
	}
}

func TestPolicyCheckWriteBlocksProtectedPaths(t *testing.T) {
	workspace := t.TempDir()
	policy := DefaultPolicy(workspace)
	for _, rel := range []string{".git/config", ".agents/skills/x/SKILL.md", "src/main.go"} {
		path := filepath.Join(workspace, rel)
		ok, code, _ := policy.CheckWrite(path)
		wantOK := rel == "src/main.go"
		if ok != wantOK {
			t.Fatalf("CheckWrite(%s) = %v (%s), want %v", rel, ok, code, wantOK)
		}
		if !wantOK && code != "protected_path" {
			t.Fatalf("CheckWrite(%s) code = %s, want protected_path", rel, code)
		}
	}
	outside := filepath.Join(filepath.Dir(workspace), "outside.txt")
	if ok, code, _ := policy.CheckWrite(outside); ok || code != "outside_workspace" {
		t.Fatalf("outside CheckWrite = %v (%s), want outside_workspace", ok, code)
	}
}

func TestPolicyCheckCommandBlocksProtectedWrites(t *testing.T) {
	policy := DefaultPolicy(t.TempDir())
	if ok, _, _ := policy.CheckCommand(`echo x>.git\config`); ok {
		t.Fatal("expected .git redirection to be denied")
	}
	if ok, _, _ := policy.CheckCommand(`git status`); !ok {
		t.Fatal("git status must remain allowed")
	}
}

func TestPolicyCheckWriteAllowsNewWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "tasks", "sess")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy(workspace)
	ok, code, reason := policy.CheckWrite(filepath.Join(workspace, "same.txt"))
	if !ok {
		t.Fatalf("new workspace file denied: %s %s", code, reason)
	}
}

func TestWindowsEnvironmentDoesNotInheritSecrets(t *testing.T) {
	if !Available() {
		t.Skipf("AppContainer unavailable: %s", BackendStatus().Error)
	}
	defer Close()

	t.Setenv("OPENAI_API_KEY", "sk-test-secret-that-must-not-leak")
	root := sandboxTestRoot(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), Request{
		Command: "cmd.exe",
		Args:    []string{"/d", "/c", "echo %OPENAI_API_KEY%"},
		Dir:     workspace,
		Timeout: 20 * time.Second,
	}, DefaultPolicy(workspace))
	if err != nil {
		t.Fatalf("sandboxed run: %v", err)
	}
	if strings.Contains(res.Output, "sk-test-secret-that-must-not-leak") {
		t.Fatalf("secret leaked into sandbox environment: %q", res.Output)
	}
}

func TestWindowsNetworkOff(t *testing.T) {
	if !Available() {
		t.Skipf("AppContainer unavailable: %s", BackendStatus().Error)
	}
	defer Close()
	root := sandboxTestRoot(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), Request{
		Command: "cmd.exe",
		Args:    []string{"/d", "/c", "curl.exe -s -o NUL --max-time 8 https://example.com && echo NET_OK || echo NET_FAIL"},
		Dir:     workspace,
		Timeout: 20 * time.Second,
	}, DefaultPolicy(workspace))
	if err != nil {
		t.Fatalf("sandboxed run: %v", err)
	}
	t.Logf("output:\n%s", res.Output)
	if strings.Contains(strings.ToLower(res.Output), "net_ok") {
		t.Fatalf("network unexpectedly reachable: %q", res.Output)
	}
}
