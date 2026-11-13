package agentkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"agentmate/internal/config"
	"agentmate/internal/sandbox"
	"agentmate/internal/terminal"
)

const (
	// maxShellOutput 限制交还给模型的合并后的 stdout/stderr 输出量。npm/go
	// 构建可能打印数兆字节；超出上限的尾部会被丢弃，但子进程仍会继续排空其管道。
	maxShellOutput = 64 * 1024
	// defaultShellTimeout 在模型未传入 timeoutSeconds
	// 时为单条命令设上限。它必须低于默认的 600s
	// 回合超时，以便模型仍能获得命令被终止的结果，而不会丢失该回合。
	defaultShellTimeout = 300 * time.Second
	maxShellTimeout     = 600 * time.Second
)

type shellArgs struct {
	Command        string `json:"command" jsonschema:"shell command line to run with the task workspace as the working directory"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"kill the command after this many seconds (default 300, max 600)"`
	// Escalate 特意设计为每次调用都需要显式启用。模型必须先在沙箱内运行该命令，并且仅在沙箱
	// 拒绝后才请求使用此选项；它绝不是抢先越过边界的方式。
	Escalate bool `json:"escalate,omitempty" jsonschema:"request permission to run THIS invocation outside the sandbox; only set after an in-sandbox command was denied, never preemptively"`
	// Justification 会原样显示在确认卡片上，以便用户判断是否有必要越过边界。
	Justification string `json:"justification,omitempty" jsonschema:"when escalate is true, explain to the user why this command must leave the sandbox"`
}

type shellResult struct {
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
	TimedOut  bool   `json:"timedOut,omitempty"`
	Shell     string `json:"shell,omitempty"`
}

// ShellTool 通过默认的 workspace-write 沙箱策略在任务工作区内运行 shell
// 命令。requireConfirmation 会通过 ADK HITL 路由每次调用，其约定与
// write_workspace_file 相同。
func ShellTool(workspace string, requireConfirmation bool) (tool.Tool, error) {
	return ShellToolWithPolicy(workspace, requireConfirmation, sandbox.DefaultPolicy(workspace))
}

// ShellToolWithPolicy 通过会话的 OS 沙箱运行每条命令。普通的 ShellTool
// 包装器为测试以及在没有聊天策略的情况下构造工具的调用方保留历史默认值。
func ShellToolWithPolicy(workspace string, requireConfirmation bool, policy sandbox.Policy) (tool.Tool, error) {
	execution := shellExecution{
		policy:         policy,
		approvalPolicy: config.ApprovalPolicyRequestApproval,
		spec:           terminal.LegacySpec(),
	}
	guard := NewPreviewGuardWithPolicy(workspace, policy)
	provider := func(args shellArgs) bool {
		if !shellPreviewAllows(guard, execution, args) {
			return false
		}
		return requireConfirmation
	}
	return newShellTool(workspace, requireConfirmation, provider, execution)
}

// ShellToolWithPolicyAndMode 应用聊天的审批策略。在自动批准模式下，安全命令会立即运行；命中风险检测器的命
// 令会在处理器启动前暂停以等待 HITL。mode
// 参数接受旧调用点使用的遗留组合配置名称。allowEscalation
// 控制每次调用的逃生通道；当它为 false 时，escalate=true
// 的调用会被拒绝，而不会被静默降级为沙箱内运行。
func ShellToolWithPolicyAndMode(workspace string, requireConfirmation bool, approvalPolicy string, policy sandbox.Policy, allowEscalation bool) (tool.Tool, error) {
	return ShellToolWithSpec(workspace, requireConfirmation, approvalPolicy, policy, allowEscalation, terminal.LegacySpec())
}

// ShellToolWithSpec 是会话感知的入口点。调用方会一次性解析用户的终端设置并传入得到的
// spec；工具本身绝不按平台 shell 进行分支。
func ShellToolWithSpec(workspace string, requireConfirmation bool, approvalPolicy string, policy sandbox.Policy, allowEscalation bool, spec terminal.Spec) (tool.Tool, error) {
	approval := config.NormalizeApprovalPolicy(approvalPolicy)
	execution := shellExecution{
		policy:          policy,
		approvalPolicy:  approval,
		allowEscalation: allowEscalation,
		spec:            spec,
	}
	// 只有当命令原本会在受限环境中运行时，提权才有意义。danger-full-access 工具
	// 本来就是直连，而 never 策略故意不提供跨越边界的方式。
	escalationNeedsConfirmation := allowEscalation &&
		policy.Mode != sandbox.ModeDangerFullAccess &&
		approval != config.ApprovalPolicyNever
	guard := NewPreviewGuardWithPolicy(workspace, policy)
	provider := func(args shellArgs) bool {
		// handler 会拒绝的调用不得打开死胡同式的审批卡片：应让它运行并把错误返回给模型。
		if !shellPreviewAllows(guard, execution, args) {
			return false
		}
		switch approval {
		case config.ApprovalPolicyRequestApproval:
			// Request-approval
			// 会在每次可执行调用前请求确认，包括原本可以安全自动批准的命令。
			return true
		case config.ApprovalPolicyNever:
			return false
		}
		if requireConfirmation {
			return true
		}
		if args.Escalate && escalationNeedsConfirmation {
			return true
		}
		risky, _ := sandbox.AssessCommand(args.Command)
		return risky
	}
	return newShellTool(workspace, requireConfirmation, provider, execution)
}

