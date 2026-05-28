//go:build !windows

package cli

import "syscall"

// detachSysProcAttr starts the daemon child in its own session so it survives
// the parent shell exiting.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
