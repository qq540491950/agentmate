//go:build windows

package mcpmgr

import "syscall"

// createNoWindow is CREATE_NO_WINDOW (0x08000000) from the Windows process
// creation flags. Spelled out here instead of importing
// golang.org/x/sys/windows so the module graph stays unchanged.
const createNoWindow = 0x08000000

// stdioProcessAttrs hides the console window of stdio MCP children on
// Windows. The desktop app is built with -H windowsgui and owns no console,
// so an MCP child without CREATE_NO_WINDOW allocates a visible cmd.exe
// console at injection time and keeps it open for the whole session
// (issue #67). HideWindow additionally suppresses the window flash.
func stdioProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}