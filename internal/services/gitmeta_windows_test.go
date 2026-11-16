//go:build windows

package services

import "testing"

func TestGitProcessAttrsHideWindow(t *testing.T) {
	attrs := gitProcessAttrs()
	if attrs == nil || !attrs.HideWindow {
		t.Fatalf("attrs = %+v, want HideWindow", attrs)
	}
	if attrs.CreationFlags&0x08000000 == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW", attrs.CreationFlags)
	}
}
