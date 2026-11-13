//go:build windows

package agentkit

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow 是 Windows 进程创建标志中的
// CREATE_NO_WINDOW（0x08000000）。这里直接写出该常量，而不是导入
// golang.org/x/sys/windows，从而使模块图保持不变。
const createNoWindow = 0x08000000

// shellProcessAttrs 在 Windows 上隐藏 shell 子进程的控制台窗口。桌面应用使用 -H
// windowsgui 构建，没有控制台，因此如果没有 CREATE_NO_WINDOW，每个 cmd.exe
// 子进程（git pull、go
// build、dir…）都会分配一个新的可见控制台并遮挡应用窗口（issue
// #54）。HideWindow 还会抑制仍会创建控制台窗口的子进程所产生的窗口闪烁。
func shellProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

// killShellProcess 会终止由 cmd.exe 启动的整个进程树（taskkill
// /T），因此超时的构建不会留下孤立的子进程。当 taskkill
// 无法处理（进程已退出）时，回退到 Kill 并忽略 "process done"。
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
