// Package terminal resolves the non-interactive shell used by run_command.
//
// The package is deliberately independent of agentkit: detection is useful to
// the settings service, and the shell tool only needs the resulting Spec.
package terminal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"agentmate/internal/config"
	"agentmate/internal/sandbox"
)

const (
	SandboxOK         = "ok"
	SandboxUnknown    = "unknown"
	SandboxFullAccess = "requires-full-access"
	defaultWSLShell   = "/bin/bash"
)

// Detected is one shell shown in the settings picker. Path is the executable
// path that would actually run; Source explains where it came from.
type Detected struct {
	Mode         string `json:"mode"`
	DisplayName  string `json:"displayName"`
	Path         string `json:"path"`
	Version      string `json:"version,omitempty"`
	Source       string `json:"source"`
	Sandbox      string `json:"sandbox"`
	AutoEligible bool   `json:"autoEligible"`
	WSLDistro    string `json:"wslDistro,omitempty"`
}

// Inventory is the complete detection result for the current host.
type Inventory struct {
	OS         string     `json:"os"`
	Auto       Detected   `json:"auto"`
	Shells     []Detected `json:"shells"`
	WSL        []Detected `json:"wsl,omitempty"`
	WSLDistros []string   `json:"wslDistros,omitempty"`
	Errors     []string   `json:"errors,omitempty"`
}

// ProbeResult is the fixed-command result shown by the settings test action.
type ProbeResult struct {
	Mode        string `json:"mode"`
	DisplayName string `json:"displayName"`
	Path        string `json:"path"`
	Output      string `json:"output"`
	ExitCode    int    `json:"exitCode"`
	Sandbox     string `json:"sandbox"`
}

// Spec is the runtime shell adapter consumed by the shell tool.
type Spec struct {
	Mode               string
	DisplayName        string
	Path               string
	Version            string
	Sandbox            string
	RequiresFullAccess bool
	Env                []string
	WSLDistro          string
	WSLUser            string
	WSLShell           string
	LoadProfile        bool
}

// Invocation is the exact process invocation for one command.
type Invocation struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
}

// Host makes detection testable without depending on the test machine.
type Host struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	Probe    func(context.Context, string, ...string) (string, int, error)
}

// DefaultHost uses the real process environment and hidden direct probes.
func DefaultHost() Host {
	return Host{
		GOOS:     runtime.GOOS,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
		Probe:    defaultProbe,
	}
}

func defaultProbe(ctx context.Context, name string, args ...string) (string, int, error) {
	res, err := sandbox.Run(ctx, sandbox.Request{
		Command:     name,
		Args:        args,
		Dir:         os.TempDir(),
		Timeout:     3 * time.Second,
		OutputLimit: 32 << 10,
	}, sandbox.Policy{
		Mode:      sandbox.ModeDangerFullAccess,
		Network:   sandbox.NetworkOn,
		Workspace: os.TempDir(),
	})
	if err != nil {
		return "", -1, err
	}
	return res.Output, res.ExitCode, nil
}

func normalizeHost(h Host) Host {
	if h.GOOS == "" {
		h.GOOS = runtime.GOOS
	}
	if h.Getenv == nil {
		h.Getenv = os.Getenv
	}
	if h.LookPath == nil {
		h.LookPath = exec.LookPath
	}
	if h.Stat == nil {
		h.Stat = os.Stat
	}
	if h.Probe == nil {
		h.Probe = defaultProbe
	}
	return h
}

// Detect lists the shells available on this host. Missing shells are omitted;
// WSL distributions are returned separately because they need full access.
func Detect(ctx context.Context, host Host, sandboxMode ...string) Inventory {
	host = normalizeHost(host)
	candidates, errs := detectCandidates(ctx, host, true, true)
	inv := Inventory{OS: host.GOOS, Errors: errs}
	for _, c := range candidates {
		if c.Mode == config.TerminalModeWSL {
			inv.WSL = append(inv.WSL, c.Detected)
			continue
		}
		inv.Shells = append(inv.Shells, c.Detected)
	}
	if auto := firstAuto(candidates, firstString(sandboxMode)); auto != nil {
		inv.Auto = auto.Detected
		inv.Auto.Mode = config.TerminalModeAuto
		inv.Auto.DisplayName = "自动（" + auto.DisplayName + "）"
	}
	for _, w := range inv.WSL {
		if w.WSLDistro != "" {
			inv.WSLDistros = append(inv.WSLDistros, w.WSLDistro)
		}
	}
	return inv
}

