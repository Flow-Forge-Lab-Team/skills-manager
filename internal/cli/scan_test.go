package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanReportsUnregistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create a skills directory with an unregistered skill
	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	fooDir := filepath.Join(skillsDir, "foo")
	if err := os.MkdirAll(fooDir, 0755); err != nil {
		t.Fatalf("create foo dir: %v", err)
	}

	skillMd := `---
name: foo
description: A test skill
---

# foo

Test.
`

	if err := os.WriteFile(filepath.Join(fooDir, "SKILL.md"), []byte(skillMd), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	args := []string{"--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runScan(args, &stdout, &stderr, gf)

	if code != ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, ExitSuccess)
	}

	output := stdout.String()
	if !strings.Contains(output, "foo") || !strings.Contains(output, "unregistered") {
		t.Errorf("expected 'foo' and 'unregistered' in output, got: %s", output)
	}
}

func TestScanRespectsScanIgnore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create skills directory
	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	fooDir := filepath.Join(skillsDir, "foo")
	if err := os.MkdirAll(fooDir, 0755); err != nil {
		t.Fatalf("create foo dir: %v", err)
	}

	skillMd := `---
name: foo
description: A test skill
---

# foo

Test.
`

	if err := os.WriteFile(filepath.Join(fooDir, "SKILL.md"), []byte(skillMd), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Create scan-ignore.txt with the foo path
	ignoreFile := filepath.Join(home, "scan-ignore.txt")
	if err := os.WriteFile(ignoreFile, []byte(fooDir+"\n"), 0644); err != nil {
		t.Fatalf("write scan-ignore.txt: %v", err)
	}

	args := []string{"--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runScan(args, &stdout, &stderr, gf)

	if code != ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, ExitSuccess)
	}

	output := stdout.String()
	if strings.Contains(output, "foo") {
		t.Errorf("expected 'foo' to be ignored, but it appears in output: %s", output)
	}
}

func TestScanIngestAutoHighConfidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create skills directory
	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	// High-confidence skill (has exclusive declaration)
	highDir := filepath.Join(skillsDir, "high-conf")
	if err := os.MkdirAll(highDir, 0755); err != nil {
		t.Fatalf("create high-conf dir: %v", err)
	}

	highMd := `---
name: high-conf
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# high-conf

Secure.
`

	if err := os.WriteFile(filepath.Join(highDir, "SKILL.md"), []byte(highMd), 0644); err != nil {
		t.Fatalf("write high-conf SKILL.md: %v", err)
	}

	// Low-confidence skill (minimal content)
	lowDir := filepath.Join(skillsDir, "low-conf")
	if err := os.MkdirAll(lowDir, 0755); err != nil {
		t.Fatalf("create low-conf dir: %v", err)
	}

	lowMd := `---
name: low-conf
description: Generic skill with no keywords
---

# low-conf

Content here.
`

	if err := os.WriteFile(filepath.Join(lowDir, "SKILL.md"), []byte(lowMd), 0644); err != nil {
		t.Fatalf("write low-conf SKILL.md: %v", err)
	}

	args := []string{"--auto-ingest", "--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runScan(args, &stdout, &stderr, gf)

	if code != ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, ExitSuccess)
	}

	// Check that high-conf was ingested
	libraryPath := filepath.Join(home, "library", "high-conf")
	if _, err := os.Stat(libraryPath); err != nil {
		t.Errorf("high-conf not ingested: %v", err)
	}

	// Check that low-conf was NOT ingested
	lowLibraryPath := filepath.Join(home, "library", "low-conf")
	if _, err := os.Stat(lowLibraryPath); err == nil {
		t.Errorf("low-conf should not have been ingested")
	}
}

