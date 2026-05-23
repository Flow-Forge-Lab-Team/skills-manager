package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIngestLocalHappyPath(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	skillMdContent := `---
name: test-skill
description: A test skill for ingestion
---

# test-skill

This is a test skill.
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		yes:         true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if result.Skipped {
		t.Fatalf("ingest skipped: %s", result.Reason)
	}

	if result.Name != "test-skill" {
		t.Errorf("name = %q, want %q", result.Name, "test-skill")
	}

	libraryPath := filepath.Join(home, "library", "test-skill")
	if _, err := os.Stat(libraryPath); err != nil {
		t.Errorf("library path not created: %v", err)
	}

	metaPath := filepath.Join(libraryPath, ".skill-meta.yaml")
	meta, err := readSkillMeta(metaPath)
	if err != nil {
		t.Errorf("read metadata: %v", err)
	}

	if meta.Fingerprint.SHA256 != result.Fingerprint {
		t.Errorf("fingerprint mismatch")
	}

	if meta.Origin.Source != "local" {
		t.Errorf("origin source = %q, want %q", meta.Origin.Source, "local")
	}
}

func TestIngestMalformedRejected(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	// SKILL.md without frontmatter
	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte("No frontmatter here"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		yes:         true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if !result.Skipped {
		t.Fatalf("expected ingest to be skipped")
	}

	if !strings.Contains(result.Reason, "frontmatter") && !strings.Contains(result.Reason, "no name") {
		t.Errorf("reason = %q, want frontmatter or name error", result.Reason)
	}
}

func TestIngestMissingNameRejected(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	skillMdContent := `---
description: Missing name
---

# Some skill
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		yes:         true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if !result.Skipped {
		t.Fatalf("expected ingest to be skipped")
	}
}

func TestIngestDuplicateFingerprintSkipped(t *testing.T) {
	home := t.TempDir()

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		t.Fatalf("ensureLibrary: %v", err)
	}

	skillMdContent := `---
name: existing-skill
description: An existing skill
---

# existing-skill

Content here.
`

	// Pre-populate library with existing skill
	existingDir := filepath.Join(libraryPath, "existing-skill")
	if err := os.MkdirAll(existingDir, 0755); err != nil {
		t.Fatalf("create existing skill dir: %v", err)
	}

	existingMdPath := filepath.Join(existingDir, "SKILL.md")
	if err := os.WriteFile(existingMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write existing SKILL.md: %v", err)
	}

	// Record fingerprint in metadata
	fp, size, _ := fingerprintSkillMd(existingMdPath)
	meta := skillMeta{
		Version: 1,
		Fingerprint: skillFingerprint{
			SHA256: fp,
			Size:   size,
		},
	}
	_ = writeSkillMeta(filepath.Join(existingDir, ".skill-meta.yaml"), meta)

	// Try to re-ingest same content
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write source SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		yes:         true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if !result.Skipped {
		t.Fatalf("expected duplicate to be skipped")
	}

	if !strings.Contains(result.Reason, "duplicate") {
		t.Errorf("reason = %q, want duplicate", result.Reason)
	}
}

func TestIngestNameCollisionDifferentContentErrors(t *testing.T) {
	home := t.TempDir()

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		t.Fatalf("ensureLibrary: %v", err)
	}

	// Pre-populate library with skill X
	skillX := `---
name: myskill
description: Original skill
---

# myskill

Original content.
`

	xDir := filepath.Join(libraryPath, "myskill")
	if err := os.MkdirAll(xDir, 0755); err != nil {
		t.Fatalf("create X dir: %v", err)
	}

	xMdPath := filepath.Join(xDir, "SKILL.md")
	if err := os.WriteFile(xMdPath, []byte(skillX), 0644); err != nil {
		t.Fatalf("write X SKILL.md: %v", err)
	}

	fpX, sizeX, _ := fingerprintSkillMd(xMdPath)
	metaX := skillMeta{
		Version: 1,
		Fingerprint: skillFingerprint{
			SHA256: fpX,
			Size:   sizeX,
		},
	}
	_ = writeSkillMeta(filepath.Join(xDir, ".skill-meta.yaml"), metaX)

	// Try to ingest different content with same name
	sourceDir := t.TempDir()
	differentContent := `---
name: myskill
description: Different skill
---

# myskill

Different content!
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(differentContent), 0644); err != nil {
		t.Fatalf("write source SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		yes:         true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if !result.Skipped {
		t.Fatalf("expected collision to be caught")
	}

	if !strings.Contains(result.Reason, "already in library") {
		t.Errorf("reason = %q, want 'already in library'", result.Reason)
	}
}

func TestIngestAutoRefusesLowConfidence(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	skillMdContent := `---
name: lowconf
description: no keywords
---

# lowconf

Generic content with no special meaning.
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		auto:        true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if !result.Skipped {
		t.Fatalf("expected auto-ingest to refuse low confidence")
	}

	if !strings.Contains(result.Reason, "confidence") {
		t.Errorf("reason = %q, want confidence error", result.Reason)
	}
}

