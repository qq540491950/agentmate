package agentkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"agentmate/internal/sandbox"
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
}

type shellResult struct {
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
	TimedOut  bool   `json:"timedOut,omitempty"`
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
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("任务工作区为空，无法初始化终端工具")
	}
	return functiontool.New(functiontool.Config{
		Name: "run_command",
		Description: "Run a shell command with the user's task workspace as the working directory and return the combined stdout/stderr plus exit code. " +
			"Use it for git, builds, tests and package installs. Commands are non-interactive; output is capped at 64KB and the process is killed after timeoutSeconds " +
			"(default 300, max 600). A non-zero exitCode is a normal result, not a tool failure.",
		RequireConfirmation: requireConfirmation,
	}, func(ctx agent.Context, args shellArgs) (shellResult, error) {
		timeout := defaultShellTimeout
		if args.TimeoutSeconds > 0 {
			timeout = time.Duration(args.TimeoutSeconds) * time.Second
			if timeout > maxShellTimeout {
				timeout = maxShellTimeout
			}
		}
		return runShellCommandWithPolicy(ctx, workspace, args.Command, timeout, maxShellOutput, policy)
	})
}

func runShellCommandWithPolicy(ctx context.Context, workspace, command string, timeout time.Duration, limit int, policy sandbox.Policy) (shellResult, error) {
	if ok, code, reason := policy.CheckCommand(command); !ok {
		return shellResult{}, fmt.Errorf("%s: %s", code, reason)
	}
	shell, args, env := shellInvocation(command)
	res, err := sandbox.Run(ctx, sandbox.Request{
		Command:     shell,
		Args:        args,
		Dir:         workspace,
		Timeout:     timeout,
		OutputLimit: limit,
		Env:         env,
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

	shell, args, env := shellInvocation(command)
	cmd := exec.CommandContext(runCtx, shell, args...)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), env...)
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

	result := shellResult{Output: out.String(), Truncated: out.Truncated()}
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

// shellInvocation picks the platform shell. Windows switches the console to
// UTF-8 so Chinese output from git/go/npm is not mangled in the model context.
func shellInvocation(command string) (name string, args []string, env []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/d", "/c", "chcp 65001>nul & " + command}, nil
	}
	return "/bin/sh", []string{"-c", command}, nil
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
