//go:build windows

package agentkit

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// shellProcessAttrs keeps the default process attributes on Windows.
func shellProcessAttrs() *syscall.SysProcAttr {
	return nil
}

// killShellProcess kills the whole tree started by cmd.exe (taskkill /T), so
// a timed-out build does not leave orphaned child processes behind. When
// taskkill cannot help (the process already exited) fall back to Kill and
// swallow "process done".
func killShellProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err == nil {
		return nil
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
