package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

func TestDiscoverRequiresExplicitScope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"discover"}, &stdout, &stderr)
	if code != ExitUsageError {
		t.Fatalf("code = %d, want %d", code, ExitUsageError)
	}
	if !strings.Contains(stderr.String(), "explicit scope") {
		t.Fatalf("stderr = %q, want explicit scope error", stderr.String())
	}
}

func TestDiscoverGlobalReportsSkillsAndMissingTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "build", "---\nname: build\ncompatible: [claude, codex, grok]\n---\n# Build\n")
	writeScanSkill(t, filepath.Join(home, ".codex", "skills"), "review", "---\nname: review\n---\n# Review changed\n")
	writeScanSkill(t, filepath.Join(home, ".codex", "skills", ".system"), "openai-docs", "---\nname: openai-docs\n---\n# OpenAI Docs\n")
	writeScanSkill(t, filepath.Join(home, ".grok", "skills"), "review-copy", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Join(home, ".grok", "skills"), "copy-a", "# Shared\n")
	writeScanSkill(t, filepath.Join(home, ".grok", "skills"), "copy-b", "# Shared\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if got.Summary.ToolsFound != 3 {
		t.Fatalf("tools found = %d, want 3: %+v", got.Summary.ToolsFound, got.Tools)
	}
	if got.Summary.ToolsMissing == 0 {
		t.Fatalf("tools missing = 0, want coverage gaps")
	}
	if got.Summary.GlobalSkills != 7 {
		t.Fatalf("global skills = %d, want 7", got.Summary.GlobalSkills)
	}
	if got.Summary.DriftGroups == 0 {
		t.Fatalf("expected drift group for same-name different hash: %+v", got.DriftGroups)
	}
	if got.Summary.DuplicateContent == 0 {
		t.Fatalf("expected duplicate content group: %+v", got.DriftGroups)
	}
	if got.Report.Facts.GlobalSkills != got.Summary.GlobalSkills {
		t.Fatalf("report facts = %+v, want summary %+v", got.Report.Facts, got.Summary)
	}
	if !hasDiscoverReportItem(got.Report.ReviewFacts, "same_name_different_hash", "review") {
		t.Fatalf("missing same-name drift report item: %+v", got.Report.ReviewFacts)
	}
	if !hasDiscoverCoverageGap(got.Report.CoverageGaps, "missing_tool", "antigravity", "") {
		t.Fatalf("missing missing-tool coverage gap: %+v", got.Report.CoverageGaps)
	}
	if !hasDiscoverCoverageGap(got.Report.CoverageGaps, "global_skill_absent_from_detected_tool", "", "build") {
		t.Fatalf("missing global skill coverage gap: %+v", got.Report.CoverageGaps)
	}
	if !hasDiscoverRecommendation(got.Report.Recommendations, "ignore", "build", "codex", "") {
		t.Fatalf("missing codex legacy-root ignore recommendation: %+v", got.Report.Recommendations)
	}
	if hasDiscoverRecommendation(got.Report.Recommendations, "install_global", "build", "codex", "") {
		t.Fatalf("legacy codex root should not produce install_global: %+v", got.Report.Recommendations)
	}
	if hasDiscoverRecommendation(got.Report.Recommendations, "install_global", "build", "grok", "") {
		t.Fatalf("claude global skill should already be visible to grok: %+v", got.Report.Recommendations)
	}
	if !hasDiscoverRecommendation(got.Report.Recommendations, "review_drift", "review", "", "") {
		t.Fatalf("missing review drift recommendation: %+v", got.Report.Recommendations)
	}
	if !hasDiscoverRecommendation(got.Report.Recommendations, "remove", "", "", "") {
		t.Fatalf("missing duplicate removal candidate: %+v", got.Report.Recommendations)
	}
	for _, rec := range got.Report.Recommendations {
		if !rec.RequiresPlan {
			t.Fatalf("recommendation does not require dry-run plan: %+v", rec)
		}
	}
	for _, inst := range got.Installations {
		if inst.Scope != "global" {
			t.Fatalf("scope = %q, want global", inst.Scope)
		}
		if inst.ContentSHA256 == "" || inst.ContentSizeBytes == 0 {
			t.Fatalf("missing hash info: %+v", inst)
		}
		if inst.Ownership != "unmanaged" {
			t.Fatalf("ownership = %q, want unmanaged", inst.Ownership)
		}
	}
	if !hasDiscoverInstall(got.Installations, "codex", "openai-docs", filepath.Join(".codex", "skills", ".system", "openai-docs")) {
		t.Fatalf("missing nested codex skill install: %+v", got.Installations)
	}
	if !hasDiscoverInstall(got.Installations, "grok", "review", filepath.Join(".grok", "skills", "review-copy")) {
		t.Fatalf("missing declared-name skill install: %+v", got.Installations)
	}
	if !hasDiscoverInstall(got.Installations, "claude", "build", filepath.Join(".claude", "skills", "build")) {
		t.Fatalf("missing generated-name skill install: %+v", got.Installations)
	}
	if inst, ok := findDiscoverInstall(got.Installations, "claude", "build", filepath.Join(".claude", "skills", "build")); !ok || len(inst.CompatibleToolIDs) != 3 {
		t.Fatalf("missing compatible tool metadata: %+v", inst)
	}
}

