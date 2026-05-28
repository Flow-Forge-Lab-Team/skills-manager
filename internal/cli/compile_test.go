package cli

import (
	"os"
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

	written, err := compileForHarness(home, projectPath, "cursor", "")
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
	// react → the exact inferred glob list (sorted), not alwaysApply. Assert the
	// parsed YAML list rather than a substring, so a wrong shape/quoting/order
	// can't slip through (the blind spot that hid the Copilot applyTo bug).
	frm, raw := parse("react-helper")
	if got := frm["globs"]; !globsEqual(got, []string{"**/*.jsx", "**/*.tsx"}) {
		t.Fatalf("react-helper globs = %#v, want [**/*.jsx **/*.tsx]\n%s", got, raw)
	}
	if frm["alwaysApply"] != false {
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
	if _, err := compileForHarness(home, projectPath, "cursor", ""); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, filepath.Join(projectPath, ".cursor", "rules", "override.mdc"))
	end := strings.Index(raw[4:], "---")
	if !strings.HasPrefix(raw, "---\n") || end == -1 {
		t.Fatalf("override.mdc missing frontmatter:\n%s", raw)
	}
	var fm map[string]interface{}
	if err := yaml.Unmarshal([]byte(raw[4:end+4]), &fm); err != nil {
		t.Fatalf("override frontmatter not valid yaml: %v\n%s", err, raw)
	}
	// The override globs must replace the inferred ones exactly (no tsx).
	if got := fm["globs"]; !globsEqual(got, []string{"**/*.custom"}) {
		t.Fatalf("override globs = %#v, want [**/*.custom]\n%s", got, raw)
	}
	if fm["description"] != "Custom desc" {
		t.Fatalf("override description = %v, want \"Custom desc\"", fm["description"])
	}
}

func TestCompileUnsupportedHarness(t *testing.T) {
	home, projectPath := setupCompileProject(t, []sampleSkill{{name: "x", tags: []string{"go"}}})
	if _, err := compileForHarness(home, projectPath, "emacs", ""); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported-harness error, got %v", err)
	}
}

func TestCompileHarnessesForProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "project.yaml"), "version: 1\nharnesses: [claude, cursor, codex]\n")
	got := compileHarnessesForProject(projectPath, "")
	if len(got) != 1 || got[0] != "cursor" {
		t.Fatalf("compileHarnessesForProject = %v, want [cursor]", got)
	}
	// A project without cursor yields nothing.
	writeFile(t, filepath.Join(projectPath, ".skills", "project.yaml"), "version: 1\nharnesses: [claude, codex]\n")
	if got := compileHarnessesForProject(projectPath, ""); len(got) != 0 {
		t.Fatalf("expected no compile harnesses, got %v", got)
	}
}

func TestCompileCursorFiltersLockByCompatibility(t *testing.T) {
	// A lock can contain skills installed only for another harness; those must
	// not be compiled to Cursor rules.
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "portable-skill", "SKILL.md"), "---\nname: portable-skill\ndescription: p\n---\nbody\n")
	writeFile(t, filepath.Join(libraryPath, "claude-skill", "SKILL.md"), "---\nname: claude-skill\ndescription: c\n---\nbody\n")
	cat := catalog{Version: 1, Skills: []catalogSkill{
		{Name: "portable-skill", Tags: []string{"go"}},
		{Name: "claude-skill", Tags: []string{"go"}, Compatibility: compatibility{Mode: "exclusive", Harness: "claude"}},
	}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: portable-skill\n  - name: claude-skill\n")

	written, err := compileForHarness(home, projectPath, "cursor", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("compiled %d rules, want 1 (claude-skill excluded): %v", len(written), written)
	}
	if _, err := os.Stat(filepath.Join(projectPath, ".cursor", "rules", "claude-skill.mdc")); !os.IsNotExist(err) {
		t.Fatal("exclusive:claude skill must not be compiled to a Cursor rule")
	}
}

