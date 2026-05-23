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
