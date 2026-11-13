//go:build !windows

package mcpmgr

import "syscall"

// stdioProcessAttrs 使 stdio MCP
// 子进程保留在默认进程组中：在这些平台上没有需要隐藏的控制台窗口。
func stdioProcessAttrs() *syscall.SysProcAttr {
	return nil
}
