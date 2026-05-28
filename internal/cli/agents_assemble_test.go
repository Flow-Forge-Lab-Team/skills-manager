package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupAssembleProject creates a library + project with three skills and an
// install lock referencing all three. Returns home and projectPath.
func setupAssembleProject(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")

	// always-on via catalog tag
	writeFile(t, filepath.Join(libraryPath, "alpha", "SKILL.md"), "---\nname: alpha\norder: 2\n---\nAlpha guidance.\n")
	// included via frontmatter flag, ordered first
	writeFile(t, filepath.Join(libraryPath, "beta", "SKILL.md"), "---\nname: beta\nagents_md: true\norder: 1\n---\nBeta guidance.\n")
	// not included
	writeFile(t, filepath.Join(libraryPath, "gamma", "SKILL.md"), "---\nname: gamma\n---\nGamma guidance.\n")

	cat := catalog{Version: 1, Skills: []catalogSkill{
		{Name: "alpha", Tags: []string{"always-on", "go"}},
		{Name: "beta", Tags: []string{"react"}},
		{Name: "gamma", Tags: []string{"misc"}},
	}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}

	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "project.yaml"), "version: 1\nname: demo\ncategories: [Engineering]\ntags: [go, react]\n")
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: alpha\n  - name: beta\n  - name: gamma\n")
	return home, projectPath
}

func TestAssembleSelectsOrdersAndHeaders(t *testing.T) {
	home, projectPath := setupAssembleProject(t)
	wrote, path, err := assembleAgentsMd(home, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("expected AGENTS.md to be written")
	}
	content := readFile(t, path)

	if !strings.Contains(content, "## beta") || !strings.Contains(content, "## alpha") {
		t.Fatalf("included skills missing:\n%s", content)
	}
	if strings.Contains(content, "## gamma") {
		t.Fatalf("gamma is neither always-on nor agents_md and must be excluded:\n%s", content)
	}
	// order: beta (1) before alpha (2).
	if strings.Index(content, "## beta") > strings.Index(content, "## alpha") {
		t.Fatalf("beta (order 1) should precede alpha (order 2):\n%s", content)
	}
	// metadata header.
	for _, want := range []string{"**Project:** demo", "**Categories:** Engineering", "**Tags:** go, react"} {
		if !strings.Contains(content, want) {
			t.Fatalf("header missing %q:\n%s", want, content)
		}
	}
	if !strings.Contains(content, agentsBeginMarker) || !strings.Contains(content, agentsEndMarker) {
		t.Fatal("generated markers missing")
	}
}

func TestAssemblePreservesUserContent(t *testing.T) {
	home, projectPath := setupAssembleProject(t)
	agentsPath := filepath.Join(projectPath, "AGENTS.md")
	writeFile(t, agentsPath, "# My hand-written notes\n\nKeep this intact.\n")

	if _, _, err := assembleAgentsMd(home, projectPath); err != nil {
		t.Fatal(err)
	}
	content := readFile(t, agentsPath)
	if !strings.Contains(content, "Keep this intact.") {
		t.Fatalf("user content was lost:\n%s", content)
	}
	if !strings.Contains(content, "## beta") {
		t.Fatalf("generated block missing:\n%s", content)
	}
	// Regenerating must not duplicate the block or drop user content.
	if _, _, err := assembleAgentsMd(home, projectPath); err != nil {
		t.Fatal(err)
	}
	content2 := readFile(t, agentsPath)
	if strings.Count(content2, agentsBeginMarker) != 1 {
		t.Fatalf("regeneration duplicated the generated block:\n%s", content2)
	}
	if !strings.Contains(content2, "Keep this intact.") {
		t.Fatalf("user content lost on regeneration:\n%s", content2)
	}
}

func TestAssembleNoIncludedSkillsLeavesFileUntouched(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "gamma", "SKILL.md"), "---\nname: gamma\n---\nGamma.\n")
	cat := catalog{Version: 1, Skills: []catalogSkill{{Name: "gamma", Tags: []string{"misc"}}}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: gamma\n")
	agentsPath := filepath.Join(projectPath, "AGENTS.md")
	writeFile(t, agentsPath, "untouched\n")

	wrote, _, err := assembleAgentsMd(home, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatal("expected no write when no skills qualify")
	}
	if got := readFile(t, agentsPath); got != "untouched\n" {
		t.Fatalf("AGENTS.md should be untouched, got: %q", got)
	}
}

func TestAssembleClearsStaleBlockWhenNothingQualifies(t *testing.T) {
	home, projectPath := setupAssembleProject(t)
	agentsPath := filepath.Join(projectPath, "AGENTS.md")
	writeFile(t, agentsPath, "# Notes\n\nUser content.\n")
	// First pass: generates a block (alpha/beta qualify).
	if _, _, err := assembleAgentsMd(home, projectPath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, agentsPath), agentsBeginMarker) {
		t.Fatal("expected a generated block on first pass")
	}
	// Now the lock only contains gamma (which doesn't qualify): the stale block
	// must be cleared, user content kept.
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: gamma\n")
	wrote, _, err := assembleAgentsMd(home, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("expected a write to clear the stale block")
	}
	content := readFile(t, agentsPath)
	if strings.Contains(content, agentsBeginMarker) || strings.Contains(content, "## beta") {
		t.Fatalf("stale generated block should be cleared:\n%s", content)
	}
	if !strings.Contains(content, "User content.") {
		t.Fatalf("user content must be preserved:\n%s", content)
	}
}

func TestAssembleSurfacesMalformedLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "library"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	// A malformed lock must surface an error, not a silent no-op.
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills: : : not yaml\n  - [unbalanced\n")
	if _, _, err := assembleAgentsMd(home, projectPath); err == nil {
		t.Fatal("expected an error for a malformed install lock")
	}
}

func TestAssembleMergeBlockReplacesGeneratedRegion(t *testing.T) {
	existing := "top user content\n\n" + agentsBeginMarker + "\nOLD GENERATED\n" + agentsEndMarker + "\n\nbottom user content\n"
	generated := agentsBeginMarker + "\nNEW GENERATED\n" + agentsEndMarker
	merged := mergeAgentsBlock(existing, generated)
	if strings.Contains(merged, "OLD GENERATED") {
		t.Fatalf("old generated content not replaced:\n%s", merged)
	}
	if !strings.Contains(merged, "NEW GENERATED") {
		t.Fatalf("new generated content missing:\n%s", merged)
	}
	for _, want := range []string{"top user content", "bottom user content"} {
		if !strings.Contains(merged, want) {
			t.Fatalf("user content %q lost:\n%s", want, merged)
		}
	}
}
