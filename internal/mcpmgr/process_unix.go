//go:build !windows

package mcpmgr

import "syscall"

// stdioProcessAttrs leaves stdio MCP children in the default process group:
// there is no console window to hide on these platforms.
func stdioProcessAttrs() *syscall.SysProcAttr {
	return nil
}