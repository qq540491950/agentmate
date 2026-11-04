//go:build !windows

package agentkit

import (
	"errors"
	"os/exec"
	"syscall"
)

// shellProcessAttrs puts the command in its own process group so a timeout
// can kill the whole tree (sh -> build tools), not just the shell.
func shellProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killShellProcess kills the command's process group. A command that already
// exited is not an error: exec treats a non-nil Cancel error as a Wait failure.
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
