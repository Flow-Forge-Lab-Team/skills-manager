//go:build !windows

package cli

import (
	"os"
	"syscall"
)

// detachSysProcAttr starts the daemon child in its own session so it survives
// the parent shell exiting.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// daemonSupported reports whether background daemon management works on this
// platform. Detachment + signal-based liveness/stop are Unix-only.
func daemonSupported() bool { return true }

// processAlive probes liveness without affecting the process (signal 0).
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// stopProcess asks the process to terminate.
func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}
