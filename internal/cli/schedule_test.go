package cli

import (
	"strings"
	"testing"
)

func TestDetectOS(t *testing.T) {
	os := detectOS()
	if os == "" {
		t.Error("detectOS returned empty string")
	}
}

func TestResolveBinaryPath(t *testing.T) {
	p, err := resolveBinaryPath()
	if err != nil {
		t.Fatalf("resolveBinaryPath error: %v", err)
	}
	if p == "" {
		t.Error("resolveBinaryPath returned empty")
	}
}

func TestGenerateLaunchdPlist(t *testing.T) {
	cfg := ScheduleConfig{
		BinaryPath: "/usr/local/bin/skills-manager",
		LogDir:     "/Users/test/.skills-manager/logs",
	}
	plist := generateLaunchdPlist(cfg, false)

	if !strings.Contains(plist, "com.skills-manager.daily-check") {
		t.Error("plist missing label")
	}
	if !strings.Contains(plist, "/usr/local/bin/skills-manager") {
		t.Error("plist missing binary path")
	}
	if !strings.Contains(plist, "check.stdout.log") {
		t.Error("plist missing stdout log")
	}
	if strings.Contains(plist, "RunAtLoad") {
		t.Error("plist should not run at load")
	}
}

func TestGenerateLaunchdPlist_WithSummarize(t *testing.T) {
	cfg := ScheduleConfig{BinaryPath: "/bin/skills-manager", LogDir: "/tmp/logs"}
	plist := generateLaunchdPlist(cfg, true)
	if !strings.Contains(plist, "/bin/sh") {
		t.Error("expected shell wrapper for summarize chain")
	}
	if !strings.Contains(plist, "summarize") {
		t.Error("plist missing summarize in command chain")
	}
}

func TestScheduledProgramArgs(t *testing.T) {
	cfg := ScheduleConfig{BinaryPath: "/bin/sm"}
	args := scheduledProgramArgs(cfg, false)
	if len(args) != 5 || args[0] != "/bin/sm" || args[1] != "check" {
		t.Fatalf("unexpected args: %v", args)
	}
	args2 := scheduledProgramArgs(cfg, true)
	if len(args2) != 3 || args2[0] != "/bin/sh" {
		t.Fatalf("unexpected summarize args: %v", args2)
	}
}

func TestDefaultScheduleConfig_Basic(t *testing.T) {
	cfg, err := defaultScheduleConfig()
	if err != nil {
		t.Logf("defaultScheduleConfig returned error (may be env-specific): %v", err)
		return
	}
	if cfg.Provider != ProviderLocal {
		t.Errorf("expected ProviderLocal, got %s", cfg.Provider)
	}
}

func TestScheduleStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	st := ScheduleState{
		Provider:         ProviderLocal,
		Backend:          BackendLaunchd,
		Interval:         "daily",
		BinaryPath:       "/bin/skills-manager",
		IncludeSummarize: true,
	}
	if err := saveScheduleState(home, st); err != nil {
		t.Fatal(err)
	}
	got, found, err := loadScheduleState(home)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if got.Backend != BackendLaunchd || !got.IncludeSummarize {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestParseScheduleOptions_InvalidProvider(t *testing.T) {
	_, _, err := parseScheduleOptions([]string{"--provider", "cloud"})
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}
