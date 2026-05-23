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
