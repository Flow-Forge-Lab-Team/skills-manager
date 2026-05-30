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
	if entry.Provider != "codex-cli" || entry.Model != "gpt-5.5" || entry.Fingerprint.SHA256 == "" || entry.Fingerprint.Size == 0 || entry.PromptSHA256 == "" {
		t.Fatalf("cache metadata = %+v", entry)
	}
}

func TestCompatCheckAllRerunsWhenModelChanges(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "---\nname: alpha\n---\n# alpha\n")
	cacheCompatCheckResult(t, home, "alpha", []string{"codex"})
	writeFile(t, filepath.Join(home, "config.yaml"), "version: 1\nllm:\n  provider: \"codex-cli\"\n  model: \"gpt-5.6\"\n")

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	calls := 0
	llmCommandRunner = func(_ time.Duration, _ string, _ []string, _ string) (string, error) {
		calls++
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
	if summary.Ran != 1 || summary.Skipped != 0 || summary.Stale != 1 || summary.Results[0].Reason != "model-mismatch" {
		t.Fatalf("summary = %+v", summary)
	}
	var entry compatCheckCacheEntry
	data, err := os.ReadFile(compatCheckCachePath(home, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Model != "gpt-5.6" {
		t.Fatalf("cache model = %q, want gpt-5.6", entry.Model)
	}
}

func TestCompatCheckAllSkipsFromCacheWithoutProviderMetadata(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "---\nname: alpha\n---\n# alpha\n")
	fromPath := filepath.Join(home, "alpha-output.json")
	writeFile(t, fromPath, validCompatCheckProviderJSON("alpha", "codex"))

	var fromStdout bytes.Buffer
	var fromStderr bytes.Buffer
	code := Run([]string{"compat-check", "alpha", "--to", "codex", "--from", fromPath}, &fromStdout, &fromStderr)
	if code != ExitSuccess {
		t.Fatalf("--from returned %d\nstdout:\n%s\nstderr:\n%s", code, fromStdout.String(), fromStderr.String())
	}

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(time.Duration, string, []string, string) (string, error) {
		t.Fatal("provider should not run for current --from cache")
		return "", nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code = Run([]string{"--json", "compat-check", "--all", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("--all returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var summary compatCheckBatchSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("parse json summary: %v\n%s", err, stdout.String())
	}
	if summary.Ran != 0 || summary.Skipped != 1 || summary.Stale != 0 || summary.Results[0].Reason != "current" {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestCompatCheckAllRerunsWhenPromptChanges(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "---\nname: alpha\n---\n# alpha\n")
	cacheCompatCheckResult(t, home, "alpha", []string{"codex"})
	mutateCompatCheckCache(t, home, "alpha", func(entry *compatCheckCacheEntry) {
		entry.PromptSHA256 = "old-prompt"
	})

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	calls := 0
	llmCommandRunner = func(_ time.Duration, _ string, _ []string, _ string) (string, error) {
		calls++
		return validCompatCheckProviderJSON("alpha", "codex"), nil
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
	if calls != 1 || summary.Ran != 1 || summary.Stale != 1 || summary.Results[0].Reason != "prompt-mismatch" {
		t.Fatalf("calls=%d summary=%+v", calls, summary)
	}
}

func TestCompatCheckAllReportsHumanSummary(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "---\nname: alpha\n---\n# alpha\n")
	cacheCompatCheckResult(t, home, "alpha", []string{"codex"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"compat-check", "--all", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "compat-check --all: ran=0 skipped=1 stale=0 failed=0") ||
		!strings.Contains(stdout.String(), "alpha: skipped (current)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestCompatCheckAllReportsHumanProgress(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "---\nname: alpha\n---\n# alpha\n")
	cacheCompatCheckResult(t, home, "alpha", []string{"codex"})
	writeFile(t, filepath.Join(home, "library", "beta", "SKILL.md"), "---\nname: beta\n---\n# beta\n")

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(_ time.Duration, _ string, _ []string, stdin string) (string, error) {
		if !strings.Contains(stdin, "handoff for beta") {
			return "", fmt.Errorf("unexpected prompt")
		}
		return validCompatCheckProviderJSON("beta", "codex"), nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"compat-check", "--all", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	expected := []string{
		"compat-check --all: checking 2 skills",
		"alpha: cached (current)",
		"beta: missing",
		"beta: running provider",
		"beta: ran (missing)",
		"compat-check --all: ran=1 skipped=1 stale=0 failed=0",
	}
	for _, want := range expected {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "beta: running provider") > strings.Index(out, "compat-check --all: ran=1") {
		t.Fatalf("progress was printed after final summary:\n%s", out)
	}
}

func TestCompatCheckAllJSONDoesNotInterleaveProgress(t *testing.T) {
	home := setupCompatCheckBatchHome(t)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "---\nname: alpha\n---\n# alpha\n")
	cacheCompatCheckResult(t, home, "alpha", []string{"codex"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"--json", "compat-check", "--all", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "compat-check --all:") || strings.Contains(stdout.String(), "alpha: cached") {
		t.Fatalf("stdout contains progress:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var summary compatCheckBatchSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("parse json summary: %v\n%s", err, stdout.String())
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

func TestCompatCheckCachePathDisambiguatesCase(t *testing.T) {
	home := t.TempDir()
	if compatCheckCachePath(home, "API") == compatCheckCachePath(home, "api") {
		t.Fatal("cache paths must not collide for distinct case-sensitive skill names")
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

func mutateCompatCheckCache(t *testing.T, home, skill string, mutate func(*compatCheckCacheEntry)) {
	t.Helper()
	path := compatCheckCachePath(home, skill)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry compatCheckCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	mutate(&entry)
	data, err = json.MarshalIndent(entry, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
