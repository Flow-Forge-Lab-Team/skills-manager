package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const cronMarker = "# skills-manager daily-check"

type scheduleOptions struct {
	provider string
}

func parseScheduleOptions(args []string) (scheduleOptions, []string, error) {
	opts := scheduleOptions{provider: string(ProviderLocal)}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--provider requires a value")
			}
			opts.provider = args[i+1]
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) > 0 {
		return opts, nil, fmt.Errorf("unknown argument: %s", rest[0])
	}
	if opts.provider != string(ProviderLocal) {
		return opts, nil, fmt.Errorf("only --provider local is supported in v0.2")
	}
	return opts, rest, nil
}

func runSetupSchedule(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	stdout := gf.outWriter(realStdout)
	opts, _, err := parseScheduleOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	_ = opts

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	cfg, err := defaultScheduleConfig()
	if err != nil {
		fmt.Fprintf(stderr, "schedule config: %v\n", err)
		return ExitOpError
	}

	includeSummarize := llmProviderConfigured(home)
	interactive := !gf.NonInteractive && !gf.JSON && !gf.Quiet && stdinIsTTY()

	choice := "local"
	if interactive {
		fmt.Fprintln(realStdout, "How should the skill check run?")
		fmt.Fprintln(realStdout, "")
		fmt.Fprintln(realStdout, "  [l] Local OS scheduler (launchd on macOS, cron on Linux)")
		fmt.Fprintln(realStdout, "      No OAuth, no cloud account, free, inspectable")
		fmt.Fprintln(realStdout, "      Only runs when this machine is awake")
		if includeSummarize {
			fmt.Fprintln(realStdout, "      Will also run summarize --pending when LLM provider is configured")
		}
		fmt.Fprintln(realStdout, "")
		fmt.Fprintln(realStdout, "  [m] Manual only (no schedule)")
		fmt.Fprintln(realStdout, "      You run `skills-manager check` when you want")
		fmt.Fprintln(realStdout, "")
		fmt.Fprint(realStdout, "Choose [l/m]: ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		switch line {
		case "m", "manual":
			choice = "manual"
		case "l", "local", "":
			choice = "local"
		default:
			fmt.Fprintf(stderr, "unknown choice: %s\n", line)
			return ExitUsageError
		}
	} else if gf.NonInteractive {
		choice = "local"
	}

	if choice == "manual" {
		st := ScheduleState{
			Provider:   ProviderLocal,
			Backend:    BackendManual,
			Interval:   cfg.Interval,
			BinaryPath: cfg.BinaryPath,
		}
		if err := saveScheduleState(home, st); err != nil {
			fmt.Fprintf(stderr, "save schedule state: %v\n", err)
			return ExitOpError
		}
		if gf.JSON {
			if err := writeJSON(realStdout, map[string]interface{}{
				"provider": "local",
				"backend":  "manual",
				"message":  "manual mode recorded; no OS scheduler installed",
			}); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
			return ExitSuccess
		}
		fmt.Fprintln(stdout, "Manual mode: run `skills-manager check` when you want updates polled.")
		return ExitSuccess
	}

	osName := detectOS()
	var backend ScheduleBackend
	switch osName {
	case "darwin":
		if err := installLaunchd(cfg, includeSummarize); err != nil {
			fmt.Fprintf(stderr, "install launchd: %v\n", err)
			return ExitOpError
		}
		backend = BackendLaunchd
	case "linux":
		if err := installCron(cfg, home, includeSummarize); err != nil {
			fmt.Fprintf(stderr, "install cron: %v\n", err)
			return ExitOpError
		}
		backend = BackendCron
	case "windows":
		fmt.Fprintln(stderr, "Windows Task Scheduler is not automated yet; use manual checks or WSL cron.")
		return ExitOpError
	default:
		fmt.Fprintf(stderr, "unsupported OS for local scheduling: %s\n", osName)
		return ExitOpError
	}

	st := ScheduleState{
		Provider:         ProviderLocal,
		Backend:          backend,
		Interval:         cfg.Interval,
		BinaryPath:       cfg.BinaryPath,
		IncludeSummarize: includeSummarize,
	}
	if err := saveScheduleState(home, st); err != nil {
		fmt.Fprintf(stderr, "save schedule state: %v\n", err)
		return ExitOpError
	}

	if gf.JSON {
		if err := writeJSON(realStdout, map[string]interface{}{
			"provider":          "local",
			"backend":           string(backend),
			"interval":          cfg.Interval,
			"include_summarize": includeSummarize,
			"binary_path":       cfg.BinaryPath,
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}

	fmt.Fprintf(stdout, "Installed local %s schedule (daily 09:00).\n", backend)
	fmt.Fprintf(stdout, "Logs: %s\n", cfg.LogDir)
	fmt.Fprintln(stdout, "Run `skills-manager status` to verify; `skills-manager unschedule` to remove.")
	return ExitSuccess
}

func runUnschedule(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	stdout := gf.outWriter(realStdout)
	if _, _, err := parseScheduleOptions(args); err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	st, found, err := loadScheduleState(home)
	if err != nil {
		fmt.Fprintf(stderr, "read schedule state: %v\n", err)
		return ExitOpError
	}

	removed := []string{}
	switch {
	case !found || st.Backend == BackendManual:
		// still try OS cleanup in case state was lost
		if detectOS() == "darwin" {
			if err := uninstallLaunchd(); err == nil {
				removed = append(removed, "launchd")
			}
		}
		if detectOS() == "linux" {
			if err := uninstallCron(home); err == nil {
				removed = append(removed, "cron")
			}
		}
	case st.Backend == BackendLaunchd:
		if err := uninstallLaunchd(); err != nil {
			fmt.Fprintf(stderr, "uninstall launchd: %v\n", err)
			return ExitOpError
		}
		removed = append(removed, "launchd")
	case st.Backend == BackendCron:
		if err := uninstallCron(home); err != nil {
			fmt.Fprintf(stderr, "uninstall cron: %v\n", err)
			return ExitOpError
		}
		removed = append(removed, "cron")
	}

	if err := clearScheduleState(home); err != nil {
		fmt.Fprintf(stderr, "clear schedule state: %v\n", err)
		return ExitOpError
	}

	if gf.JSON {
		if err := writeJSON(realStdout, map[string]interface{}{
			"removed": removed,
			"cleared": true,
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}
	if len(removed) == 0 {
		fmt.Fprintln(stdout, "No OS scheduler was configured (schedule state cleared).")
	} else {
		fmt.Fprintf(stdout, "Removed: %s\n", strings.Join(removed, ", "))
	}
	return ExitSuccess
}

func scheduledProgramArgs(cfg ScheduleConfig, includeSummarize bool) []string {
	checkArgs := []string{cfg.BinaryPath, "check", "--non-interactive", "--quiet", "--json"}
	if !includeSummarize {
		return checkArgs
	}
	inner := strings.Join(append(checkArgs,
		"&&", cfg.BinaryPath, "summarize", "--pending", "--auto", "--non-interactive"), " ")
	return []string{"/bin/sh", "-c", inner}
}

func buildCronShellCommand(cfg ScheduleConfig, includeSummarize bool) string {
	bin := shellSingleQuote(cfg.BinaryPath)
	if includeSummarize {
		inner := bin + " check --non-interactive --quiet --json && " +
			bin + " summarize --pending --auto --non-interactive"
		return "/bin/sh -c " + shellSingleQuote(inner)
	}
	return bin + " check --non-interactive --quiet --json"
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func stripManagedCronBlock(crontab string) []string {
	lines := strings.Split(crontab, "\n")
	out := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		if strings.TrimSpace(line) == cronMarker {
			skip = true
			continue
		}
		if skip {
			if strings.TrimSpace(line) == "" {
				skip = false
			}
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func installCron(cfg ScheduleConfig, home string, includeSummarize bool) error {
	logPath := filepath.Join(cfg.LogDir, "check.log")
	cmdLine := buildCronShellCommand(cfg, includeSummarize)
	cronLine := fmt.Sprintf("0 9 * * * %s >> %s 2>&1", cmdLine, shellSingleQuote(logPath))

	fragmentPath := filepath.Join(home, "cron-fragment")
	content := cronMarker + "\n" + cronLine + "\n"
	if err := os.WriteFile(fragmentPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write cron fragment: %w", err)
	}

	existing, _ := exec.Command("crontab", "-l").Output()
	lines := stripManagedCronBlock(string(existing))
	lines = append(lines, cronMarker, cronLine, "")
	newTab := strings.Join(lines, "\n")

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(newTab)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab install failed: %w\n%s", err, string(output))
	}
	return nil
}

func uninstallCron(home string) error {
	_ = os.Remove(filepath.Join(home, "cron-fragment"))
	existing, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		if strings.Contains(err.Error(), "no crontab") {
			return nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}
	kept := stripManagedCronBlock(string(existing))
	newTab := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if newTab == "" {
		cmd := exec.Command("crontab", "-r")
		if output, err := cmd.CombinedOutput(); err != nil {
			if !strings.Contains(string(output), "no crontab") {
				return fmt.Errorf("crontab -r: %w", err)
			}
		}
		return nil
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(newTab + "\n")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab update: %w\n%s", err, string(output))
	}
	return nil
}
