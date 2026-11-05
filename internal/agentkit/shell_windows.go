//go:build windows

package agentkit

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW (0x08000000) from the Windows process
// creation flags. It is spelled out here instead of importing
// golang.org/x/sys/windows so the module graph stays unchanged.
const createNoWindow = 0x08000000

// shellProcessAttrs hides the console window of shell children on Windows.
// The desktop app is built with -H windowsgui and owns no console, so without
// CREATE_NO_WINDOW every cmd.exe child (git pull, go build, dir…) allocates a
// fresh visible console that covers the app window (issue #54). HideWindow
// additionally suppresses the window flash of children that still create one.
func shellProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
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
	kill := exec.Command("taskkill", "/T", "/F", "/PID", pid)
	if attrs := shellProcessAttrs(); attrs != nil {
		kill.SysProcAttr = attrs
	}
	if err := kill.Run(); err == nil {
		return nil
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
