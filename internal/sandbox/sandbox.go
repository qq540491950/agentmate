// Package sandbox 在操作系统强制执行的策略下运行本地子进程。
//
// 该包刻意独立于 agentkit 和 mcpmgr，以便 shell 命令和 stdio MCP
// 服务器能够共享同一执行契约，而不会产生导入循环。
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

// Mode 选择子进程的文件系统边界。
type Mode string

const (
	// ModeReadOnly 允许读取工作区，但拒绝所有写入。对于子进程，它由操作系统后端强制执行；对于进
	// 程内文件工具，它由 Policy.CheckWrite 强制执行。
	ModeReadOnly Mode = "read-only"
	// ModeWorkspaceWrite 仅允许在工作区和显式可写根目录下写入。它是每个本地工具的默认值。
	ModeWorkspaceWrite Mode = "workspace-write"
	// ModeDangerFullAccess 会移除沙箱。它必须仅由用户明确决定来选用。
	ModeDangerFullAccess Mode = "danger-full-access"
)

// Network 选择子进程是否可以访问网络。
type Network string

const (
	NetworkOff Network = "off"
	NetworkOn  Network = "on"
)

// Policy 是单个子进程的完整执行策略。
type Policy struct {
	Mode           Mode
	Network        Network
	Workspace      string
	WritableRoots  []string
	ProtectedPaths []string
	// ProxyURL 仅作为 HTTP_PROXY/HTTPS_PROXY
	// 注入沙箱子进程环境。直接（danger-full-access）执行会忽略它，并继续继承调用方
	// 的环境。
	ProxyURL string
	// ExtraEnv 用于显式配置的值（例如 stdio MCP
	// 服务器自身的环境）。它会被追加到经过清理的基础环境之后，因此可以有意提供服务
	// 器专属的密钥。
	ExtraEnv []string
}

// Request 描述一个子进程。
type Request struct {
	Command     string
	Args        []string
	Dir         string
	Timeout     time.Duration
	OutputLimit int
	// Env 仅由直接的 danger-full-access 后端使用。沙箱运行会根据 Policy
	// 构建其环境。
	Env []string
}

// Result 是一个子进程的可观测结果。
type Result struct {
	ExitCode  int
	Output    string
	Truncated bool
	TimedOut  bool
	// Denied 保留给在进程启动前发生的策略拒绝。操作系统级的拒绝通常表现为非零退出码。
	Denied       bool
	DeniedCode   string
	DeniedReason string
}

// Status 描述平台后端及其就绪状态。
type Status struct {
	Available  bool
	Backend    string
	Error      string
	ProfileSID string
}

// ErrUnavailable 表示操作系统后端无法初始化。调用方必须失败关闭，且不得通过直接
// exec 路径重试。
var ErrUnavailable = errors.New("沙箱不可用")

// DefaultPolicy 返回任务工作区的安全默认值。
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

// WithProtected 将路径添加到受保护集合。
func (p Policy) WithProtected(paths ...string) Policy {
	p.ProtectedPaths = append(append([]string(nil), p.ProtectedPaths...), paths...)
	return p.normalized()
}

// WithWritableRoots 添加可以由子进程写入的路径。
func (p Policy) WithWritableRoots(paths ...string) Policy {
	p.WritableRoots = append(append([]string(nil), p.WritableRoots...), paths...)
	return p.normalized()
}

// WithProxy 设置暴露给沙箱内子进程的代理。空 URL
// 会将代理变量排除在子进程环境之外。
func (p Policy) WithProxy(proxyURL string) Policy {
	p.ProxyURL = strings.TrimSpace(proxyURL)
	return p
}

// WithExtraEnv 追加显式配置的子进程环境条目。shell 适配器使用它而不是
// Request.Env，以便相同的值同时应用于沙箱后端和 danger-full-access 后端。
func (p Policy) WithExtraEnv(env ...string) Policy {
	p.ExtraEnv = append(append([]string(nil), p.ExtraEnv...), env...)
	return p
}

func (p Policy) normalized() Policy {
	if p.Mode == "" {
		p.Mode = ModeWorkspaceWrite
	}
	if p.Network == "" {
		p.Network = NetworkOff
	}
	p.ProxyURL = strings.TrimSpace(p.ProxyURL)
	p.Workspace = canonicalRoot(p.Workspace)
	p.WritableRoots = cleanUnique(canonicalRoots(p.WritableRoots))
	p.ProtectedPaths = cleanUnique(canonicalRoots(p.ProtectedPaths))
	return p
}

