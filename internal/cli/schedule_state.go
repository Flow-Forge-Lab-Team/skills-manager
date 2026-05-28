package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ScheduleBackend identifies which OS scheduler was installed.
type ScheduleBackend string

const (
	BackendManual  ScheduleBackend = "manual"
	BackendLaunchd ScheduleBackend = "launchd"
	BackendCron    ScheduleBackend = "cron"
)

// ScheduleState is persisted under the manager home after setup-schedule.
type ScheduleState struct {
	Version          int
	Provider         ScheduleProvider
	Backend          ScheduleBackend
	Interval         string
	InstalledAt      string
	BinaryPath       string
	IncludeSummarize bool
}

func scheduleStatePath(home string) string {
	return filepath.Join(home, "schedule.yaml")
}

func loadScheduleState(home string) (ScheduleState, bool, error) {
	var st ScheduleState
	data, err := os.ReadFile(scheduleStatePath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return st, false, nil
		}
		return st, false, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		if value == "~" {
			value = ""
		}
		switch key {
		case "version":
			st.Version, _ = strconv.Atoi(value)
		case "provider":
			st.Provider = ScheduleProvider(value)
		case "backend":
			st.Backend = ScheduleBackend(value)
		case "interval":
			st.Interval = value
		case "installed_at":
			st.InstalledAt = value
		case "binary_path":
			st.BinaryPath = value
		case "include_summarize":
			st.IncludeSummarize = value == "true"
		}
	}
	return st, true, scanner.Err()
}

func saveScheduleState(home string, st ScheduleState) error {
	if st.Version == 0 {
		st.Version = 1
	}
	if st.InstalledAt == "" {
		st.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "version: %d\n", st.Version)
	writeConfigString(&b, "", "provider", string(st.Provider))
	writeConfigString(&b, "", "backend", string(st.Backend))
	writeConfigString(&b, "", "interval", st.Interval)
	writeConfigString(&b, "", "installed_at", st.InstalledAt)
	writeConfigString(&b, "", "binary_path", st.BinaryPath)
	if st.IncludeSummarize {
		b.WriteString("include_summarize: true\n")
	} else {
		b.WriteString("include_summarize: false\n")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return os.WriteFile(scheduleStatePath(home), []byte(b.String()), 0o600)
}

func clearScheduleState(home string) error {
	err := os.Remove(scheduleStatePath(home))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
