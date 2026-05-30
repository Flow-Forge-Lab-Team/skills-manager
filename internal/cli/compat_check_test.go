package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompatCheckAllSkipsCurrentFingerprintCache(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "---\nname: alpha\n---\n# alpha\n")
	cacheCompatCheckResult(t, home, "alpha", []string{"codex"})

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(time.Duration, string, []string, string) (string, error) {
		t.Fatal("provider should not run for current cache")
		return "", nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"--json", "compat-check", "--all", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var summary compatCheckBatchSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("parse json summary: %v\n%s", err, stdout.String())
	}
	if summary.Ran != 0 || summary.Skipped != 1 || summary.Stale != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.Results) != 1 || summary.Results[0].Status != "skipped" || summary.Results[0].Reason != "current" {
		t.Fatalf("results = %+v", summary.Results)
	}
}

func TestCompatCheckAllRerunsWhenSkillFingerprintChanges(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	skillPath := filepath.Join(home, "library", "alpha", "SKILL.md")
	writeFile(t, skillPath, "---\nname: alpha\n---\n# alpha old\n")
	cacheCompatCheckResult(t, home, "alpha", []string{"codex"})
	writeFile(t, skillPath, "---\nname: alpha\n---\n# alpha changed\n")

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	calls := 0
	llmCommandRunner = func(_ time.Duration, _ string, _ []string, stdin string) (string, error) {
		calls++
		if !strings.Contains(stdin, "# skills-manager compat-check handoff for alpha") {
			return "", fmt.Errorf("prompt missing alpha")
		}
		return validCompatCheckProviderJSON("alpha", "codex"), nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"--json", "compat-check", "--all", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	var summary compatCheckBatchSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("parse json summary: %v\n%s", err, stdout.String())
	}
	if summary.Ran != 1 || summary.Skipped != 0 || summary.Stale != 1 || summary.Failed != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Results[0].Reason != "fingerprint-mismatch" {
		t.Fatalf("reason = %q, want fingerprint-mismatch", summary.Results[0].Reason)
	}
	var entry compatCheckCacheEntry
	data, err := os.ReadFile(compatCheckCachePath(home, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Provider != "codex-cli" || entry.Model != "gpt-5.5" || entry.Fingerprint.SHA256 == "" || entry.Fingerprint.Size == 0 {
		t.Fatalf("cache metadata = %+v", entry)
	}
}

func TestCompatCheckAllIsolatesProviderFailures(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "---\nname: alpha\n---\n# alpha\n")
	writeFile(t, filepath.Join(home, "library", "beta", "SKILL.md"), "---\nname: beta\n---\n# beta\n")

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(_ time.Duration, _ string, _ []string, stdin string) (string, error) {
		switch {
		case strings.Contains(stdin, "handoff for alpha"):
			return `{"result":"not json"}`, nil
		case strings.Contains(stdin, "handoff for beta"):
			return validCompatCheckProviderJSON("beta", "codex"), nil
		default:
			return "", fmt.Errorf("unexpected prompt")
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"--json", "compat-check", "--all", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitPartial {
		t.Fatalf("Run returned %d, want partial\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var summary compatCheckBatchSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("parse json summary: %v\n%s", err, stdout.String())
	}
	if summary.Ran != 1 || summary.Skipped != 0 || summary.Stale != 0 || summary.Failed != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	statuses := map[string]string{}
	for _, result := range summary.Results {
		statuses[result.Skill] = result.Status
	}
	if statuses["alpha"] != "failed" || statuses["beta"] != "ran" {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func setupCompatCheckBatchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "config.yaml"), "version: 1\nllm:\n  provider: \"codex-cli\"\n  model: \"gpt-5.5\"\n")
	return home
}

func cacheCompatCheckResult(t *testing.T, home, skill string, targets []string) {
	t.Helper()
	parsed, err := parseCompatCheckOutput([]byte(validCompatCheckProviderJSON(skill, targets[0])), "test output")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := skillFingerprintForFile(filepath.Join(home, "library", skill, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCompatCheckCache(home, skill, targets, fingerprint, parsed, "codex-cli", "gpt-5.5"); err != nil {
		t.Fatal(err)
	}
}