func TestCompileCursorOnlyProjectUsesMatch(t *testing.T) {
	// A cursor-only project records nothing in installed.lock (cursor has no
	// copy target), so the compiler must fall back to the project match.
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "go-rules", "SKILL.md"), "---\nname: go-rules\ndescription: Go conventions\n---\n# Go\n\nUse gofmt.\n")
	writeFile(t, filepath.Join(libraryPath, "claude-only", "SKILL.md"), "---\nname: claude-only\ndescription: claude\n---\nbody\n")
	cat := catalog{Version: 1, Skills: []catalogSkill{
		{Name: "go-rules", Tags: []string{"go"}},
		// exclusive to claude → not compiled for cursor
		{Name: "claude-only", Tags: []string{"go"}, Compatibility: compatibility{Mode: "exclusive", Harness: "claude"}},
	}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	writeFile(t, filepath.Join(projectPath, ".skills", "project.yaml"), "version: 1\ncategories: []\ntags: [go]\nharnesses: [cursor]\n")
	// no installed.lock on purpose

	written, err := compileForHarness(home, projectPath, "cursor", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("cursor-only project compiled %d rules, want 1 (go-rules; claude-only excluded): %v", len(written), written)
	}
	if _, err := os.Stat(filepath.Join(projectPath, ".cursor", "rules", "go-rules.mdc")); err != nil {
		t.Fatalf("go-rules.mdc not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectPath, ".cursor", "rules", "claude-only.mdc")); !os.IsNotExist(err) {
		t.Fatal("claude-only is cursor-incompatible and must not be compiled")
	}
}

func TestCompileCursorPrunesStaleRules(t *testing.T) {
	home, projectPath := setupCompileProject(t, []sampleSkill{
		{name: "alpha", tags: []string{"go"}},
		{name: "beta", tags: []string{"react"}},
	})
	rulesDir := filepath.Join(projectPath, ".cursor", "rules")
	// A user-authored rule that must never be deleted.
	if _, err := compileForHarness(home, projectPath, "cursor", ""); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(rulesDir, "user-hand-written.mdc"), "---\ndescription: mine\n---\nkeep\n")

	// Narrow the install set to just alpha and recompile.
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: alpha\n")
	if _, err := compileForHarness(home, projectPath, "cursor", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rulesDir, "beta.mdc")); !os.IsNotExist(err) {
		t.Fatal("stale beta.mdc should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(rulesDir, "alpha.mdc")); err != nil {
		t.Fatalf("alpha.mdc should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rulesDir, "user-hand-written.mdc")); err != nil {
		t.Fatal("user-authored rule must be preserved")
	}
}

func TestCompileMatchFallbackSkipsUnmetRequirements(t *testing.T) {
	// A cursor-only match-fallback skill whose required tool is missing must not
	// be compiled — install would have blocked it.
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	libraryPath := filepath.Join(home, "library")
	writeFile(t, filepath.Join(libraryPath, "ok-skill", "SKILL.md"), "---\nname: ok-skill\ndescription: ok\n---\nbody\n")
	writeFile(t, filepath.Join(libraryPath, "needs-tool", "SKILL.md"), "---\nname: needs-tool\ndescription: needs\n---\nbody\n")
	cat := catalog{Version: 1, Skills: []catalogSkill{
		{Name: "ok-skill", Tags: []string{"go"}},
		{Name: "needs-tool", Tags: []string{"go"}, Requirements: requirements{Tools: []toolRequirement{{Name: "definitely-not-a-real-binary-xyz", Required: true}}}},
	}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(home, "proj")
	// cursor-only, no lock → match fallback.
	writeFile(t, filepath.Join(projectPath, ".skills", "project.yaml"), "version: 1\ntags: [go]\nharnesses: [cursor]\n")

	written, err := compileForHarness(home, projectPath, "cursor", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("compiled %d rules, want 1 (needs-tool blocked by missing requirement): %v", len(written), written)
	}
	if _, err := os.Stat(filepath.Join(projectPath, ".cursor", "rules", "needs-tool.mdc")); !os.IsNotExist(err) {
		t.Fatal("skill with unmet required tool must not be compiled")
	}

	// Same filtering must apply when the skills come from an install lock.
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: ok-skill\n  - name: needs-tool\n")
	_ = os.RemoveAll(filepath.Join(projectPath, ".cursor"))
	written, err = compileForHarness(home, projectPath, "cursor", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("locked: compiled %d rules, want 1 (needs-tool still blocked): %v", len(written), written)
	}
	if _, err := os.Stat(filepath.Join(projectPath, ".cursor", "rules", "needs-tool.mdc")); !os.IsNotExist(err) {
		t.Fatal("locked skill with unmet required tool must not be compiled")
	}
}

func TestReconcileCompileHarnessesPrunesWhenDisabled(t *testing.T) {
	home, projectPath := setupCompileProject(t, []sampleSkill{{name: "alpha", tags: []string{"go"}}})
	cfgPath := filepath.Join(projectPath, ".skills", "project.yaml")
	rulesDir := filepath.Join(projectPath, ".cursor", "rules")

	// Cursor enabled → reconcile compiles.
	writeFile(t, cfgPath, "version: 1\nharnesses: [cursor]\n")
	if err := reconcileCompileHarnesses(home, projectPath, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rulesDir, "alpha.mdc")); err != nil {
		t.Fatalf("expected alpha.mdc after enable: %v", err)
	}
	// Cursor disabled → reconcile prunes generated rules.
	writeFile(t, cfgPath, "version: 1\nharnesses: [claude]\n")
	if err := reconcileCompileHarnesses(home, projectPath, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rulesDir, "alpha.mdc")); !os.IsNotExist(err) {
		t.Fatal("generated cursor rule should be pruned when cursor is disabled")
	}
}

