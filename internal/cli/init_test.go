package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectProjectDefaultsRealisticShapes(t *testing.T) {
	tests := []struct {
		name           string
		files          map[string]string
		wantCategories []string
		wantTags       []string
	}{
		{
			name: "next app",
			files: map[string]string{
				"package.json":         `{"dependencies":{"next":"15.0.0","react":"19.0.0","@supabase/supabase-js":"2.0.0","stripe":"16.0.0"},"devDependencies":{"@playwright/test":"1.0.0","tailwindcss":"3.0.0"}}`,
				"next.config.mjs":      "",
				"components.json":      "{}",
				"prisma/schema.prisma": "model User { id String @id }",
			},
			wantCategories: []string{"Engineering", "Quality", "Data", "Design"},
			wantTags:       []string{"nextjs", "nodejs", "playwright", "prisma", "react", "shadcn", "stripe", "supabase", "tailwind"},
		},
		{
			name: "python service",
			files: map[string]string{
				"pyproject.toml": "[project]\nname = \"svc\"\n",
				"Dockerfile":     "FROM python:3.12\n",
			},
			wantCategories: []string{"Engineering", "Operations"},
			wantTags:       []string{"python"},
		},
		{
			name: "go cli",
			files: map[string]string{
				"go.mod": "module example.com/tool\n",
			},
			wantCategories: []string{"Engineering"},
			wantTags:       []string{"go"},
		},
		{
			name: "rust infra",
			files: map[string]string{
				"Cargo.toml":               "[package]\nname = \"infra\"\n",
				".github/workflows/ci.yml": "name: ci\n",
			},
			wantCategories: []string{"Engineering", "Operations"},
			wantTags:       []string{"rust"},
		},
		{
			name: "agent tooling",
			files: map[string]string{
				".mcp.json":      "{}",
				"jest.config.ts": "export default {}\n",
				"composer.json":  "{}",
			},
			wantCategories: []string{"Engineering", "Quality", "Agent-tooling"},
			wantTags:       []string{"jest", "mcp", "php"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := t.TempDir()
			for rel, content := range tt.files {
				writeFile(t, filepath.Join(project, rel), content)
			}

			got := detectProjectDefaults(project)
			assertStringSlice(t, got.Categories, tt.wantCategories)
			assertStringSlice(t, got.Tags, tt.wantTags)
		})
	}
}

func TestRunInitWritesDetectedProjectConfigAndEmptyLock(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "package.json"), `{"dependencies":{"next":"15.0.0","react":"19.0.0","@sentry/nextjs":"8.0.0"},"devDependencies":{"vitest":"2.0.0"}}`)
	writeFile(t, filepath.Join(project, ".claude", "settings.json"), "{}")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"init", project, "--non-interactive"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("init returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	configPath := filepath.Join(project, ".skills", "project.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read project.yaml: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"name:",
		`- "Engineering"`,
		`- "Quality"`,
		`- "Design"`,
		`- "Operations"`,
		`- "nextjs"`,
		`- "nodejs"`,
		`- "react"`,
		`- "sentry"`,
		`- "vitest"`,
		`- "claude"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("project.yaml missing %q:\n%s", want, text)
		}
	}

	lock, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if lock.Version != 1 {
		t.Fatalf("lock version = %d, want 1", lock.Version)
	}
	if len(lock.Skills) != 0 {
		t.Fatalf("lock skills = %v, want empty", lock.Skills)
	}

	gitignore, err := os.ReadFile(filepath.Join(project, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), ".skills/skills/\n") {
		t.Fatalf(".gitignore missing .skills/skills/:\n%s", string(gitignore))
	}
}

func TestRunInitPreservesExistingGitignoreEntry(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, ".gitignore"), "dist\n.skills/skills/\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"init", project, "--no-detect", "--non-interactive"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("init returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	gitignore, err := os.ReadFile(filepath.Join(project, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if got, want := string(gitignore), "dist\n.skills/skills/\n"; got != want {
		t.Fatalf(".gitignore = %q, want %q", got, want)
	}
}

func TestRunInitRefusesExistingProjectConfigWithoutForce(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), "version: 1\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"init", project, "--non-interactive"}, &stdout, &stderr)
	if code != ExitUsageError {
		t.Fatalf("init returned %d, want %d", code, ExitUsageError)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("stderr = %q, want already exists", stderr.String())
	}
}

func TestRunInitRejectsMultiplePathsIncludingDot(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"init", ".", "other", "--non-interactive"}, &stdout, &stderr)
	if code != ExitUsageError {
		t.Fatalf("init returned %d, want %d", code, ExitUsageError)
	}
	if !strings.Contains(stderr.String(), "at most one path") {
		t.Fatalf("stderr = %q, want at most one path", stderr.String())
	}
}

func TestRunInitNoDetectSkipsHarnessDetection(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, ".claude", "settings.json"), "{}")
	writeFile(t, filepath.Join(project, "package.json"), `{"dependencies":{"next":"15.0.0"}}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"init", project, "--no-detect", "--non-interactive"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("init returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	data, err := os.ReadFile(filepath.Join(project, ".skills", "project.yaml"))
	if err != nil {
		t.Fatalf("read project.yaml: %v", err)
	}
	text := string(data)
	for _, want := range []string{"categories: []", "tags: []", "harnesses: []"} {
		if !strings.Contains(text, want) {
			t.Fatalf("project.yaml missing %q:\n%s", want, text)
		}
	}
	for _, notWant := range []string{"- Engineering", "- nextjs", "- claude"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("project.yaml contains detected value %q:\n%s", notWant, text)
		}
	}

	config, err := readProjectConfig(filepath.Join(project, ".skills", "project.yaml"))
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if len(config.Harnesses) != 0 {
		t.Fatalf("harnesses = %v, want explicit empty list", config.Harnesses)
	}
}

func TestRunInitRefusesExistingLockWithoutForce(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, ".skills", "installed.lock"), `version: 1
generated_at: "2026-05-23T00:00:00Z"
generated_by: "skills-manager 0.1.0"
skills:
  - name: stale
    fingerprint: "abc"
    installed_at: "2026-05-23T00:00:00Z"
    harnesses: [claude]
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"init", project, "--non-interactive"}, &stdout, &stderr)
	if code != ExitUsageError {
		t.Fatalf("init returned %d, want %d", code, ExitUsageError)
	}
	if !strings.Contains(stderr.String(), "installed.lock") || !strings.Contains(stderr.String(), "--force") {
		t.Fatalf("stderr = %q, want installed.lock force message", stderr.String())
	}
}

func TestWriteProjectConfigQuotesListValues(t *testing.T) {
	project := t.TempDir()
	configPath := filepath.Join(project, ".skills", "project.yaml")
	err := writeProjectConfig(configPath, projectConfig{
		Name:       "dotnet-app",
		Categories: []string{"Engineering"},
		Tags:       []string{"c#", "dotnet"},
		Harnesses:  []string{"codex"},
	}, mustParseTime(t, "2026-05-23T00:00:00Z"))
	if err != nil {
		t.Fatalf("write project config: %v", err)
	}

	config, err := readProjectConfig(configPath)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	assertStringSlice(t, config.Tags, []string{"c#", "dotnet"})
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return parsed
}

func assertStringSlice(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