func TestDiscoverTreatsClaudeGlobalSkillsAsVisibleToGrok(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "claude-visible", "---\nname: claude-visible\ncompatible: [claude, grok]\n---\n# Claude visible\n")
	if err := os.MkdirAll(filepath.Join(home, ".grok", "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if hasDiscoverCoverageGap(got.Report.CoverageGaps, "global_skill_absent_from_detected_tool", "", "claude-visible") {
		t.Fatalf("claude global skill should not be absent from grok coverage: %+v", got.Report.CoverageGaps)
	}
	if hasDiscoverRecommendation(got.Report.Recommendations, "install_global", "claude-visible", "grok", "") {
		t.Fatalf("claude global skill should not produce duplicate grok install: %+v", got.Report.Recommendations)
	}
}

func TestDiscoverClaudeGlobalGrokVisibilityRespectsCompatibility(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "claude-only", "---\nname: claude-only\nexclusive: claude\n---\n# Claude only\n")
	if err := os.MkdirAll(filepath.Join(home, ".grok", "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if !hasDiscoverCoverageGap(got.Report.CoverageGaps, "global_skill_absent_from_detected_tool", "", "claude-only") {
		t.Fatalf("incompatible claude skill should still be absent from grok coverage: %+v", got.Report.CoverageGaps)
	}
	if !hasDiscoverRecommendation(got.Report.Recommendations, "needs_port", "claude-only", "grok", "") {
		t.Fatalf("incompatible claude skill should require porting for grok: %+v", got.Report.Recommendations)
	}
}

func TestDiscoverNormalizesExclusiveCompatibility(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "grok-case", "---\nname: grok-case\nexclusive: Grok\n---\n# Grok case\n")
	if err := os.MkdirAll(filepath.Join(home, ".grok", "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	inst, ok := findDiscoverInstall(got.Installations, "claude", "grok-case", filepath.Join(".claude", "skills", "grok-case"))
	if !ok || inst.ExclusiveToolID != "grok" {
		t.Fatalf("exclusive tool = %q, want grok: %+v", inst.ExclusiveToolID, inst)
	}
	if hasDiscoverRecommendation(got.Report.Recommendations, "needs_port", "grok-case", "grok", "") {
		t.Fatalf("display-cased exclusive target should match grok: %+v", got.Report.Recommendations)
	}
}

func TestDiscoverRecommendationsRespectCompatibilityAndContextCost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "claude-only", "---\nname: claude-only\nexclusive: claude\n---\n# Claude only\n")
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "unknown-cost", "---\nname: unknown-cost\ncompatible: [claude, gemini]\n---\n# Unknown cost\n")
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "skills"), 0755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if !hasDiscoverRecommendation(got.Report.Recommendations, "needs_port", "claude-only", "codex", "") {
		t.Fatalf("missing needs-port recommendation: %+v", got.Report.Recommendations)
	}
	if !hasDiscoverRecommendation(got.Report.Recommendations, "ignore", "unknown-cost", "gemini", "") {
		t.Fatalf("missing unknown-context ignore recommendation: %+v", got.Report.Recommendations)
	}
	if hasDiscoverRecommendation(got.Report.Recommendations, "install_global", "claude-only", "codex", "") {
		t.Fatalf("exclusive skill should not produce install_global to codex: %+v", got.Report.Recommendations)
	}
}

func TestDiscoverRecommendationsIncludeProjectLocalCandidates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	repo := filepath.Join(home, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "project-only", "---\nname: project-only\n---\n# Project\n")
	writeScanSkill(t, filepath.Join(repo, ".claude", "skills"), "project-only", "---\nname: project-only\n---\n# Project\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", repo}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if !hasDiscoverRecommendation(got.Report.Recommendations, "install_project", "project-only", "", got.Projects[0].ProjectID) {
		t.Fatalf("missing project-local recommendation: %+v", got.Report.Recommendations)
	}
}