func TestPruneAllCompileHarnessesRemovesGenerated(t *testing.T) {
	home, projectPath := setupCompileProject(t, []sampleSkill{{name: "alpha", tags: []string{"go"}}})
	rulesDir := filepath.Join(projectPath, ".cursor", "rules")
	if _, err := compileForHarness(home, projectPath, "cursor", ""); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(rulesDir, "user.mdc"), "---\ndescription: mine\n---\nkeep\n")

	pruneAllCompileHarnesses(projectPath)

	if _, err := os.Stat(filepath.Join(rulesDir, "alpha.mdc")); !os.IsNotExist(err) {
		t.Fatal("generated rule should be removed on prune")
	}
	if _, err := os.Stat(filepath.Join(rulesDir, "user.mdc")); err != nil {
		t.Fatal("user-authored rule must be preserved on prune")
	}
}

func TestRenderCursorRuleEscapesBackslashes(t *testing.T) {
	r := compiledRule{Name: "win", Description: `Use C:\Users\path`, Globs: []string{`a\b`}, Body: "x"}
	out := renderCursorRule(r)
	end := strings.Index(out[4:], "---")
	if end == -1 {
		t.Fatalf("no frontmatter terminator:\n%s", out)
	}
	var fm map[string]interface{}
	if err := yaml.Unmarshal([]byte(out[4:end+4]), &fm); err != nil {
		t.Fatalf("frontmatter with backslashes must be valid yaml: %v\n%s", err, out)
	}
	if fm["description"] != `Use C:\Users\path` {
		t.Fatalf("backslash description round-trip wrong: %v", fm["description"])
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

func TestCompileCopilotPerFileAndSingleFile(t *testing.T) {
	skills := []sampleSkill{
		{name: "react-rule", tags: []string{"react"}},      // glob → per-file applyTo
		{name: "always-rule", tags: []string{"always-on"}}, // always → applyTo "**"
		{name: "general-rule", tags: []string{"misc"}},     // no scope → single-file fallback
	}
	home, projectPath := setupCompileProject(t, skills)

	written, err := compileForHarness(home, projectPath, "copilot", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 3 {
		t.Fatalf("wrote %d outputs, want 3 (2 per-file + 1 single-file): %v", len(written), written)
	}

	readFM := func(name string) map[string]interface{} {
		raw := readFile(t, filepath.Join(projectPath, ".github", "instructions", name+".instructions.md"))
		end := strings.Index(raw[4:], "---")
		if !strings.HasPrefix(raw, "---\n") || end == -1 {
			t.Fatalf("%s missing frontmatter:\n%s", name, raw)
		}
		var fm map[string]interface{}
		if err := yaml.Unmarshal([]byte(raw[4:end+4]), &fm); err != nil {
			t.Fatalf("%s invalid frontmatter: %v", name, err)
		}
		return fm
	}
	// react infers a multi-glob applyTo; it must be a comma-separated list with
	// no spaces (a space would make the next pattern unmatchable in Copilot).
	if got := readFM("react-rule")["applyTo"]; got != "**/*.jsx,**/*.tsx" {
		t.Fatalf("react-rule applyTo = %q, want \"**/*.jsx,**/*.tsx\" (comma-separated, no spaces)", got)
	}
	if got := readFM("always-rule")["applyTo"]; got != "**" {
		t.Fatalf("always-rule applyTo = %v, want **", got)
	}
	// general-rule has no scope → single file, not per-file.
	if _, err := os.Stat(filepath.Join(projectPath, ".github", "instructions", "general-rule.instructions.md")); !os.IsNotExist(err) {
		t.Fatal("general-rule should fold into the single file, not a per-file instruction")
	}
	single := readFile(t, filepath.Join(projectPath, ".github", "copilot-instructions.md"))
	if !strings.Contains(single, copilotBeginMarker) || !strings.Contains(single, "## general-rule") {
		t.Fatalf("single-file fallback missing general-rule:\n%s", single)
	}
}

func TestCompileCopilotOverrideAndPreserveAndPrune(t *testing.T) {
	skills := []sampleSkill{
		{name: "ovr", tags: []string{"misc"}, frontMore: "copilot:\n  applyTo: \"**/*.go\"\n"}, // override → per-file
		{name: "gen", tags: []string{"misc"}},
	}
	home, projectPath := setupCompileProject(t, skills)
	// Pre-existing user content in the single file.
	writeFile(t, filepath.Join(projectPath, ".github", "copilot-instructions.md"), "# Team rules\n\nAlways write tests.\n")

	if _, err := compileForHarness(home, projectPath, "copilot", ""); err != nil {
		t.Fatal(err)
	}
	// ovr → per-file via override.
	raw := readFile(t, filepath.Join(projectPath, ".github", "instructions", "ovr.instructions.md"))
	if !strings.Contains(raw, "**/*.go") {
		t.Fatalf("override applyTo not applied:\n%s", raw)
	}
	single := readFile(t, filepath.Join(projectPath, ".github", "copilot-instructions.md"))
	if !strings.Contains(single, "Always write tests.") || !strings.Contains(single, "## gen") {
		t.Fatalf("user content not preserved alongside generated block:\n%s", single)
	}

	// Narrow to just ovr (gen removed): single-file block cleared, user content kept.
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: ovr\n")
	if _, err := compileForHarness(home, projectPath, "copilot", ""); err != nil {
		t.Fatal(err)
	}
	single = readFile(t, filepath.Join(projectPath, ".github", "copilot-instructions.md"))
	if strings.Contains(single, "## gen") || strings.Contains(single, copilotBeginMarker) {
		t.Fatalf("stale general block should be cleared:\n%s", single)
	}
	if !strings.Contains(single, "Always write tests.") {
		t.Fatalf("user content must remain:\n%s", single)
	}

	// Disable copilot entirely → per-file instructions pruned.
	pruneAllCompileHarnesses(projectPath)
	if _, err := os.Stat(filepath.Join(projectPath, ".github", "instructions", "ovr.instructions.md")); !os.IsNotExist(err) {
		t.Fatal("per-file instruction should be pruned")
	}
}

func TestCompileCopilotIgnoresCursorOverride(t *testing.T) {
	// A skill with a cursor-only override and a react tag must scope Copilot by
	// the tag-inferred glob, not the Cursor override.
	skills := []sampleSkill{{
		name:      "x",
		tags:      []string{"react"},
		frontMore: "cursor:\n  globs:\n    - \"**/*.custom\"\n",
	}}
	home, projectPath := setupCompileProject(t, skills)
	if _, err := compileForHarness(home, projectPath, "copilot", ""); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, filepath.Join(projectPath, ".github", "instructions", "x.instructions.md"))
	if strings.Contains(raw, "**/*.custom") {
		t.Fatalf("cursor override leaked into copilot applyTo:\n%s", raw)
	}
	if !strings.Contains(raw, "*.tsx") {
		t.Fatalf("copilot applyTo should use the react-inferred glob:\n%s", raw)
	}
}

func TestCompileCopilotRemovesGeneratedOnlyFallbackFile(t *testing.T) {
	// Single-file fallback created solely by the compiler must be removed (not
	// left empty) when nothing qualifies anymore.
	skills := []sampleSkill{{name: "gen", tags: []string{"misc"}}}
	home, projectPath := setupCompileProject(t, skills)
	singlePath := filepath.Join(projectPath, ".github", "copilot-instructions.md")

	if _, err := compileForHarness(home, projectPath, "copilot", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(singlePath); err != nil {
		t.Fatalf("expected generated fallback file: %v", err)
	}
	// Disable copilot → file should be removed entirely (no user content).
	pruneAllCompileHarnesses(projectPath)
	if _, err := os.Stat(singlePath); !os.IsNotExist(err) {
		t.Fatalf("generated-only fallback file should be removed, not left empty (err=%v)", err)
	}
}

// globsEqual reports whether a parsed YAML globs value (a []interface{} of
// strings) equals the expected list, in order.
func globsEqual(parsed interface{}, want []string) bool {
	list, ok := parsed.([]interface{})
	if !ok || len(list) != len(want) {
		return false
	}
	for i, v := range list {
		s, ok := v.(string)
		if !ok || s != want[i] {
			return false
		}
	}
	return true
}
