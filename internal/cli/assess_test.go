package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAssessHandoffUsesPrivacyBoundedProjectInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeScanSkill(t, filepath.Join(home, "library"), "review", "---\nname: review\n---\n# Review\nOPENAI_API_KEY=sk-assessfixturesecret123456789\n")
	project := t.TempDir()
	writeFile(t, filepath.Join(project, ".git", "config"), "[remote \"origin\"]\n\turl = https://ghp_remotecredential123456789@github.com/acme/private.git\n")
	writeFile(t, filepath.Join(project, "AGENTS.md"), "# Agent instructions\nUse go test.\nGITHUB_TOKEN=ghp_assessfixturesecret123456789\nUse gho_barefixturesecret123456789 for the API.\nPASSWORD=\"correct horse battery staple\"\n")
	writeFile(t, filepath.Join(project, "secret.txt"), "do not include this source content\n")
	writeFile(t, filepath.Join(project, ".env"), "PASSWORD=super-secret-password\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "assess", "review", "--project", project, "--target", "codex", "--handoff"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("assess handoff returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var result assessResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal result: %v\n%s", err, stdout.String())
	}
	if result.PromptPath == "" || result.Input.ProjectFingerprint == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	prompt := readFile(t, result.PromptPath)
	for _, want := range []string{"Project input is privacy-bounded", "AGENTS.md", "Use go test.", "Target skill SKILL.md", "# Review"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "do not include this source content") || strings.Contains(prompt, "secret.txt") {
		t.Fatalf("prompt included non-instruction project file:\n%s", prompt)
	}
	for _, raw := range []string{"sk-assessfixturesecret123456789", "ghp_assessfixturesecret123456789", "gho_barefixturesecret123456789", "ghp_remotecredential123456789", "correct horse battery staple", "super-secret-password", ".env"} {
		if strings.Contains(prompt, raw) {
			t.Fatalf("prompt included secret %q:\n%s", raw, prompt)
		}
	}
	if !strings.Contains(prompt, "https://github.com/[redacted]") {
		t.Fatalf("prompt missing redacted repo remote:\n%s", prompt)
	}
	if got := strings.Count(prompt, "[REDACTED_SECRET]"); got < 2 {
		t.Fatalf("prompt redactions = %d, want at least 2:\n%s", got, prompt)
	}
	logText := readFile(t, filepath.Join(home, "logs", "skills-manager.log"))
	if !strings.Contains(logText, "assess.audit") || strings.Contains(logText, "sk-assessfixturesecret") || strings.Contains(logText, "ghp_assessfixture") {
		t.Fatalf("privacy audit log missing safe assess record:\n%s", logText)
	}
}