func TestScanAutoIngestReportsPerSkillProgress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	skillsDir := filepath.Join(home, ".claude", "skills")
	writeScanSkill(t, skillsDir, "high-conf", `---
name: high-conf
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# high-conf

Secure.
`)
	writeScanSkill(t, skillsDir, "low-conf", `---
name: low-conf
description: Generic skill with no keywords
---

# low-conf

Content.
`)
	writeScanSkill(t, skillsDir, "bad-name", `---
name: ../bad
description: Invalid skill name
---

# bad-name

Invalid.
`)

	args := []string{"--auto-ingest", "--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	code := runScan(args, &stdout, &stderr, globalFlags{})

	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, ExitSuccess, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"Evaluating high-conf (" + filepath.Join(skillsDir, "high-conf") + ")",
		"Ingested high-conf (" + filepath.Join(skillsDir, "high-conf") + ")",
		"Evaluating low-conf (" + filepath.Join(skillsDir, "low-conf") + ")",
		"Refused low-conf (" + filepath.Join(skillsDir, "low-conf") + "): auto-ingest refused",
		"Evaluating bad-name (" + filepath.Join(skillsDir, "bad-name") + ")",
		"Failed ../bad (" + filepath.Join(skillsDir, "bad-name") + "): invalid skill name",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestScanAutoIngestReportsAlreadyKnownSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	skillsDir := filepath.Join(home, ".claude", "skills")
	skillMd := `---
name: known
description: Known skill
---

# known

Known.
`
	skillDir := writeScanSkill(t, skillsDir, "known", skillMd)

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		t.Fatalf("ensureLibrary: %v", err)
	}
	libraryDir := filepath.Join(libraryPath, "known")
	if err := os.MkdirAll(libraryDir, 0755); err != nil {
		t.Fatalf("create library skill dir: %v", err)
	}
	librarySkillMd := filepath.Join(libraryDir, "SKILL.md")
	if err := os.WriteFile(librarySkillMd, []byte(skillMd), 0644); err != nil {
		t.Fatalf("write library SKILL.md: %v", err)
	}
	fp, size, err := fingerprintSkillMd(librarySkillMd)
	if err != nil {
		t.Fatalf("fingerprint library SKILL.md: %v", err)
	}
	if err := writeSkillMeta(filepath.Join(libraryDir, ".skill-meta.yaml"), skillMeta{
		Version: 1,
		Fingerprint: skillFingerprint{
			SHA256: fp,
			Size:   size,
		},
	}); err != nil {
		t.Fatalf("write skill meta: %v", err)
	}

	args := []string{"--auto-ingest", "--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	code := runScan(args, &stdout, &stderr, globalFlags{})

	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, ExitSuccess, stderr.String())
	}
	want := "Already known: known (" + skillDir + ") - in library"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("expected already-known progress %q, got:\n%s", want, stdout.String())
	}
}

func TestScanAutoIngestPreflightGroupsMissingDependencies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	t.Setenv("PATH", t.TempDir())

	skillsDir := filepath.Join(home, ".claude", "skills")
	writeScanSkill(t, skillsDir, "needs-model", `---
name: needs-model
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# needs-model

Requires tool use.
`)
	writeScanSkill(t, skillsDir, "needs-mcp", `---
name: needs-mcp
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# needs-mcp

Call mcp__linear__ to update Linear.
`)
	writeScanSkill(t, skillsDir, "needs-tool", `---
name: needs-tool
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# needs-tool

Run ffmpeg before finishing.
`)

	args := []string{"--auto-ingest", "--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	code := runScan(args, &stdout, &stderr, globalFlags{})

	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, ExitSuccess, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"Auto-ingest preflight:",
		"Discovered candidates: 3",
		"Eligible for auto-ingest: 0",
		"Blocked by missing dependencies: 3",
		"model=tool_use: needs-model (" + filepath.Join(skillsDir, "needs-model") + ")",
		"mcp_servers=linear: needs-mcp (" + filepath.Join(skillsDir, "needs-mcp") + ")",
		"tools=ffmpeg: needs-tool (" + filepath.Join(skillsDir, "needs-tool") + ")",
		"Options:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestScanAutoIngestPreflightContinuesWithEligibleOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	skillsDir := filepath.Join(home, ".claude", "skills")
	writeScanSkill(t, skillsDir, "high-conf", `---
name: high-conf
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# high-conf

Secure.
`)
	writeScanSkill(t, skillsDir, "needs-model", `---
name: needs-model
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# needs-model

Requires tool use.
`)

	args := []string{"--auto-ingest", "--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	code := runScan(args, &stdout, &stderr, globalFlags{})

	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, ExitSuccess, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "library", "high-conf")); err != nil {
		t.Fatalf("eligible skill was not ingested: %v\nstdout:\n%s", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, "library", "needs-model")); err == nil {
		t.Fatalf("dependency-blocked skill should not be ingested\nstdout:\n%s", stdout.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"Eligible for auto-ingest: 1",
		"Blocked by missing dependencies: 1",
		"Blocked needs-model (" + filepath.Join(skillsDir, "needs-model") + "): missing required dependencies (model=tool_use)",
		"Evaluating high-conf (" + filepath.Join(skillsDir, "high-conf") + ")",
		"Ingested high-conf (" + filepath.Join(skillsDir, "high-conf") + ")",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Evaluating needs-model") {
		t.Fatalf("blocked candidate should not be evaluated after preflight:\n%s", output)
	}
}

