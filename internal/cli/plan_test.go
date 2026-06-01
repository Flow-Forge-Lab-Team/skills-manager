package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanEmptyInventoryReturnsEmptyPlans(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [],
  "installations": [],
  "report": {"recommendations": []}
}`)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "plan", "--inventory", inventory}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("plan exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var out actionPlanOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal plan output: %v\n%s", err, stdout.String())
	}
	if out.Plans == nil || len(out.Plans) != 0 {
		t.Fatalf("plans = %#v, want empty non-nil slice", out.Plans)
	}
	if strings.Contains(stdout.String(), `"plans": null`) {
		t.Fatalf("plans should marshal as an empty array, got:\n%s", stdout.String())
	}
}

func TestPlanJSONFileBucketsAreArrays(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "review")
	targetRoot := filepath.Join(home, ".grok", "skills")
	if err := os.MkdirAll(targetRoot, 0755); err != nil {
		t.Fatal(err)
	}
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-global",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, targetRoot, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "plan", "--inventory", inventory, "--recommendation", "rec-global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("plan exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), `null`) {
		t.Fatalf("plan JSON should use arrays for list fields, got:\n%s", stdout.String())
	}
}

func TestPlanProjectInstallPreservesUnmanagedCollision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	project := filepath.Join(home, "app")
	sourcePath := filepath.Join(project, ".claude", "skills", "review")
	targetPath := filepath.Join(project, ".codex", "skills", "review")
	writeScanSkill(t, filepath.Dir(sourcePath), "review", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Dir(targetPath), "review", "---\nname: review\n---\n# Local Review\n")
	inventory := writePlanInventoryFixture(t, home, `{
  "projects": [{"project_id": "proj-1", "root_path": %q}],
  "installations": [
    {
      "installation_id": "source-1",
      "skill_name": "review",
      "tool_id": "claude",
      "scope": "project",
      "project_id": "proj-1",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "abc",
      "managed": true,
      "ownership": "manager",
      "format": "skill_md",
      "compatible_tool_ids": ["codex"],
      "present": true
    },
    {
      "installation_id": "target-1",
      "skill_name": "review",
      "tool_id": "codex",
      "scope": "project",
      "project_id": "proj-1",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "def",
      "managed": false,
      "ownership": "unmanaged",
      "format": "skill_md",
      "present": true
    }
  ],
  "report": {"recommendations": [{
    "recommendation_id": "rec-project",
    "kind": "install_project",
    "title": "Install review into project",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "codex",
    "target_project_id": "proj-1",
    "requires_plan": true
  }]}
}`, project, sourcePath, filepath.Join(sourcePath, "SKILL.md"), targetPath, filepath.Join(targetPath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-project")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsPlanString(plan.Blockers, "target exists but is not manager-owned: "+targetPath) {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != targetPath || plan.Files.Preserve[0].Ownership != "unmanaged" {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("collision should not create/update: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
}

func TestPlanRemoveOnlyRemovesManagerOwnedInstalls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	managedPath := filepath.Join(home, "app", ".codex", "skills", "dup")
	unmanagedPath := filepath.Join(home, ".claude", "skills", "dup")
	inventory := writePlanInventoryFixture(t, home, `{
  "installations": [
    {
      "installation_id": "managed-1",
      "skill_name": "dup",
      "tool_id": "codex",
      "scope": "project",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "abc",
      "managed": true,
      "ownership": "manager",
      "format": "skill_md",
      "present": true
    },
    {
      "installation_id": "unmanaged-1",
      "skill_name": "dup",
      "tool_id": "claude",
      "scope": "global",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "abc",
      "managed": false,
      "ownership": "unmanaged",
      "format": "skill_md",
      "present": true
    }
  ],
  "report": {"recommendations": [{
    "recommendation_id": "rec-remove",
    "kind": "remove",
    "title": "Remove duplicate",
    "reason": "duplicate",
    "confidence": "low",
    "skill_name": "dup",
    "source_installation_ids": ["managed-1", "unmanaged-1"],
    "requires_plan": true
  }]}
}`, managedPath, filepath.Join(managedPath, "SKILL.md"), unmanagedPath, filepath.Join(unmanagedPath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-remove")
	plan := onlyPlan(t, out)
	if plan.Status != "ready" {
		t.Fatalf("plan status = %s, blockers = %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Remove) != 1 || plan.Files.Remove[0].Path != managedPath {
		t.Fatalf("remove files = %#v", plan.Files.Remove)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != unmanagedPath {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanRemovePreservesOneManagerOwnedDuplicate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	firstPath := filepath.Join(home, "app", ".codex", "skills", "first")
	secondPath := filepath.Join(home, "app", ".codex", "skills", "second")
	inventory := writePlanInventoryFixture(t, home, `{
  "installations": [
    {
      "installation_id": "managed-1",
      "skill_name": "first",
      "tool_id": "codex",
      "scope": "project",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "abc",
      "managed": true,
      "ownership": "manager",
      "format": "skill_md",
      "present": true
    },
    {
      "installation_id": "managed-2",
      "skill_name": "second",
      "tool_id": "codex",
      "scope": "project",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "abc",
      "managed": true,
      "ownership": "manager",
      "format": "skill_md",
      "present": true
    }
  ],
  "report": {"recommendations": [{
    "recommendation_id": "rec-remove",
    "kind": "remove",
    "title": "Remove duplicate",
    "reason": "duplicate",
    "confidence": "low",
    "source_installation_ids": ["managed-1", "managed-2"],
    "requires_plan": true
  }]}
}`, firstPath, filepath.Join(firstPath, "SKILL.md"), secondPath, filepath.Join(secondPath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-remove")
	plan := onlyPlan(t, out)
	if plan.Status != "ready" {
		t.Fatalf("plan status = %s, blockers = %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != firstPath {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
	if len(plan.Files.Remove) != 1 || plan.Files.Remove[0].Path != secondPath {
		t.Fatalf("remove files = %#v", plan.Files.Remove)
	}
}

func TestPlanIngestBlocksInvalidSkillName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, "app", ".codex", "skills", "bad")
	inventory := writePlanInventoryFixture(t, home, `{
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "../bad",
    "tool_id": "codex",
    "scope": "project",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-ingest",
    "kind": "ingest",
    "title": "Ingest unmanaged skill",
    "reason": "inventory",
    "confidence": "low",
    "skill_name": "../bad",
    "source_installation_ids": ["source-1"],
    "requires_plan": true
  }]}
}`, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-ingest")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "invalid skill name for ingest") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("invalid ingest should not create/update: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != sourcePath {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanIngestUsesSkillMarkdownSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "review")
	writeScanSkill(t, filepath.Dir(sourcePath), "review", "---\nname: review\n---\n# Review\n")
	inventory := writePlanInventoryFixture(t, home, `{
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-ingest",
    "kind": "ingest",
    "title": "Ingest unmanaged skill",
    "reason": "inventory",
    "confidence": "low",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "requires_plan": true
  }]}
}`, sourcePath, sourcePath)

	out := runPlanJSON(t, inventory, "rec-ingest")
	plan := onlyPlan(t, out)
	target := filepath.Join(home, ".skills-manager", "library", "review", "SKILL.md")
	source := filepath.Join(sourcePath, "SKILL.md")
	if plan.Status != "ready" {
		t.Fatalf("plan status = %s, blockers = %#v", plan.Status, plan.Blockers)
	}
	if !containsPlanFileWithSource(plan.Files.Create, target, source) {
		t.Fatalf("create files = %#v, want %s sourced from %s", plan.Files.Create, target, source)
	}
}

func TestPlanIngestBlocksExistingLibraryTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, "app", ".claude", "skills", "review")
	libraryPath := filepath.Join(home, ".skills-manager", "library", "review", "SKILL.md")
	writeFile(t, libraryPath, "# Existing\n")
	inventory := writePlanInventoryFixture(t, home, `{
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "project",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-ingest",
    "kind": "ingest",
    "title": "Ingest unmanaged skill",
    "reason": "inventory",
    "confidence": "low",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "requires_plan": true
  }]}
}`, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-ingest")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "library target exists but ownership is unknown") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("existing library target should not create/update: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if !containsPlanFile(plan.Files.Preserve, libraryPath) {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanIngestBlocksExistingLibraryDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, "app", ".claude", "skills", "review")
	libraryPath := filepath.Join(home, ".skills-manager", "library", "review")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(libraryPath, "notes.md"), "# Local note\n")
	inventory := writePlanInventoryFixture(t, home, `{
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "project",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-ingest",
    "kind": "ingest",
    "title": "Ingest unmanaged skill",
    "reason": "inventory",
    "confidence": "low",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "requires_plan": true
  }]}
}`, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-ingest")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "library target exists but ownership is unknown") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("existing library directory should not create/update: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if !containsPlanFile(plan.Files.Preserve, libraryPath) {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanGlobalInstallBlocksMissingToolPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "ghost")
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "claude", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "ghost",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "compatible_tool_ids": ["grok"],
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-global",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "ghost",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, filepath.Join(home, ".claude", "skills"), sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-global")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "missing global target path") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != sourcePath {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanGlobalInstallBlocksMissingTargetRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "ghost")
	targetRoot := filepath.Join(home, ".grok", "skills")
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "ghost",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "compatible_tool_ids": ["grok"],
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-global",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "ghost",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, targetRoot, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-global")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "target root is missing") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("missing target root should not create/update: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != filepath.Join(targetRoot, "ghost") {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanGlobalInstallBlocksInvalidTargetSkillName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "escape")
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "../escape",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "compatible_tool_ids": ["grok"],
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-global",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "../escape",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, filepath.Join(home, ".grok", "skills"), sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-global")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "invalid skill name for target path") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("invalid target skill should not create/update: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != sourcePath {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanGlobalInstallBlocksNonSkillSourceWithoutTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "commands", "review.md")
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "command_md",
    "compatible_tool_ids": ["grok"],
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-global",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, filepath.Join(home, ".grok", "skills"), sourcePath, sourcePath)

	out := runPlanJSON(t, inventory, "rec-global")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "global install only supports skill_md sources") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("non-skill source should not create/update: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != sourcePath {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanGlobalInstallAllowsDefaultCompatibility(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "review")
	targetRoot := filepath.Join(home, ".grok", "skills")
	if err := os.MkdirAll(targetRoot, 0755); err != nil {
		t.Fatal(err)
	}
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-global",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, targetRoot, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-global")
	plan := onlyPlan(t, out)
	if plan.Status != "ready" || len(plan.Blockers) != 0 {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if !containsPlanFile(plan.Files.Create, filepath.Join(targetRoot, "review")) {
		t.Fatalf("create files = %#v", plan.Files.Create)
	}
	if !containsPlanFile(plan.Files.Create, filepath.Join(home, ".skills-manager", "manifests", "global-grok.json")) {
		t.Fatalf("create files should include global manifest: %#v", plan.Files.Create)
	}
	if file := findPlanFile(t, plan.Files.Create, filepath.Join(targetRoot, "review")); file.CompatibilityStatus != "installable" {
		t.Fatalf("compatibility status = %q, want installable", file.CompatibilityStatus)
	}
}

func TestPlanApplyGlobalInstallRecordsManifestAndRemoveRollsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)
	sourcePath := filepath.Join(home, ".claude", "skills", "review")
	targetRoot := filepath.Join(home, ".grok", "skills")
	targetPath := filepath.Join(targetRoot, "review")
	writeScanSkill(t, filepath.Dir(sourcePath), "review", "---\nname: review\n---\n# Review\n")
	if err := os.MkdirAll(targetRoot, 0755); err != nil {
		t.Fatal(err)
	}
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-global",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, targetRoot, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "--inventory", inventory, "--recommendation", "rec-global", "--apply", "--confirm"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("plan apply exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(targetPath, "SKILL.md")); err != nil {
		t.Fatalf("expected global target copy: %v", err)
	}
	manifestPath := filepath.Join(managerHome, "manifests", "global-grok.json")
	manifest, err := readManifest(manifestPath)
	if err != nil {
		t.Fatalf("read global manifest: %v", err)
	}
	if manifest.ProjectPath != targetRoot || !containsPlanString(manifest.ManagedPaths, "review") {
		t.Fatalf("global manifest = %#v", manifest)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("discover exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var discovered discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &discovered); err != nil {
		t.Fatalf("unmarshal discover: %v", err)
	}
	foundManaged := false
	for _, inst := range discovered.Installations {
		if inst.ToolID == "grok" && inst.SkillName == "review" && inst.Managed && inst.Ownership == "manager" {
			foundManaged = true
		}
	}
	if !foundManaged {
		t.Fatalf("discover did not mark global install manager-owned: %#v", discovered.Installations)
	}

	removeInventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [
    {
      "installation_id": "source-1",
      "skill_name": "review",
      "tool_id": "claude",
      "scope": "global",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "abc",
      "managed": false,
      "ownership": "unmanaged",
      "format": "skill_md",
      "present": true
    },
    {
      "installation_id": "target-1",
      "skill_name": "review",
      "tool_id": "grok",
      "scope": "global",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "def",
      "managed": true,
      "ownership": "manager",
      "format": "skill_md",
      "present": true
    }
  ],
  "report": {"recommendations": [{
    "recommendation_id": "rec-remove",
    "kind": "remove",
    "title": "Remove duplicate",
    "reason": "rollback",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1", "target-1"],
    "requires_plan": true
  }]}
}`, targetRoot, sourcePath, filepath.Join(sourcePath, "SKILL.md"), targetPath, filepath.Join(targetPath, "SKILL.md"))

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"plan", "--inventory", removeInventory, "--recommendation", "rec-remove", "--apply", "--confirm"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("remove apply exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("target still exists after remove apply: %v", err)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("global manifest should be removed: %v", err)
	}
}

