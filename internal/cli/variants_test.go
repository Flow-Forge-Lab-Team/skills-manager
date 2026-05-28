package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPicksVariantPerHarness(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), `version: 1
name: demo
categories: [Engineering]
harnesses: [claude, antigravity, codex]
`)
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), `version: 1
skills:
  - name: code-reviewer
    categories: [Engineering]
    compatibility:
      mode: portable
    requirements:
      tools: []
`)
	skillDir := filepath.Join(home, "library", "code-reviewer")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: code-reviewer\n---\nCANONICAL BODY\n")
	writeFile(t, filepath.Join(skillDir, "SKILL.claude.md"), "---\nname: code-reviewer\n---\nCLAUDE VARIANT BODY\n")
	writeFile(t, filepath.Join(skillDir, "SKILL.antigravity.md"), "---\nname: code-reviewer\n---\nANTIGRAVITY VARIANT BODY\n")
	writeFile(t, filepath.Join(skillDir, ".variants.yaml"), `version: 1
default: SKILL.md
overrides:
  claude: SKILL.claude.md
  antigravity: SKILL.antigravity.md
canonical_fingerprint: stamp
`)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &stdout, &stderr); code != 0 {
		t.Fatalf("install returned %d\nstdout:%s\nstderr:%s", code, stdout.String(), stderr.String())
	}

	cases := map[string]string{
		".claude/skills/code-reviewer/SKILL.md": "CLAUDE VARIANT BODY",
		".agents/skills/code-reviewer/SKILL.md": "ANTIGRAVITY VARIANT BODY", // antigravity base
		".codex/skills/code-reviewer/SKILL.md":  "CANONICAL BODY",           // no variant → canonical
	}
	for rel, want := range cases {
		got := readFile(t, filepath.Join(project, filepath.FromSlash(rel)))
		if !strings.Contains(got, want) {
			t.Fatalf("%s = %q, want to contain %q", rel, got, want)
		}
	}
	// Variant sources and the manifest must not ship into harness dirs.
	for _, rel := range []string{
		".claude/skills/code-reviewer/SKILL.claude.md",
		".claude/skills/code-reviewer/.variants.yaml",
		".agents/skills/code-reviewer/SKILL.antigravity.md",
		".codex/skills/code-reviewer/.variants.yaml",
	} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s should not be installed into the harness copy (err=%v)", rel, err)
		}
	}
}

func TestVariantsStaleAndRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	skillDir := filepath.Join(home, "library", "ported")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: ported\n---\nv1\n")
	writeFile(t, filepath.Join(skillDir, "SKILL.claude.md"), "---\nname: ported\n---\nv1 claude\n")
	writeFile(t, filepath.Join(skillDir, ".variants.yaml"), "version: 1\noverrides:\n  claude: SKILL.claude.md\ncanonical_fingerprint: oldstamp\n")

	stale, err := variantsStale(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Fatal("expected stale variants when canonical fingerprint mismatches")
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"variants", "ported", "--refresh"}, &stdout, &stderr); code != 0 {
		t.Fatalf("refresh returned %d\nstderr:%s", code, stderr.String())
	}
	stale, err = variantsStale(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("variants should be fresh after --refresh")
	}
}

func TestDoctorSurfacesStaleVariants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "ported")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: ported\n---\nchanged\n")
	writeFile(t, filepath.Join(skillDir, "SKILL.claude.md"), "---\nname: ported\n---\nold claude\n")
	writeFile(t, filepath.Join(skillDir, ".variants.yaml"), "version: 1\noverrides:\n  claude: SKILL.claude.md\ncanonical_fingerprint: oldstamp\n")
	if got := staleVariantSkills(libraryPath); len(got) != 1 || got[0] != "ported" {
		t.Fatalf("staleVariantSkills = %v, want [ported]", got)
	}
}

func TestSelectVariantFileAndHarnessesForBase(t *testing.T) {
	vf := variantsFile{Overrides: map[string]string{"claude": "SKILL.claude.md", "antigravity": "SKILL.antigravity.md"}}
	if got := selectVariantFile(vf, []string{"codex", "claude"}); got != "SKILL.claude.md" {
		t.Fatalf("selectVariantFile = %q, want SKILL.claude.md", got)
	}
	if got := selectVariantFile(vf, []string{"codex", "grok"}); got != "" {
		t.Fatalf("selectVariantFile = %q, want empty (canonical)", got)
	}
	// .agents/skills serves antigravity + gemini; canonical order puts
	// antigravity first.
	if got := harnessesForBase([]string{"gemini", "antigravity", "claude"}, ".agents/skills"); len(got) != 2 || got[0] != "antigravity" {
		t.Fatalf("harnessesForBase = %v, want [antigravity gemini]", got)
	}
}

func TestCompileHonorsVariantOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "ruler")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: ruler\ndescription: canonical\n---\nCANONICAL\n")
	writeFile(t, filepath.Join(skillDir, "SKILL.cursor.md"), "---\nname: ruler\ndescription: cursor port\n---\nCURSOR PORTED BODY\n")
	writeFile(t, filepath.Join(skillDir, ".variants.yaml"), "version: 1\noverrides:\n  cursor: SKILL.cursor.md\ncanonical_fingerprint: x\n")
	cat := catalog{Version: 1, Skills: []catalogSkill{{Name: "ruler", Tags: []string{"go"}}}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: ruler\n")

	if _, err := compileForHarness(home, projectPath, "cursor", ""); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(projectPath, ".cursor", "rules", "ruler.mdc"))
	if !strings.Contains(got, "CURSOR PORTED BODY") {
		t.Fatalf("compile should use the cursor variant body:\n%s", got)
	}
	if strings.Contains(got, "CANONICAL") {
		t.Fatalf("compile should not use canonical when a cursor variant exists:\n%s", got)
	}
}

func TestVariantPathTraversalIsRejected(t *testing.T) {
	if isLocalFile("../evil.md") || isLocalFile("a/b.md") || isLocalFile("..") || isLocalFile("") {
		t.Fatal("traversal/non-local names must be rejected")
	}
	if !isLocalFile("SKILL.claude.md") {
		t.Fatal("a plain local filename must be accepted")
	}
	// A malicious override is ignored by selection rather than escaping the dir.
	vf := variantsFile{Overrides: map[string]string{"claude": "../../etc/passwd"}}
	if got := selectVariantFile(vf, []string{"claude"}); got != "" {
		t.Fatalf("unsafe override should be ignored, got %q", got)
	}
}

func TestCompileFailsOnMissingDeclaredVariant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "ruler")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: ruler\ndescription: c\n---\nCANONICAL\n")
	// Declares a cursor variant file that does not exist.
	writeFile(t, filepath.Join(skillDir, ".variants.yaml"), "version: 1\noverrides:\n  cursor: SKILL.cursor.md\ncanonical_fingerprint: x\n")
	cat := catalog{Version: 1, Skills: []catalogSkill{{Name: "ruler", Tags: []string{"go"}}}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: ruler\n")

	if _, err := compileForHarness(home, projectPath, "cursor", ""); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing-variant error, got %v", err)
	}
	// And it must not have silently written canonical content.
	if _, err := os.Stat(filepath.Join(projectPath, ".cursor", "rules", "ruler.mdc")); !os.IsNotExist(err) {
		t.Fatal("no rule should be written when a declared variant is missing")
	}
}

func TestCompileFailsOnMalformedVariantsManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "ruler")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: ruler\ndescription: c\n---\nbody\n")
	writeFile(t, filepath.Join(skillDir, ".variants.yaml"), "version: 1\noverrides: : : not-yaml\n  - [\n")
	cat := catalog{Version: 1, Skills: []catalogSkill{{Name: "ruler", Tags: []string{"go"}}}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: ruler\n")
	if _, err := compileForHarness(home, projectPath, "cursor", ""); err == nil {
		t.Fatal("expected error for malformed .variants.yaml")
	}
}

func TestInstallPreservesTargetOnMissingVariant(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), "version: 1\nname: demo\ncategories: [Engineering]\nharnesses: [claude]\n")
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), "version: 1\nskills:\n  - name: rev\n    categories: [Engineering]\n    compatibility:\n      mode: portable\n    requirements:\n      tools: []\n")
	skillDir := filepath.Join(home, "library", "rev")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: rev\n---\nCANONICAL\n")
	// Install once cleanly (no variants).
	var o, e bytes.Buffer
	if code := Run([]string{"install", "--project", project}, &o, &e); code != 0 {
		t.Fatalf("first install failed: %s", e.String())
	}
	target := filepath.Join(project, ".claude", "skills", "rev", "SKILL.md")
	before := readFile(t, target)
	// Now declare a claude variant whose file is missing, and reinstall.
	writeFile(t, filepath.Join(skillDir, ".variants.yaml"), "version: 1\noverrides:\n  claude: SKILL.claude.md\n")
	o.Reset()
	e.Reset()
	if code := Run([]string{"install", "--project", project}, &o, &e); code == 0 {
		t.Fatal("reinstall should fail when declared variant is missing")
	}
	// The previously-installed target must not be left as a raw/garbled copy.
	after := readFile(t, target)
	if after != before {
		t.Fatalf("managed target should be untouched on variant failure:\nbefore=%q\nafter=%q", before, after)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "rev", ".variants.yaml")); err == nil {
		t.Fatal(".variants.yaml must not be left in the harness copy")
	}
}

func TestInstallDryRunValidatesVariant(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), "version: 1\nname: demo\ncategories: [Engineering]\nharnesses: [claude]\n")
	writeFile(t, filepath.Join(home, "library", "catalog.yaml"), "version: 1\nskills:\n  - name: rev\n    categories: [Engineering]\n    compatibility:\n      mode: portable\n    requirements:\n      tools: []\n")
	skillDir := filepath.Join(home, "library", "rev")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: rev\n---\nbody\n")
	writeFile(t, filepath.Join(skillDir, ".variants.yaml"), "version: 1\noverrides:\n  claude: SKILL.claude.md\n") // missing file
	var o, e bytes.Buffer
	if code := Run([]string{"install", "--project", project, "--dry-run"}, &o, &e); code == 0 {
		t.Fatalf("dry-run should surface the missing variant, got success\nstdout:%s", o.String())
	}
}
