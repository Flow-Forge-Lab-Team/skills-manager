package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
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
	if got, want := len(manifest.ManagedPaths), 6; got != want {
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
	// Clear the lock since we changed the library content; simulates library update scenario
	if err := os.Remove(filepath.Join(project, ".skills", "installed.lock")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

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
	// Note: after clearing the lock and syncing, local.md prevents the overwrite of SKILL.md
	// So the original "first\n" is preserved
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
	if got, want := manifest.ManagedPaths, []string{".claude/skills/review", ".skills/installed.lock"}; strings.Join(got, ",") != strings.Join(want, ",") {
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
	if !strings.Contains(stdout.String(), "warning, installing despite missing required") {
		t.Fatalf("stdout = %q, want missing requirement override warning", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "deploy", "SKILL.md")); err != nil {
		t.Fatalf("expected override to install copy: %v", err)
	}
}

func TestInstallBlocksOnMissingRequiredMCPServer(t *testing.T) {
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
  - name: linear-skill
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      mcp_servers:
        - name: linear
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "linear-skill", "SKILL.md"), "linear skill\n")

	// Without env var, should block
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("Run returned %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "blocked") || !strings.Contains(stdout.String(), "mcp_servers=linear") {
		t.Fatalf("stdout = %q, want 'blocked' and 'mcp_servers=linear'", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "linear-skill")); !os.IsNotExist(err) {
		t.Fatalf("expected missing MCP requirement to block copy, got err %v", err)
	}

	// With env var set, should install
	t.Setenv("SKILLS_MANAGER_MCP_LINEAR", "available")
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "linear-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected env var to allow install: %v", err)
	}
}

func TestInstallBlocksOnMissingRequiredModelCapability(t *testing.T) {
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
  - name: agent-skill
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      model:
        tool_use: required
`)
	writeFile(t, filepath.Join(home, "library", "agent-skill", "SKILL.md"), "agent skill\n")

	// Without env var, should block
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("Run returned %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "blocked") || !strings.Contains(stdout.String(), "model=tool_use") {
		t.Fatalf("stdout = %q, want 'blocked' and 'model=tool_use'", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "agent-skill")); !os.IsNotExist(err) {
		t.Fatalf("expected missing model requirement to block copy, got err %v", err)
	}

	// With env var set, should install
	t.Setenv("SKILLS_MANAGER_MODEL_TOOL_USE", "available")
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "agent-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected env var to allow install: %v", err)
	}
}

func TestInstallAllowMissingRequirementsBypassesMCP(t *testing.T) {
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
  - name: linear-skill
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      mcp_servers:
        - name: linear
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "linear-skill", "SKILL.md"), "linear skill\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project, "--allow-missing-requirements"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "warning") || !strings.Contains(stdout.String(), "mcp_servers=linear") {
		t.Fatalf("stdout = %q, want 'warning' and 'mcp_servers=linear'", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "linear-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected --allow-missing-requirements to bypass MCP block: %v", err)
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

	// Pre-write a lock file that includes the review skill (legacy lock with empty fingerprint)
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0"
skills:
  - name: review
    version: ~
    commit: ~
    fingerprint: ~
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
generated_by: "skills-manager 0.1.0"
skills:
  - name: missing-skill
    version: ~
    commit: ~
    fingerprint: ~
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
	if !strings.Contains(stderr.String(), "missing from library") {
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

	// Pre-write a lock file that references a missing skill and available skill
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0"
skills:
  - name: missing-skill
    version: ~
    commit: ~
    fingerprint: ~
    installed_at: "2026-01-01T00:00:00Z"
    harnesses:
      - claude
  - name: available
    version: ~
    commit: ~
    fingerprint: ~
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

func TestLockDrivenInstallHonorsLockedHarnesses(t *testing.T) {
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
  - name: test-skill
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "test-skill", "SKILL.md"), "skill\n")

	// Pre-write a lock with only claude harness
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0"
skills:
  - name: test-skill
    version: ~
    commit: ~
    fingerprint: ~
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

	// Should only exist in .claude/skills, NOT in .codex/skills
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "test-skill", "SKILL.md")); err != nil {
		t.Fatalf("expected skill in .claude/skills (locked harness): %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "test-skill")); !os.IsNotExist(err) {
		t.Fatalf("skill should NOT be in .codex/skills (not in locked harnesses), got err %v", err)
	}
}

func TestLockDrivenInstallSkipsWhenLockedHarnessesInactive(t *testing.T) {
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
  - name: test-skill
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "test-skill", "SKILL.md"), "skill\n")

	// Pre-write a lock with grok harness, which is not active in project.yaml
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0"
skills:
  - name: test-skill
    version: ~
    commit: ~
    fingerprint: ~
    installed_at: "2026-01-01T00:00:00Z"
    harnesses:
      - grok
`
	writeFile(t, filepath.Join(project, ".skills", "installed.lock"), lockContent)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)

	if code != 4 {
		t.Fatalf("Run returned %d, want 4 (partial)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// Should mention "no locked harnesses are active"
	if !strings.Contains(stdout.String(), "no locked harnesses are active in project.yaml") {
		t.Fatalf("stdout = %q, want 'no locked harnesses are active' message", stdout.String())
	}

	// Should not be installed anywhere
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "test-skill")); !os.IsNotExist(err) {
		t.Fatalf("skill should not be installed when locked harnesses inactive, got err %v", err)
	}

	// Lock entry should remain unchanged
	lockPath := filepath.Join(project, ".skills", "installed.lock")
	lock, err := readInstallLock(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if len(lock.Skills) != 1 || lock.Skills[0].Name != "test-skill" {
		t.Fatalf("lock entry should be preserved unchanged")
	}
}

