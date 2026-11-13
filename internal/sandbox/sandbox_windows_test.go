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

// TestDangerFullAccessHidesConsole 固定了沙箱重构后重新出现的回归问题：桌面应用是一个没有控制台的 GUI 进程，因此
// danger-full-access 直接路径必须传递 CREATE_NO_WINDOW，否则每次 cmd.exe
// 工具调用都会分配一个控制台窗口，并在应用上方闪一下（issue #54）。
//
// 测试二进制会重新执行自身两次。中间进程会分离其控制台（FreeConsole），并像 GUI
// 应用一样精确调用 sandbox.Run；孙进程会报告 GetConsoleWindow。没有
// CREATE_NO_WINDOW 时，cmd.exe 会分配一个控制台，孙进程会看到非零句柄。
func TestDangerFullAccessHidesConsole(t *testing.T) {
	switch os.Getenv("AGENTMATE_CONSOLE_PROBE") {
	case "report":
		hwnd, _, _ := getConsoleWindow.Call()
		fmt.Printf("CONSOLE=%d\n", hwnd)
		os.Exit(0)
	case "parent":
		probe := os.Getenv("AGENTMATE_CONSOLE_PROBE_BAT")
		dir := os.Getenv("AGENTMATE_CONSOLE_PROBE_DIR")
		// 分离控制台，使此进程看起来像 -H windowsgui 应用。
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

func TestWindowsReadOnlyAllowsReadsAndBlocksWrites(t *testing.T) {
	if !Available() {
		t.Skipf("AppContainer unavailable: %s", BackendStatus().Error)
	}
	defer Close()

	root := sandboxTestRoot(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(workspace, "read.txt")
	if err := os.WriteFile(inside, []byte("read-ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 证明只读模式也会撤销同一目录上先前 workspace-write 运行遗留的 Modify ACE。
	warm, err := Run(context.Background(), Request{
		Command: "cmd.exe",
		Args:    []string{"/d", "/c", "echo warm>warmed.txt"},
		Dir:     workspace,
		Timeout: 20 * time.Second,
	}, DefaultPolicy(workspace))
	if err != nil || warm.ExitCode != 0 {
		t.Fatalf("workspace-write warmup failed: %+v err=%v", warm, err)
	}

	policy := DefaultPolicy(workspace)
	policy.Mode = ModeReadOnly
	run := func(command string) Result {
		t.Helper()
		res, err := Run(context.Background(), Request{
			Command:     "cmd.exe",
			Args:        []string{"/d", "/c", command},
			Dir:         workspace,
			Timeout:     20 * time.Second,
			OutputLimit: 16 << 10,
		}, policy)
		if err != nil {
			t.Fatalf("sandboxed run %q: %v (status %+v)", command, err, BackendStatus())
		}
		return res
	}

	if res := run("type read.txt"); res.ExitCode != 0 || !strings.Contains(res.Output, "read-ok") {
		t.Fatalf("read failed: %+v", res)
	}
	run("echo changed>read.txt")
	if data, err := os.ReadFile(inside); err != nil || string(data) != "read-ok" {
		t.Fatalf("workspace write was not blocked: %q err=%v", data, err)
	}
	run(`echo escaped>"` + outside + `"`)
	if data, err := os.ReadFile(outside); err != nil || string(data) != "original" {
		t.Fatalf("outside write was not blocked: %q err=%v", data, err)
	}
}

func TestWindowsReadOnlyCanRunGitStatus(t *testing.T) {
	if !Available() {
		t.Skipf("AppContainer unavailable: %s", BackendStatus().Error)
	}
	defer Close()

	root := sandboxTestRoot(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	initCmd := exec.Command("git", "-C", workspace, "init", "-q")
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v: %s", err, out)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	gitBin := filepath.Join(filepath.Dir(filepath.Dir(gitPath)), "mingw64", "bin")
	if _, err := os.Stat(filepath.Join(gitBin, "git.exe")); err != nil {
		t.Skipf("Git for Windows layout unavailable: %v", err)
	}
	sandboxGit := filepath.Join(workspace, "gitbin")
	if err := os.Mkdir(sandboxGit, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"git.exe", "libintl-8.dll", "libiconv-2.dll",
		"libpcre2-8-0.dll", "libwinpthread-1.dll", "zlib1.dll",
	} {
		data, err := os.ReadFile(filepath.Join(gitBin, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(sandboxGit, name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	policy := DefaultPolicy(workspace)
	policy.Mode = ModeReadOnly
	res, err := Run(context.Background(), Request{
		Command:     filepath.Join(sandboxGit, "git.exe"),
		Args:        []string{"status", "--short"},
		Dir:         workspace,
		Timeout:     20 * time.Second,
		OutputLimit: 16 << 10,
	}, policy)
	if err != nil {
		t.Fatalf("sandboxed git status: %v (status %+v)", err, BackendStatus())
	}
	if res.ExitCode != 0 {
		t.Fatalf("git status failed: %+v", res)
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

// TestWithinCanonicalizesWindowsShortPaths 固定了 CI 失败：配置目录以
// C:\Users\RUNNER~1\... 形式到达，而工作区被解析为长路径形式
// C:\Users\runneradmin\...。词法检查判定为“outside”，应用目录被添加为受保护路径
// ，且每次工作区写入都被拒绝。
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

func TestWindowsProxyEnvironment(t *testing.T) {
	if !Available() {
		t.Skipf("AppContainer unavailable: %s", BackendStatus().Error)
	}
	defer Close()

	t.Setenv("HTTP_PROXY", "http://parent-proxy.invalid:7890")
	t.Setenv("HTTPS_PROXY", "http://parent-proxy.invalid:7890")
	t.Setenv("NO_PROXY", "*")

	root := sandboxTestRoot(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := func(policy Policy) string {
		t.Helper()
		res, err := Run(context.Background(), Request{
			Command: "cmd.exe",
			Args: []string{"/d", "/c",
				"echo HTTP_PROXY=[%HTTP_PROXY%]&echo HTTPS_PROXY=[%HTTPS_PROXY%]&echo NO_PROXY=[%NO_PROXY%]"},
			Dir:     workspace,
			Timeout: 20 * time.Second,
		}, policy)
		if err != nil {
			t.Fatalf("sandboxed run: %v", err)
		}
		return res.Output
	}

	if got := probe(DefaultPolicy(workspace)); strings.Contains(strings.ToLower(got), "parent-proxy.invalid") ||
		strings.Contains(got, "NO_PROXY=[*]") {
		t.Fatalf("unconfigured sandbox inherited proxy variables: %q", got)
	}

	got := probe(DefaultPolicy(workspace).WithProxy("http://127.0.0.1:7890"))
	for _, want := range []string{
		"HTTP_PROXY=[http://127.0.0.1:7890]",
		"HTTPS_PROXY=[http://127.0.0.1:7890]",
		"NO_PROXY=[localhost,127.0.0.1,::1]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("sandbox environment missing %q: %q", want, got)
		}
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
