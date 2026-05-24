package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillMetaCanonicalYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".skill-meta.yaml")
	err := writeSkillMeta(path, skillMeta{
		Version: 1,
		Origin: skillOrigin{
			Type:        "local",
			Source:      "test",
			InstalledAt: "2026-05-24T10:00:00Z",
		},
		Categorization: skillCategorization{
			Source:        "llm",
			CategorizedAt: "2026-05-24T10:01:00Z",
			Confidence:    "high",
		},
		Categories:    []string{"Engineering"},
		Tags:          []string{"c#", "go"},
		Compatibility: compatibility{Mode: "portable"},
		Requirements: requirements{
			Tools: []toolRequirement{
				{Name: "git", Required: true},
				{Name: "jq", Required: false},
			},
			Model: modelRequirement{ToolUse: "none"},
		},
		Summary: "Build C# skills",
	})
	if err != nil {
		t.Fatalf("writeSkillMeta: %v", err)
	}
	assertFileContent(t, path, `version: 1
origin:
    type: local
    source: test
    installed_at: "2026-05-24T10:00:00Z"
categorization:
    source: llm
    categorized_at: "2026-05-24T10:01:00Z"
    confidence: high
categories:
    - Engineering
tags:
    - c#
    - go
compatibility:
    mode: portable
requirements:
    tools:
        - name: git
          required: true
        - name: jq
          required: false
    model:
        tool_use: none
summary: Build C# skills
`)
}

func TestCatalogCanonicalYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	err := writeCatalog(path, catalog{Skills: []catalogSkill{
		{
			Name:          "zeta",
			Summary:       "Last",
			Categories:    []string{"Quality"},
			Compatibility: compatibility{Mode: "portable"},
		},
		{
			Name:          "alpha",
			Summary:       "Build C# skills",
			Categories:    []string{"Engineering"},
			Tags:          []string{"c#"},
			Compatibility: compatibility{Mode: "compatible", Harnesses: []string{"claude", "codex"}},
			Requirements: requirements{Tools: []toolRequirement{
				{Name: "gh", Required: true},
				{Name: "jq", Required: false},
			}},
		},
	}})
	if err != nil {
		t.Fatalf("writeCatalog: %v", err)
	}
	assertFileContent(t, path, `version: 1
skills:
    - name: alpha
      summary: Build C# skills
      categories:
        - Engineering
      tags:
        - c#
      compatibility:
        mode: compatible
        harnesses:
            - claude
            - codex
      requirements:
        tools:
            - name: gh
              required: true
            - name: jq
              required: false
    - name: zeta
      summary: Last
      categories:
        - Quality
      compatibility:
        mode: portable
`)
}

func TestInstallLockCanonicalYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installed.lock")
	err := writeInstallLock(path, installLock{
		GeneratedAt: "2026-05-24T10:00:00Z",
		GeneratedBy: "skills-manager test",
		Skills: []installLockEntry{{
			Name:        "alpha",
			Version:     "1.2.3",
			Commit:      "abc123",
			Fingerprint: "sha",
			InstalledAt: "2026-05-24T10:01:00Z",
			Harnesses:   []string{"codex"},
		}},
	})
	if err != nil {
		t.Fatalf("writeInstallLock: %v", err)
	}
	assertFileContent(t, path, `version: 1
generated_at: "2026-05-24T10:00:00Z"
generated_by: skills-manager test
skills:
    - name: alpha
      version: 1.2.3
      commit: abc123
      fingerprint: sha
      installed_at: "2026-05-24T10:01:00Z"
      harnesses:
        - codex
`)
}

func TestProjectConfigCanonicalYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".skills", "project.yaml")
	err := writeProjectConfig(path, projectConfig{
		Name:       "demo",
		Categories: []string{"Engineering"},
		Tags:       []string{"c#"},
		Harnesses:  []string{"codex"},
	}, time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("writeProjectConfig: %v", err)
	}
	assertFileContent(t, path, `version: 1
name: demo
created: 2026-05-24
last_synced: 2026-05-24T10:00:00Z

categories:
  - "Engineering"

tags:
  - "c#"

# Active harnesses (auto-detected; user can override)
harnesses:
  - "codex"

# Per-project overrides
skills:
  always_include: []
  never_include: []
  pinned_versions: {}
`)
}

func TestReadLegacyInlineYAMLShapes(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte(`version: 1
skills:
  - name: legacy
    categories: [Engineering]
    compatibility: {mode: portable}
    requirements:
      tools: ["git"]
      mcp_servers: ["filesystem"]
      model: {tool_use: required}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := readCatalog(catalogPath)
	if err != nil {
		t.Fatalf("readCatalog: %v", err)
	}
	if len(cat.Skills) != 1 || len(cat.Skills[0].Requirements.Tools) != 1 {
		t.Fatalf("catalog = %#v", cat)
	}
	if !cat.Skills[0].Requirements.Tools[0].Required {
		t.Fatalf("legacy scalar tool should default to required")
	}
}