func TestUninstallRemovesInstalledLock(t *testing.T) {
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
  - name: test-skill
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "test-skill", "SKILL.md"), "skill\n")

	// First install to create the lock
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lockPath := filepath.Join(project, ".skills", "installed.lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file to exist after install: %v", err)
	}

	// Now uninstall
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"uninstall", "--project", project, "--confirm"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("uninstall returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// Lock file should be gone
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("expected lock file to be removed by uninstall, got err %v", err)
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

func TestInstallFailsOnLockFingerprintDivergence(t *testing.T) {
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
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "actual content\n")

	// Pre-write a lock with a fingerprint that doesn't match the library
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0"
skills:
  - name: review
    version: ~
    commit: ~
    fingerprint: "0000000000000000000000000000000000000000000000000000000000000000"
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
	if !strings.Contains(stderr.String(), "review") {
		t.Fatalf("stderr should mention skill name review: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "lock=000000") {
		t.Fatalf("stderr should show lock fingerprint prefix: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "library=") {
		t.Fatalf("stderr should show library fingerprint prefix: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "review")); !os.IsNotExist(err) {
		t.Fatalf("expected no skill copied on fingerprint divergence")
	}

	// Now test with --skip-missing-locked
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"install", "--project", project, "--skip-missing-locked"}, &stdout, &stderr)

	if code != 4 {
		t.Fatalf("Run returned %d, want 4 (partial)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// Check that lock entry was preserved unchanged
	lockPath := filepath.Join(project, ".skills", "installed.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	newLockContent := string(data)
	if !strings.Contains(newLockContent, "review") {
		t.Fatalf("review should remain in lock when skipped: %s", newLockContent)
	}
}

func TestInstallPartialWhenAllLockedSkipped(t *testing.T) {
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

	// Pre-write a lock with one skill that's missing from library
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0"
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
	code := Run([]string{"install", "--project", project, "--skip-missing-locked"}, &stdout, &stderr)

	// Should exit 4 (partial) even though no other candidates exist
	if code != 4 {
		t.Fatalf("Run returned %d, want 4 (partial)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
}

func TestLockFingerprintReflectsLibrary(t *testing.T) {
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
  - name: skill-x
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "skill-x", "SKILL.md"), "skill content\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lockPath := filepath.Join(project, ".skills", "installed.lock")
	_, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}

	// Extract the fingerprint from the lock (it's quoted)
	var lock installLock
	lock, err = readInstallLock(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if len(lock.Skills) == 0 {
		t.Fatalf("lock has no skills")
	}

	lockFP := lock.Skills[0].Fingerprint
	if lockFP == "" {
		t.Fatalf("lock fingerprint is empty")
	}

	// Compute the expected library fingerprint
	libFP, err := fingerprintDir(filepath.Join(home, "library", "skill-x"))
	if err != nil {
		t.Fatalf("fingerprint library: %v", err)
	}

	if lockFP != libFP {
		t.Fatalf("lock fingerprint = %s, want library fingerprint %s", lockFP, libFP)
	}

	// Verify that reinstalling preserves the lock fingerprint (stability test)
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"install", "--project", project}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("reinstall returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	_, err = os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock after reinstall: %v", err)
	}
	lock, err = readInstallLock(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if len(lock.Skills) == 0 {
		t.Fatalf("lock has no skills after reinstall")
	}

	newLockFP := lock.Skills[0].Fingerprint
	if newLockFP != lockFP {
		t.Fatalf("lock fingerprint changed after reinstall: %s -> %s", lockFP, newLockFP)
	}
}

func TestSyncDropsStaleLockEntries(t *testing.T) {
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
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// Drop beta from the project's selection by removing it from the catalog match
	// (simulate the user editing project.yaml to drop the Engineering category for beta).
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: alpha
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
  - name: beta
    categories: [Quality]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lock, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	for _, s := range lock.Skills {
		if s.Name == "beta" {
			t.Fatalf("sync left stale beta in lock: %+v", lock.Skills)
		}
	}
	if len(lock.Skills) != 1 || lock.Skills[0].Name != "alpha" {
		t.Fatalf("expected only alpha in lock after sync, got %+v", lock.Skills)
	}
}

func TestLockOmitsLibraryFingerprintForLocallyEditedManagedCopy(t *testing.T) {
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

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("first install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// Locally edit the managed copy.
	writeFile(t, filepath.Join(project, ".claude", "skills", "review", "local.md"), "local edit\n")
	// Update the library so its fingerprint diverges from both old and current target.
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "second\n")
	// Sync should preserve the local-edited copy and NOT claim library content for it.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "local edits") {
		t.Fatalf("expected local-edits preserve message; stdout:\n%s", stdout.String())
	}

	// Get the current library fingerprint — the lock must NOT claim this for review,
	// because review's only target has local edits.
	libFP, err := fingerprintDir(filepath.Join(home, "library", "review"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	var reviewFP string
	for _, s := range lock.Skills {
		if s.Name == "review" {
			reviewFP = s.Fingerprint
		}
	}
	if reviewFP == libFP {
		t.Fatalf("lock falsely claims current library fingerprint for locally-edited review:\nlock=%s\nlibrary=%s", reviewFP, libFP)
	}
}

func TestUninstallPreservesLocallyEditedLockFile(t *testing.T) {
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

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lockPath := filepath.Join(project, ".skills", "installed.lock")
	writeFile(t, lockPath, "# manually edited by user\nversion: 1\nskills:\n  - name: review\n    fingerprint: \"deadbeef\"\n    installed_at: \"2026-01-01T00:00:00Z\"\n    harnesses:\n      - claude\n")

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"uninstall", "--project", project, "--confirm"}, &stdout, &stderr)
	if code != 4 {
		t.Fatalf("uninstall returned %d, want 4 (partial — preserved edited lock)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected uninstall to preserve the locally-edited lock file: %v", err)
	}
}

func TestBlockedInstallDoesNotAdvanceLockFingerprint(t *testing.T) {
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
  - name: needs-tool
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	writeFile(t, filepath.Join(home, "library", "needs-tool", "SKILL.md"), "v1\n")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("first install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	firstFP, err := fingerprintDir(filepath.Join(home, "library", "needs-tool"))
	if err != nil {
		t.Fatal(err)
	}

	// Update the library AND introduce a required-tool gate that fails on this machine.
	writeFile(t, filepath.Join(home, "library", "needs-tool", "SKILL.md"), "v2\n")
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: needs-tool
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools:
        - name: definitely-missing-tool-xyz
          required: true
`)

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("blocked install returned %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lock, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	var lockedFP string
	for _, s := range lock.Skills {
		if s.Name == "needs-tool" {
			lockedFP = s.Fingerprint
		}
	}
	if lockedFP != firstFP {
		t.Fatalf("blocked install advanced lock fingerprint:\nfirst-install lib fp: %s\nlock now claims: %s", firstFP, lockedFP)
	}
}

func TestIdempotentInstallDoesNotChurnLockTimestamps(t *testing.T) {
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
`)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "alpha\n")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("first install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	first, err := os.ReadFile(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatal(err)
	}

	// Sleep at least one second so a re-write would produce a different RFC3339 timestamp.
	time.Sleep(1100 * time.Millisecond)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("second install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	second, err := os.ReadFile(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("idempotent install changed lock contents.\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestLockedInstallPreservesEntryWhenTargetIsUnmanaged(t *testing.T) {
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
	writeFile(t, filepath.Join(home, "library", "review", "SKILL.md"), "lib content\n")

	// Compute the real library fingerprint so the lock's fingerprint check passes.
	libFP, err := fingerprintDir(filepath.Join(home, "library", "review"))
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}

	// Pre-existing committed copy at the target — looks like a checkout of
	// a repo where .claude/skills/review was committed previously.
	writeFile(t, filepath.Join(project, ".claude", "skills", "review", "SKILL.md"), "committed-by-teammate\n")

	// Lock that pins review.
	lockContent := `version: 1
generated_at: "2026-01-01T00:00:00Z"
generated_by: "skills-manager 0.1.0"
skills:
  - name: review
    version: ~
    commit: ~
    fingerprint: "` + libFP + `"
    installed_at: "2026-01-01T00:00:00Z"
    harnesses:
      - claude
`
	writeFile(t, filepath.Join(project, ".skills", "installed.lock"), lockContent)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lock, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if len(lock.Skills) != 1 || lock.Skills[0].Name != "review" {
		t.Fatalf("install dropped locked review entry when target was unmanaged: %+v", lock.Skills)
	}
}

func TestInstallBlocksObjectFormToolRequirements(t *testing.T) {
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
  - name: tool-needer
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools:
        - name: definitely-missing-tool-xyz
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "tool-needer", "SKILL.md"), "needs tool\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("Run returned %d, want 3 (blocked)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "blocked, missing required: tools=definitely-missing-tool-xyz") {
		t.Fatalf("expected blocked message; stdout:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "tool-needer")); !os.IsNotExist(err) {
		t.Fatalf("skill should not be installed when required tool missing")
	}
}

func TestInstallBlocksMissingCredentialAndRuntimeRequirements(t *testing.T) {
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
  - name: runtime-needer
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      scripts:
        required_runtimes:
          - definitely-missing-runtime-xyz
      credentials:
        - name: MISSING_TOKEN_FOR_TEST
          source: env
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "runtime-needer", "SKILL.md"), "needs runtime and credential\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("Run returned %d, want 3 (blocked)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "credentials=MISSING_TOKEN_FOR_TEST") {
		t.Fatalf("expected credential block; stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "runtimes=definitely-missing-runtime-xyz") {
		t.Fatalf("expected runtime block; stdout:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "runtime-needer")); !os.IsNotExist(err) {
		t.Fatalf("skill should not be installed when required runtime/credential missing")
	}
}

// --- FLO-232 redesign: lock-as-desired-set contract -------------------------
//
// The lock is the project's committed desired install set. Partial runs
// (--only, blocked, no-compatible-harness) MUST NOT silently shrink it.
//
// The five tests below pin the contract:
//   1. Bootstrap install (no lock, no --only): lock includes ALL desired
//      skills, even ones blocked on this machine.
//   2. After bootstrap, fixing the tool lets a plain install (now
//      lock-driven) install the previously-blocked skill from the lock.
//   3. Bootstrap with --only does NOT write a lock at all (surgical: this
//      is not authoritative for the project).
//   4. Lock-driven install with --only preserves all other lock entries.
//   5. Sync with --only rewrites the lock to the full project.yaml set,
//      not just the named skill.

func TestBootstrapLockIncludesBlockedSkills(t *testing.T) {
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
      tools:
        - name: definitely-missing-tool-xyz
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "alpha\n")
	writeFile(t, filepath.Join(home, "library", "beta", "SKILL.md"), "beta\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"install", "--project", project}, &stdout, &stderr)
	// beta is blocked, alpha installed: partial.
	if code != 4 {
		t.Fatalf("Run returned %d, want 4 (partial: alpha installed, beta blocked)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lock, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	names := map[string]bool{}
	for _, s := range lock.Skills {
		names[s.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Fatalf("bootstrap lock must include the FULL desired set (alpha + beta) even when beta was blocked; got %+v", lock.Skills)
	}

	betaFP := ""
	for _, s := range lock.Skills {
		if s.Name == "beta" {
			betaFP = s.Fingerprint
		}
	}
	libFP, err := fingerprintDir(filepath.Join(home, "library", "beta"))
	if err != nil {
		t.Fatal(err)
	}
	if betaFP != libFP {
		t.Fatalf("blocked-skill lock entry should carry the library fingerprint so teammates with the tool install the right bytes; lock=%s lib=%s", betaFP, libFP)
	}
}

func TestBootstrapWithOnlyDoesNotWriteLock(t *testing.T) {
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

	var stdout, stderr bytes.Buffer
	code := Run([]string{"install", "--project", project, "--only", "alpha"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatalf("--only alpha should have installed alpha: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "beta")); !os.IsNotExist(err) {
		t.Fatalf("--only alpha must not install beta")
	}

	lockPath := filepath.Join(project, ".skills", "installed.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("--only without an existing lock must NOT bootstrap a lock (a one-skill action shouldn't freeze a project-wide desired set); got: %v", err)
	}
}

func TestPlainInstallAfterBlockedBootstrapInstallsPreviouslyBlocked(t *testing.T) {
	// After bootstrap put a blocked skill in the lock with the library
	// fingerprint, fixing the tool and running plain install should now
	// install it via the lock-driven path. This is the regression target:
	// previously, a partial bootstrap shrank the lock to just the installed
	// skill, so the blocked one never got reconsidered.
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
  - name: gated
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools:
        - name: definitely-missing-tool-xyz
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "alpha", "SKILL.md"), "alpha\n")
	writeFile(t, filepath.Join(home, "library", "gated", "SKILL.md"), "gated\n")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 4 {
		t.Fatalf("bootstrap returned %d, want 4 (partial)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// "Fix" the requirement by overriding it. This stands in for "install
	// the missing tool". We use --allow-missing-requirements to make this
	// reproducible without depending on the host environment.
	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"install", "--project", project, "--allow-missing-requirements"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "gated", "SKILL.md")); err != nil {
		t.Fatalf("gated skill must be installed by lock-driven install once requirements are satisfied: %v", err)
	}
}

func TestLockDrivenOnlyPreservesOtherLockEntries(t *testing.T) {
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

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("bootstrap returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	before, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatal(err)
	}
	beforeNames := map[string]string{}
	for _, s := range before.Skills {
		beforeNames[s.Name] = s.Fingerprint
	}
	if len(beforeNames) != 2 {
		t.Fatalf("expected 2 skills in lock after bootstrap, got %+v", before.Skills)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"install", "--project", project, "--only", "alpha"}, &stdout, &stderr); code != 0 {
		t.Fatalf("install --only alpha returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	after, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatal(err)
	}
	afterNames := map[string]string{}
	for _, s := range after.Skills {
		afterNames[s.Name] = s.Fingerprint
	}
	if len(afterNames) != 2 {
		t.Fatalf("lock-driven --only must NOT shrink the lock; before=%v after=%v", beforeNames, afterNames)
	}
	if afterNames["beta"] != beforeNames["beta"] {
		t.Fatalf("beta lock entry should be preserved unchanged; before=%q after=%q", beforeNames["beta"], afterNames["beta"])
	}
}

func TestSyncWithOnlyKeepsFullDesiredSetInLock(t *testing.T) {
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

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("bootstrap returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync", "--project", project, "--only", "alpha"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync --only alpha returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	lock, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range lock.Skills {
		names[s.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Fatalf("sync --only must preserve beta in the lock (it's still desired by project.yaml); got %+v", lock.Skills)
	}
	// beta managed copy must still exist (was not pruned by --only sync).
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "beta", "SKILL.md")); err != nil {
		t.Fatalf("sync --only alpha must not prune beta's managed copy: %v", err)
	}
}

func setupUninstallFixture(t *testing.T) (string, string) {
	t.Helper()
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
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("install returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	return home, project
}

func TestUninstallDryRunDoesNotModifyOrBackup(t *testing.T) {
	home, project := setupUninstallFixture(t)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"uninstall", "--project", project, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dry-run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Fatalf("stdout = %q, want dry-run marker", stdout.String())
	}
	if !strings.Contains(stdout.String(), "- remove .claude/skills/review") {
		t.Fatalf("stdout = %q, want remove line", stdout.String())
	}
	// File still present.
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("dry-run must not remove files: %v", err)
	}
	// No backups created.
	if _, err := os.Stat(filepath.Join(home, "backups")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create backups, got err %v", err)
	}
}

func TestUninstallCreatesBackupsByDefault(t *testing.T) {
	home, project := setupUninstallFixture(t)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"uninstall", "--project", project, "--confirm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("uninstall returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	backupsRoot := filepath.Join(home, "backups", projectSlug(project))
	entries, err := os.ReadDir(backupsRoot)
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one backup snapshot, got %d", len(entries))
	}
	backupSkill := filepath.Join(backupsRoot, entries[0].Name(), ".claude", "skills", "review", "SKILL.md")
	if _, err := os.Stat(backupSkill); err != nil {
		t.Fatalf("expected backed-up skill at %s: %v", backupSkill, err)
	}
}

func TestUninstallNoBackupSkipsBackups(t *testing.T) {
	home, project := setupUninstallFixture(t)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"uninstall", "--project", project, "--confirm", "--no-backup"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("uninstall returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "backups")); !os.IsNotExist(err) {
		t.Fatalf("--no-backup must not create backups, got err %v", err)
	}
}

func TestUninstallPreviewListsPreservedPaths(t *testing.T) {
	home, project := setupUninstallFixture(t)

	// Pre-existing unmanaged file should be recorded as preserved.
	writeFile(t, filepath.Join(project, ".claude", "skills", "other", "SKILL.md"), "user\n")
	// Re-run install to register the unmanaged path as preserved.
	var stdout, stderr bytes.Buffer
	_ = Run([]string{"install", "--project", project}, &stdout, &stderr)

	// Add another preserved entry by mutating manifest directly to simulate a
	// previously-recorded preservation outside the managed set.
	mp := filepath.Join(home, "manifests", projectSlug(project)+".json")
	m, err := readManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	m.PreservedPaths = unionSorted(m.PreservedPaths, []string{".claude/skills/other"})
	if err := writeManifest(mp, m); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"uninstall", "--project", project, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dry-run returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "preserve .claude/skills/other (already preserved)") {
		t.Fatalf("stdout = %q, want already-preserved line for unmanaged path", stdout.String())
	}
	// Pre-existing user file must remain on disk after a real uninstall.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"uninstall", "--project", project, "--confirm"}, &stdout, &stderr); code != 0 {
		t.Fatalf("uninstall returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "other", "SKILL.md")); err != nil {
		t.Fatalf("unmanaged user file must survive uninstall: %v", err)
	}
}

func TestUninstallRefusesUnknownArg(t *testing.T) {
	_, project := setupUninstallFixture(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"uninstall", "--project", project, "--bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("uninstall returned %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown uninstall argument") {
		t.Fatalf("stderr = %q, want unknown argument message", stderr.String())
	}
}

func TestRunStatus_BasicAndJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// minimal library
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: demo
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements: {}
`)
	writeFile(t, filepath.Join(home, "library", "demo", "SKILL.md"), "demo skill\n")

	// seed a detected unregistered
	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO detected (path, skill_name, detected_at, action) VALUES ('/p1', 'extra', ?, 'pending')`, time.Now().UTC().Format(time.RFC3339))
	db.Close()

	var stdout, stderr bytes.Buffer
	code := Run([]string{"status"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Library:          1 skills") {
		t.Fatalf("stdout missing library count: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Unregistered:     1 detected") {
		t.Fatalf("stdout missing unregistered: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Scheduled checks: 0 tracked") {
		t.Fatalf("stdout missing scheduled note: %s", stdout.String())
	}

	// json
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"status", "--json"}, &stdout, &stderr) // --json consumed global
	if code != 0 {
		t.Fatal(code)
	}
	var j map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &j); err != nil {
		t.Fatalf("json unmarshal: %v out=%s", err, stdout.String())
	}
	if j["library_skills"].(float64) != 1 || j["unregistered"].(float64) != 1 {
		t.Fatalf("json counts wrong: %+v", j)
	}
}

func TestRunDoctor_ProblemsAndRebuild(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// library with gh req (will miss in test env)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: needs-gh
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools:
        - name: definitely-missing-tool-abc123
          required: true
      scripts:
        required_runtimes:
          - definitely-missing-runtime-abc123
      credentials:
        - name: MISSING_DOCTOR_TOKEN
          source: env
          required: true
`)
	writeFile(t, filepath.Join(home, "library", "needs-gh", "SKILL.md"), `---
name: needs-gh
---
needs gh
`)
	writeFile(t, filepath.Join(home, "library", "needs-gh", ".skill-meta.yaml"), `version: 1
requirements:
  tools:
    - name: definitely-missing-tool-abc123
      required: true
  scripts:
    required_runtimes:
      - definitely-missing-runtime-abc123
  credentials:
    - name: MISSING_DOCTOR_TOKEN
      source: env
      required: true
`)

	// seed a manifest with drift (nonexistent path)
	proj := t.TempDir()
	m := installManifest{
		Version:      1,
		ProjectPath:  proj,
		ProjectSlug:  "p",
		ManagedPaths: []string{"missing/path"},
		Files:        map[string]string{"missing/path": "deadbeef"},
	}
	if err := writeManifest(filepath.Join(home, "manifests", "p.json"), m); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("doctor should exit 1 on problems, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String() + stderr.String()
	if !strings.Contains(out, "definitely-missing-tool-abc123") {
		t.Fatalf("missing tool problem not reported: %s", out)
	}
	if !strings.Contains(out, "MISSING_DOCTOR_TOKEN") {
		t.Fatalf("missing credential problem not reported: %s", out)
	}
	if !strings.Contains(out, "definitely-missing-runtime-abc123") {
		t.Fatalf("missing runtime problem not reported: %s", out)
	}
	if !strings.Contains(out, "manifest integrity") {
		t.Fatalf("manifest problem not reported: %s", out)
	}

	// rebuild-state should work (even if drift)
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"doctor", "--rebuild-state"}, &stdout, &stderr)
	// after rebuild, state drift gone but tool still missing -> still 1
	if code != 1 {
		t.Fatalf("doctor --rebuild-state with other problems: code=%d out=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "rebuilt state.db") {
		t.Fatalf("no rebuild message: %s", stdout.String())
	}
}