// Resolve returns the runtime spec for the saved setting. Only auto may fall
// back; an explicitly selected shell must exist.
func Resolve(ctx context.Context, settings config.TerminalSettings, host Host, sandboxMode ...string) (Spec, error) {
	host = normalizeHost(host)
	settings = config.NormalizeTerminalSettings(settings)
	if settings.Mode == config.TerminalModeCustom {
		return customSpec(settings)
	}
	candidates, _ := detectCandidates(ctx, host, false, settings.Mode == config.TerminalModeWSL)
	var chosen *candidate
	if settings.Mode == config.TerminalModeAuto {
		chosen = firstAuto(candidates, firstString(sandboxMode))
	} else {
		for i := range candidates {
			c := &candidates[i]
			if c.Mode != settings.Mode {
				continue
			}
			if settings.Mode == config.TerminalModeWSL && settings.WSLDistro != "" &&
				!strings.EqualFold(c.WSLDistro, settings.WSLDistro) {
				continue
			}
			chosen = c
			break
		}
	}
	if chosen == nil {
		if settings.Mode == config.TerminalModeAuto {
			return LegacySpec(), nil
		}
		if settings.Mode == config.TerminalModeWSL {
			return Spec{}, fmt.Errorf("未检测到可用的 WSL 发行版，请先安装 WSL 或选择其他 Shell")
		}
		return Spec{}, fmt.Errorf("未检测到 Shell %q，请在设置中重新检测或选择自动", settings.Mode)
	}
	return specFromCandidate(*chosen, settings), nil
}

// LegacySpec preserves the historical cmd.exe / /bin/sh behavior for callers
// that do not have a settings snapshot (mostly tests and old tool wrappers).
func LegacySpec() Spec {
	if runtime.GOOS == "windows" {
		return Spec{
			Mode:        config.TerminalModeCmd,
			DisplayName: "cmd.exe",
			Path:        "cmd.exe",
			Sandbox:     SandboxOK,
		}
	}
	return Spec{
		Mode:        config.TerminalModeSh,
		DisplayName: "sh",
		Path:        "/bin/sh",
		Sandbox:     SandboxOK,
	}
}

// Test runs a fixed command through the selected shell. It uses direct
// execution because this is a shell availability test, not a sandbox test.
func Test(ctx context.Context, settings config.TerminalSettings, host Host, sandboxMode ...string) (ProbeResult, error) {
	spec, err := Resolve(ctx, settings, host, firstString(sandboxMode))
	if err != nil {
		return ProbeResult{}, err
	}
	inv := spec.Invocation(os.TempDir(), "echo agentmate-shell-ok")
	policy := sandbox.Policy{
		Mode:      sandbox.ModeDangerFullAccess,
		Network:   sandbox.NetworkOn,
		Workspace: os.TempDir(),
	}.WithExtraEnv(inv.Env...)
	res, err := sandbox.Run(ctx, sandbox.Request{
		Command:     inv.Command,
		Args:        inv.Args,
		Dir:         inv.Dir,
		Timeout:     10 * time.Second,
		OutputLimit: 32 << 10,
	}, policy)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{
		Mode:        spec.Mode,
		DisplayName: spec.DisplayName,
		Path:        spec.Path,
		Output:      strings.TrimSpace(res.Output),
		ExitCode:    res.ExitCode,
		Sandbox:     spec.Sandbox,
	}, nil
}

