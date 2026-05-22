package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0", code)
	}
	if got, want := strings.TrimSpace(stdout.String()), "skills-manager "+Version; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestUnknownArgument(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"nope"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run returned %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown argument: nope") {
		t.Fatalf("stderr = %q, want unknown argument message", stderr.String())
	}
}

func TestInstallCopiesAcrossDistinctHarnessPathsAndUninstallReverses(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
tags: [go]
harnesses: [claude, codex, grok, antigravity, gemini, hermes, openclaw]
`)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: linear-feature
    categories: [Engineering]
    tags: [linear]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "linear-feature", "SKILL.md"), "---\nname: linear-feature\n---\n")
	writeFile(t, filepath.Join(home, "library", "linear-feature", "references", "note.md"), "note\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	for _, rel := range []string{
		".agents/skills/linear-feature/SKILL.md",
		".claude/skills/linear-feature/SKILL.md",
		".codex/skills/linear-feature/SKILL.md",
		".grok/skills/linear-feature/SKILL.md",
		"skills/linear-feature/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected copied skill at %s: %v", rel, err)
		}
	}

	manifestPath := filepath.Join(home, "manifests", projectSlug(project)+".json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest installManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if got, want := len(manifest.ManagedPaths), 5; got != want {
		t.Fatalf("managed paths = %d, want %d: %#v", got, want, manifest.ManagedPaths)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"uninstall", "--project", project, "--confirm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("uninstall returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "linear-feature")); !os.IsNotExist(err) {
		t.Fatalf("expected uninstall to remove manager-owned copy, got err %v", err)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("expected uninstall to remove manifest, got err %v", err)
	}
}

func TestInstallPreservesUnmanagedAndLocallyEditedTargets(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
harnesses: [claude]
`)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: review
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "first\n")
	writeFile(t, filepath.Join(project, ".claude", "skills", "review", "SKILL.md"), "user-owned\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assertFileContent(t, filepath.Join(project, ".claude", "skills", "review", "SKILL.md"), "user-owned\n")
	if !strings.Contains(stdout.String(), "preserve .claude/skills/review") {
		t.Fatalf("stdout = %q, want preserve message", stdout.String())
	}

	if err := os.RemoveAll(filepath.Join(project, ".claude", "skills", "review")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	writeFile(t, filepath.Join(project, ".claude", "skills", "review", "local.md"), "local edit\n")
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "second\n")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"sync", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("sync returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assertFileContent(t, filepath.Join(project, ".claude", "skills", "review", "SKILL.md"), "first\n")
	if !strings.Contains(stdout.String(), "local edits") {
		t.Fatalf("stdout = %q, want local edit preserve message", stdout.String())
	}
}

func TestSyncPrunesStaleManagedInstallsWhenCompatibilityNarrows(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
harnesses: [claude, codex]
`)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: review
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "review\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("expected initial codex copy: %v", err)
	}

	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: review
    categories: [Engineering]
    compatibility:
      mode: exclusive
      harness: claude
    requirements:
      tools: []
`)

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"sync", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("sync returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "review")); !os.IsNotExist(err) {
		t.Fatalf("expected stale codex copy to be removed, got err %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("expected compatible claude copy to remain: %v", err)
	}

	manifestPath := filepath.Join(home, "manifests", projectSlug(project)+".json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest installManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if got, want := manifest.ManagedPaths, []string{".claude/skills/review"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("managed paths = %#v, want %#v", got, want)
	}
	if _, ok := manifest.Files[".codex/skills/review"]; ok {
		t.Fatalf("stale codex fingerprint still recorded: %#v", manifest.Files)
	}
}

func TestUninstallPreservesLocallyEditedManagedTargets(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
harnesses: [claude]
`)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: review
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "first\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	writeFile(t, filepath.Join(project, ".claude", "skills", "review", "local.md"), "local edit\n")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"uninstall", "--project", project, "--confirm"}, &stdout, &stderr)
	if code != 4 {
		t.Fatalf("uninstall returned %d, want 4\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "review", "local.md")); err != nil {
		t.Fatalf("expected uninstall to preserve locally edited copy: %v", err)
	}
	if !strings.Contains(stdout.String(), "manager-owned copy has local edits") {
		t.Fatalf("stdout = %q, want local edit preserve message", stdout.String())
	}
}

func TestManifestPathSeparatesSameNamedProjects(t *testing.T) {
	home := t.TempDir()
	parentA := filepath.Join(t.TempDir(), "left")
	parentB := filepath.Join(t.TempDir(), "right")
	projectA := filepath.Join(parentA, "app")
	projectB := filepath.Join(parentB, "app")

	pathA := manifestPath(home, projectA)
	pathB := manifestPath(home, projectB)
	if pathA == pathB {
		t.Fatalf("manifestPath returned same path for distinct projects: %s", pathA)
	}
	if !strings.HasPrefix(filepath.Base(pathA), "app-") {
		t.Fatalf("manifest path %s should keep readable project basename", pathA)
	}
}

func TestInstallBlocksMissingRequiredToolUnlessOverridden(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
harnesses: [codex]
`)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: deploy
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools:
        - name: definitely-missing-skills-manager-tool
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "deploy", "SKILL.md"), "deploy\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("Run returned %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "deploy")); !os.IsNotExist(err) {
		t.Fatalf("expected missing requirement to block copy, got err %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"install", "--project", project, "--allow-missing-requirements"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "deploy", "SKILL.md")); err != nil {
		t.Fatalf("expected override to install copy: %v", err)
	}
}

func TestInstallDoesNotBlockForIncompatibleHarnessRequirements(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
harnesses: [codex]
`)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: claude-only
    categories: [Engineering]
    compatibility:
      mode: exclusive
      harness: claude
    requirements:
      tools:
        - name: definitely-missing-skills-manager-tool
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "claude-only", "SKILL.md"), "claude\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "no compatible active harnesses") {
		t.Fatalf("stdout = %q, want incompatible harness skip", stdout.String())
	}
}

func TestInstallBlocksInlineAndStringListToolRequirements(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
harnesses: [codex]
`)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: inline
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: [definitely-missing-inline-tool]
  - name: string-list
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools:
        - definitely-missing-list-tool
`)
	writeFile(t, filepath.Join(home, "library", "inline", "SKILL.md"), "inline\n")
	writeFile(t, filepath.Join(home, "library", "string-list", "SKILL.md"), "list\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("Run returned %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "definitely-missing-inline-tool") {
		t.Fatalf("stdout = %q, want inline missing tool", stdout.String())
	}
	if !strings.Contains(stdout.String(), "definitely-missing-list-tool") {
		t.Fatalf("stdout = %q, want list missing tool", stdout.String())
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}
