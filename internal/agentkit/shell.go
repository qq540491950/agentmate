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
	// maxShellOutput caps how much combined stdout/stderr is handed back to
	// the model. npm/go builds can print megabytes; the tail beyond the cap is
	// discarded but the child keeps draining its pipes.
	maxShellOutput = 64 * 1024
	// defaultShellTimeout bounds a single command when the model does not pass
	// timeoutSeconds. It must stay below the default 600s turn timeout so the
	// model still gets the killed-command result instead of losing the turn.
	defaultShellTimeout = 300 * time.Second
	maxShellTimeout     = 600 * time.Second
)

type shellArgs struct {
	Command        string `json:"command" jsonschema:"shell command line to run with the task workspace as the working directory"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"kill the command after this many seconds (default 300, max 600)"`
	// Escalate is deliberately opt-in per call. The model must first run the
	// command inside the sandbox and only request this after the sandbox
	// denied it; it is never a way to preempt the boundary.
	Escalate bool `json:"escalate,omitempty" jsonschema:"request permission to run THIS invocation outside the sandbox; only set after an in-sandbox command was denied, never preemptively"`
	// Justification is shown verbatim on the confirmation card so the user can
	// decide whether crossing the boundary is necessary.
	Justification string `json:"justification,omitempty" jsonschema:"when escalate is true, explain to the user why this command must leave the sandbox"`
}

type shellResult struct {
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
	TimedOut  bool   `json:"timedOut,omitempty"`
	Shell     string `json:"shell,omitempty"`
}

// ShellTool runs shell commands inside the task workspace through the default
// workspace-write sandbox policy. requireConfirmation routes every call
// through ADK HITL, the same contract as write_workspace_file.
func ShellTool(workspace string, requireConfirmation bool) (tool.Tool, error) {
	return ShellToolWithPolicy(workspace, requireConfirmation, sandbox.DefaultPolicy(workspace))
}

// ShellToolWithPolicy runs every command through the session's OS sandbox.
// The plain ShellTool wrapper keeps the historical default for tests and
// callers that construct a tool without a chat policy.
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

// ShellToolWithPolicyAndMode applies the chat's approval policy. In
// auto-approve mode a safe command runs immediately; a command matching the
// risk detector pauses for HITL before the handler starts. The mode argument
// accepts legacy combined-profile names for old call sites. allowEscalation
// gates the per-call escape hatch; when it is false an escalate=true call is
// rejected instead of being silently downgraded to a sandboxed run.
func ShellToolWithPolicyAndMode(workspace string, requireConfirmation bool, approvalPolicy string, policy sandbox.Policy, allowEscalation bool) (tool.Tool, error) {
	return ShellToolWithSpec(workspace, requireConfirmation, approvalPolicy, policy, allowEscalation, terminal.LegacySpec())
}

// ShellToolWithSpec is the session-aware entry point. The caller resolves the
// user's terminal setting once and passes the resulting spec; the tool itself
// never branches on a platform shell.
func ShellToolWithSpec(workspace string, requireConfirmation bool, approvalPolicy string, policy sandbox.Policy, allowEscalation bool, spec terminal.Spec) (tool.Tool, error) {
	approval := config.NormalizeApprovalPolicy(approvalPolicy)
	execution := shellExecution{
		policy:          policy,
		approvalPolicy:  approval,
		allowEscalation: allowEscalation,
		spec:            spec,
	}
	// Escalation is only meaningful while the command would otherwise run
	// confined. A danger-full-access tool is already direct, and never-policy
	// deliberately has no way to cross the boundary.
	escalationNeedsConfirmation := allowEscalation &&
		policy.Mode != sandbox.ModeDangerFullAccess &&
		approval != config.ApprovalPolicyNever
	guard := NewPreviewGuardWithPolicy(workspace, policy)
	provider := func(args shellArgs) bool {
		// A call the handler will reject must not open a dead-end approval
		// card: let it run and return the error to the model.
		if !shellPreviewAllows(guard, execution, args) {
			return false
		}
		switch approval {
		case config.ApprovalPolicyRequestApproval:
			// Request-approval asks before every executable call, including a
			// command that would otherwise be safe to auto-approve.
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

// shellExecution resolves per-call sandbox overrides without mutating the
// session policy. Only the approved invocation gets a danger policy.
type shellExecution struct {
	policy          sandbox.Policy
	approvalPolicy  string
	allowEscalation bool
	spec            terminal.Spec
}

// shellPreviewAllows is the read-only gate shared by both shell entry points.
// A failure means the handler itself will reject the call, so no confirmation
// prompt should be shown.
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
		// The command is already direct; ignore escalation rather than adding
		// a second, meaningless confirmation.
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

// runShellCommand is the execution core, split from the tool wrapper so tests
// can drive it with a plain context.
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
		// Killing the process makes Wait return an error; the timeout result
		// carries the output produced so far so the model can react.
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

// boundedBuffer keeps the first limit bytes written to it and reports whether
// anything was dropped. Writes past the cap are accepted (discarded) so the
// child never blocks on a full pipe.
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