func TestDiscoverRecommendationsSkipSameHarnessProjectDuplicates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	repo := filepath.Join(home, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "copy-a", "---\nname: duplicate-project\n---\n# Project A\n")
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "copy-b", "---\nname: duplicate-project\n---\n# Project B\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", repo}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if hasDiscoverRecommendation(got.Report.Recommendations, "install_project", "duplicate-project", "", got.Projects[0].ProjectID) {
		t.Fatalf("same-harness duplicates should not produce project-local recommendation: %+v", got.Report.Recommendations)
	}
	if !hasDiscoverRecommendation(got.Report.Recommendations, "review_drift", "duplicate-project", "", "") {
		t.Fatalf("same-harness duplicates should still produce drift review: %+v", got.Report.Recommendations)
	}
}

func TestDiscoverHumanSummaryGolden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Join(home, ".codex", "skills"), "review", "---\nname: review\n---\n# Review changed\n")
	writeScanSkill(t, filepath.Join(home, ".grok", "skills"), "copy-a", "# Shared\n")
	writeScanSkill(t, filepath.Join(home, ".grok", "skills"), "copy-b", "# Shared\n")
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "shared", "---\nname: shared\n---\n# Shared\n")
	devRoot := filepath.Join(home, "dev")
	repo := filepath.Join(devRoot, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "shared", "---\nname: shared\n---\n# Shared Project\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"discover", "--global", "--projects", devRoot}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	got := normalizeDiscoverGolden(stdout.String(), home)
	want := `Discover assessment
Facts: 3 tools present, 4 tools missing, 5 global skills, 1 project-local skills, 1 projects
Review facts: 3 drift/overlap, 1 duplicate-content
Coverage gaps: 7

Exact review facts:
  - Project override: shared - shared exists both globally and in a project scope.
  - Duplicate content: 6bf161bcbe46 - Identical content appears under different names across grok/global x2.
  - Same-name drift: review - review has different content across claude/global, codex/global.
  - Same-name drift: shared - shared has different content across claude/global, codex/project.

Coverage gaps:
  - Global skill coverage gap: copy-a - copy-a is visible in grok but absent from claude, codex.
  - Global skill coverage gap: copy-b - copy-b is visible in grok but absent from claude, codex.
  - Global skill coverage gap: shared - shared is visible in claude, grok but absent from codex.
  - Missing tool coverage: Antigravity - Antigravity was not detected in this scan scope.
  - Missing tool coverage: Gemini CLI - Gemini CLI was not detected in this scan scope.
  - Missing tool coverage: Hermes - Hermes was not detected in this scan scope.
  - Missing tool coverage: OpenClaw - OpenClaw was not detected in this scan scope.

Recommendations:
  - Ignore global gap: copy-a -> codex (confidence: low) - copy-a is absent from codex, but the discovered Codex global root is legacy ~/.codex/skills; ignore until the documented .agents/skills target is modeled.
  - Ignore global gap: copy-b -> codex (confidence: low) - copy-b is absent from codex, but the discovered Codex global root is legacy ~/.codex/skills; ignore until the documented .agents/skills target is modeled.
  - Ingest unmanaged skill: copy-a (confidence: medium) - grok/global is unmanaged inventory; ingest would first create a dry-run plan and preserve the source path.
  - Ingest unmanaged skill: copy-b (confidence: medium) - grok/global is unmanaged inventory; ingest would first create a dry-run plan and preserve the source path.
  - Ingest unmanaged skill: review (confidence: medium) - claude/global is unmanaged inventory; ingest would first create a dry-run plan and preserve the source path.
  - Ingest unmanaged skill: review (confidence: medium) - codex/global is unmanaged inventory; ingest would first create a dry-run plan and preserve the source path.
  - Ingest unmanaged skill: shared (confidence: medium) - claude/global is unmanaged inventory; ingest would first create a dry-run plan and preserve the source path.
  - Ingest unmanaged skill: shared (confidence: medium) - codex/project is unmanaged inventory; ingest would first create a dry-run plan and preserve the source path.
  - Install globally: copy-a -> claude (confidence: medium) - copy-a is visible in grok and absent from claude; claude has requested/on-demand loading cost, so a global install can be planned safely.
  - Install globally: copy-b -> claude (confidence: medium) - copy-b is visible in grok and absent from claude; claude has requested/on-demand loading cost, so a global install can be planned safely.
  - Review duplicate removal: 6bf161bcbe46 (confidence: low) - Exact bytes are installed under different names; generate a dry-run plan before removing any copy.
  - Review duplicate content (confidence: high) - Identical content appears under different names across grok/global x2.
  - ... 3 more

Installations:
  - copy-a                   grok         global   $HOME/.grok/skills/copy-a
  - copy-b                   grok         global   $HOME/.grok/skills/copy-b
  - review                   claude       global   $HOME/.claude/skills/review
  - review                   codex        global   $HOME/.codex/skills/review
  - shared                   claude       global   $HOME/.claude/skills/shared
  - shared                   codex        project  $HOME/dev/app/.codex/skills/shared
`
	if got != want {
		t.Fatalf("discover summary mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDiscoverGlobalHashesFullSkillDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	skillMd := "---\nname: multi\n---\n# Multi\n"
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "multi", skillMd)
	writeScanSkill(t, filepath.Join(home, ".codex", "skills"), "multi", skillMd)
	writeFile(t, filepath.Join(home, ".claude", "skills", "multi", "scripts", "run.sh"), "echo claude\n")
	writeFile(t, filepath.Join(home, ".codex", "skills", "multi", "scripts", "run.sh"), "echo codex\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if !hasDiscoverDriftGroup(got.DriftGroups, "same_name_different_hash", "multi") {
		t.Fatalf("missing helper-file drift group: %+v", got.DriftGroups)
	}
}

func TestDiscoverPersistsInventoryAndMarksMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)

	skillPath := writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "persisted", "---\nname: persisted\n---\n# Persisted\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("first discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	db, err := state.Open(managerHome)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if got := countDiscoverTable(t, db, "discovery_scans"); got != 1 {
		t.Fatalf("discovery scans = %d, want 1", got)
	}
	var present int
	if err := db.QueryRow(`SELECT present FROM discovery_installations WHERE skill_name=?`, "persisted").Scan(&present); err != nil {
		t.Fatalf("query persisted install: %v", err)
	}
	if present != 1 {
		t.Fatalf("present = %d, want 1", present)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("repeat discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	if got := countDiscoverTable(t, db, "discovery_scans"); got != 2 {
		t.Fatalf("discovery scans after repeat = %d, want 2", got)
	}
	if got := countDiscoverTable(t, db, "discovery_installations"); got != 1 {
		t.Fatalf("discovery installations after repeat = %d, want 1", got)
	}

	if err := os.RemoveAll(skillPath); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("second discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	if got := countDiscoverTable(t, db, "discovery_scans"); got != 3 {
		t.Fatalf("discovery scans after removal = %d, want 3", got)
	}
	var missingSince string
	if err := db.QueryRow(`SELECT present, COALESCE(missing_since, '') FROM discovery_installations WHERE skill_name=?`, "persisted").Scan(&present, &missingSince); err != nil {
		t.Fatalf("query missing install: %v", err)
	}
	if present != 0 || missingSince == "" {
		t.Fatalf("present=%d missing_since=%q, want no-longer-present timestamp", present, missingSince)
	}
}