func TestPlanApplyProjectInstallKeepsProjectMetadataAndUninstallRollsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)
	project := filepath.Join(home, "app")
	sourcePath := filepath.Join(project, ".claude", "skills", "review")
	targetPath := filepath.Join(project, ".codex", "skills", "review")
	writeScanSkill(t, filepath.Dir(sourcePath), "review", "---\nname: review\n---\n# Review\n")
	writeFile(t, filepath.Join(project, ".skills", "project.yaml"), "version: 1\nname: app\nharnesses:\n  - claude\n")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	inventory := writePlanInventoryFixture(t, home, `{
  "projects": [{"project_id": "proj-1", "root_path": %q}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "project",
    "project_id": "proj-1",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": true,
    "ownership": "manager",
    "format": "skill_md",
    "compatible_tool_ids": ["codex"],
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-project",
    "kind": "install_project",
    "title": "Install review into project",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "codex",
    "target_project_id": "proj-1",
    "requires_plan": true
  }]}
}`, project, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "--inventory", inventory, "--recommendation", "rec-project", "--apply", "--confirm"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("project apply exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(targetPath, "SKILL.md")); err != nil {
		t.Fatalf("expected project target copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".skills", "project.yaml")); err != nil {
		t.Fatalf("expected project config: %v", err)
	}
	projectConfig, err := readProjectConfig(filepath.Join(project, ".skills", "project.yaml"))
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if !containsPlanString(projectConfig.Harnesses, "codex") {
		t.Fatalf("project config harnesses = %#v", projectConfig.Harnesses)
	}
	lock, err := readInstallLock(filepath.Join(project, ".skills", "installed.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if len(lock.Skills) != 1 || lock.Skills[0].Name != "review" || !containsPlanString(lock.Skills[0].Harnesses, "codex") {
		t.Fatalf("lock = %#v", lock)
	}
	manifest, err := readManifest(manifestPath(managerHome, project))
	if err != nil {
		t.Fatalf("read project manifest: %v", err)
	}
	if !containsPlanString(manifest.ManagedPaths, filepath.ToSlash(filepath.Join(".codex", "skills", "review"))) ||
		!containsPlanString(manifest.ManagedPaths, filepath.ToSlash(filepath.Join(".skills", "installed.lock"))) {
		t.Fatalf("project manifest = %#v", manifest)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"uninstall", "--project", project, "--confirm", "--no-backup"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("uninstall exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("target still exists after uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".skills", "installed.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock still exists after uninstall: %v", err)
	}
}

func TestPlanApplyProjectInstallRefusesUnmanagedConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	project := filepath.Join(home, "app")
	sourcePath := filepath.Join(project, ".claude", "skills", "review")
	targetPath := filepath.Join(project, ".codex", "skills", "review")
	writeScanSkill(t, filepath.Dir(sourcePath), "review", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Dir(targetPath), "review", "---\nname: review\n---\n# Local Review\n")
	inventory := writePlanInventoryFixture(t, home, `{
  "projects": [{"project_id": "proj-1", "root_path": %q}],
  "installations": [
    {
      "installation_id": "source-1",
      "skill_name": "review",
      "tool_id": "claude",
      "scope": "project",
      "project_id": "proj-1",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "abc",
      "managed": true,
      "ownership": "manager",
      "format": "skill_md",
      "compatible_tool_ids": ["codex"],
      "present": true
    },
    {
      "installation_id": "target-1",
      "skill_name": "review",
      "tool_id": "codex",
      "scope": "project",
      "project_id": "proj-1",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "def",
      "managed": false,
      "ownership": "unmanaged",
      "format": "skill_md",
      "present": true
    }
  ],
  "report": {"recommendations": [{
    "recommendation_id": "rec-project",
    "kind": "install_project",
    "title": "Install review into project",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "codex",
    "target_project_id": "proj-1",
    "requires_plan": true
  }]}
}`, project, sourcePath, filepath.Join(sourcePath, "SKILL.md"), targetPath, filepath.Join(targetPath, "SKILL.md"))

	before, err := os.ReadFile(filepath.Join(targetPath, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "--inventory", inventory, "--recommendation", "rec-project", "--apply", "--confirm"}, &stdout, &stderr)
	if code != ExitOpError {
		t.Fatalf("plan apply exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitOpError, stdout.String(), stderr.String())
	}
	after, err := os.ReadFile(filepath.Join(targetPath, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("unmanaged target changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestPlanGlobalInstallBlocksUnknownTargetStatError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "review")
	lockedRoot := filepath.Join(home, "locked")
	if err := os.MkdirAll(lockedRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockedRoot, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedRoot, 0700) })
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "compatible_tool_ids": ["grok"],
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-global",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, lockedRoot, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-global")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "target ownership is uncertain") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("unknown stat error should not create/update: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != filepath.Join(lockedRoot, "review") {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanProjectInstallDerivesNativeTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	project := filepath.Join(home, "app")
	claudePath := filepath.Join(project, ".claude", "skills", "review")
	codexPath := filepath.Join(project, ".codex", "skills", "review")
	inventory := writePlanInventoryFixture(t, home, `{
  "projects": [{"project_id": "proj-1", "root_path": %q}],
  "installations": [
    {
      "installation_id": "source-1",
      "skill_name": "review",
      "tool_id": "claude",
      "scope": "project",
      "project_id": "proj-1",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "abc",
      "managed": false,
      "ownership": "unmanaged",
      "format": "skill_md",
      "present": true
    },
    {
      "installation_id": "source-2",
      "skill_name": "review",
      "tool_id": "codex",
      "scope": "project",
      "project_id": "proj-1",
      "source_path": %q,
      "content_path": %q,
      "content_sha256": "def",
      "managed": false,
      "ownership": "unmanaged",
      "format": "skill_md",
      "present": true
    }
  ],
  "report": {"recommendations": [{
    "recommendation_id": "rec-project",
    "kind": "install_project",
    "title": "Keep project-local coverage",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1", "source-2"],
    "target_project_id": "proj-1",
    "requires_plan": true
  }]}
}`, project, claudePath, filepath.Join(claudePath, "SKILL.md"), codexPath, filepath.Join(codexPath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-project")
	plan := onlyPlan(t, out)
	if plan.Status != "ready" || len(plan.Blockers) != 0 {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("native project targets should not create/update existing sources: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if len(plan.Files.Skip) != 2 {
		t.Fatalf("skip files = %#v", plan.Files.Skip)
	}
}

func TestPlanProjectInstallBlocksMissingProjectWithoutTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, "app", ".claude", "skills", "review")
	inventory := writePlanInventoryFixture(t, home, `{
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "review",
    "tool_id": "claude",
    "scope": "project",
    "project_id": "proj-1",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": true,
    "ownership": "manager",
    "format": "skill_md",
    "compatible_tool_ids": ["codex"],
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-missing-project",
    "kind": "install_project",
    "title": "Install review into project",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "review",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "codex",
    "target_project_id": "missing-project",
    "requires_plan": true
  }]}
}`, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-missing-project")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "target project not found") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	if len(plan.Files.Create) != 0 || len(plan.Files.Update) != 0 {
		t.Fatalf("missing project should not create/update targets: create=%#v update=%#v", plan.Files.Create, plan.Files.Update)
	}
	if len(plan.Files.Preserve) != 1 || plan.Files.Preserve[0].Path != sourcePath {
		t.Fatalf("preserve files = %#v", plan.Files.Preserve)
	}
}

func TestPlanGlobalInstallBlocksIncompatibleHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "claude-only")
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "claude-only",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "compatible_tool_ids": ["claude"],
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-incompatible",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "claude-only",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, filepath.Join(home, ".grok", "skills"), sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-incompatible")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "needs-port") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	targetPath := filepath.Join(home, ".grok", "skills", "claude-only")
	if len(plan.Files.Skip) != 1 || plan.Files.Skip[0].Path != targetPath {
		t.Fatalf("skip files = %#v", plan.Files.Skip)
	}
	if plan.Files.Skip[0].CompatibilityStatus != "needs-port" || len(plan.Files.Skip[0].FollowUpActions) == 0 {
		t.Fatalf("skip compatibility = %#v", plan.Files.Skip[0])
	}
}

