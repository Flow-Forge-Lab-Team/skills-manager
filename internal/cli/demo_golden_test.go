package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadmeDemoTranscriptMatchesGolden(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".codex", "config.json"), "{}\n")
	writeFile(t, filepath.Join(project, "package.json"), "{\"dependencies\":{\"next\":\"15.0.0\",\"react\":\"19.0.0\"}}\n")
	for _, dir := range []string{".agents", ".claude", ".grok", ".hermes", ".openclaw"} {
		if err := os.MkdirAll(filepath.Join(project, dir), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	root := repoRoot(t)
	commands := []demoCommand{
		{
			Display: "skills-manager add ./examples/hello-skill --yes",
			Args:    []string{"add", filepath.Join(root, "examples", "hello-skill"), "--yes"},
		},
		{
			Display: "skills-manager add ./examples/python-skill --yes",
			Args:    []string{"add", filepath.Join(root, "examples", "python-skill"), "--yes"},
		},
		{
			Display: "skills-manager init ./demo-project --non-interactive",
			Args:    []string{"init", project, "--non-interactive"},
		},
		{
			Display: "skills-manager match --project ./demo-project --explain",
			Args:    []string{"match", "--project", project, "--explain"},
		},
		{
			Display: "skills-manager install --project ./demo-project",
			Args:    []string{"install", "--project", project},
		},
	}

	var transcript strings.Builder
	for i, cmd := range commands {
		if i > 0 {
			transcript.WriteString("\n")
		}
		transcript.WriteString("$ ")
		transcript.WriteString(cmd.Display)
		transcript.WriteString("\n")

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := Run(cmd.Args, &stdout, &stderr); code != ExitSuccess {
			t.Fatalf("%s returned %d\nstdout:\n%s\nstderr:\n%s", cmd.Display, code, stdout.String(), stderr.String())
		}
		transcript.WriteString(normalizeDemoOutput(stdout.String(), home, project))
	}

	goldenPath := filepath.Join(root, "docs", "demo_transcript.txt")
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden transcript: %v", err)
	}
	if got, want := strings.TrimSpace(transcript.String()), strings.TrimSpace(string(golden)); got != want {
		t.Fatalf("demo transcript drifted\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if !strings.Contains(string(readme), strings.TrimSpace(string(golden))) {
		t.Fatalf("README.md does not contain docs/demo_transcript.txt")
	}
}

func TestMatchExplainRespectsAlwaysInclude(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: pinned-skill
    categories: [Data]
    tags: [python]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "pinned-skill", "SKILL.md"), "---\nname: pinned-skill\n---\n")
	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
tags: [go]
harnesses: [claude]
skills:
  always_include: [pinned-skill]
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"match", "--project", project, "--explain"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("match returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "pinned-skill (rejected)") {
		t.Fatalf("always_include skill was rejected:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "pinned-skill (score: 999) - always_include") {
		t.Fatalf("always_include skill did not appear as accepted:\n%s", stdout.String())
	}
}

type demoCommand struct {
	Display string
	Args    []string
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func normalizeDemoOutput(out, home, project string) string {
	out = strings.ReplaceAll(out, home, "$SKILLS_MANAGER_HOME")
	out = strings.ReplaceAll(out, project, "./demo-project")
	return out
}