// Invocation builds the process command for this shell.
func (s Spec) Invocation(workspace, command string) Invocation {
	dir := workspace
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	switch s.Mode {
	case config.TerminalModeCmd:
		return Invocation{
			Command: s.Path,
			Args:    []string{"/d", "/c", "chcp 65001>nul & " + command},
			Dir:     dir,
		}
	case config.TerminalModePowerShell, config.TerminalModePwsh:
		return Invocation{
			Command: s.Path,
			Args:    powershellArgs(command, s.LoadProfile),
			Env:     append([]string(nil), s.Env...),
			Dir:     dir,
		}
	case config.TerminalModeGitBash:
		return Invocation{
			Command: s.Path,
			Args:    posixArgs(s.Path, command, s.LoadProfile),
			Env:     append([]string(nil), s.Env...),
			Dir:     dir,
		}
	case config.TerminalModeBash, config.TerminalModeZsh, config.TerminalModeSh:
		return Invocation{
			Command: s.Path,
			Args:    posixArgs(s.Path, command, s.LoadProfile),
			Env:     append([]string(nil), s.Env...),
			Dir:     dir,
		}
	case config.TerminalModeWSL:
		shell := strings.TrimSpace(s.WSLShell)
		if shell == "" {
			shell = defaultWSLShell
		}
		args := []string{"-d", s.WSLDistro}
		if strings.TrimSpace(s.WSLUser) != "" {
			args = append(args, "-u", s.WSLUser)
		}
		if strings.TrimSpace(workspace) != "" {
			args = append(args, "--cd", workspace)
		}
		args = append(args, "--exec", shell)
		args = append(args, posixArgs(shell, command, s.LoadProfile)...)
		return Invocation{Command: s.Path, Args: args, Dir: dir}
	case config.TerminalModeCustom:
		return Invocation{
			Command: s.Path,
			Args:    []string{"-c", command},
			Env:     append([]string(nil), s.Env...),
			Dir:     dir,
		}
	default:
		return Invocation{
			Command: s.Path,
			Args:    []string{"-c", command},
			Env:     append([]string(nil), s.Env...),
			Dir:     dir,
		}
	}
}

func powershellArgs(command string, loadProfile bool) []string {
	args := []string{"-NoLogo", "-NonInteractive"}
	if !loadProfile {
		args = append(args, "-NoProfile")
	}
	script := "[Console]::OutputEncoding=[Text.Encoding]::UTF8; $global:LASTEXITCODE=$null; " + command +
		"; if ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }"
	return append(args, "-Command", script)
}

func posixArgs(shellPath, command string, loadProfile bool) []string {
	if loadProfile {
		return []string{"-lc", command}
	}
	switch strings.ToLower(filepath.Base(shellPath)) {
	case "bash", "bash.exe":
		return []string{"--noprofile", "--norc", "-c", command}
	case "zsh", "zsh.exe":
		return []string{"-f", "-c", command}
	default:
		return []string{"-c", command}
	}
}

type candidate struct {
	Detected
	Priority int
	Env      []string
	WSLShell string
}

func firstAuto(candidates []candidate, sandboxMode string) *candidate {
	for i := range candidates {
		if candidates[i].Mode == config.TerminalModeWSL {
			continue
		}
		if candidates[i].Sandbox == SandboxFullAccess && sandboxMode != "" &&
			sandboxMode != "danger-full-access" {
			continue
		}
		if candidates[i].AutoEligible {
			return &candidates[i]
		}
	}
	return nil
}