// shellExecution 会解析每次调用的沙箱覆盖设置，而不修改会话策略。仅获批准的调用会获得 danger
// 策略。
type shellExecution struct {
	policy          sandbox.Policy
	approvalPolicy  string
	allowEscalation bool
	spec            terminal.Spec
}

// shellPreviewAllows 是两个 shell 入口点共享的只读门禁。失败意味着 handler
// 本身会拒绝该调用，因此不应显示确认提示。
func shellPreviewAllows(guard PreviewGuard, execution shellExecution, args shellArgs) bool {
	if strings.TrimSpace(args.Command) == "" {
		return false
	}
	if ok, _, _ := guard.CheckCommand(args.Command); !ok {
		return false
	}
	if _, err := execution.policyFor(args); err != nil {
		return false
	}
	return true
}

func (e shellExecution) policyFor(args shellArgs) (sandbox.Policy, error) {
	if e.spec.RequiresFullAccess && e.policy.Mode != sandbox.ModeDangerFullAccess {
		return sandbox.Policy{}, fmt.Errorf("Shell %s 运行在 Windows 沙箱之外，请将会话权限切换为完全访问后再执行", e.spec.DisplayName)
	}
	if !args.Escalate {
		return e.policy, nil
	}
	if e.policy.Mode == sandbox.ModeDangerFullAccess {
		// 命令已是直接执行；应忽略升级，而不是再添加一次无意义的确认。
		return e.policy, nil
	}
	if !e.allowEscalation {
		return sandbox.Policy{}, fmt.Errorf("当前沙箱配置不允许脱离沙箱执行")
	}
	if e.approvalPolicy == config.ApprovalPolicyNever {
		return sandbox.Policy{}, fmt.Errorf("审批策略为 never，禁止脱离沙箱执行；请先切换到请求批准或帮我批准")
	}
	if strings.TrimSpace(args.Justification) == "" {
		return sandbox.Policy{}, fmt.Errorf("脱离沙箱执行必须提供 justification，向用户说明为什么必须越界")
	}
	escalated := e.policy
	escalated.Mode = sandbox.ModeDangerFullAccess
	escalated.Network = sandbox.NetworkOn
	return escalated, nil
}

func (e shellExecution) run(ctx context.Context, workspace string, args shellArgs, timeout time.Duration, limit int) (shellResult, error) {
	policy, err := e.policyFor(args)
	if err != nil {
		return shellResult{}, err
	}
	return runShellCommandWithPolicySpec(ctx, workspace, args.Command, timeout, limit, policy, e.spec)
}

func newShellTool(workspace string, requireConfirmation bool, provider any, execution shellExecution) (tool.Tool, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("任务工作区为空，无法初始化终端工具")
	}
	return functiontool.New(functiontool.Config{
		Name: "run_command",
		Description: "Run a shell command with the user's task workspace as the working directory and return the combined stdout/stderr plus exit code. " +
			"Current terminal shell: " + shellDescription(execution.spec) + ". " +
			"Use it for git, builds, tests and package installs. Commands are non-interactive; output is capped at 64KB and the process is killed after timeoutSeconds " +
			"(default 300, max 600). A non-zero exitCode is a normal result, not a tool failure. " +
			"Escalation is a last resort: first try the command inside the sandbox. Only if the sandbox denied it, retry once with escalate=true and a justification " +
			"explaining why the command must run outside the sandbox. Never set escalate preemptively.",
		RequireConfirmation:         requireConfirmation,
		RequireConfirmationProvider: provider,
	}, func(ctx agent.Context, args shellArgs) (shellResult, error) {
		timeout := defaultShellTimeout
		if args.TimeoutSeconds > 0 {
			timeout = time.Duration(args.TimeoutSeconds) * time.Second
			if timeout > maxShellTimeout {
				timeout = maxShellTimeout
			}
		}
		return execution.run(ctx, workspace, args, timeout, maxShellOutput)
	})
}

