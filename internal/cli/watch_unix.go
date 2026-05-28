//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// processAlive reports whether pid is a live skills-manager watcher. It probes
// liveness (signal 0) and then confirms the process is actually our watcher, so
// a stale pid file whose PID was reused by an unrelated process is not treated
// as the watcher (and is never signalled by --stop).
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return processIsWatcher(pid)
}

// processIsWatcher checks that pid's command line looks like this binary running
// `watch`, so a reused PID for an unrelated process is not mistaken for the
// watcher. Best-effort: if ps is unavailable we fall back to "alive" rather than
// locking the user out of stopping a real watcher.
func processIsWatcher(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return true
	}
	cmd := string(out)
	if !strings.Contains(cmd, "watch") {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return true
	}
	return strings.Contains(cmd, exe) || strings.Contains(cmd, filepath.Base(exe))
}

// stopProcess asks the process to terminate.
func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}
