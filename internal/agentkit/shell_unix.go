//go:build !windows

package agentkit

import (
	"errors"
	"os/exec"
	"syscall"
)

// shellProcessAttrs 将命令放入其自己的进程组，以便超时可以终止整个进程树（sh ->
// build tools），而不仅是 shell。
func shellProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killShellProcess 终止命令的进程组。已经退出的命令不算错误：exec 将非 nil 的
// Cancel 错误视为 Wait 失败。
func killShellProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}
