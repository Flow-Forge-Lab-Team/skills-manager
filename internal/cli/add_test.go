package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAddRoutesByPrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"./foo", "local", false},
		{"../foo", "local", false},
		{"/foo/bar", "local", false},
		{"~/foo", "local", false},
		{"github.com/owner/repo", "github", false},
		{"https://github.com/owner/repo", "github", false},
		{"http://github.com/owner/repo", "github", false},
		{"acme/skill", "marketplace", false},
		{"my-market/my-skill", "marketplace", false},
		{"invalid:stuff", "", true},
		{"foo.bar/baz", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			kind, err := classifyAddArgument(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if kind != tt.expected {
				t.Errorf("kind = %q, want %q", kind, tt.expected)
			}
		})
	}
}

func TestAddLocalHappyPath(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()

	skillMdContent := `---
name: local-test-skill
description: A local skill for testing
---

# local-test-skill

Local skill content.
`

	if err := os.WriteFile(filepath.Join(sourceDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	t.Setenv("SKILLS_MANAGER_HOME", home)

	args := []string{sourceDir, "--yes"}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runAdd(args, &stdout, &stderr, gf)

	if code != ExitSuccess {
		t.Errorf("exit code = %d, want %d", code, ExitSuccess)
	}

	libraryPath := filepath.Join(home, "library", "local-test-skill")
	if _, err := os.Stat(libraryPath); err != nil {
		t.Errorf("library path not created: %v", err)
	}
}

func TestAddMarketplaceMissingErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	args := []string{"nonexistent/skill", "--yes"}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runAdd(args, &stdout, &stderr, gf)

	if code == ExitSuccess {
		t.Errorf("expected failure, got success")
	}

	stderrStr := stderr.String()
	if len(stderrStr) == 0 {
		t.Errorf("expected error message in stderr")
	}
}