func TestDiscoverPersistsDriftGroupsAndOverlap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)

	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Join(home, ".codex", "skills"), "review", "---\nname: review\n---\n# Review changed\n")
	writeScanSkill(t, filepath.Join(home, ".grok", "skills"), "copy-a", "# Shared\n")
	writeScanSkill(t, filepath.Join(home, ".grok", "skills"), "copy-b", "# Shared\n")

	devRoot := filepath.Join(home, "dev")
	repo := filepath.Join(devRoot, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "shared", "---\nname: shared\n---\n# Shared\n")
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "shared", "---\nname: shared\n---\n# Shared Project\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global", "--projects", devRoot}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if count := countDiscoverRecommendations(got.Report.Recommendations, "review_drift", "shared"); count != 2 {
		t.Fatalf("shared review recommendations = %d, want overlap and drift recommendations: %+v", count, got.Report.Recommendations)
	}

	db, err := state.Open(managerHome)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	assertPersistedDriftGroup(t, db, "same_name_different_hash", "review", "", 2)
	assertPersistedDriftGroup(t, db, "same_hash_different_name", "", "", 2)
	assertPersistedDriftGroup(t, db, "global_project_overlap", "shared", "", 2)

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("global-only discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	assertPersistedDriftGroup(t, db, "global_project_overlap", "shared", "", 2)
}