func detectCandidates(ctx context.Context, host Host, withVersion, includeWSL bool) ([]candidate, []string) {
	var out []candidate
	var errs []string
	if host.GOOS == "windows" {
		out = append(out, windowsCandidates(host)...)
	} else {
		out = append(out, unixCandidates(host)...)
	}
	if includeWSL && host.GOOS == "windows" {
		wslPath := findWSL(host)
		if wslPath != "" {
			distros := wslDistros(ctx, host, wslPath)
			for _, distro := range distros {
				out = append(out, candidate{
					Detected: Detected{
						Mode:         config.TerminalModeWSL,
						DisplayName:  "WSL：" + distro,
						Path:         wslPath,
						Source:       "wsl",
						Sandbox:      SandboxFullAccess,
						WSLDistro:    distro,
						AutoEligible: false,
					},
					Priority: 90,
				})
			}
			if len(distros) == 0 {
				errs = append(errs, "WSL 已安装但未检测到发行版")
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority == out[j].Priority {
			return out[i].DisplayName < out[j].DisplayName
		}
		return out[i].Priority < out[j].Priority
	})
	seen := map[string]bool{}
	filtered := out[:0]
	for _, c := range out {
		key := strings.ToLower(c.Path) + "\x00" + strings.ToLower(c.WSLDistro)
		if seen[key] {
			continue
		}
		seen[key] = true
		filtered = append(filtered, c)
	}
	out = filtered
	if withVersion {
		for i := range out {
			if out[i].Mode != config.TerminalModeWSL {
				out[i].Version = versionFor(ctx, host, out[i])
			}
		}
	}
	return out, errs
}

func windowsCandidates(host Host) []candidate {
	var out []candidate
	systemRoot := host.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	if p := firstExisting(host,
		host.Getenv("ComSpec"),
		filepath.Join(systemRoot, "System32", "cmd.exe"),
	); p != "" {
		out = append(out, candidate{Detected: Detected{
			Mode: config.TerminalModeCmd, DisplayName: "命令提示符（cmd.exe）",
			Path: p, Source: "system", Sandbox: SandboxOK, AutoEligible: true,
		}, Priority: 40})
	} else if p, ok := lookPath(host, "cmd.exe"); ok {
		out = append(out, candidate{Detected: Detected{
			Mode: config.TerminalModeCmd, DisplayName: "命令提示符（cmd.exe）",
			Path: p, Source: "path", Sandbox: SandboxOK, AutoEligible: true,
		}, Priority: 40})
	}

	p := firstExisting(host,
		filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
	)
	if p == "" {
		if found, ok := lookPath(host, "powershell.exe"); ok {
			p = found
		}
	}
	if p != "" {
		out = append(out, candidate{Detected: Detected{
			Mode: config.TerminalModePowerShell, DisplayName: "Windows PowerShell 5.1",
			Path: p, Source: "system", Sandbox: SandboxOK, AutoEligible: true,
		}, Priority: 30})
	}

	if p, source, eligible := findPwsh(host); p != "" {
		sandboxStatus := SandboxOK
		if source == "store-alias" || source == "private-runtime" {
			sandboxStatus = SandboxUnknown
		}
		out = append(out, candidate{Detected: Detected{
			Mode: config.TerminalModePwsh, DisplayName: "PowerShell 7+",
			Path: p, Source: source, Sandbox: sandboxStatus, AutoEligible: eligible,
		}, Priority: 10})
	}

	if root := findGitRoot(host); root != "" {
		bash := firstExisting(host,
			filepath.Join(root, "usr", "bin", "bash.exe"),
			filepath.Join(root, "bin", "bash.exe"),
		)
		if bash != "" {
			out = append(out, candidate{Detected: Detected{
				Mode: config.TerminalModeGitBash, DisplayName: "Git Bash",
				Path: bash, Source: "git", Sandbox: SandboxFullAccess, AutoEligible: true,
			}, Priority: 20, Env: gitBashEnv(host, root)})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

func unixCandidates(host Host) []candidate {
	var out []candidate
	login := strings.TrimSpace(host.Getenv("SHELL"))
	if abs, ok := validAbsolute(host, login); ok {
		mode := shellModeForPath(abs)
		out = append(out, candidate{Detected: Detected{
			Mode: mode, DisplayName: "登录 Shell（" + filepath.Base(abs) + "）",
			Path: abs, Source: "SHELL", Sandbox: SandboxUnknown, AutoEligible: true,
		}, Priority: 1})
	}
	shells := []struct {
		mode, display, name string
		paths               []string
		priority            int
	}{
		{config.TerminalModeZsh, "zsh", "zsh", []string{"/bin/zsh", "/usr/bin/zsh"}, 20},
		{config.TerminalModeBash, "bash", "bash", []string{"/bin/bash", "/usr/bin/bash"}, 30},
		{config.TerminalModeSh, "sh", "sh", []string{"/bin/sh", "/usr/bin/sh"}, 40},
	}
	for _, sh := range shells {
		p := firstExisting(host, sh.paths...)
		if p == "" {
			if found, ok := lookPath(host, sh.name); ok {
				p = found
			}
		}
		if p != "" {
			out = append(out, candidate{Detected: Detected{
				Mode: sh.mode, DisplayName: sh.display, Path: p,
				Source: "standard", Sandbox: SandboxUnknown, AutoEligible: true,
			}, Priority: sh.priority})
		}
	}
	if p, ok := lookPath(host, "pwsh"); ok {
		out = append(out, candidate{Detected: Detected{
			Mode: config.TerminalModePwsh, DisplayName: "PowerShell 7+",
			Path: p, Source: "path", Sandbox: SandboxUnknown, AutoEligible: true,
		}, Priority: 50})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

func shellModeForPath(path string) string {
	switch strings.ToLower(filepath.Base(path)) {
	case "bash", "bash.exe":
		return config.TerminalModeBash
	case "zsh", "zsh.exe":
		return config.TerminalModeZsh
	case "sh", "sh.exe":
		return config.TerminalModeSh
	default:
		return config.TerminalModeCustom
	}
}

func findPwsh(host Host) (string, string, bool) {
	programFiles := host.Getenv("ProgramFiles")
	localAppData := host.Getenv("LocalAppData")
	if p := firstExisting(host,
		filepath.Join(programFiles, "PowerShell", "7", "pwsh.exe"),
	); p != "" {
		return p, "system", true
	}
	if p := firstExisting(host,
		filepath.Join(localAppData, "Microsoft", "WindowsApps", "pwsh.exe"),
	); p != "" {
		// Store aliases are valid executables but often cannot be launched
		// inside the Windows AppContainer. Keep them selectable explicitly,
		// but let auto prefer the system install, Git Bash, or Windows
		// PowerShell.
		return p, "store-alias", false
	}
	if p, ok := lookPath(host, "pwsh.exe"); ok {
		if privateRuntime(p) {
			return p, "private-runtime", false
		}
		return p, "path", true
	}
	return "", "", false
}

func privateRuntime(path string) bool {
	lower := strings.ToLower(filepath.Clean(path))
	return strings.Contains(lower, string(filepath.Separator)+".cache"+string(filepath.Separator)) ||
		strings.Contains(lower, string(filepath.Separator)+"temp"+string(filepath.Separator))
}

func findGitRoot(host Host) string {
	var candidates []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		base := host.Getenv(env)
		if base == "" {
			continue
		}
		if env == "LocalAppData" {
			candidates = append(candidates, filepath.Join(base, "Programs", "Git", "cmd", "git.exe"))
		} else {
			candidates = append(candidates, filepath.Join(base, "Git", "cmd", "git.exe"))
		}
	}
	if p, ok := lookPath(host, "git.exe"); ok {
		candidates = append(candidates, p)
	}
	for _, git := range candidates {
		abs, ok := validAbsolute(host, git)
		if !ok {
			continue
		}
		dir := filepath.Dir(abs)
		if strings.EqualFold(filepath.Base(dir), "cmd") || strings.EqualFold(filepath.Base(dir), "bin") {
			return filepath.Dir(dir)
		}
	}
	return ""
}

func gitBashEnv(host Host, root string) []string {
	path := strings.Join([]string{
		filepath.Join(root, "mingw64", "bin"),
		filepath.Join(root, "usr", "bin"),
		filepath.Join(root, "cmd"),
		host.Getenv("PATH"),
	}, string(os.PathListSeparator))
	return []string{
		"CHERE_INVOKING=1",
		"MSYSTEM=MINGW64",
		"PATH=" + path,
	}
}

func findWSL(host Host) string {
	systemRoot := host.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	if p := firstExisting(host, filepath.Join(systemRoot, "System32", "wsl.exe")); p != "" {
		return p
	}
	if p, ok := lookPath(host, "wsl.exe"); ok {
		return p
	}
	return ""
}

func wslDistros(ctx context.Context, host Host, wslPath string) []string {
	out, code, err := host.Probe(ctx, wslPath, "--list", "--quiet")
	if err != nil || code != 0 {
		return nil
	}
	return parseWSLList(out)
}

func parseWSLList(out string) []string {
	raw := []byte(out)
	if bytes.Contains(raw, []byte{0}) && len(raw)%2 == 0 {
		words := make([]uint16, len(raw)/2)
		for i := range words {
			words[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
		}
		out = string(utf16.Decode(words))
	}
	out = strings.TrimPrefix(out, "\uFEFF")
	out = strings.ReplaceAll(out, "\x00", "")
	var distros []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[strings.ToLower(line)] {
			continue
		}
		seen[strings.ToLower(line)] = true
		distros = append(distros, line)
	}
	return distros
}

func versionFor(ctx context.Context, host Host, c candidate) string {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	switch c.Mode {
	case config.TerminalModeCmd:
		out, code, err := host.Probe(probeCtx, c.Path, "/d", "/c", "ver")
		if err == nil && code == 0 {
			return strings.TrimSpace(out)
		}
	case config.TerminalModePowerShell, config.TerminalModePwsh:
		out, code, err := host.Probe(probeCtx, c.Path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$PSVersionTable.PSVersion.ToString()")
		if err == nil && code == 0 {
			return strings.TrimSpace(out)
		}
	case config.TerminalModeGitBash, config.TerminalModeBash:
		out, code, err := host.Probe(probeCtx, c.Path, "--version")
		if err == nil && code == 0 {
			return firstLine(out)
		}
	case config.TerminalModeZsh:
		out, code, err := host.Probe(probeCtx, c.Path, "--version")
		if err == nil && code == 0 {
			return firstLine(out)
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func firstExisting(host Host, paths ...string) string {
	for _, path := range paths {
		if abs, ok := validAbsolute(host, path); ok {
			return abs
		}
	}
	return ""
}

func validAbsolute(host Host, path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	info, err := host.Stat(abs)
	if err != nil || info.IsDir() {
		return "", false
	}
	return filepath.Clean(abs), true
}

func lookPath(host Host, name string) (string, bool) {
	p, err := host.LookPath(name)
	if err != nil {
		return "", false
	}
	return validAbsolute(host, p)
}

func customSpec(settings config.TerminalSettings) (Spec, error) {
	path := strings.TrimSpace(settings.CustomPath)
	if path == "" {
		return Spec{}, fmt.Errorf("自定义 Shell 路径不能为空")
	}
	if !filepath.IsAbs(path) {
		return Spec{}, fmt.Errorf("自定义 Shell 必须是绝对路径")
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Spec{}, fmt.Errorf("自定义 Shell 不存在或不是文件: %s", path)
	}
	return Spec{
		Mode:        config.TerminalModeCustom,
		DisplayName: filepath.Base(path),
		Path:        filepath.Clean(path),
		Sandbox:     SandboxUnknown,
		LoadProfile: settings.LoadProfile,
	}, nil
}

func specFromCandidate(c candidate, settings config.TerminalSettings) Spec {
	s := Spec{
		Mode:        c.Mode,
		DisplayName: c.DisplayName,
		Path:        c.Path,
		Version:     c.Version,
		Sandbox:     c.Sandbox,
		Env:         append([]string(nil), c.Env...),
		WSLDistro:   c.WSLDistro,
		WSLUser:     settings.WSLUser,
		WSLShell:    settings.WSLShell,
		LoadProfile: settings.LoadProfile,
	}
	if s.Mode == config.TerminalModeWSL || c.Sandbox == SandboxFullAccess {
		s.RequiresFullAccess = true
		s.Sandbox = SandboxFullAccess
	}
	if s.Mode == config.TerminalModeWSL {
		if s.WSLShell == "" {
			s.WSLShell = c.WSLShell
		}
		if s.WSLShell == "" {
			s.WSLShell = defaultWSLShell
		}
	}
	return s
}

func firstString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