func TestAssessAutoCachesBySkillHashProjectTargetPromptAndProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "config.yaml"), "version: 1\nllm:\n  provider: \"codex-cli\"\n  model: \"gpt-5.5\"\n")
	skillPath := filepath.Join(home, "library", "review", "SKILL.md")
	writeFile(t, skillPath, "---\nname: review\n---\n# Review\n")
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "AGENTS.md"), "# Agent instructions\n")

	calls := 0
	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(_ time.Duration, name string, args []string, stdin string) (string, error) {
		calls++
		if name != "codex" || !strings.Contains(stdin, "# skills-manager") {
			t.Fatalf("unexpected provider call name=%s args=%v stdin=\n%s", name, args, stdin)
		}
		var input assessInput
		start := strings.Index(stdin, "{\n")
		end := strings.Index(stdin, "\n\nTarget skill SKILL.md")
		if start == -1 || end == -1 {
			t.Fatalf("prompt missing input JSON:\n%s", stdin)
		}
		if err := json.Unmarshal([]byte(stdin[start:end]), &input); err != nil {
			t.Fatalf("unmarshal prompt input: %v\n%s", err, stdin[start:end])
		}
		return fmt.Sprintf(`{
  "schema_version": 1,
  "assessment": {
    "skill": "review",
    "target_harness": "codex",
    "project_id": %q,
    "deterministic_facts": {
      "skill_sha256": %q,
      "project_fingerprint": %q,
      "inventory_fingerprint": %q,
      "prompt_version": "assess-v1"
    },
    "advisory_judgment": {
      "global_fit": "unclear",
      "project_fit": "yes",
      "risk": "low",
      "compatibility": "compatible",
      "confidence": "medium",
      "reasons": ["bounded fixture assessment"]
    }
  }
}`, input.Project.ProjectID, input.SkillSHA256, input.ProjectFingerprint, input.InventoryFingerprint), nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "assess", "review", "--project", project, "--target", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("first assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var first assessResult
	if err := json.Unmarshal(stdout.Bytes(), &first); err != nil {
		t.Fatalf("unmarshal first: %v\n%s", err, stdout.String())
	}
	if first.Cached || first.CacheStatus != "stored" || calls != 1 {
		t.Fatalf("first result=%+v calls=%d", first, calls)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "assess", "review", "--project", project, "--target", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("cached assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var second assessResult
	if err := json.Unmarshal(stdout.Bytes(), &second); err != nil {
		t.Fatalf("unmarshal second: %v\n%s", err, stdout.String())
	}
	if !second.Cached || second.CacheStatus != "hit" || calls != 1 {
		t.Fatalf("second result=%+v calls=%d", second, calls)
	}

	writeFile(t, skillPath, "---\nname: review\n---\n# Review changed\n")
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "assess", "review", "--project", project, "--target", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("stale assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 2 {
		t.Fatalf("provider calls after skill change = %d, want 2", calls)
	}
}

func TestAssessInventoryChangesInvalidateCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "config.yaml"), "version: 1\nllm:\n  provider: \"codex-cli\"\n  model: \"gpt-5.5\"\n")
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "---\nname: review\n---\n# Review\n")
	inventoryPath := filepath.Join(t.TempDir(), "discover.json")
	writeAssessInventoryFixture(t, inventoryPath, "abc")

	calls := 0
	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(_ time.Duration, _ string, _ []string, stdin string) (string, error) {
		calls++
		var input assessInput
		start := strings.Index(stdin, "{\n")
		end := strings.Index(stdin, "\n\nTarget skill SKILL.md")
		if start == -1 || end == -1 {
			t.Fatalf("prompt missing input JSON:\n%s", stdin)
		}
		if err := json.Unmarshal([]byte(stdin[start:end]), &input); err != nil {
			t.Fatalf("unmarshal prompt input: %v", err)
		}
		return fmt.Sprintf(`{
  "schema_version": 1,
  "assessment": {
    "skill": "review",
    "target_harness": "codex",
    "deterministic_facts": {
      "skill_sha256": %q,
      "project_fingerprint": %q,
      "inventory_fingerprint": %q,
      "prompt_version": "assess-v1"
    },
    "advisory_judgment": {
      "global_fit": "unclear",
      "project_fit": "unclear",
      "risk": "low",
      "compatibility": "compatible",
      "confidence": "medium",
      "reasons": ["inventory fixture assessment"]
    }
  }
}`, input.SkillSHA256, input.ProjectFingerprint, input.InventoryFingerprint), nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "assess", "review", "--inventory", inventoryPath, "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("first inventory assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Fatalf("calls after first run = %d, want 1", calls)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "assess", "review", "--inventory", inventoryPath, "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("cached inventory assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Fatalf("calls after cached run = %d, want 1", calls)
	}

	writeAssessInventoryFixture(t, inventoryPath, "def")
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "assess", "review", "--inventory", inventoryPath, "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("changed inventory assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 2 {
		t.Fatalf("calls after inventory change = %d, want 2", calls)
	}
}

func TestAssessFromReportsCacheWriteFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "---\nname: review\n---\n# Review\n")
	input, _, err := buildAssessInput(home, assessOptions{skill: "review", target: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	fromPath := filepath.Join(t.TempDir(), "assessment.json")
	writeFile(t, fromPath, fmt.Sprintf(`{
  "schema_version": 1,
  "assessment": {
    "skill": "review",
    "target_harness": "codex",
    "deterministic_facts": {
      "skill_sha256": %q,
      "project_fingerprint": %q,
      "inventory_fingerprint": %q,
      "prompt_version": "assess-v1"
    },
    "advisory_judgment": {
      "global_fit": "unclear",
      "project_fit": "unclear",
      "risk": "low",
      "compatibility": "compatible",
      "confidence": "medium",
      "reasons": ["fixture"]
    }
  }
}`, input.SkillSHA256, input.ProjectFingerprint, input.InventoryFingerprint))
	writeFile(t, filepath.Join(home, "assessments"), "not a directory")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "assess", "review", "--from", fromPath}, &stdout, &stderr)
	if code != ExitOpError {
		t.Fatalf("code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitOpError, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no stored JSON", stdout.String())
	}
	if !strings.Contains(stderr.String(), "write assessment cache") {
		t.Fatalf("stderr = %q, want cache write error", stderr.String())
	}
}

func TestAssessFromDoesNotPopulateConfiguredProviderCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "config.yaml"), "version: 1\nllm:\n  provider: \"codex-cli\"\n  model: \"gpt-5.5\"\n")
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "---\nname: review\n---\n# Review\n")
	input, _, err := buildAssessInput(home, assessOptions{skill: "review", target: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	fromPath := filepath.Join(t.TempDir(), "assessment.json")
	writeFile(t, fromPath, fmt.Sprintf(`{
  "schema_version": 1,
  "assessment": {
    "skill": "review",
    "target_harness": "codex",
    "deterministic_facts": {
      "skill_sha256": %q,
      "project_fingerprint": %q,
      "inventory_fingerprint": %q,
      "prompt_version": "assess-v1"
    },
    "advisory_judgment": {
      "global_fit": "unclear",
      "project_fit": "unclear",
      "risk": "low",
      "compatibility": "compatible",
      "confidence": "medium",
      "reasons": ["imported fixture"]
    }
  }
}`, input.SkillSHA256, input.ProjectFingerprint, input.InventoryFingerprint))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "assess", "review", "--from", fromPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("from assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	calls := 0
	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(_ time.Duration, _ string, _ []string, stdin string) (string, error) {
		calls++
		var promptInput assessInput
		start := strings.Index(stdin, "{\n")
		end := strings.Index(stdin, "\n\nTarget skill SKILL.md")
		if start == -1 || end == -1 {
			t.Fatalf("prompt missing input JSON:\n%s", stdin)
		}
		if err := json.Unmarshal([]byte(stdin[start:end]), &promptInput); err != nil {
			t.Fatalf("unmarshal prompt input: %v", err)
		}
		return fmt.Sprintf(`{
  "schema_version": 1,
  "assessment": {
    "skill": "review",
    "target_harness": "codex",
    "deterministic_facts": {
      "skill_sha256": %q,
      "project_fingerprint": %q,
      "inventory_fingerprint": %q,
      "prompt_version": "assess-v1"
    },
    "advisory_judgment": {
      "global_fit": "unclear",
      "project_fit": "yes",
      "risk": "low",
      "compatibility": "compatible",
      "confidence": "medium",
      "reasons": ["configured provider fixture"]
    }
  }
}`, promptInput.SkillSHA256, promptInput.ProjectFingerprint, promptInput.InventoryFingerprint), nil
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "assess", "review", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("auto assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Fatalf("configured provider calls = %d, want 1", calls)
	}
}

func TestAssessRejectsInvalidAdvisoryEnums(t *testing.T) {
	output := `{
  "schema_version": 1,
  "assessment": {
    "skill": "review",
    "target_harness": "codex",
    "deterministic_facts": {
      "skill_sha256": "abc",
      "project_fingerprint": "def",
      "prompt_version": "assess-v1"
    },
    "advisory_judgment": {
      "global_fit": "maybe",
      "project_fit": "unclear",
      "risk": "critical",
      "compatibility": "compatible",
      "confidence": "medium",
      "reasons": ["fixture"]
    }
  }
}`
	if _, err := parseAssessProviderOutput([]byte(output), "fixture"); err == nil {
		t.Fatal("parseAssessProviderOutput accepted invalid advisory enum values")
	}
}

func TestAssessDeepFileSignalsCapsResults(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 205; i++ {
		writeFile(t, filepath.Join(root, fmt.Sprintf("%03d.txt", i)), "signal\n")
	}

	signals := collectAssessDeepFileSignals(root)
	if len(signals) != 200 {
		t.Fatalf("deep file signals = %d, want 200", len(signals))
	}
}

func TestAssessHandoffQuotesImportCommandPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "---\nname: review\n---\n# Review\n")
	project := filepath.Join(t.TempDir(), "My Project")
	writeFile(t, filepath.Join(project, "AGENTS.md"), "# Project rules\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"assess", "review", "--project", project, "--handoff"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("handoff returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	want := "--project '" + project + "'"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("import command = %q, want quoted %q", stdout.String(), want)
	}
}

func TestAssessRejectsMissingProjectPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "---\nname: review\n---\n# Review\n")
	missing := filepath.Join(t.TempDir(), "missing")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "assess", "review", "--project", missing, "--handoff"}, &stdout, &stderr)
	if code != ExitOpError {
		t.Fatalf("code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitOpError, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want none", stdout.String())
	}
	if !strings.Contains(stderr.String(), "project path") {
		t.Fatalf("stderr = %q, want project path error", stderr.String())
	}
}

