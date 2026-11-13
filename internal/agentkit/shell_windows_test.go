//go:build windows

package agentkit

import "testing"

// TestShellProcessAttrsHideConsole 固定 issue #54：桌面应用是一个没有控制台的
// GUI 进程，因此通过 run_command 启动的命令不得在应用之上分配可见的控制台窗口。
func TestShellProcessAttrsHideConsole(t *testing.T) {
	attrs := shellProcessAttrs()
	if attrs == nil {
		t.Fatal("shellProcessAttrs() = nil, want SysProcAttr with CREATE_NO_WINDOW")
	}
	if !attrs.HideWindow {
		t.Fatal("HideWindow = false, want true")
	}
	if attrs.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW (%#x) set", attrs.CreationFlags, createNoWindow)
	}
}