func (p Policy) validate() error {
	if p.Workspace == "" {
		return fmt.Errorf("沙箱工作区为空")
	}
	if p.Mode != ModeReadOnly && p.Mode != ModeWorkspaceWrite && p.Mode != ModeDangerFullAccess {
		return fmt.Errorf("未知沙箱模式 %q", p.Mode)
	}
	if p.Network != NetworkOff && p.Network != NetworkOn {
		return fmt.Errorf("未知网络策略 %q", p.Network)
	}
	if p.Mode == ModeReadOnly || p.Mode == ModeWorkspaceWrite {
		if st, err := os.Stat(p.Workspace); err != nil {
			return fmt.Errorf("沙箱工作区不可用: %w", err)
		} else if !st.IsDir() {
			return fmt.Errorf("沙箱工作区不是目录: %s", p.Workspace)
		}
	}
	return nil
}

// CheckWrite 报告 path 是否可以被写入。返回的 code
// 对工具结果消费方是稳定的；reason 是面向用户的中文文本。
func (p Policy) CheckWrite(path string) (bool, string, string) {
	p = p.normalized()
	if p.Mode == ModeDangerFullAccess {
		return true, "", ""
	}
	if p.Mode == ModeReadOnly {
		return false, "read_only", "只读沙箱不允许写入"
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

// CheckCommand 是针对明显会重定向或复制到受保护路径的 shell
// 命令的纵深防御预检。它有意不是安全边界：操作系统沙箱和文件工具策略仍是硬性控
// 制。诸如 "git status" 的命令必须继续正常工作，即使 git 在 .git 下维护元数据。
func (p Policy) CheckCommand(command string) (bool, string, string) {
	if p.normalized().Mode == ModeDangerFullAccess {
		return true, "", ""
	}
	if protectedWritePattern.MatchString(command) {
		return false, "protected_path", "命令试图写入受保护路径（.git/.agents/.codex）"
	}
	return true, "", ""
}

// Run 执行一个子进程。当平台后端不可用时，只读和 workspace-write
// 策略绝不静默回退到直接执行。
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
		var ok bool
		var code, reason string
		if policy.Mode == ModeReadOnly {
			ok = Within(policy.Workspace, req.Dir)
			if !ok {
				ok, code, reason = false, "outside_workspace", fmt.Sprintf("命令工作目录越出工作区范围：%s", req.Dir)
			}
		} else {
			ok, code, reason = policy.CheckWrite(req.Dir)
		}
		if !ok {
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

// Available 报告当前平台是否可以强制执行受限模式。
func Available() bool {
	return statusPlatform().Available
}

// BackendStatus 为设置 UI 暴露后端诊断信息。
func BackendStatus() Status {
	return statusPlatform()
}

// Close 释放缓存的后端资源（profile ACL 和作业句柄）。
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

// Within 报告 target 是 root 本身，还是位于 root
// 之下。两侧都会先被规范化：Windows 8.3
// 写法（RUNNER~1）和符号链接的临时目录必须按同一棵树进行比较，否则应用目录内的
// 工作区会被误读为在其外部，并被整体保护。
func Within(root, target string) bool {
	return withinPath(canonicalRoot(root), canonicalRoot(target))
}

// sanitizedEnvironment 为沙箱中的子进程构建最小环境。
// 它会刻意丢弃每一个不在运行时允许列表中的父进程变量。
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

// sandboxEnvironment 返回仅适用于受限子进程的环境变量值。直接后端绝不调用此辅助函数，因此已配置的
// 代理无法在 danger-full-access 模式下静默替换父进程环境。
func (p Policy) sandboxEnvironment() []string {
	env := proxyEnvironment(p.ProxyURL)
	return append(env, p.ExtraEnv...)
}

// proxyEnvironment 将应用内单一的代理设置映射为 HTTP
// 客户端通常使用的变量。环回地址保持直连，与全局传输针对本地模型和 MCP
// 端点的无代理豁免一致。
func proxyEnvironment(proxyURL string) []string {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil
	}
	return []string{
		"HTTP_PROXY=" + proxyURL,
		"HTTPS_PROXY=" + proxyURL,
		"NO_PROXY=localhost,127.0.0.1,::1",
	}
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
