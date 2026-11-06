//go:build windows

package mcpmgr

import "testing"

// TestStdioProcessAttrsHideConsole pins issue #67: the desktop app is a GUI
// process without a console, so stdio MCP children started at injection time
// must not allocate a visible console window over the app.
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