func TestDiscoverPersistsProjectMissingForRelativeRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)

	devRoot := filepath.Join(home, "dev")
	repo := filepath.Join(devRoot, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	skillPath := writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "project-skill", "---\nname: project-skill\n---\n# Project\n")
	t.Chdir(devRoot)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", "."}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("first discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	if err := os.RemoveAll(skillPath); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--projects", "."}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("second discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	db, err := state.Open(managerHome)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var present int
	var missingSince string
	if err := db.QueryRow(`SELECT present, COALESCE(missing_since, '') FROM discovery_installations WHERE skill_name=?`, "project-skill").Scan(&present, &missingSince); err != nil {
		t.Fatalf("query project install: %v", err)
	}
	if present != 0 || missingSince == "" {
		t.Fatalf("present=%d missing_since=%q, want relative-root scan to mark missing", present, missingSince)
	}
}

func TestDiscoverProjectsPrunesGeneratedDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	devRoot := filepath.Join(home, "dev")
	repo := filepath.Join(devRoot, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "project-skill", "---\nname: project-skill\n---\n# Project\n")
	writeFile(t, filepath.Join(repo, ".cursor", "rules", "react.mdc"), "# Cursor rule\n")
	writeFile(t, filepath.Join(repo, "AGENTS.md"), "# Agent instructions\n")
	managerHome := filepath.Join(home, ".skills-manager")
	if err := writeManifest(manifestPath(managerHome, repo), installManifest{
		Version:      1,
		ProjectPath:  repo,
		ProjectSlug:  projectSlug(repo),
		ManagedPaths: []string{".codex/skills/project-skill"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(manifestPath(managerHome, discoverWalkRoot(repo)), installManifest{
		Version:      1,
		ProjectPath:  discoverWalkRoot(repo),
		ProjectSlug:  projectSlug(discoverWalkRoot(repo)),
		ManagedPaths: []string{".codex/skills/project-skill"},
	}); err != nil {
		t.Fatal(err)
	}

	generatedRepo := filepath.Join(devRoot, "node_modules", "ignored")
	if err := os.MkdirAll(filepath.Join(generatedRepo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScanSkill(t, filepath.Join(generatedRepo, ".codex", "skills"), "ignored", "---\nname: ignored\n---\n# Ignored\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", devRoot}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if got.Summary.ProjectsFound != 1 {
		t.Fatalf("projects found = %d, want 1: %+v", got.Summary.ProjectsFound, got.Projects)
	}
	if got.Summary.ProjectLocalSkills != 3 {
		t.Fatalf("project skills = %d, want 3: %+v", got.Summary.ProjectLocalSkills, got.Installations)
	}
	if got.Summary.ToolsFound != 3 {
		t.Fatalf("tools found = %d, want 3: %+v", got.Summary.ToolsFound, got.Tools)
	}
	if !hasDiscoverToolPattern(got.Tools, "codex", ".codex/skills") {
		t.Fatalf("missing codex project pattern: %+v", got.Tools)
	}
	if !hasDiscoverToolPattern(got.Tools, "cursor", ".cursor/rules") {
		t.Fatalf("missing cursor project pattern: %+v", got.Tools)
	}
	if !hasDiscoverToolPattern(got.Tools, "agents_md", "AGENTS.md") {
		t.Fatalf("missing AGENTS.md project pattern: %+v", got.Tools)
	}
	if !hasDiscoverOwnership(got.Installations, "codex", "project-skill", true, "manager") {
		t.Fatalf("missing managed project install ownership: %+v", got.Installations)
	}
	if !hasDiscoverOwnership(got.Installations, "cursor", "react", false, "unmanaged") {
		t.Fatalf("missing unmanaged project install ownership: %+v", got.Installations)
	}
	for _, inst := range got.Installations {
		if strings.Contains(inst.SourcePath, "node_modules") {
			t.Fatalf("generated path should be pruned: %+v", inst)
		}
		if inst.ProjectID == "" || inst.Scope != "project" {
			t.Fatalf("bad project install fields: %+v", inst)
		}
	}
}

func TestDiscoverProjectsSkipsSecretBearingFilesAndDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	devRoot := filepath.Join(home, "dev")
	repo := filepath.Join(devRoot, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".cursor", "rules", "react.mdc"), "# Cursor rule\n")
	writeFile(t, filepath.Join(repo, ".cursor", "rules", "secrets.mdc"), "OPENAI_API_KEY=sk-discoversecret123456789\n")
	safeSkillBody := "---\nname: safe-skill\n---\n# Safe\n"
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "safe-skill", safeSkillBody)
	writeFile(t, filepath.Join(repo, ".codex", "skills", "safe-skill", ".env"), "PASSWORD=discover-nested-secret\n")
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills", ".env", "leaked"), "leaked", "---\nname: leaked\n---\n# Leaked\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", devRoot}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("discover returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if !hasDiscoverInstall(got.Installations, "cursor", "react", filepath.Join(".cursor", "rules", "react.mdc")) {
		t.Fatalf("missing normal cursor rule: %+v", got.Installations)
	}
	safeSkill, ok := findDiscoverInstall(got.Installations, "codex", "safe-skill", filepath.Join(".codex", "skills", "safe-skill"))
	if !ok {
		t.Fatalf("missing safe skill: %+v", got.Installations)
	}
	if safeSkill.ContentSizeBytes != int64(len(safeSkillBody)) {
		t.Fatalf("safe skill content size = %d, want SKILL.md-only size %d", safeSkill.ContentSizeBytes, len(safeSkillBody))
	}
	for _, inst := range got.Installations {
		if strings.Contains(inst.SourcePath, "secrets.mdc") || strings.Contains(inst.SourcePath, ".env") || inst.SkillName == "leaked" {
			t.Fatalf("secret-bearing path was discovered: %+v", inst)
		}
	}
	logText := readFile(t, filepath.Join(home, ".skills-manager", "logs", "skills-manager.log"))
	if !strings.Contains(logText, "discover.audit") || strings.Contains(logText, "sk-discoversecret") {
		t.Fatalf("privacy audit log missing safe discover record:\n%s", logText)
	}
}

