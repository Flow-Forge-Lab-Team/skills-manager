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
	code = Run([]string{"install", "--project", project, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dry-run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "preserve .claude/skills/review (manager-owned copy has local edits)") {
		t.Fatalf("stdout = %q, want local edit preserve preview", stdout.String())
	}
	if strings.Contains(stdout.String(), "copy review -> .claude/skills/review") {
		t.Fatalf("stdout = %q, should not preview copy over local edits", stdout.String())
	}

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

func TestInstallPersistsManifestBeforeReturningAfterPartialFailure(t *testing.T) {
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
  - name: alpha
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
  - name: missing-source
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "alpha\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("Run returned %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatalf("expected first skill to be copied before later failure: %v", err)
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
	if got, want := manifest.ManagedPaths, []string{".claude/skills/alpha"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("managed paths = %#v, want %#v", got, want)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"uninstall", "--project", project, "--confirm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("uninstall returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("expected uninstall to remove partially installed copy, got err %v", err)
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

func TestSyncDryRunPreservesLocallyEditedStaleInstalls(t *testing.T) {
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
	writeFile(t, filepath.Join(project, ".codex", "skills", "review", "local.md"), "local edit\n")
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
	code = Run([]string{"sync", "--project", project, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("sync dry-run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "preserve stale .codex/skills/review") {
		t.Fatalf("stdout = %q, want stale local edit preserve preview", stdout.String())
	}
	if strings.Contains(stdout.String(), "remove stale .codex/skills/review") {
		t.Fatalf("stdout = %q, should not preview removal for locally edited stale install", stdout.String())
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

func TestPartialUninstallRemovesFingerprintsForDeletedPaths(t *testing.T) {
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
  - name: alpha
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
  - name: beta
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "alpha\n")
	writeFile(t, filepath.Join(home, "library", "beta", "SKILL.md"), "beta\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	writeFile(t, filepath.Join(project, ".claude", "skills", "beta", "local.md"), "local edit\n")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"uninstall", "--project", project, "--confirm"}, &stdout, &stderr)
	if code != 4 {
		t.Fatalf("uninstall returned %d, want 4\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
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
	if _, ok := manifest.Files[".claude/skills/alpha"]; ok {
		t.Fatalf("removed alpha fingerprint still recorded: %#v", manifest.Files)
	}
	if _, ok := manifest.Files[".claude/skills/beta"]; !ok {
		t.Fatalf("preserved beta fingerprint missing: %#v", manifest.Files)
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
	if !strings.Contains(stdout.String(), "warning, installing despite missing required tools") {
		t.Fatalf("stdout = %q, want missing requirement override warning", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "deploy", "SKILL.md")); err != nil {
		t.Fatalf("expected override to install copy: %v", err)
	}
}

func TestInstallRejectsSkillNamesThatEscapeHarnessRoots(t *testing.T) {
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
  - name: ../owned
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("Run returned %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid skill name") {
		t.Fatalf("stderr = %q, want invalid skill name message", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "owned")); !os.IsNotExist(err) {
		t.Fatalf("expected no escaped install target, got err %v", err)
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

func TestInstallWritesLockFile(t *testing.T) {
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
  - name: alpha
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
  - name: beta
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "alpha\n")
	writeFile(t, filepath.Join(home, "library", "beta", "SKILL.md"), "beta\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lockPath := filepath.Join(project, ".skills", "installed.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}

	lockContent := string(data)
	if !strings.Contains(lockContent, "version: 1") {
		t.Fatalf("lock missing version: %s", lockContent)
	}
	if !strings.Contains(lockContent, "- name: alpha") {
		t.Fatalf("lock missing alpha skill: %s", lockContent)
	}
	if !strings.Contains(lockContent, "- name: beta") {
		t.Fatalf("lock missing beta skill: %s", lockContent)
	}
	if !strings.Contains(lockContent, "fingerprint:") {
		t.Fatalf("lock missing fingerprint field: %s", lockContent)
	}
	if !strings.Contains(lockContent, "harnesses:") {
		t.Fatalf("lock missing harnesses field: %s", lockContent)
	}
	if !strings.Contains(lockContent, "installed_at:") {
		t.Fatalf("lock missing installed_at field: %s", lockContent)
	}
}

func TestInstallReproducesFromLock(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create project that would NOT match review skill via categories
	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Security]
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
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "review\n")

	// Pre-write a lock file that includes the review skill
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0-dev"
skills:
  - name: review
    version: ~
    commit: ~
    fingerprint: "abc123"
    installed_at: "2026-01-01T00:00:00Z"
    harnesses:
      - claude
`
	writeFile(t, filepath.Join(project, ".skills", "installed.lock"), lockContent)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// Should have installed review despite category mismatch (due to lock)
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("expected locked skill review to be installed: %v", err)
	}
	if !strings.Contains(stdout.String(), "review: locked skill") {
		t.Fatalf("stdout = %q, want locked skill reason", stdout.String())
	}
}

func TestInstallFailsOnMissingLockedSkill(t *testing.T) {
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
  - name: available
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "available", "SKILL.md"), "available\n")

	// Pre-write a lock file that references a missing skill
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0-dev"
skills:
  - name: missing-skill
    version: ~
    commit: ~
    fingerprint: "abc123"
    installed_at: "2026-01-01T00:00:00Z"
    harnesses:
      - claude
`
	writeFile(t, filepath.Join(project, ".skills", "installed.lock"), lockContent)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)

	if code != 3 {
		t.Fatalf("Run returned %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "missing from the library") {
		t.Fatalf("stderr = %q, want missing skill message", stderr.String())
	}
	if !strings.Contains(stderr.String(), "sync-library") {
		t.Fatalf("stderr = %q, want sync-library suggestion", stderr.String())
	}
	if !strings.Contains(stderr.String(), "skip-missing-locked") {
		t.Fatalf("stderr = %q, want skip-missing-locked suggestion", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "available")); !os.IsNotExist(err) {
		t.Fatalf("expected no skills installed on missing locked skill error")
	}
}

func TestInstallSkipMissingLockedExits4(t *testing.T) {
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
  - name: available
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "available", "SKILL.md"), "available\n")

	// Pre-write a lock file that references a missing skill
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0-dev"
skills:
  - name: missing-skill
    version: ~
    commit: ~
    fingerprint: "abc123"
    installed_at: "2026-01-01T00:00:00Z"
    harnesses:
      - claude
  - name: available
    version: ~
    commit: ~
    fingerprint: "def456"
    installed_at: "2026-01-01T00:00:00Z"
    harnesses:
      - claude
`
	writeFile(t, filepath.Join(project, ".skills", "installed.lock"), lockContent)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project, "--skip-missing-locked"}, &stdout, &stderr)

	if code != 4 {
		t.Fatalf("Run returned %d, want 4 (partial)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// available skill should be installed
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "available", "SKILL.md")); err != nil {
		t.Fatalf("expected available skill to be installed: %v", err)
	}

	// lock file should still contain the missing skill entry
	lockPath := filepath.Join(project, ".skills", "installed.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	lockContent = string(data)
	if !strings.Contains(lockContent, "missing-skill") {
		t.Fatalf("missing-skill should remain in lock even when skipped: %s", lockContent)
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
