//go:build windows

package cli

import "syscall"

// detachSysProcAttr starts the daemon child detached and in a new process
// group so it is not tied to the parent console.
func detachSysProcAttr() *syscall.SysProcAttr {
	const (
		detachedProcess       = 0x00000008
		createNewProcessGroup = 0x00000200
	)
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}