func TestDiscoverProjectsSavesReusesAndRemovesApprovedRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)

	devRoot := filepath.Join(home, "dev")
	repo := filepath.Join(devRoot, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "project-skill", "---\nname: project-skill\n---\n# Project\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", devRoot, "--save-project-roots"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("save roots discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var saved discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &saved); err != nil {
		t.Fatalf("unmarshal saved discover output: %v\n%s", err, stdout.String())
	}
	approvedRoot := discoverWalkRoot(devRoot)
	if saved.Summary.ProjectsFound != 1 || len(saved.ApprovedProjectRoots) != 1 || saved.ApprovedProjectRoots[0] != approvedRoot {
		t.Fatalf("saved output = %+v, want one project and saved root %q", saved, approvedRoot)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--list-project-roots"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("list roots code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	var listed discoverProjectRootsOutput
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal list output: %v\n%s", err, stdout.String())
	}
	if len(listed.ProjectRoots) != 1 || listed.ProjectRoots[0] != approvedRoot {
		t.Fatalf("list output = %+v, want saved root %q", listed, approvedRoot)
	}

	staleRoot := filepath.Join(home, "missing-dev")
	writeFile(t, discoverProjectRootsPath(managerHome), approvedRoot+"\n"+staleRoot+"\n")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--saved-project-roots"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("saved-roots discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	var reused discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &reused); err != nil {
		t.Fatalf("unmarshal reused discover output: %v\n%s", err, stdout.String())
	}
	if reused.Summary.ProjectsFound != 1 || reused.Summary.ProjectLocalSkills != 1 {
		t.Fatalf("reused summary = %+v, want saved root project skill", reused.Summary)
	}
	if len(reused.SkippedProjectRoots) != 1 || reused.SkippedProjectRoots[0] != staleRoot {
		t.Fatalf("skipped roots = %+v, want stale root %q", reused.SkippedProjectRoots, staleRoot)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--remove-project-root", devRoot}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("remove root code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	var removed discoverProjectRootsOutput
	if err := json.Unmarshal(stdout.Bytes(), &removed); err != nil {
		t.Fatalf("unmarshal remove output: %v\n%s", err, stdout.String())
	}
	if !removed.Updated || !removed.Removed || len(removed.ProjectRoots) != 1 || removed.ProjectRoots[0] != staleRoot {
		t.Fatalf("remove output = %+v, want only stale root remaining", removed)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--saved-project-roots"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("empty saved-roots discover code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	var empty discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &empty); err != nil {
		t.Fatalf("unmarshal empty saved discover output: %v\n%s", err, stdout.String())
	}
	if empty.Summary.ProjectsFound != 0 || empty.Summary.ProjectLocalSkills != 0 {
		t.Fatalf("empty saved roots summary = %+v, want no projects", empty.Summary)
	}
	if len(empty.SkippedProjectRoots) != 1 || empty.SkippedProjectRoots[0] != staleRoot {
		t.Fatalf("empty skipped roots = %+v, want stale root %q", empty.SkippedProjectRoots, staleRoot)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--remove-project-root", staleRoot}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("remove stale root code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}
	var removedStale discoverProjectRootsOutput
	if err := json.Unmarshal(stdout.Bytes(), &removedStale); err != nil {
		t.Fatalf("unmarshal stale remove output: %v\n%s", err, stdout.String())
	}
	if !removedStale.Updated || !removedStale.Removed || len(removedStale.ProjectRoots) != 0 {
		t.Fatalf("stale remove output = %+v, want empty roots", removedStale)
	}
}

