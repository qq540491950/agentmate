//go:build !windows

package services

import "syscall"

func gitProcessAttrs() *syscall.SysProcAttr {
	return nil
}
