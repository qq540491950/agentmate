// Package sandbox runs local child processes behind an OS-enforced policy.
//
// The package is deliberately independent of agentkit and mcpmgr so both
// shell commands and stdio MCP servers can share one execution contract
// without an import cycle.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mode selects the filesystem boundary for a child process.
type Mode string

const (
	// ModeWorkspaceWrite permits writes only under the workspace and explicit
	// writable roots. It is the default for every local tool.
	ModeWorkspaceWrite Mode = "workspace-write"
	// ModeDangerFullAccess removes the sandbox. It must only be selected by
	// an explicit user decision.
	ModeDangerFullAccess Mode = "danger-full-access"
)

// Network selects whether the child may reach the network.
type Network string

const (
	NetworkOff Network = "off"
	NetworkOn  Network = "on"
)

// Policy is the complete execution policy for one child process.
type Policy struct {
	Mode           Mode
	Network        Network
	Workspace      string
	WritableRoots  []string
	ProtectedPaths []string
	// ExtraEnv is for explicitly configured values (for example a stdio MCP
	// server's own environment). It is appended after the sanitized base
	// environment, so it can intentionally provide a server-specific secret.
	ExtraEnv []string
}

// Request describes one child process.
type Request struct {
	Command     string
	Args        []string
	Dir         string
	Timeout     time.Duration
	OutputLimit int
	// Env is used only by the direct danger-full-access backend. Sandboxed
	// runs build their environment from Policy.
	Env []string
}

// Result is the observable result of one child process.
type Result struct {
	ExitCode  int
	Output    string
	Truncated bool
	TimedOut  bool
	// Denied is reserved for policy denials that happen before a process
	// starts. OS-level denials normally surface as a non-zero exit code.
	Denied       bool
	DeniedCode   string
	DeniedReason string
}

// Status describes the platform backend and its readiness.
type Status struct {
	Available  bool
	Backend    string
	Error      string
	ProfileSID string
}

// ErrUnavailable means the OS backend could not be initialized. Callers must
// fail closed and must not retry through a direct exec path.
var ErrUnavailable = errors.New("沙箱不可用")

// DefaultPolicy returns the safe default for a task workspace.
func DefaultPolicy(workspace string) Policy {
	ws := strings.TrimSpace(workspace)
	ws = canonicalRoot(ws)
	p := Policy{
		Mode:      ModeWorkspaceWrite,
		Network:   NetworkOff,
		Workspace: ws,
	}
	p.ProtectedPaths = defaultProtectedPaths(ws)
	return p
}

// WithProtected adds paths to the protected set.
func (p Policy) WithProtected(paths ...string) Policy {
	p.ProtectedPaths = append(append([]string(nil), p.ProtectedPaths...), paths...)
	return p.normalized()
}

// WithWritableRoots adds paths that may be written by the child.
func (p Policy) WithWritableRoots(paths ...string) Policy {
	p.WritableRoots = append(append([]string(nil), p.WritableRoots...), paths...)
	return p.normalized()
}

func (p Policy) normalized() Policy {
	if p.Mode == "" {
		p.Mode = ModeWorkspaceWrite
	}
	if p.Network == "" {
		p.Network = NetworkOff
	}
	p.Workspace = canonicalRoot(p.Workspace)
	p.WritableRoots = cleanUnique(canonicalRoots(p.WritableRoots))
	p.ProtectedPaths = cleanUnique(canonicalRoots(p.ProtectedPaths))
	return p
}

func (p Policy) validate() error {
	if p.Workspace == "" {
		return fmt.Errorf("沙箱工作区为空")
	}
	if p.Mode != ModeWorkspaceWrite && p.Mode != ModeDangerFullAccess {
		return fmt.Errorf("未知沙箱模式 %q", p.Mode)
	}
	if p.Network != NetworkOff && p.Network != NetworkOn {
		return fmt.Errorf("未知网络策略 %q", p.Network)
	}
	if p.Mode == ModeWorkspaceWrite {
		if st, err := os.Stat(p.Workspace); err != nil {
			return fmt.Errorf("沙箱工作区不可用: %w", err)
		} else if !st.IsDir() {
			return fmt.Errorf("沙箱工作区不是目录: %s", p.Workspace)
		}
	}
	return nil
}