func TestPlanGlobalInstallBlocksDetectorExclusiveMismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "claude-detected")
	targetRoot := filepath.Join(home, ".grok", "skills")
	writeScanSkill(t, filepath.Dir(sourcePath), "claude-detected", "---\nname: claude-detected\n---\nCall AskUserQuestion before proceeding.\n")
	if err := os.MkdirAll(targetRoot, 0755); err != nil {
		t.Fatal(err)
	}
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "claude-detected",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-detected",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "claude-detected",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, targetRoot, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-detected")
	plan := onlyPlan(t, out)
	if plan.Status != "blocked" || !containsSubstring(plan.Blockers, "incompatible") {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	skip := findPlanFile(t, plan.Files.Skip, filepath.Join(targetRoot, "claude-detected"))
	if skip.CompatibilityStatus != "incompatible" || !containsSubstring(skip.FollowUpActions, "skills-manager port claude-detected --target grok") {
		t.Fatalf("skip compatibility = %#v", skip)
	}
}

func TestPlanGlobalInstallAllowsVariantBackedExclusiveTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	sourcePath := filepath.Join(home, ".claude", "skills", "ported")
	targetRoot := filepath.Join(home, ".grok", "skills")
	writeScanSkill(t, filepath.Dir(sourcePath), "ported", "---\nname: ported\nexclusive: claude\n---\n# Claude\n")
	writeFile(t, filepath.Join(sourcePath, ".variants.yaml"), "version: 1\noverrides:\n  grok: SKILL.grok.md\n")
	writeFile(t, filepath.Join(sourcePath, "SKILL.grok.md"), "---\nname: ported\ncompatible: [grok]\n---\n# Grok\n")
	if err := os.MkdirAll(targetRoot, 0755); err != nil {
		t.Fatal(err)
	}
	inventory := writePlanInventoryFixture(t, home, `{
  "tools": [{"tool_id": "grok", "global_roots": [%q], "status": "present"}],
  "installations": [{
    "installation_id": "source-1",
    "skill_name": "ported",
    "tool_id": "claude",
    "scope": "global",
    "source_path": %q,
    "content_path": %q,
    "content_sha256": "abc",
    "managed": false,
    "ownership": "unmanaged",
    "format": "skill_md",
    "exclusive_tool_id": "claude",
    "present": true
  }],
  "report": {"recommendations": [{
    "recommendation_id": "rec-variant",
    "kind": "install_global",
    "title": "Install globally",
    "reason": "coverage",
    "confidence": "medium",
    "skill_name": "ported",
    "source_installation_ids": ["source-1"],
    "target_tool_id": "grok",
    "requires_plan": true
  }]}
}`, targetRoot, sourcePath, filepath.Join(sourcePath, "SKILL.md"))

	out := runPlanJSON(t, inventory, "rec-variant")
	plan := onlyPlan(t, out)
	if plan.Status != "ready" {
		t.Fatalf("plan status/blockers = %s %#v", plan.Status, plan.Blockers)
	}
	create := findPlanFile(t, plan.Files.Create, filepath.Join(targetRoot, "ported"))
	if create.CompatibilityStatus != "installable" {
		t.Fatalf("create compatibility = %#v", create)
	}
}

