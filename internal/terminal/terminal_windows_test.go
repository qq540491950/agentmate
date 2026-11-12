//go:build windows

package terminal

import (
	"context"
	"strings"
	"testing"

	"agentmate/internal/config"
)

func TestWindowsAutoPrefersGitBashOverPowerShell(t *testing.T) {
	host := fakeHost{
		goos: "windows",
		env: map[string]string{
			"ComSpec":      `C:\Windows\System32\cmd.exe`,
			"SystemRoot":   `C:\Windows`,
			"ProgramFiles": `C:\Program Files`,
			"LocalAppData": `C:\Users\me\AppData\Local`,
			"PATH":         `C:\Windows\System32`,
		},
		look: map[string]string{
			"pwsh.exe": `C:\Users\me\.cache\codex-runtimes\pwsh.exe`,
			"git.exe":  `C:\Program Files\Git\cmd\git.exe`,
		},
		exists: map[string]bool{
			strings.ToLower(`C:\Windows\System32\cmd.exe`):                               true,
			strings.ToLower(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`): true,
			strings.ToLower(`C:\Program Files\Git\cmd\git.exe`):                          true,
			strings.ToLower(`C:\Program Files\Git\bin\bash.exe`):                         true,
			strings.ToLower(`C:\Users\me\.cache\codex-runtimes\pwsh.exe`):                true,
		},
		probe: func(context.Context, string, ...string) (string, int, error) {
			return "", 0, nil
		},
	}
	inv := Detect(context.Background(), host.host())
	if inv.Auto.DisplayName != "自动（Git Bash）" {
		t.Fatalf("auto = %q, want Git Bash: %+v", inv.Auto.DisplayName, inv.Shells)
	}
	if len(inv.Shells) < 4 {
		t.Fatalf("shells = %+v, want pwsh/git/powershell/cmd", inv.Shells)
	}
	if inv.Shells[0].Mode != config.TerminalModePwsh || inv.Shells[0].AutoEligible {
		t.Fatalf("private pwsh should be visible but not auto-eligible: %+v", inv.Shells[0])
	}
	spec, err := Resolve(context.Background(), config.TerminalSettings{Mode: config.TerminalModeAuto}, host.host())
	if err != nil {
		t.Fatal(err)
	}
	if spec.Mode != config.TerminalModeGitBash || !strings.HasSuffix(spec.Path, `bin\bash.exe`) {
		t.Fatalf("resolved auto = %+v, want Git Bash", spec)
	}
	workspaceAuto, err := Resolve(context.Background(), config.TerminalSettings{Mode: config.TerminalModeAuto}, host.host(), "workspace-write")
	if err != nil {
		t.Fatal(err)
	}
	if workspaceAuto.Mode != config.TerminalModePowerShell {
		t.Fatalf("workspace-write auto = %+v, want PowerShell 5.1", workspaceAuto)
	}
	workspaceInventory := Detect(context.Background(), host.host(), "workspace-write")
	if !strings.Contains(workspaceInventory.Auto.DisplayName, "Windows PowerShell 5.1") {
		t.Fatalf("workspace-write inventory auto = %+v", workspaceInventory.Auto)
	}
}

func TestWindowsStoreAliasIsExplicitOnly(t *testing.T) {
	host := fakeHost{
		goos: "windows",
		env: map[string]string{
			"ComSpec":      `C:\Windows\System32\cmd.exe`,
			"SystemRoot":   `C:\Windows`,
			"ProgramFiles": `C:\Program Files`,
			"LocalAppData": `C:\Users\me\AppData\Local`,
		},
		exists: map[string]bool{
			strings.ToLower(`C:\Windows\System32\cmd.exe`):                               true,
			strings.ToLower(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`): true,
			strings.ToLower(`C:\Users\me\AppData\Local\Microsoft\WindowsApps\pwsh.exe`):  true,
		},
		probe: func(context.Context, string, ...string) (string, int, error) {
			return "", 0, nil
		},
	}
	inv := Detect(context.Background(), host.host())
	if !strings.Contains(inv.Auto.DisplayName, "Windows PowerShell 5.1") {
		t.Fatalf("auto should skip store alias: %+v", inv)
	}
	for _, shell := range inv.Shells {
		if shell.Mode == config.TerminalModePwsh && shell.AutoEligible {
			t.Fatalf("store alias must not be auto-eligible: %+v", shell)
		}
	}
}