// CheckWrite reports whether path may be written. The returned code is stable
// for tool-result consumers; reason is user-facing Chinese text.
func (p Policy) CheckWrite(path string) (bool, string, string) {
	p = p.normalized()
	if p.Mode == ModeDangerFullAccess {
		return true, "", ""
	}
	full := canonicalTarget(path)
	if full == "" {
		return false, "outside_workspace", "路径不在工作区范围内"
	}
	for _, protected := range p.ProtectedPaths {
		if withinPath(protected, full) {
			return false, "protected_path", fmt.Sprintf("受保护路径不可写：%s", full)
		}
	}
	for _, root := range append([]string{p.Workspace}, p.WritableRoots...) {
		if withinPath(root, full) {
			return true, "", ""
		}
	}
	return false, "outside_workspace", fmt.Sprintf("路径越出工作区范围：%s", full)
}

var protectedWritePattern = regexp.MustCompile(
	`(?i)(>>?|\bcopy\b|\bmove\b|\bdel\b|\berase\b|\brmdir\b|\brm\b|\bmkdir\b|\btouch\b|\bset-content\b|\bout-file\b|\bnew-item\b)[^\r\n]{0,120}(\.git|\.agents|\.codex)`,
)

// CheckCommand is a defense-in-depth preflight for shell commands that
// obviously redirect or copy into a protected path. It is deliberately not a
// security boundary: the OS sandbox and the file-tool policy remain the hard
// controls. A command such as "git status" must keep working even though git
// maintains metadata under .git.
func (p Policy) CheckCommand(command string) (bool, string, string) {
	if p.normalized().Mode == ModeDangerFullAccess {
		return true, "", ""
	}
	if protectedWritePattern.MatchString(command) {
		return false, "protected_path", "命令试图写入受保护路径（.git/.agents/.codex）"
	}
	return true, "", ""
}

// Run executes one child process. A workspace-write policy never silently
// falls back to direct execution when the platform backend is unavailable.
func Run(ctx context.Context, req Request, policy Policy) (Result, error) {
	policy = policy.normalized()
	if err := policy.validate(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.Command) == "" {
		return Result{}, fmt.Errorf("命令不能为空")
	}
	if req.OutputLimit <= 0 {
		req.OutputLimit = 64 << 10
	}
	if req.Timeout <= 0 {
		req.Timeout = 300 * time.Second
	}
	if policy.Mode == ModeDangerFullAccess {
		return runDirect(ctx, req, append(os.Environ(), policy.ExtraEnv...))
	}
	if req.Dir != "" {
		if ok, code, reason := policy.CheckWrite(req.Dir); !ok {
			return Result{
				ExitCode:     -1,
				Denied:       true,
				DeniedCode:   code,
				DeniedReason: reason,
			}, nil
		}
	}
	return runPlatform(ctx, req, policy)
}

// Available reports whether the current platform can enforce workspace-write.
func Available() bool {
	return statusPlatform().Available
}

// BackendStatus exposes backend diagnostics for the settings UI.
func BackendStatus() Status {
	return statusPlatform()
}

// Close releases cached backend resources (profile ACLs and job handles).
func Close() {
	closePlatform()
}

func runDirect(ctx context.Context, req Request, env []string) (Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, req.Command, req.Args...)
	cmd.Dir = req.Dir
	if len(env) > 0 {
		cmd.Env = env
	}
	configureDirectProcess(cmd)
	out := &limitedBuffer{limit: req.OutputLimit}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("启动命令失败: %w", err)
	}
	waitErr := cmd.Wait()
	res := Result{Output: out.String(), Truncated: out.Truncated()}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		res.ExitCode = -1
		return res, nil
	}
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("命令已取消: %w", ctx.Err())
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return Result{}, fmt.Errorf("命令执行失败: %w", waitErr)
	}
	return res, nil
}