func writePlanInventoryFixture(t *testing.T, home, format string, args ...interface{}) string {
	t.Helper()
	path := filepath.Join(home, "discover.json")
	writeFile(t, path, fmtJSON(format, args...))
	return path
}

func fmtJSON(format string, args ...interface{}) string {
	return strings.TrimSpace(fmt.Sprintf(format, args...)) + "\n"
}

func runPlanJSON(t *testing.T, inventory, rec string) actionPlanOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := []string{"--json", "plan", "--inventory", inventory, "--recommendation", rec}
	code := Run(args, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("plan exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var out actionPlanOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal plan output: %v\n%s", err, stdout.String())
	}
	return out
}

func onlyPlan(t *testing.T, out actionPlanOutput) actionPlan {
	t.Helper()
	if len(out.Plans) != 1 {
		t.Fatalf("plans = %d, want 1: %#v", len(out.Plans), out.Plans)
	}
	return out.Plans[0]
}

func containsPlanString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func containsPlanFile(files []actionPlanFile, want string) bool {
	for _, file := range files {
		if file.Path == want {
			return true
		}
	}
	return false
}

func containsPlanFileWithSource(files []actionPlanFile, wantPath, wantSource string) bool {
	for _, file := range files {
		if file.Path == wantPath && file.Source == wantSource {
			return true
		}
	}
	return false
}

func findPlanFile(t *testing.T, files []actionPlanFile, want string) actionPlanFile {
	t.Helper()
	for _, file := range files {
		if file.Path == want {
			return file
		}
	}
	t.Fatalf("plan file not found: %s in %#v", want, files)
	return actionPlanFile{}
}