func TestDiscoverProjectsAcceptsGitFileWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	repo := filepath.Join(home, "worktree")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	commonGitDir := filepath.Join(home, "main.git")
	worktreeGitDir := filepath.Join(commonGitDir, "worktrees", "worktree")
	if err := os.MkdirAll(worktreeGitDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(worktreeGitDir, "commondir"), "../..\n")
	writeFile(t, filepath.Join(commonGitDir, "config"), "[remote \"origin\"]\n\turl = https://example.com/repo.git\n")
	writeFile(t, filepath.Join(repo, ".git"), "gitdir: "+worktreeGitDir+"\n")
	writeScanSkill(t, filepath.Join(repo, ".claude", "skills"), "worktree-skill", "---\nname: worktree-skill\n---\n# Worktree\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", repo}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if got.Summary.ProjectsFound != 1 || got.Summary.ProjectLocalSkills != 1 {
		t.Fatalf("summary = %+v, want one project and one project-local skill", got.Summary)
	}
	if got.Projects[0].RepoRemote != "https://example.com/repo.git" {
		t.Fatalf("repo remote = %q, want worktree origin", got.Projects[0].RepoRemote)
	}
}

func TestDiscoverGlobalIncludesSymlinkedSkillDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	librarySkill := filepath.Join(home, ".skills-manager", "library", "linked-skill")
	writeFile(t, filepath.Join(librarySkill, "SKILL.md"), "---\nname: linked-skill\n---\n# Linked\n")
	linkPath := filepath.Join(home, ".claude", "skills", "linked-skill")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(librarySkill, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if got.Summary.GlobalSkills != 1 {
		t.Fatalf("global skills = %d, want 1: %+v", got.Summary.GlobalSkills, got.Installations)
	}
	inst, ok := findDiscoverInstall(got.Installations, "claude", "linked-skill", filepath.Join(".claude", "skills", "linked-skill"))
	if !ok {
		t.Fatalf("missing symlinked skill install: %+v", got.Installations)
	}
	if inst.ContentSizeBytes == 0 {
		t.Fatalf("content size = 0, want hashed symlink target content: %+v", inst)
	}
	resolvedLibrarySkill, err := filepath.EvalSymlinks(librarySkill)
	if err != nil {
		t.Fatal(err)
	}
	if inst.ContentPath != resolvedLibrarySkill {
		t.Fatalf("content path = %q, want resolved target %q", inst.ContentPath, resolvedLibrarySkill)
	}
}

func TestDiscoverGlobalFollowsSymlinkedSkillRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	realRoot := filepath.Join(home, "dotfiles", "claude-skills")
	writeScanSkill(t, realRoot, "root-linked-skill", "---\nname: root-linked-skill\n---\n# Linked root\n")
	linkRoot := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(filepath.Dir(linkRoot), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if got.Summary.GlobalSkills != 1 {
		t.Fatalf("global skills = %d, want 1: %+v", got.Summary.GlobalSkills, got.Installations)
	}
	if !hasDiscoverInstall(got.Installations, "claude", "root-linked-skill", filepath.Join(".claude", "skills", "root-linked-skill")) {
		t.Fatalf("missing symlink-root skill install: %+v", got.Installations)
	}
}

func TestDiscoverProjectsFollowsSymlinkedApprovedRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	repo := filepath.Join(home, "real", "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	writeScanSkill(t, filepath.Join(repo, ".codex", "skills"), "project-skill", "---\nname: project-skill\n---\n# Project\n")
	linkRoot := filepath.Join(home, "linked-dev")
	if err := os.Symlink(filepath.Join(home, "real"), linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", linkRoot}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, want %d\nstderr:\n%s", code, ExitSuccess, stderr.String())
	}

	var got discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal discover output: %v\n%s", err, stdout.String())
	}
	if got.Summary.ProjectsFound != 1 || got.Summary.ProjectLocalSkills != 1 {
		t.Fatalf("summary = %+v, want one project and one project-local skill", got.Summary)
	}
}

func TestDiscoverProjectsErrorsForMissingRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--projects", filepath.Join(home, "missing")}, &stdout, &stderr)
	if code != ExitOpError {
		t.Fatalf("code = %d, want %d", code, ExitOpError)
	}
	if !strings.Contains(stderr.String(), "no such file or directory") {
		t.Fatalf("stderr = %q, want missing root error", stderr.String())
	}
}

func hasDiscoverInstall(installs []discoverInstallation, toolID, skillName, pathFragment string) bool {
	_, ok := findDiscoverInstall(installs, toolID, skillName, pathFragment)
	return ok
}

func countDiscoverTable(t *testing.T, db *state.DB, name string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + name).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", name, err)
	}
	return n
}

func assertPersistedDriftGroup(t *testing.T, db *state.DB, groupType, skillName, contentSHA string, minInstalls int) {
	t.Helper()
	rows, err := db.Query(`
SELECT g.group_id, COUNT(gi.installation_id)
FROM discovery_drift_groups g
JOIN discovery_drift_group_installations gi ON gi.group_id = g.group_id
WHERE g.group_type = ?
  AND (? = '' OR g.skill_name = ?)
  AND (? = '' OR g.content_sha256 = ?)
  AND g.present = 1
GROUP BY g.group_id`, groupType, skillName, skillName, contentSHA, contentSHA)
	if err != nil {
		t.Fatalf("query persisted %s group: %v", groupType, err)
	}
	defer rows.Close()
	for rows.Next() {
		var groupID string
		var installs int
		if err := rows.Scan(&groupID, &installs); err != nil {
			t.Fatalf("scan persisted %s group: %v", groupType, err)
		}
		if installs >= minInstalls {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate persisted %s group: %v", groupType, err)
	}
	t.Fatalf("missing persisted %s group for skill=%q hash=%q with at least %d installs", groupType, skillName, contentSHA, minInstalls)
}

func findDiscoverInstall(installs []discoverInstallation, toolID, skillName, pathFragment string) (discoverInstallation, bool) {
	pathFragment = filepath.ToSlash(pathFragment)
	for _, inst := range installs {
		if inst.ToolID == toolID && inst.SkillName == skillName && strings.Contains(filepath.ToSlash(inst.SourcePath), pathFragment) {
			return inst, true
		}
	}
	return discoverInstallation{}, false
}

func hasDiscoverToolPattern(tools []discoverTool, toolID, pattern string) bool {
	for _, tool := range tools {
		if tool.ToolID != toolID || !tool.Detected || tool.Status != "present" {
			continue
		}
		for _, got := range tool.ProjectPatterns {
			if got == pattern {
				return true
			}
		}
	}
	return false
}

func hasDiscoverOwnership(installs []discoverInstallation, toolID, skillName string, managed bool, ownership string) bool {
	for _, inst := range installs {
		if inst.ToolID == toolID && inst.SkillName == skillName && inst.Managed == managed && inst.Ownership == ownership {
			return true
		}
	}
	return false
}

func hasDiscoverDriftGroup(groups []discoverDriftGroup, groupType, skillName string) bool {
	for _, group := range groups {
		if group.GroupType == groupType && group.SkillName == skillName {
			return true
		}
	}
	return false
}

func hasDiscoverReportItem(items []discoverReportItem, kind, skillName string) bool {
	for _, item := range items {
		if item.Kind == kind && item.SkillName == skillName {
			return true
		}
	}
	return false
}

func hasDiscoverCoverageGap(gaps []discoverCoverageGap, kind, toolID, skillName string) bool {
	for _, gap := range gaps {
		if gap.Kind == kind && (toolID == "" || gap.ToolID == toolID) && (skillName == "" || gap.SkillName == skillName) {
			return true
		}
	}
	return false
}

func hasDiscoverRecommendation(recs []discoverRecommendation, kind, skillName, targetToolID, targetProjectID string) bool {
	for _, rec := range recs {
		if rec.Kind != kind {
			continue
		}
		if skillName != "" && rec.SkillName != skillName {
			continue
		}
		if targetToolID != "" && rec.TargetToolID != targetToolID {
			continue
		}
		if targetProjectID != "" && rec.TargetProjectID != targetProjectID {
			continue
		}
		return true
	}
	return false
}

func countDiscoverRecommendations(recs []discoverRecommendation, kind, skillName string) int {
	count := 0
	for _, rec := range recs {
		if rec.Kind == kind && rec.SkillName == skillName {
			count++
		}
	}
	return count
}

func normalizeDiscoverGolden(output, home string) string {
	output = strings.ReplaceAll(output, discoverWalkRoot(home), "$HOME")
	output = strings.ReplaceAll(output, home, "$HOME")
	return output
}