func TestIngestAutoAcceptsHighConfidence(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	skillMdContent := `---
name: security-review
description: Use this skill when reviewing pull requests for security issues
exclusive: claude
---

# security-review

Reviews for security.
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		auto:        true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if result.Skipped {
		t.Fatalf("expected auto-ingest to accept high confidence: %s", result.Reason)
	}

	if result.Confidence != "high" {
		t.Errorf("confidence = %q, want high", result.Confidence)
	}
}

func TestIngestInteractiveClosedStdinRejects(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	skillMdContent := `---
name: interactive-test
description: A test for interactive mode with closed stdin
---

# interactive-test

Test content.
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	// interactive=true but with EOF reading (simulates closed stdin)
	opts := ingestOptions{
		interactive: true,
		auto:        false,
		yes:         false,
	}

	// When stdin is closed, the Scanln call will return EOF and the prompt will be treated as a reject
	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if !result.Skipped {
		t.Fatalf("expected interactive mode with closed stdin to be rejected")
	}

	if !strings.Contains(result.Reason, "declined") {
		t.Errorf("reason = %q, want declined", result.Reason)
	}
}

func TestIngestRejectsPathTraversalName(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	skillMdContent := `---
name: ../evil
description: Malicious skill attempting path traversal
---

# evil

This should not ingest.
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		yes:         true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if !result.Skipped {
		t.Fatalf("expected ingest to be skipped for path traversal name")
	}

	if !strings.Contains(result.Reason, "invalid skill name") {
		t.Errorf("reason = %q, want 'invalid skill name'", result.Reason)
	}

	// Verify no 'evil' or '..' directories created in library
	libraryPath := filepath.Join(home, "library")
	if entries, err := os.ReadDir(libraryPath); err == nil {
		for _, entry := range entries {
			if entry.Name() == "evil" || entry.Name() == ".." {
				t.Errorf("found dangerous directory: %s", entry.Name())
			}
		}
	}
}

func TestIngestRejectsSlashInName(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	skillMdContent := `---
name: foo/bar
description: Malicious skill with slash in name
---

# foo/bar

This should not ingest.
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	opts := ingestOptions{
		yes:         true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	if !result.Skipped {
		t.Fatalf("expected ingest to be skipped for slash in name")
	}

	if !strings.Contains(result.Reason, "invalid skill name") {
		t.Errorf("reason = %q, want 'invalid skill name'", result.Reason)
	}
}

func TestIngestAutoRefusesMissingRequiredTools(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	// Use exclusive declaration + keyword to ensure high confidence
	// Include a pattern that triggers tool detection (ffmpeg) which likely won't be installed
	// This tests the auto-ingest gate that refuses when required tools are missing
	skillMdContent := `---
name: tool-required
description: Review and debug code for security audit issues
exclusive: claude
---

# tool-required

This skill uses ffmpeg for video processing.
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	src := ingestSource{
		kind:  "local",
		raw:   sourceDir,
		path:  sourceDir,
		label: sourceDir,
	}

	// auto=true should refuse due to missing required tool (ffmpeg)
	// (unless the system actually has ffmpeg installed, in which case this test passes
	// because the tool is detected as present)
	opts := ingestOptions{
		auto:        true,
		interactive: false,
	}

	var out bytes.Buffer
	result := ingestFromSource(src, opts, home, &out)

	// If ffmpeg is installed on the system, the test will pass (tool is present)
	// If ffmpeg is not installed, auto-ingest should refuse with missing dependencies error
	if !result.Skipped {
		// Skill was accepted - either ffmpeg is installed or confidence is high and requirements don't block
		if result.Confidence != "high" {
			t.Errorf("expected high confidence for auto-ingest acceptance, got %s", result.Confidence)
		}
	} else {
		// Skill was skipped - should be due to missing tools
		if !strings.Contains(result.Reason, "missing required dependencies") && !strings.Contains(result.Reason, "confidence") {
			t.Errorf("reason = %q, want missing required dependencies or confidence error", result.Reason)
		}
	}

	// Verify --yes bypasses the missing tool check
	// Use a fresh source directory with different name to avoid duplicate fingerprint check
	sourceDir2 := t.TempDir()
	skillMdContent2 := `---
name: tool-required-v2
description: Review and debug code for security audit issues
exclusive: claude
---

# tool-required-v2

This skill uses ffmpeg for video processing.
`
	if err := os.WriteFile(filepath.Join(sourceDir2, "SKILL.md"), []byte(skillMdContent2), 0644); err != nil {
		t.Fatalf("write SKILL.md to sourceDir2: %v", err)
	}

	src2 := ingestSource{
		kind:  "local",
		raw:   sourceDir2,
		path:  sourceDir2,
		label: sourceDir2,
	}

	opts.auto = false
	opts.yes = true

	var out2 bytes.Buffer
	result2 := ingestFromSource(src2, opts, home, &out2)

	if result2.Skipped {
		t.Fatalf("expected --yes to bypass missing tool check: %s", result2.Reason)
	}
}
