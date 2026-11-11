//go:build windows

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var getConsoleWindow = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
var freeConsole = syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole")

// TestDangerFullAccessHidesConsole pins the regression that returned with the
// sandbox refactor: the desktop app is a GUI process without a console, so the
// danger-full-access direct path must pass CREATE_NO_WINDOW or every cmd.exe
// tool call allocates a console window that flashes over the app (issue #54).
//
// The test binary re-executes itself twice. The middle process detaches its
// console (FreeConsole) and calls sandbox.Run exactly like the GUI app; the
// grandchild reports GetConsoleWindow. Without CREATE_NO_WINDOW cmd.exe
// allocates a console and the grandchild sees a non-zero handle.
func TestDangerFullAccessHidesConsole(t *testing.T) {
	switch os.Getenv("AGENTMATE_CONSOLE_PROBE") {
	case "report":
		hwnd, _, _ := getConsoleWindow.Call()
		fmt.Printf("CONSOLE=%d\n", hwnd)
		os.Exit(0)
	case "parent":
		probe := os.Getenv("AGENTMATE_CONSOLE_PROBE_BAT")
		dir := os.Getenv("AGENTMATE_CONSOLE_PROBE_DIR")
		// Detach the console so this process looks like the -H windowsgui app.
		_, _, _ = freeConsole.Call()
		policy := DefaultPolicy(dir)
		policy.Mode = ModeDangerFullAccess
		res, err := Run(context.Background(), Request{
			Command:     "cmd.exe",
			Args:        []string{"/d", "/c", probe},
			Dir:         dir,
			Timeout:     30 * time.Second,
			OutputLimit: 16 << 10,
		}, policy)
		fmt.Printf("RESULT_ERR=%v\nRESULT_OUT=%q\n", err, res.Output)
		os.Exit(0)
	}

	dir := t.TempDir()
	probe := filepath.Join(dir, "probe.bat")
	body := fmt.Sprintf("@echo off\r\nset AGENTMATE_CONSOLE_PROBE=report\r\n\"%s\" -test.run=^TestDangerFullAccessHidesConsole$\r\n", os.Args[0])
	if err := os.WriteFile(probe, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestDangerFullAccessHidesConsole$")
	cmd.Env = append(os.Environ(),
		"AGENTMATE_CONSOLE_PROBE=parent",
		"AGENTMATE_CONSOLE_PROBE_BAT="+probe,
		"AGENTMATE_CONSOLE_PROBE_DIR="+dir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe parent: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "CONSOLE=0") {
		return
	}
	t.Fatalf("danger-full-access child inherited a console window:\n%s", out)
}

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

// TestWithinCanonicalizesWindowsShortPaths pins the CI failure where the
// config directory arrived as C:\Users\RUNNER~1\... while the workspace was
// resolved to the long C:\Users\runneradmin\... spelling. The lexical check
// said "outside", the app directory was added as a protected path, and every
// workspace write was denied.
func TestWithinCanonicalizesWindowsShortPaths(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "tasks", "sess")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	short, err := shortPath(root)
	if err != nil || strings.EqualFold(short, root) {
		t.Skipf("8.3 short names unavailable: short=%q err=%v", short, err)
	}
	if !Within(short, workspace) {
		t.Fatalf("Within(%q, %q) = false; short and long spellings must match", short, workspace)
	}
	if !Within(root, filepath.Join(short, "tasks", "sess")) {
		t.Fatalf("Within(%q, %q) = false; short and long spellings must match", root, short)
	}
}

func shortPath(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	if n == 0 || int(n) > len(buf) {
		return "", os.ErrInvalid
	}
	return windows.UTF16ToString(buf[:n]), nil
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
