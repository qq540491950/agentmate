//go:build windows

package mcpmgr

import "testing"

// TestStdioProcessAttrsHideConsole 固定 issue #67：桌面应用是没有控制台的 GUI
// 进程，因此在注入时启动的 stdio MCP 子进程不得在应用之上分配可见的控制台窗口。
func TestStdioProcessAttrsHideConsole(t *testing.T) {
	attrs := stdioProcessAttrs()
	if attrs == nil {
		t.Fatal("stdioProcessAttrs() = nil, want SysProcAttr with CREATE_NO_WINDOW")
	}
	if !attrs.HideWindow {
		t.Fatal("HideWindow = false, want true")
	}
	if attrs.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW (%#x) set", attrs.CreationFlags, createNoWindow)
	}
}
