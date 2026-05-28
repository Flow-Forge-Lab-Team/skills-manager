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

// daemonSupported is false on Windows: os.Process.Signal only supports Kill, so
// the signal-based liveness/stop the daemon relies on is unavailable. Windows
// users run `skills-manager watch` in the foreground instead.
func daemonSupported() bool { return false }

func processAlive(pid int) bool { return false }

func stopProcess(pid int) error { return nil }
