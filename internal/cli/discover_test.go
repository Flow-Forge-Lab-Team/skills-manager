package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if got.Summary.GlobalSkills != 6 {
		t.Fatalf("global skills = %d, want 6", got.Summary.GlobalSkills)
	}
	if got.Summary.DriftGroups == 0 {
		t.Fatalf("expected drift group for same-name different hash: %+v", got.DriftGroups)
	}
	if got.Summary.DuplicateContent == 0 {
		t.Fatalf("expected duplicate content group: %+v", got.DriftGroups)
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
	for _, inst := range got.Installations {
		if strings.Contains(inst.SourcePath, "node_modules") {
			t.Fatalf("generated path should be pruned: %+v", inst)
		}
		if inst.ProjectID == "" || inst.Scope != "project" {
			t.Fatalf("bad project install fields: %+v", inst)
		}
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
	if !hasDiscoverInstall(got.Installations, "claude", "linked-skill", filepath.Join(".claude", "skills", "linked-skill")) {
		t.Fatalf("missing symlinked skill install: %+v", got.Installations)
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
	pathFragment = filepath.ToSlash(pathFragment)
	for _, inst := range installs {
		if inst.ToolID == toolID && inst.SkillName == skillName && strings.Contains(filepath.ToSlash(inst.SourcePath), pathFragment) {
			return true
		}
	}
	return false
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
