package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedCatalogAppliesRemapAndInferredRequirements(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "pair-agent", "SKILL.md"), `---
name: pair-agent
description: Pair with a coding agent
---
Use rg to search files.
Use gh pr view to inspect GitHub pull requests.
Use the mcp__linear__ connector to update Linear.
`)
	remapPath := filepath.Join(t.TempDir(), "remap.json")
	writeJSONFile(t, remapPath, []seedCatalogRemapEntry{
		{
			Name:       "pair-agent",
			Locs:       []string{"claude"},
			Categories: []string{"Engineering", "Operations"},
			Tags:       []string{"gstack", "orchestration", "slash-command"},
		},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"seed-catalog", "--from", remapPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	meta, err := readSkillMeta(filepath.Join(libraryPath, "pair-agent", ".skill-meta.yaml"))
	if err != nil {
		t.Fatalf("readSkillMeta: %v", err)
	}
	if strings.Join(meta.Categories, ",") != "Engineering,Operations" {
		t.Fatalf("categories = %+v", meta.Categories)
	}
	for _, want := range []string{"gstack", "orchestration", "slash-command"} {
		if !stringSliceContains(meta.Tags, want) {
			t.Fatalf("tags missing %q: %+v", want, meta.Tags)
		}
	}
	if meta.Categorization.Source != "seed-remap" || meta.Categorization.Confidence != "high" {
		t.Fatalf("categorization = %+v", meta.Categorization)
	}
	if meta.Compatibility.Mode != "exclusive" || meta.Compatibility.Harness != "claude" {
		t.Fatalf("compatibility = %+v", meta.Compatibility)
	}
	for _, want := range []string{"gh", "rg"} {
		if !hasToolRequirement(meta.Requirements.Tools, want) {
			t.Fatalf("tools missing %q: %+v", want, meta.Requirements.Tools)
		}
	}
	if !hasMCPRequirement(meta.Requirements.MCPServers, "linear") {
		t.Fatalf("mcp servers missing linear: %+v", meta.Requirements.MCPServers)
	}
	if !meta.Requirements.Inferred {
		t.Fatalf("requirements.inferred = false, want true")
	}
	if _, err := os.Stat(filepath.Join(libraryPath, "catalog.yaml")); err != nil {
		t.Fatalf("catalog.yaml not written: %v", err)
	}
}

func TestSeedCatalogIsRepeatable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "docs", "SKILL.md"), `---
name: docs
description: Work with PDFs and documents
---
Use this for PDF review.
`)
	remapPath := filepath.Join(t.TempDir(), "remap.json")
	writeJSONFile(t, remapPath, []seedCatalogRemapEntry{
		{Name: "docs", Locs: []string{"codex", "claude"}, Categories: []string{"Documents"}, Tags: []string{"pdf", "documents"}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"seed-catalog", "--from", remapPath}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("first run returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	metaPath := filepath.Join(libraryPath, "docs", ".skill-meta.yaml")
	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	firstMeta := readFile(t, metaPath)
	firstCatalog := readFile(t, catalogPath)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"seed-catalog", "--from", remapPath}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("second run returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assertFileContent(t, metaPath, firstMeta)
	assertFileContent(t, catalogPath, firstCatalog)
}

func TestSeedCatalogDryRunDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "qa", "SKILL.md"), "---\nname: qa\n---\nReview code.\n")
	remapPath := filepath.Join(t.TempDir(), "remap.json")
	writeJSONFile(t, remapPath, []seedCatalogRemapEntry{
		{Name: "qa", Categories: []string{"Quality"}, Tags: []string{"testing"}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"seed-catalog", "--from", remapPath, "--dry-run"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if pathExists(filepath.Join(libraryPath, "qa", ".skill-meta.yaml")) {
		t.Fatalf("dry-run wrote .skill-meta.yaml")
	}
	if pathExists(filepath.Join(libraryPath, "catalog.yaml")) {
		t.Fatalf("dry-run wrote catalog.yaml")
	}
}

func TestSeedCatalogClearsExplicitPortableWhenRemapClassifiesHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "gstack-only", "SKILL.md"), "---\nname: gstack-only\n---\nBody\n")
	writeFile(t, filepath.Join(libraryPath, "gstack-only", ".skill-meta.yaml"), `version: 1
compatibility:
  mode: portable
  explicit_portable: true
`)
	remapPath := filepath.Join(t.TempDir(), "remap.json")
	writeJSONFile(t, remapPath, []seedCatalogRemapEntry{
		{Name: "gstack-only", Locs: []string{"claude"}, Categories: []string{"Engineering"}, Tags: []string{"gstack"}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"seed-catalog", "--from", remapPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	meta, err := readSkillMeta(filepath.Join(libraryPath, "gstack-only", ".skill-meta.yaml"))
	if err != nil {
		t.Fatalf("readSkillMeta: %v", err)
	}
	if meta.Compatibility.Mode != "exclusive" || meta.Compatibility.Harness != "claude" {
		t.Fatalf("compatibility = %+v", meta.Compatibility)
	}
	if meta.Compatibility.ExplicitPortable {
		t.Fatalf("explicit_portable remained true for exclusive compatibility")
	}
}

func TestSeedCatalogPreservesRemapCompatibilityWithWeakDetection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "weak-signal", "SKILL.md"), `---
name: weak-signal
---
Follow AGENTS.md conventions.
`)
	remapPath := filepath.Join(t.TempDir(), "remap.json")
	writeJSONFile(t, remapPath, []seedCatalogRemapEntry{
		{Name: "weak-signal", Locs: []string{"claude", "codex"}, Categories: []string{"Engineering"}, Tags: []string{"workflow"}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"seed-catalog", "--from", remapPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	meta, err := readSkillMeta(filepath.Join(libraryPath, "weak-signal", ".skill-meta.yaml"))
	if err != nil {
		t.Fatalf("readSkillMeta: %v", err)
	}
	if meta.Compatibility.Mode != "compatible" {
		t.Fatalf("sidecar compatibility mode = %q, want compatible", meta.Compatibility.Mode)
	}
	if strings.Join(meta.Compatibility.Harnesses, ",") != "claude,codex" {
		t.Fatalf("sidecar harnesses = %+v", meta.Compatibility.Harnesses)
	}
	cat, err := readCatalog(filepath.Join(libraryPath, "catalog.yaml"))
	if err != nil {
		t.Fatalf("readCatalog: %v", err)
	}
	if len(cat.Skills) != 1 || cat.Skills[0].Compatibility.Mode != "compatible" {
		t.Fatalf("catalog compatibility = %+v", cat.Skills)
	}
}

func TestSeedCatalogPreservesUnmodeledRequirementFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "runtime-heavy", "SKILL.md"), "---\nname: runtime-heavy\n---\nUse rg to search.\n")
	writeFile(t, filepath.Join(libraryPath, "runtime-heavy", ".skill-meta.yaml"), `version: 1
requirements:
  model:
    tool_use: required
    min_context_tokens: 32000
    reasoning: medium
    notes: "Needs reliable tool-call planning"
  tools:
    - name: "gh"
      required: true
      check: "gh auth status"
  scripts:
    allow_auto_run: false
    required_runtimes: ["node"]
  credentials:
    - name: "github"
      source: "gh"
      required: true
`)
	beforeMeta, err := readSkillMeta(filepath.Join(libraryPath, "runtime-heavy", ".skill-meta.yaml"))
	if err != nil {
		t.Fatalf("readSkillMeta before seed: %v", err)
	}
	if !hasToolRequirement(beforeMeta.Requirements.Tools, "gh") {
		t.Fatalf("precondition: gh requirement was not parsed: %+v", beforeMeta.Requirements.Tools)
	}
	if !sidecarHasUnmodeledRequirements(filepath.Join(libraryPath, "runtime-heavy", ".skill-meta.yaml")) {
		t.Fatalf("precondition: unmodeled requirements were not detected")
	}
	remapPath := filepath.Join(t.TempDir(), "remap.json")
	writeJSONFile(t, remapPath, []seedCatalogRemapEntry{
		{Name: "runtime-heavy", Categories: []string{"Engineering"}, Tags: []string{"github"}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"seed-catalog", "--from", remapPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	metaText := readFile(t, filepath.Join(libraryPath, "runtime-heavy", ".skill-meta.yaml"))
	for _, want := range []string{
		`tool_use: "required"`,
		`min_context_tokens: 32000`,
		`reasoning: medium`,
		`notes: "Needs reliable tool-call planning"`,
		`name: "rg"`,
		`check: "gh auth status"`,
		`scripts:`,
		`required_runtimes: ["node"]`,
		`credentials:`,
		`source: "gh"`,
		`categories:`,
		`tags:`,
	} {
		if !strings.Contains(metaText, want) {
			t.Fatalf("meta missing %q:\n%s", want, metaText)
		}
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"seed-catalog", "--from", remapPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("second Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assertFileContent(t, filepath.Join(libraryPath, "runtime-heavy", ".skill-meta.yaml"), metaText)
}

func TestSeedCatalogWarnsOnMissingAndOverBroadMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "broad", "SKILL.md"), "---\nname: broad\n---\nBody\n")
	writeFile(t, filepath.Join(libraryPath, "missing", "SKILL.md"), "---\nname: missing\n---\nBody\n")
	remapPath := filepath.Join(t.TempDir(), "remap.json")
	writeJSONFile(t, remapPath, []seedCatalogRemapEntry{
		{Name: "broad", Categories: []string{"Engineering", "Quality", "Operations"}, Tags: []string{"engineering"}},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"seed-catalog", "--from", remapPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		`broad: 3 categories`,
		`broad: broad tag "engineering"`,
		`missing: no remap entry`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
}

func writeJSONFile(t *testing.T, path string, value interface{}) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	writeFile(t, path, string(data))
}

func hasToolRequirement(values []toolRequirement, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}

func hasMCPRequirement(values []mcpRequirement, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}
