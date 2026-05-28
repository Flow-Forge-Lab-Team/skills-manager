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