func TestAssessAllowsProjectOnlyAndInventorySubsetHandoffs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "AGENTS.md"), "# Project rules\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "assess", "--project", project, "--handoff"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("project assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var projectResult assessResult
	if err := json.Unmarshal(stdout.Bytes(), &projectResult); err != nil {
		t.Fatalf("unmarshal project result: %v\n%s", err, stdout.String())
	}
	if projectResult.Input.Skill != "project" || projectResult.Input.ProjectFingerprint == "" {
		t.Fatalf("unexpected project-only input: %+v", projectResult.Input)
	}
	if prompt := readFile(t, projectResult.PromptPath); !strings.Contains(prompt, "No single SKILL.md was selected") {
		t.Fatalf("project-only prompt missing no-skill note:\n%s", prompt)
	}

	inventoryPath := filepath.Join(t.TempDir(), "discover.json")
	writeAssessInventoryFixture(t, inventoryPath, "abc")
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "assess", "--inventory", inventoryPath, "--handoff"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("inventory assess returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var inventoryResult assessResult
	if err := json.Unmarshal(stdout.Bytes(), &inventoryResult); err != nil {
		t.Fatalf("unmarshal inventory result: %v\n%s", err, stdout.String())
	}
	if inventoryResult.Input.Skill != "inventory-subset" || len(inventoryResult.Input.InventoryItems) != 1 || inventoryResult.Input.InventoryFingerprint == "" {
		t.Fatalf("unexpected inventory input: %+v", inventoryResult.Input)
	}
	gotRecs := inventoryResult.Input.InventoryItems[0].Recommendation
	if len(gotRecs) != 1 || gotRecs[0] != "rec-1" {
		t.Fatalf("inventory recommendations = %+v, want only matching rec-1", gotRecs)
	}
}

func writeAssessInventoryFixture(t *testing.T, path, hash string) {
	t.Helper()
	writeFile(t, path, fmt.Sprintf(`{
  "installations": [
    {
      "installation_id": "i1",
      "skill_name": "review",
      "tool_id": "claude",
      "scope": "global",
      "source_path": "/tmp/review",
      "content_path": "/tmp/review/SKILL.md",
      "content_sha256": %q,
      "content_size_bytes": 12,
      "managed": false,
      "ownership": "unmanaged",
      "format": "skill_md",
      "present": true
    }
  ],
  "report": {
    "recommendations": [
      {
        "recommendation_id": "rec-1",
        "kind": "install_global",
        "title": "Install review",
        "reason": "coverage",
        "confidence": "medium",
        "skill_name": "review",
        "source_installation_ids": ["i1"],
        "requires_plan": true
      },
      {
        "recommendation_id": "rec-other",
        "kind": "install_global",
        "title": "Install other review",
        "reason": "other project coverage",
        "confidence": "medium",
        "skill_name": "review",
        "source_installation_ids": ["other-install"],
        "requires_plan": true
      }
    ]
  }
}`, hash))
}
