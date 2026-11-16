//go:build windows

package services

import "syscall"

// gitProcessAttrs 隐藏 Git 子进程的控制台窗口，避免 Windows GUI 构建闪出黑框。
func gitProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