func TestWSLDistroDecodeAndInvocation(t *testing.T) {
	wsl := `C:\Windows\System32\wsl.exe`
	output := utf16LE("Ubuntu-24.04\r\nDebian\r\n")
	host := fakeHost{
		goos: "windows",
		env: map[string]string{
			"SystemRoot":   `C:\Windows`,
			"ComSpec":      `C:\Windows\System32\cmd.exe`,
			"ProgramFiles": `C:\Program Files`,
			"LocalAppData": `C:\Users\me\AppData\Local`,
		},
		exists: map[string]bool{
			strings.ToLower(wsl):                           true,
			strings.ToLower(`C:\Windows\System32\cmd.exe`): true,
		},
		probe: func(_ context.Context, name string, args ...string) (string, int, error) {
			if strings.EqualFold(name, wsl) && len(args) == 2 && args[0] == "--list" {
				return output, 0, nil
			}
			return "", 0, nil
		},
	}
	inv := Detect(context.Background(), host.host())
	distros := map[string]bool{}
	for _, d := range inv.WSLDistros {
		distros[d] = true
	}
	if len(inv.WSLDistros) != 2 || !distros["Ubuntu-24.04"] || !distros["Debian"] {
		t.Fatalf("wsl distros = %#v", inv.WSLDistros)
	}
	spec, err := Resolve(context.Background(), config.TerminalSettings{
		Mode:      config.TerminalModeWSL,
		WSLDistro: "Ubuntu-24.04",
		WSLUser:   "dev",
	}, host.host())
	if err != nil {
		t.Fatal(err)
	}
	if !spec.RequiresFullAccess || spec.Sandbox != SandboxFullAccess {
		t.Fatalf("wsl sandbox = %+v", spec)
	}
	run := spec.Invocation(`D:\project\agentmate`, "pwd")
	if run.Command != wsl || !containsAll(run.Args, "-d", "Ubuntu-24.04", "-u", "dev", "--cd", `D:\project\agentmate`, "--exec", "/bin/bash") {
		t.Fatalf("wsl invocation = %+v", run)
	}
}

func TestExplicitMissingShellDoesNotFallback(t *testing.T) {
	host := fakeHost{
		goos: "windows",
		env: map[string]string{
			"ComSpec":    `C:\Windows\System32\cmd.exe`,
			"SystemRoot": `C:\Windows`,
		},
		exists: map[string]bool{
			strings.ToLower(`C:\Windows\System32\cmd.exe`): true,
		},
		probe: func(context.Context, string, ...string) (string, int, error) {
			return "", 0, nil
		},
	}
	_, err := Resolve(context.Background(), config.TerminalSettings{Mode: config.TerminalModePwsh}, host.host())
	if err == nil || !strings.Contains(err.Error(), "pwsh") {
		t.Fatalf("expected explicit missing error, got %v", err)
	}
}

func TestPowerShellInvocationUsesExactExitCodeWrapper(t *testing.T) {
	spec := Spec{Mode: config.TerminalModePowerShell, Path: `C:\pwsh.exe`, DisplayName: "Windows PowerShell 5.1"}
	run := spec.Invocation(`C:\workspace`, "cmd /c exit 5")
	script := run.Args[len(run.Args)-1]
	if !containsAll(run.Args, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command") {
		t.Fatalf("args = %#v", run.Args)
	}
	if !strings.Contains(script, "$LASTEXITCODE") || !strings.Contains(script, "exit $LASTEXITCODE") {
		t.Fatalf("script does not propagate exit code: %q", script)
	}
}

func TestGitBashInvocationPreservesWorkspaceAndEnv(t *testing.T) {
	spec := Spec{
		Mode:        config.TerminalModeGitBash,
		Path:        `C:\Program Files\Git\bin\bash.exe`,
		DisplayName: "Git Bash",
		Env:         []string{"CHERE_INVOKING=1", "MSYSTEM=MINGW64"},
	}
	run := spec.Invocation(`C:\workspace with space`, "pwd")
	if run.Dir != `C:\workspace with space` || !containsAll(run.Args, "--noprofile", "--norc", "-c", "pwd") {
		t.Fatalf("git bash invocation = %+v", run)
	}
	if !containsAll(run.Env, "CHERE_INVOKING=1", "MSYSTEM=MINGW64") {
		t.Fatalf("git bash env = %#v", run.Env)
	}
}