func runShellCommandWithPolicy(ctx context.Context, workspace, command string, timeout time.Duration, limit int, policy sandbox.Policy) (shellResult, error) {
	return runShellCommandWithPolicySpec(ctx, workspace, command, timeout, limit, policy, terminal.LegacySpec())
}

func runShellCommandWithPolicySpec(ctx context.Context, workspace, command string, timeout time.Duration, limit int, policy sandbox.Policy, spec terminal.Spec) (shellResult, error) {
	if ok, code, reason := policy.CheckCommand(command); !ok {
		return shellResult{}, fmt.Errorf("%s: %s", code, reason)
	}
	inv := spec.Invocation(workspace, command)
	policy = policy.WithExtraEnv(inv.Env...)
	res, err := sandbox.Run(ctx, sandbox.Request{
		Command:     inv.Command,
		Args:        inv.Args,
		Dir:         inv.Dir,
		Timeout:     timeout,
		OutputLimit: limit,
	}, policy)
	if err != nil {
		return shellResult{}, err
	}
	if res.Denied {
		return shellResult{}, fmt.Errorf("%s: %s", res.DeniedCode, res.DeniedReason)
	}
	return shellResult{
		ExitCode:  res.ExitCode,
		Output:    res.Output,
		Truncated: res.Truncated,
		TimedOut:  res.TimedOut,
		Shell:     spec.DisplayName,
	}, nil
}

// runShellCommand 是执行核心，已从工具包装器中拆出，以便测试能用普通 context
// 驱动它。
func runShellCommand(ctx context.Context, workspace, command string, timeout time.Duration, limit int) (shellResult, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return shellResult{}, fmt.Errorf("命令不能为空")
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return shellResult{}, fmt.Errorf("任务工作区不可用: %w", err)
	}
	if !info.IsDir() {
		return shellResult{}, fmt.Errorf("任务工作区不是目录: %s", workspace)
	}
	if timeout <= 0 {
		timeout = defaultShellTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	inv := terminal.LegacySpec().Invocation(workspace, command)
	cmd := exec.CommandContext(runCtx, inv.Command, inv.Args...)
	cmd.Dir = inv.Dir
	cmd.Env = append(os.Environ(), inv.Env...)
	if attrs := shellProcessAttrs(); attrs != nil {
		cmd.SysProcAttr = attrs
	}
	cmd.Cancel = func() error { return killShellProcess(cmd) }
	cmd.WaitDelay = 5 * time.Second

	out := &boundedBuffer{limit: limit}
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		return shellResult{}, fmt.Errorf("启动命令失败: %w", err)
	}
	waitErr := cmd.Wait()

	result := shellResult{Output: out.String(), Truncated: out.Truncated(), Shell: terminal.LegacySpec().DisplayName}
	if runCtx.Err() == context.DeadlineExceeded {
		// 终止进程会使 Wait
		// 返回错误；超时结果会携带截至目前产生的输出，以便模型作出反应。
		result.TimedOut = true
		result.ExitCode = -1
		return result, nil
	}
	if ctx.Err() != nil {
		return shellResult{}, fmt.Errorf("命令已取消: %w", ctx.Err())
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return shellResult{}, fmt.Errorf("命令执行失败: %w", waitErr)
	}
	return result, nil
}

func shellDescription(spec terminal.Spec) string {
	name := spec.DisplayName
	if strings.TrimSpace(name) == "" {
		name = spec.Mode
	}
	switch spec.Mode {
	case config.TerminalModePwsh:
		return name + " (PowerShell 7+ syntax; && is supported)"
	case config.TerminalModePowerShell:
		return name + " (Windows PowerShell 5.1 syntax; use ; instead of &&)"
	case config.TerminalModeGitBash, config.TerminalModeBash, config.TerminalModeZsh, config.TerminalModeSh:
		return name + " (POSIX shell syntax)"
	case config.TerminalModeWSL:
		return name + " (Linux/WSL syntax; paths use the distro filesystem)"
	case config.TerminalModeCmd:
		return name + " (cmd.exe syntax; use %VAR%)"
	default:
		return name
	}
}

// boundedBuffer 保留写入其中的前 limit
// 个字节，并报告是否有任何内容被丢弃。超过上限的写入会被接受（丢弃），以便子进
// 程绝不会因管道已满而阻塞。
type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
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

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	if b.truncated {
		s += "\n...[output truncated]"
	}
	return s
}

func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
