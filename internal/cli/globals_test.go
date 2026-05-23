package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractGlobalFlagsStripsAndPreservesOrder(t *testing.T) {
	gf, rest, err := extractGlobalFlags([]string{
		"--json", "install", "--project", "/tmp/p", "--quiet", "--non-interactive",
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !gf.JSON || !gf.Quiet || !gf.NonInteractive {
		t.Fatalf("flags not extracted: %+v", gf)
	}
	want := []string{"install", "--project", "/tmp/p"}
	if strings.Join(rest, " ") != strings.Join(want, " ") {
		t.Fatalf("rest = %v, want %v", rest, want)
	}
}

func TestExtractGlobalFlagsConfigRequiresValue(t *testing.T) {
	if _, _, err := extractGlobalFlags([]string{"--config"}); err == nil {
		t.Fatal("expected error when --config lacks value")
	}
}

func TestHelpTopLevel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--help"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage: skills-manager") {
		t.Fatalf("help output missing usage: %s", stdout.String())
	}
}

func TestHelpSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"help", "install"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("help install exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "skills-manager install") {
		t.Fatalf("install help missing header: %s", stdout.String())
	}
}

func TestListJSONViaGlobalFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(libraryPath, "alpha", "SKILL.md"),
		"---\nname: alpha\ndescription: A\n---\nbody")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "list"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("list --json exit = %d: %s", code, stderr.String())
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &arr); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, stdout.String())
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(arr))
	}
}

func TestQuietSuppressesHumanText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(libraryPath, "alpha", "SKILL.md"),
		"---\nname: alpha\ndescription: A\n---\nbody")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--quiet", "list"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("quiet list exit = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no human stdout under --quiet, got %q", stdout.String())
	}
}

func TestQuietWithJSONStillEmitsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(libraryPath, "alpha", "SKILL.md"),
		"---\nname: alpha\ndescription: A\n---\nbody")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--quiet", "--json", "list"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("quiet+json exit = %d", code)
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &arr); err != nil {
		t.Fatalf("expected JSON output under --quiet --json: %v\ngot: %s", err, stdout.String())
	}
}

func TestInstallJSONOutput(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	seedInstallFixture(t, home, project, "alpha")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "--non-interactive", "install", "--project", project}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("install exit = %d: %s\n%s", code, stderr.String(), stdout.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, stdout.String())
	}
	if result["mode"] != "install" {
		t.Errorf("mode = %v, want install", result["mode"])
	}
	if ec, ok := result["exit_code"].(float64); !ok || int(ec) != ExitSuccess {
		t.Errorf("exit_code = %v, want %d", result["exit_code"], ExitSuccess)
	}
	installed, _ := result["installed"].([]interface{})
	if len(installed) != 1 || installed[0] != "alpha" {
		t.Errorf("installed = %v, want [alpha]", result["installed"])
	}
}

func TestUninstallJSONOutput(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	seedInstallFixture(t, home, project, "alpha")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("install setup failed: %d %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"--json", "uninstall", "--project", project, "--confirm"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("uninstall exit = %d: %s\n%s", code, stderr.String(), stdout.String())
	}
	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, stdout.String())
	}
	removed, _ := result["removed"].([]interface{})
	if len(removed) == 0 {
		t.Errorf("expected non-empty removed list, got %v", result["removed"])
	}
}

func TestLoggingWritesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"list"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("list exit = %d", code)
	}

	logPath := filepath.Join(home, "logs", "skills-manager.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("log not written: %v", err)
	}
	if !strings.Contains(string(data), "cmd=list") {
		t.Errorf("log missing cmd=list record: %s", string(data))
	}
}

func TestUnknownArgsExitUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--no-such-flag"}, &stdout, &stderr); code != ExitUsageError {
		t.Fatalf("expected exit %d, got %d (stderr=%s)", ExitUsageError, code, stderr.String())
	}
}

func TestConfigOverridesProjectYAML(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Seed library, but write the project's default project.yaml with a
	// non-matching category (would install nothing). The --config override
	// points at an alternate file that DOES match, so install should succeed.
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"),
		"version: 1\nskills:\n  - name: alpha\n    categories: [Engineering]\n    compatibility:\n      mode: portable\n    requirements:\n      tools: []\n")
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"),
		"---\nname: alpha\n---\nbody\n")
	writeFile(t, filepath.Join(project, ".skills", "project.yaml"),
		"version: 1\nname: demo\ncategories: [Unrelated]\nharnesses: [claude]\n")
	altConfig := filepath.Join(project, "alt-project.yaml")
	writeFile(t, altConfig,
		"version: 1\nname: demo\ncategories: [Engineering]\nharnesses: [claude]\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "--config", altConfig, "install", "--project", project}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("install exit = %d: %s\n%s", code, stderr.String(), stdout.String())
	}
	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, stdout.String())
	}
	installed, _ := result["installed"].([]interface{})
	if len(installed) != 1 || installed[0] != "alpha" {
		t.Fatalf("expected alpha to be installed via --config override, got %v", result["installed"])
	}
}

// seedInstallFixture writes a minimal catalog + library skill + project.yaml
// for use in install/uninstall tests.
func seedInstallFixture(t *testing.T, home, project, skill string) {
	t.Helper()
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"),
		"version: 1\nskills:\n  - name: "+skill+"\n    categories: [Engineering]\n    compatibility:\n      mode: portable\n    requirements:\n      tools: []\n")
	writeFile(t, filepath.Join(home, "library", skill, "SKILL.md"),
		"---\nname: "+skill+"\n---\nbody\n")
	writeFile(t, filepath.Join(project, ".skills", "project.yaml"),
		"version: 1\nname: demo\ncategories: [Engineering]\nharnesses: [claude]\n")
}