func TestScanAutoIngestPreflightAllBlockedDoesNotMutate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	skillsDir := filepath.Join(home, ".claude", "skills")
	writeScanSkill(t, skillsDir, "needs-model", `---
name: needs-model
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# needs-model

Requires tool use.
`)

	args := []string{"--auto-ingest", "--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	code := runScan(args, &stdout, &stderr, globalFlags{})

	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, ExitSuccess, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "library", "needs-model")); err == nil {
		t.Fatalf("dependency-blocked skill should not be ingested")
	}

	output := stdout.String()
	for _, want := range []string{
		"Eligible for auto-ingest: 0",
		"Blocked by missing dependencies: 1",
		"No eligible candidates remain after dependency preflight.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Evaluating needs-model") {
		t.Fatalf("blocked candidate should not be evaluated after preflight:\n%s", output)
	}
}

func TestScanAutoIngestJSONStdoutClean(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	skillsDir := filepath.Join(home, ".claude", "skills")
	writeScanSkill(t, skillsDir, "high-conf", `---
name: high-conf
description: Use this skill when reviewing pull requests for security
exclusive: claude
---

# high-conf

Secure.
`)

	args := []string{"--json", "scan", "--auto-ingest", "--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)

	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr=%s", code, ExitSuccess, stderr.String())
	}

	var results []scanResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("stdout is not clean JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	if len(results) != 1 || results[0].Name != "high-conf" {
		t.Fatalf("unexpected JSON results: %+v", results)
	}
}

func TestScanIngestOutcomeClassifiesProviderFailures(t *testing.T) {
	for _, reason := range []string{
		"provider ingest failed: exit status 1",
		"parse JSON from provider output: invalid character",
		"categories: at least 1 required",
		"invalid category \"Other\" (must be one of the 10 official categories)",
		"compatibility.mode must be portable|compatible|exclusive (got \"\")",
	} {
		result := ingestResult{
			Skipped: true,
			Reason:  reason,
		}
		if got := scanIngestOutcome(result); got != "Failed" {
			t.Fatalf("scanIngestOutcome(%q) = %q, want Failed", reason, got)
		}
	}
}

func TestScanIngestOutcomeKeepsIntentionalSkips(t *testing.T) {
	for _, reason := range []string{
		"duplicate fingerprint",
		"declined",
		"declined (no input)",
		"edit requested",
		"ingest requires confirmation; rerun with --auto (high-confidence cases) or --yes (accept suggestions)",
	} {
		result := ingestResult{
			Skipped: true,
			Reason:  reason,
		}
		if got := scanIngestOutcome(result); got != "Skipped" {
			t.Fatalf("scanIngestOutcome(%q) = %q, want Skipped", reason, got)
		}
	}
}

func TestScanMatchesByFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Set up library with a skill named "foo" whose SKILL.md has a specific fingerprint
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		t.Fatalf("ensureLibrary: %v", err)
	}

	skillMdContent := `---
name: foo
description: A skill in the library
---

# foo

Library content.
`

	// Pre-populate library with skill named "foo"
	fooLibraryDir := filepath.Join(libraryPath, "foo")
	if err := os.MkdirAll(fooLibraryDir, 0755); err != nil {
		t.Fatalf("create foo library dir: %v", err)
	}

	fooMdPath := filepath.Join(fooLibraryDir, "SKILL.md")
	if err := os.WriteFile(fooMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write foo SKILL.md: %v", err)
	}

	// Record the fingerprint
	fp, size, _ := fingerprintSkillMd(fooMdPath)
	meta := skillMeta{
		Version: 1,
		Fingerprint: skillFingerprint{
			SHA256: fp,
			Size:   size,
		},
	}
	_ = writeSkillMeta(filepath.Join(fooLibraryDir, ".skill-meta.yaml"), meta)

	// Now create a scanned skill directory "bar" with the SAME content/fingerprint
	// This tests that scan identifies it as "in library" by fingerprint, not by name
	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	barDir := filepath.Join(skillsDir, "bar")
	if err := os.MkdirAll(barDir, 0755); err != nil {
		t.Fatalf("create bar dir: %v", err)
	}

	barMdPath := filepath.Join(barDir, "SKILL.md")
	if err := os.WriteFile(barMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write bar SKILL.md: %v", err)
	}

	args := []string{"--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runScan(args, &stdout, &stderr, gf)

	if code != ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, ExitSuccess)
	}

	output := stdout.String()
	// bar directory should match by fingerprint and report "in library", not "unregistered"
	if !strings.Contains(output, "bar") {
		t.Errorf("expected 'bar' in output: %s", output)
	}
	if !strings.Contains(output, "in library") {
		t.Errorf("expected 'in library' status for bar (matched by fingerprint), got: %s", output)
	}
}

func writeScanSkill(t *testing.T, skillsDir, name, skillMd string) string {
	t.Helper()

	skillDir := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("create %s dir: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMd), 0644); err != nil {
		t.Fatalf("write %s SKILL.md: %v", name, err)
	}
	return skillDir
}

func TestScanJSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	fooDir := filepath.Join(skillsDir, "foo")
	if err := os.MkdirAll(fooDir, 0755); err != nil {
		t.Fatalf("create foo dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(fooDir, "SKILL.md"), []byte(`---
name: foo
description: Test
---

# foo

Test.
`), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	args := []string{"--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: true}

	code := runScan(args, &stdout, &stderr, gf)

	if code != ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, ExitSuccess)
	}

	var results []scanResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("expected results, got none")
	}

	found := false
	for _, r := range results {
		if r.Name == "foo" {
			found = true
			if r.Status != "unregistered" {
				t.Errorf("status = %q, want unregistered", r.Status)
			}
			break
		}
	}

	if !found {
		t.Errorf("foo not found in results")
	}
}

func TestScanIngestWithQuietNoPrompts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create library directory
	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("create library dir: %v", err)
	}

	// Create catalog
	if err := os.WriteFile(filepath.Join(libraryPath, "catalog.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	// Create skills directory with an unregistered skill
	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	fooDir := filepath.Join(skillsDir, "foo")
	if err := os.MkdirAll(fooDir, 0755); err != nil {
		t.Fatalf("create foo dir: %v", err)
	}

	skillMd := `---
name: foo
description: A test skill
---

# foo

Test.
`

	if err := os.WriteFile(filepath.Join(fooDir, "SKILL.md"), []byte(skillMd), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Test that --ingest with --quiet doesn't prompt and completes successfully
	args := []string{"--ingest", "--quiet", "--paths=" + skillsDir}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{Quiet: true}

	code := runScan(args, &stdout, &stderr, gf)

	// Should succeed (or at least not error on usage)
	if code == ExitUsageError {
		t.Errorf("--ingest --quiet should not return ExitUsageError, got %d", code)
	}

	// Verify no prompts were output (stdout/stderr should be empty from quiet mode)
	// The key is that --quiet suppresses output AND non-interactive mode shouldn't prompt
	output := stdout.String()
	if strings.Contains(output, "Ingest") || strings.Contains(output, "Y/n") {
		t.Errorf("should not prompt in quiet mode, got output: %s", output)
	}
}
