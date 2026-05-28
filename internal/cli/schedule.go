package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// ScheduleProvider identifies the scheduling mechanism.
type ScheduleProvider string

const (
	ProviderLocal ScheduleProvider = "local"
)

// ScheduleConfig holds the resolved configuration for a scheduled check.
type ScheduleConfig struct {
	Provider   ScheduleProvider
	Interval   string // "daily" for v0.2
	BinaryPath string // absolute path to the skills-manager binary
	LogDir     string // ~/.skills-manager/logs
}

// detectOS returns the runtime OS ("darwin", "linux", "windows").
func detectOS() string {
	return runtime.GOOS
}

// resolveBinaryPath attempts to find the current executable path.
// Falls back to "skills-manager" in PATH or a plain name.
func resolveBinaryPath() (string, error) {
	if p, err := os.Executable(); err == nil && p != "" {
		return p, nil
	}
	if p, err := exec.LookPath("skills-manager"); err == nil {
		return p, nil
	}
	return "skills-manager", nil
}

// ensureLogsDir creates the logs directory under the manager home with tight permissions.
func ensureLogsDir(home string) error {
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("create logs directory: %w", err)
	}
	return nil
}

// defaultScheduleConfig returns a baseline config for the current platform.
func defaultScheduleConfig() (ScheduleConfig, error) {
	home, err := managerHome()
	if err != nil {
		return ScheduleConfig{}, err
	}

	bin, _ := resolveBinaryPath()

	if err := ensureLogsDir(home); err != nil {
		return ScheduleConfig{}, err
	}

	return ScheduleConfig{
		Provider:   ProviderLocal,
		Interval:   "daily",
		BinaryPath: bin,
		LogDir:     filepath.Join(home, "logs"),
	}, nil
}

// --- macOS launchd backend (primary for v0.2 on darwin) ---

const launchdLabel = "com.skills-manager.daily-check"

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

func generateLaunchdPlist(cfg ScheduleConfig, includeSummarize bool) string {
	logOut := filepath.Join(cfg.LogDir, "check.stdout.log")
	logErr := filepath.Join(cfg.LogDir, "check.stderr.log")
	args := scheduledProgramArgs(cfg, includeSummarize)
	var argXML strings.Builder
	for _, a := range args {
		fmt.Fprintf(&argXML, "\t\t<string>%s</string>\n", xmlEscape(a))
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Hour</key><integer>9</integer>
		<key>Minute</key><integer>0</integer>
	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, launchdLabel, argXML.String(), logOut, logErr)
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func installLaunchd(cfg ScheduleConfig, includeSummarize bool) error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return fmt.Errorf("resolve plist path: %w", err)
	}

	// Logs directory is ensured by the caller (wizard) via defaultScheduleConfig / ensureLogsDir(home)

	content := generateLaunchdPlist(cfg, includeSummarize)

	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}

	// Write with standard plist permissions
	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write launchd plist: %w", err)
	}

	// Load (and enable at boot)
	cmd := exec.Command("launchctl", "load", "-w", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load failed: %w\n%s", err, string(output))
	}

	return nil
}

func uninstallLaunchd() error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}

	// Unload (ignore error if not loaded)
	_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()

	// Remove the file
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}

	return nil
}

func isLaunchdInstalled() (bool, error) {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(plistPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// formatScheduledCheckStatus combines poll tracking with OS scheduler state.
func formatScheduledCheckStatus(db *state.DB, home string) string {
	pollPart := checkScheduledState(db)
	osPart := osSchedulerStatusLine(home)
	if osPart == "" {
		return pollPart
	}
	if strings.HasPrefix(pollPart, "not configured") || strings.HasPrefix(pollPart, "unknown") {
		return osPart + "; " + pollPart
	}
	return pollPart + "; " + osPart
}

func osSchedulerStatusLine(home string) string {
	st, found, err := loadScheduleState(home)
	if err != nil {
		return ""
	}
	if found && st.Backend == BackendManual {
		return "manual (no OS job)"
	}
	switch detectOS() {
	case "darwin":
		installed, err := isLaunchdInstalled()
		if err == nil && installed {
			line := "launchd daily 09:00"
			staleSt := st
			if !found {
				staleSt = ScheduleState{Backend: BackendLaunchd, Interval: "daily"}
			}
			if warn := scheduleStaleWarning(staleSt, filepath.Join(home, "logs", "check.stdout.log")); warn != "" {
				line += "; " + warn
			}
			return line
		}
	case "linux":
		if found && st.Backend == BackendCron {
			line := "cron daily 09:00"
			if warn := scheduleStaleWarning(st, filepath.Join(home, "logs", "check.log")); warn != "" {
				line += "; " + warn
			}
			return line
		}
	}
	if found && st.Backend != "" && st.Backend != BackendManual {
		return string(st.Backend) + " configured"
	}
	return ""
}

func scheduleStaleWarning(st ScheduleState, logPath string) string {
	if !foundSchedule(st) || st.Interval != "daily" {
		return ""
	}
	threshold := 48 * time.Hour
	var last time.Time
	if info, err := os.Stat(logPath); err == nil {
		last = info.ModTime()
	}
	if last.IsZero() {
		return ""
	}
	if time.Since(last) > threshold {
		return fmt.Sprintf("stale (last log %s)", last.UTC().Format(time.RFC3339))
	}
	return ""
}

func foundSchedule(st ScheduleState) bool {
	return st.Backend != "" && st.Backend != BackendManual
}
