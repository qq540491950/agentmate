package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

func TestProxyEnvironmentOnlyWhenConfigured(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://parent-proxy.invalid:7890")
	t.Setenv("HTTPS_PROXY", "http://parent-proxy.invalid:7890")
	t.Setenv("NO_PROXY", "*")

	policy := DefaultPolicy(t.TempDir())
	unlessConfigured := environmentValues(sanitizedEnvironment(t.TempDir(), policy.sandboxEnvironment()))
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if _, ok := unlessConfigured[key]; ok {
			t.Fatalf("parent %s leaked into sandbox environment", key)
		}
	}

	policy = policy.WithProxy("  http://127.0.0.1:7890  ")
	configured := environmentValues(sanitizedEnvironment(t.TempDir(), policy.sandboxEnvironment()))
	if got := configured["HTTP_PROXY"]; got != "http://127.0.0.1:7890" {
		t.Fatalf("HTTP_PROXY = %q", got)
	}
	if got := configured["HTTPS_PROXY"]; got != "http://127.0.0.1:7890" {
		t.Fatalf("HTTPS_PROXY = %q", got)
	}
	if got := configured["NO_PROXY"]; got != "localhost,127.0.0.1,::1" {
		t.Fatalf("NO_PROXY = %q", got)
	}
}

func TestDangerFullAccessKeepsParentProxyEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://parent-proxy.invalid:7890")
	policy := DefaultPolicy(t.TempDir())
	policy.Mode = ModeDangerFullAccess
	policy.ProxyURL = "http://sandbox-proxy.invalid:7890"

	command, args := "sh", []string{"-c", `printf '%s' "$HTTP_PROXY"`}
	if runtime.GOOS == "windows" {
		command, args = "cmd.exe", []string{"/d", "/c", "echo %HTTP_PROXY%"}
	}
	res, err := Run(context.Background(), Request{
		Command: command,
		Args:    args,
		Dir:     policy.Workspace,
		Timeout: 10 * time.Second,
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "http://parent-proxy.invalid:7890") {
		t.Fatalf("direct execution did not inherit the parent proxy: %q", res.Output)
	}
	if strings.Contains(res.Output, "sandbox-proxy.invalid") {
		t.Fatalf("direct execution consumed the sandbox proxy: %q", res.Output)
	}
}

func environmentValues(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
