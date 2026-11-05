//go:build windows

package agentkit

import "testing"

// TestShellProcessAttrsHideConsole pins issue #54: the desktop app is a GUI
// process without a console, so commands started through run_command must not
// allocate a visible console window over the app.
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