package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestAddAutoUsesConfiguredProviderForIngest(t *testing.T) {
	home := t.TempDir()
	sourceDir := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	skillMdContent := `---
name: provider-ingest-skill
description: Review Go tests with rg
---

# provider-ingest-skill

Use rg and go test to review code.
`
	writeFile(t, filepath.Join(sourceDir, "SKILL.md"), skillMdContent)
	writeFile(t, filepath.Join(home, "config.yaml"), "version: 1\nllm:\n  provider: \"anthropic\"\n  api_key_env: \"ANTHROPIC_API_KEY\"\n  model: \"claude-3-5-haiku-latest\"\n")

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("provider path = %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q", got)
		}
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 1 || !strings.Contains(req.Messages[0].Content, "# skills-ingest") || !strings.Contains(req.Messages[0].Content, "provider-ingest-skill") {
			t.Fatalf("provider prompt missing bundled ingest instructions or skill:\n%+v", req)
		}
		output := `{"categories":["Engineering"],"tags":["go","testing"],"compatibility":{"mode":"portable","harnesses":[],"harness":"","reason":"general coding skill"},"requirements":{"model":{"tool_use":"none","min_context_tokens":16000,"reasoning":"low","notes":"No tools required"},"tools":[],"mcp_servers":[]},"confidence":{"categories":"high","tags":"high","compatibility":"high","requirements":"high"},"notes":[]}`
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":` + strconvQuote(output) + `}],"usage":{"input_tokens":20,"output_tokens":10}}`))
	}))
	defer server.Close()
	t.Setenv("SKILLS_MANAGER_ANTHROPIC_BASE_URL", server.URL)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"add", sourceDir, "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if requests != 1 {
		t.Fatalf("provider requests = %d, want 1", requests)
	}
	meta := readFile(t, filepath.Join(home, "library", "provider-ingest-skill", ".skill-meta.yaml"))
	for _, want := range []string{"source: skills-ingest-provider", `- Engineering`, `- go`, `reasoning: low`, `notes: No tools required`} {
		if !strings.Contains(meta, want) {
			t.Fatalf("metadata missing %q:\n%s", want, meta)
		}
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

func TestAddTildeExpansion(t *testing.T) {
	// Create a temporary home directory
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create a fixture skill directory structure
	fixtureDir := filepath.Join(tmpHome, "fixture-skill")
	if err := os.MkdirAll(fixtureDir, 0755); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}

	skillMdContent := `---
name: tilde-test-skill
description: Test skill for tilde expansion
---

# tilde-test-skill

Tilde expansion test.
`

	if err := os.WriteFile(filepath.Join(fixtureDir, "SKILL.md"), []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Set up manager home
	managerHome := filepath.Join(tmpHome, ".skills-manager")
	if err := os.MkdirAll(managerHome, 0755); err != nil {
		t.Fatalf("create manager home: %v", err)
	}
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)

	// Use ~/fixture-skill path
	args := []string{"~/fixture-skill", "--yes"}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runAdd(args, &stdout, &stderr, gf)

	if code != ExitSuccess {
		t.Errorf("exit code = %d, want %d (stderr: %s)", code, ExitSuccess, stderr.String())
	}

	// Verify the skill was ingested to the library
	libraryPath := filepath.Join(managerHome, "library", "tilde-test-skill")
	if _, err := os.Stat(libraryPath); err != nil {
		t.Errorf("library path not created: %v", err)
	}
}

func TestExpandTilde(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	tests := []struct {
		input   string
		want    string
		wantErr bool
		name    string
	}{
		{"~/foo", filepath.Join(tmpHome, "foo"), false, "tilde slash"},
		{"~", tmpHome, false, "bare tilde"},
		{"/absolute/path", "/absolute/path", false, "absolute path"},
		{"relative/path", "relative/path", false, "relative path"},
		{"~user/foo", "", true, "user expansion not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandTilde(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateCommitSHA(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
		name    string
	}{
		// Valid SHA (40 hex chars)
		{"abcdef0123456789abcdef0123456789abcdef01", "abcdef012345", false, "full 40-char SHA"},
		// Valid shorter SHA (12+ hex chars)
		{"abcdef012345", "abcdef012345", false, "12-char SHA"},
		{"abcdef0123456789", "abcdef012345", false, "16-char SHA"},
		// Invalid: too short (< 12)
		{"abcdef01234", "", true, "too short"},
		// Invalid: "HEAD" or similar
		{"HEAD", "", true, "HEAD literal"},
		// Invalid: non-hex chars
		{"abcdef012345zzzzzzzzzzzzzzzzzzzzzzzzzzzz", "", true, "non-hex chars"},
		// Invalid: empty
		{"", "", true, "empty string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := truncateCommitSHA(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeGitHubURL(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		// Valid forms
		{"github.com/owner/repo", "https://github.com/owner/repo", false},
		{"github.com/owner/repo.git", "https://github.com/owner/repo", false},
		{"github.com/owner/repo/", "https://github.com/owner/repo", false},
		{"https://github.com/owner/repo", "https://github.com/owner/repo", false},
		{"http://github.com/owner/repo", "https://github.com/owner/repo", false},

		// Subdirectory forms (should error)
		{"github.com/owner/repo/subdir/path", "", true},
		{"https://github.com/owner/repo/subdir", "", true},

		// Invalid forms
		{"github.com/onlyowner", "", true},
		{"invalid.com/owner/repo", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := normalizeGitHubURL(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyAddArgumentTildeUser(t *testing.T) {
	// Test that ~user/skill is classified as "local" (not "marketplace")
	kind, err := classifyAddArgument("~alice/skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind != "local" {
		t.Errorf("~alice/skill classification = %q, want %q", kind, "local")
	}

	// Also test bare ~user (no slash)
	kind, err = classifyAddArgument("~bob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind != "local" {
		t.Errorf("~bob classification = %q, want %q", kind, "local")
	}
}

func TestAddTildeUserExpansionError(t *testing.T) {
	// Test that runAdd with ~user/ path returns the proper expandTilde error
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	args := []string{"~alice/skill", "--yes"}
	var stdout, stderr bytes.Buffer
	gf := globalFlags{JSON: false}

	code := runAdd(args, &stdout, &stderr, gf)

	if code == ExitSuccess {
		t.Errorf("expected failure for ~user/ path, got success")
	}

	stderrStr := stderr.String()
	if len(stderrStr) == 0 {
		t.Errorf("expected error message in stderr")
	}

	// Verify it's the expandTilde error, not a marketplace error
	if !stringContains(stderrStr, "~user expansion not supported") {
		t.Errorf("stderr should contain '~user expansion not supported', got: %s", stderrStr)
	}
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
