package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewValidatesName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid-skill", false},
		{"ValidSkill", false},
		{"123skill", false},
		{"Foo Bar", true}, // space not allowed
		{"..", true},      // too short
		{"a", true},       // too short
		{"ab", true},      // too short
		{"toolongskillnamethatexceedssixtyfourcharacterlimitxxxxxxxxxxxxxxx", true}, // >64
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := isValidSkillName(tt.name)
			if tt.wantErr && valid {
				t.Errorf("expected invalid, but was valid")
			}
			if !tt.wantErr && !valid {
				t.Errorf("expected valid, but was invalid")
			}
		})
	}
}

func TestNewRefusesStub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create a no-op editor script that does nothing
	tmpDir := t.TempDir()
	editorScript := filepath.Join(tmpDir, "editor.sh")
	editorContent := `#!/bin/sh
# Editor that does nothing - leaves the file as-is
exit 0
`

	if err := os.WriteFile(editorScript, []byte(editorContent), 0755); err != nil {
		t.Fatalf("write editor script: %v", err)
	}

	t.Setenv("EDITOR", editorScript)

	args := []string{"stub-test"}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runNew(args, &stdout, &stderr, gf)

	if code == ExitSuccess {
		t.Errorf("expected failure (stub unchanged), got success")
	}

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "description unchanged") {
		t.Errorf("expected 'description unchanged' in stderr, got: %s", stderrStr)
	}
}

func TestNewCreatesSkillWhenEdited(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create an editor script that modifies the file
	tmpDir := t.TempDir()
	editorScript := filepath.Join(tmpDir, "editor.sh")
	editorContent := `#!/bin/sh
file="$1"
tmp="$(mktemp)"
sed 's/TODO — one sentence about when to invoke this skill/A real description of this skill/' "$file" > "$tmp"
mv "$tmp" "$file"
exit 0
`

	if err := os.WriteFile(editorScript, []byte(editorContent), 0755); err != nil {
		t.Fatalf("write editor script: %v", err)
	}

	if err := os.Chmod(editorScript, 0755); err != nil {
		t.Fatalf("chmod editor script: %v", err)
	}

	t.Setenv("EDITOR", editorScript)

	args := []string{"new-skill"}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runNew(args, &stdout, &stderr, gf)

	if code != ExitSuccess {
		t.Errorf("exit code = %d, want %d, stderr: %s", code, ExitSuccess, stderr.String())
	}

	// Verify skill was ingested
	libraryPath := filepath.Join(home, "library", "new-skill")
	if _, err := os.Stat(libraryPath); err != nil {
		t.Errorf("skill not created in library: %v", err)
	}

	skillMdPath := filepath.Join(libraryPath, "SKILL.md")
	content, err := os.ReadFile(skillMdPath)
	if err != nil {
		t.Errorf("read created SKILL.md: %v", err)
	}

	if !strings.Contains(string(content), "A real description") {
		t.Errorf("edited content not found in SKILL.md")
	}
}

func TestNewDuplicateNameErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Pre-create a skill in the library
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		t.Fatalf("ensureLibrary: %v", err)
	}

	existingDir := filepath.Join(libraryPath, "existing")
	if err := os.MkdirAll(existingDir, 0755); err != nil {
		t.Fatalf("create existing dir: %v", err)
	}

	args := []string{"existing"}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runNew(args, &stdout, &stderr, gf)

	if code == ExitSuccess {
		t.Errorf("expected failure (skill already exists), got success")
	}

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "already in library") {
		t.Errorf("expected 'already in library' in stderr, got: %s", stderrStr)
	}
}

func TestNewEditorFailureAborts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create editor script that fails
	tmpDir := t.TempDir()
	editorScript := filepath.Join(tmpDir, "editor.sh")
	if err := os.WriteFile(editorScript, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write editor script: %v", err)
	}

	t.Setenv("EDITOR", editorScript)

	args := []string{"editor-fail"}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runNew(args, &stdout, &stderr, gf)

	if code == ExitSuccess {
		t.Errorf("expected failure (editor exit code 1), got success")
	}

	// Skill should NOT be in library
	libraryPath := filepath.Join(home, "library", "editor-fail")
	if _, err := os.Stat(libraryPath); err == nil {
		t.Errorf("skill should not exist after editor failure")
	}
}
