//go:build windows

package mcpmgr

import "syscall"

// createNoWindow 是 Windows 进程创建标志中的 CREATE_NO_WINDOW
// (0x08000000)。此处直接写出该常量，而不是导入
// golang.org/x/sys/windows，以保持模块图不变。
const createNoWindow = 0x08000000

// stdioProcessAttrs 隐藏 Windows 上 stdio MCP 子进程的控制台窗口。桌面应用使用
// -H windowsgui 构建，自身不拥有控制台，因此未加 CREATE_NO_WINDOW 的 MCP
// 子进程会在注入时分配一个可见的 cmd.exe
// 控制台，并在整个会话期间保持打开（issue #67）。HideWindow
// 还会额外抑制窗口闪烁。
func stdioProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