func defaultProtectedPaths(workspace string) []string {
	var out []string
	add := func(path string) {
		if strings.TrimSpace(path) != "" {
			out = append(out, path)
		}
	}
	for _, name := range []string{".git", ".agents", ".codex"} {
		add(filepath.Join(workspace, name))
	}
	home, _ := os.UserHomeDir()
	for _, rel := range []string{
		".ssh", ".aws", ".gnupg", ".kube", ".docker", ".config",
	} {
		add(filepath.Join(home, rel))
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		add(filepath.Join(configDir, "agentmate"))
	}
	if cacheDir, err := os.UserCacheDir(); err == nil {
		add(filepath.Join(cacheDir, "agentmate"))
	}
	if runtime.GOOS == "windows" {
		for _, rel := range []string{
			filepath.Join("AppData", "Local", "Google", "Chrome", "User Data"),
			filepath.Join("AppData", "Local", "Microsoft", "Edge", "User Data"),
			filepath.Join("AppData", "Roaming", "Mozilla", "Firefox", "Profiles"),
		} {
			add(filepath.Join(home, rel))
		}
	}
	return cleanUnique(out)
}

func cleanAbs(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func canonicalRoot(path string) string {
	abs := cleanAbs(path)
	if abs == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real)
	}
	return abs
}

func canonicalRoots(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, canonicalRoot(path))
	}
	return out
}

func canonicalTarget(path string) string {
	abs := cleanAbs(path)
	if abs == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real)
	}
	ancestor := abs
	var suffix []string
	for {
		if real, err := filepath.EvalSymlinks(ancestor); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.Clean(real)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = parent
	}
	return abs
}

func cleanUnique(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = cleanAbs(path)
		if path == "" {
			continue
		}
		key := strings.ToLower(path)
		if runtime.GOOS != "windows" {
			key = path
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func withinPath(root, target string) bool {
	root = cleanAbs(root)
	target = cleanAbs(target)
	if root == "" || target == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		target = strings.ToLower(target)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return rel == "."
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// Within reports whether target is root or lives under root. Both sides are
// canonicalized first: Windows 8.3 spellings (RUNNER~1) and symlinked temp
// directories must compare as the same tree, or a workspace inside the app
// directory is misread as outside it and gets protected wholesale.
func Within(root, target string) bool {
	return withinPath(canonicalRoot(root), canonicalRoot(target))
}

// sanitizedEnvironment builds a minimal environment for a sandboxed child.
// It intentionally drops every parent variable not in the runtime allowlist.
func sanitizedEnvironment(home string, extra []string) []string {
	allowed := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "SYSTEMDRIVE": true,
		"WINDIR": true, "COMSPEC": true, "OS": true, "PROCESSOR_ARCHITECTURE": true,
		"NUMBER_OF_PROCESSORS": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true,
		"TERM": true, "TZ": true,
		"GOCACHE": true, "GOMODCACHE": true, "GOPATH": true, "GOROOT": true,
		"GOTOOLCHAIN": true, "GOPROXY": true, "GOSUMDB": true,
		"NODE_PATH": true, "NPM_CONFIG_PREFIX": true,
	}
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !allowed[strings.ToUpper(key)] {
			continue
		}
		values[strings.ToUpper(key)] = value
	}
	if home != "" {
		values["HOME"] = home
		values["USERPROFILE"] = home
		values["APPDATA"] = filepath.Join(home, "AppData", "Roaming")
		values["LOCALAPPDATA"] = filepath.Join(home, "AppData", "Local")
		values["TEMP"] = filepath.Join(home, "Temp")
		values["TMP"] = filepath.Join(home, "Temp")
		values["XDG_CONFIG_HOME"] = filepath.Join(home, ".config")
		values["XDG_CACHE_HOME"] = filepath.Join(home, ".cache")
		values["XDG_DATA_HOME"] = filepath.Join(home, ".local", "share")
	}
	for _, entry := range extra {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		values[strings.ToUpper(key)] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.limit - b.buf.Len()
	if room > 0 {
		if len(p) <= room {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:room])
		}
	}
	if len(p) > room {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	if b.truncated {
		s += "\n...[output truncated]"
	}
	return s
}

func (b *limitedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
