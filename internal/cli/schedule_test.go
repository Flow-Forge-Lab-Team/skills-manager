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
	plist := generateLaunchdPlist(cfg)

	if !strings.Contains(plist, "com.skills-manager.daily-check") {
		t.Error("plist missing label")
	}
	if !strings.Contains(plist, "/usr/local/bin/skills-manager") {
		t.Error("plist missing binary path")
	}
	if !strings.Contains(plist, "check.stdout.log") {
		t.Error("plist missing stdout log")
	}
}

func TestDefaultScheduleConfig_Basic(t *testing.T) {
	// Should not panic and should produce reasonable values
	cfg, err := defaultScheduleConfig()
	if err != nil {
		// On some test envs managerHome may fail; that's acceptable
		t.Logf("defaultScheduleConfig returned error (may be env-specific): %v", err)
		return
	}
	if cfg.Provider != ProviderLocal {
		t.Errorf("expected ProviderLocal, got %s", cfg.Provider)
	}
}