package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type sampleSkill struct {
	name      string
	tags      []string
	frontMore string // extra frontmatter lines (e.g. a cursor: block)
}

func setupCompileProject(t *testing.T, skills []sampleSkill) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	cat := catalog{Version: 1}
	var lockSkills strings.Builder
	lockSkills.WriteString("version: 1\nskills:\n")
	for _, s := range skills {
		fm := "---\nname: " + s.name + "\ndescription: Guidance for " + s.name + "\n"
		if s.frontMore != "" {
			fm += s.frontMore
		}
		fm += "---\n# " + s.name + "\n\nBody for " + s.name + ".\n"
		writeFile(t, filepath.Join(libraryPath, s.name, "SKILL.md"), fm)
		cat.Skills = append(cat.Skills, catalogSkill{Name: s.name, Tags: s.tags})
		lockSkills.WriteString("  - name: " + s.name + "\n")
	}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), lockSkills.String())
	return home, projectPath
}

func TestCompileCursorProducesReasonableRules(t *testing.T) {
	skills := []sampleSkill{
		{name: "react-helper", tags: []string{"react"}},
		{name: "next-helper", tags: []string{"nextjs"}},
		{name: "py-helper", tags: []string{"python"}},
		{name: "go-helper", tags: []string{"go"}},
		{name: "ts-helper", tags: []string{"typescript"}},
		{name: "css-helper", tags: []string{"css"}},
		{name: "docs-helper", tags: []string{"docs"}},
		{name: "always-helper", tags: []string{"always-on"}},
		{name: "untagged-helper", tags: []string{"misc"}},
		{name: "sql-helper", tags: []string{"sql"}},
	}
	home, projectPath := setupCompileProject(t, skills)

	written, err := compileForHarness(home, projectPath, "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != len(skills) {
		t.Fatalf("wrote %d rules, want %d", len(written), len(skills))
	}

	parse := func(name string) (map[string]interface{}, string) {
		raw := readFile(t, filepath.Join(projectPath, ".cursor", "rules", name+".mdc"))
		if !strings.HasPrefix(raw, "---\n") {
			t.Fatalf("%s missing frontmatter:\n%s", name, raw)
		}
		end := strings.Index(raw[4:], "---")
		if end == -1 {
			t.Fatalf("%s unterminated frontmatter", name)
		}
		var fm map[string]interface{}
		if err := yaml.Unmarshal([]byte(raw[4:end+4]), &fm); err != nil {
			t.Fatalf("%s frontmatter not valid yaml: %v\n%s", name, err, raw)
		}
		return fm, raw
	}

	// always-on → alwaysApply true, no globs.
	fm, _ := parse("always-helper")
	if fm["alwaysApply"] != true {
		t.Fatalf("always-helper alwaysApply = %v, want true", fm["alwaysApply"])
	}
	if globs, _ := fm["globs"].([]interface{}); len(globs) != 0 {
		t.Fatalf("always-helper should have no globs, got %v", fm["globs"])
	}
	// react → tsx glob, not alwaysApply.
	_, raw := parse("react-helper")
	if !strings.Contains(raw, "**/*.tsx") {
		t.Fatalf("react-helper should infer a tsx glob:\n%s", raw)
	}
	if fm, _ := parse("react-helper"); fm["alwaysApply"] != false {
		t.Fatalf("react-helper alwaysApply should be false")
	}
	// description carried through and body present.
	_, raw = parse("go-helper")
	if !strings.Contains(raw, "Guidance for go-helper") || !strings.Contains(raw, "Body for go-helper.") {
		t.Fatalf("go-helper missing description/body:\n%s", raw)
	}
	// untagged → no globs, not alwaysApply (description-only rule).
	fmU, _ := parse("untagged-helper")
	if globs, _ := fmU["globs"].([]interface{}); len(globs) != 0 {
		t.Fatalf("untagged-helper should have no inferred globs, got %v", fmU["globs"])
	}
}

func TestCompileCursorFrontmatterOverride(t *testing.T) {
	skills := []sampleSkill{{
		name:      "override",
		tags:      []string{"react"}, // would infer tsx, but override wins
		frontMore: "cursor:\n  globs:\n    - \"**/*.custom\"\n  alwaysApply: false\n  description: Custom desc\n",
	}}
	home, projectPath := setupCompileProject(t, skills)
	if _, err := compileForHarness(home, projectPath, "cursor"); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, filepath.Join(projectPath, ".cursor", "rules", "override.mdc"))
	if !strings.Contains(raw, "**/*.custom") {
		t.Fatalf("override globs not applied:\n%s", raw)
	}
	if strings.Contains(raw, "**/*.tsx") {
		t.Fatalf("inferred globs should be replaced by override:\n%s", raw)
	}
	if !strings.Contains(raw, "Custom desc") {
		t.Fatalf("override description not applied:\n%s", raw)
	}
}

func TestCompileUnsupportedHarness(t *testing.T) {
	home, projectPath := setupCompileProject(t, []sampleSkill{{name: "x", tags: []string{"go"}}})
	if _, err := compileForHarness(home, projectPath, "emacs"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported-harness error, got %v", err)
	}
}

func TestCompileHarnessesForProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "project.yaml"), "version: 1\nharnesses: [claude, cursor, codex]\n")
	got := compileHarnessesForProject(projectPath)
	if len(got) != 1 || got[0] != "cursor" {
		t.Fatalf("compileHarnessesForProject = %v, want [cursor]", got)
	}
	// A project without cursor yields nothing.
	writeFile(t, filepath.Join(projectPath, ".skills", "project.yaml"), "version: 1\nharnesses: [claude, codex]\n")
	if got := compileHarnessesForProject(projectPath); len(got) != 0 {
		t.Fatalf("expected no compile harnesses, got %v", got)
	}
}

func TestInferGlobsFromTags(t *testing.T) {
	if g := inferGlobsFromTags([]string{"react", "typescript"}); len(g) == 0 {
		t.Fatal("expected globs for react/typescript")
	}
	if g := inferGlobsFromTags([]string{"misc"}); len(g) != 0 {
		t.Fatalf("unknown tags should infer no globs, got %v", g)
	}
}
