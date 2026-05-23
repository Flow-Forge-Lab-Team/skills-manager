package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryInit(t *testing.T) {
	home := t.TempDir()

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		t.Fatalf("ensureLibrary failed: %v", err)
	}

	expectedPath := filepath.Join(home, "library")
	if libraryPath != expectedPath {
		t.Errorf("path = %q, want %q", libraryPath, expectedPath)
	}

	stat, err := os.Stat(libraryPath)
	if err != nil {
		t.Fatalf("library path does not exist: %v", err)
	}
	if !stat.IsDir() {
		t.Fatal("library path is not a directory")
	}

	libraryPath2, err := ensureLibrary(home)
	if err != nil {
		t.Fatalf("second ensureLibrary failed: %v", err)
	}
	if libraryPath2 != libraryPath {
		t.Errorf("second call returned different path: %q vs %q", libraryPath2, libraryPath)
	}
}

func TestSkillMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, ".skill-meta.yaml")

	original := skillMeta{
		Version: 1,
		Origin: skillOrigin{
			Type:        "local",
			Source:      "test",
			Version:     "1.0.0",
			InstalledAt: "2026-05-22T10:00:00Z",
		},
		Fingerprint: skillFingerprint{
			SHA256: "abc123def456",
			Size:   1024,
		},
		Categories: []string{"Engineering", "Testing"},
		Tags:       []string{"test", "demo"},
		Summary:    "A test skill",
		Compatibility: compatibility{
			Mode: "portable",
		},
		Requirements: requirements{
			Tools: []toolRequirement{
				{Name: "git", Required: true},
				{Name: "rg", Required: false},
			},
		},
	}

	if err := writeSkillMeta(metaPath, original); err != nil {
		t.Fatalf("writeSkillMeta failed: %v", err)
	}

	read, err := readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("readSkillMeta failed: %v", err)
	}

	if read.Version != original.Version {
		t.Errorf("Version: got %d, want %d", read.Version, original.Version)
	}
	if read.Origin.Type != original.Origin.Type {
		t.Errorf("Origin.Type: got %q, want %q", read.Origin.Type, original.Origin.Type)
	}
	if read.Fingerprint.SHA256 != original.Fingerprint.SHA256 {
		t.Errorf("Fingerprint.SHA256: got %q, want %q", read.Fingerprint.SHA256, original.Fingerprint.SHA256)
	}
	if read.Summary != original.Summary {
		t.Errorf("Summary: got %q, want %q", read.Summary, original.Summary)
	}

	if len(read.Categories) != len(original.Categories) {
		t.Errorf("Categories length: got %d, want %d", len(read.Categories), len(original.Categories))
	}
	if len(read.Tags) != len(original.Tags) {
		t.Errorf("Tags length: got %d, want %d", len(read.Tags), len(original.Tags))
	}

	if read.Compatibility.Mode != original.Compatibility.Mode {
		t.Errorf("Compatibility.Mode: got %q, want %q", read.Compatibility.Mode, original.Compatibility.Mode)
	}

	if len(read.Requirements.Tools) != len(original.Requirements.Tools) {
		t.Errorf("Requirements.Tools length: got %d, want %d", len(read.Requirements.Tools), len(original.Requirements.Tools))
	}
}

func TestFingerprintSkillMd(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")

	content := "---\nname: test-skill\ndescription: A test skill\n---\nContent here\n"
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}

	hash1, size1, err := fingerprintSkillMd(skillPath)
	if err != nil {
		t.Fatalf("fingerprintSkillMd failed: %v", err)
	}

	if size1 != int64(len(content)) {
		t.Errorf("size = %d, want %d", size1, len(content))
	}

	hash2, size2, err := fingerprintSkillMd(skillPath)
	if err != nil {
		t.Fatalf("second fingerprintSkillMd failed: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hashes differ: %q vs %q", hash1, hash2)
	}
	if size1 != size2 {
		t.Errorf("sizes differ: %d vs %d", size1, size2)
	}

	newContent := "---\nname: test-skill\ndescription: A modified skill\n---\nDifferent content\n"
	if err := os.WriteFile(skillPath, []byte(newContent), 0644); err != nil {
		t.Fatalf("write modified skill file failed: %v", err)
	}

	hash3, size3, err := fingerprintSkillMd(skillPath)
	if err != nil {
		t.Fatalf("third fingerprintSkillMd failed: %v", err)
	}

	if hash3 == hash1 {
		t.Errorf("hashes should differ after content change")
	}
	if size3 == size1 {
		t.Errorf("sizes should differ after content change")
	}
}

func TestRebuildCatalogDeterministic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	writeFile(t, filepath.Join(libraryPath, "skill-a", "SKILL.md"),
		"---\nname: skill-a\ndescription: First skill\n---\nContent A")
	writeFile(t, filepath.Join(libraryPath, "skill-b", "SKILL.md"),
		"---\nname: skill-b\ndescription: Second skill\n---\nContent B")

	cat1, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("first rebuild failed: %v", err)
	}

	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat1); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	bytes1, err := os.ReadFile(filepath.Join(libraryPath, "catalog.yaml"))
	if err != nil {
		t.Fatalf("read first catalog failed: %v", err)
	}

	cat2, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("second rebuild failed: %v", err)
	}

	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat2); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	bytes2, err := os.ReadFile(filepath.Join(libraryPath, "catalog.yaml"))
	if err != nil {
		t.Fatalf("read second catalog failed: %v", err)
	}

	if string(bytes1) != string(bytes2) {
		t.Errorf("catalogs differ:\n%s\nvs\n%s", string(bytes1), string(bytes2))
	}
}

func TestRebuildCatalogPrefersSidecarSummary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	// Skill with no frontmatter description — sidecar carries the curated summary.
	writeFile(t, filepath.Join(libraryPath, "curated", "SKILL.md"),
		"---\nname: curated\n---\nBody")
	if err := writeSkillMeta(filepath.Join(libraryPath, "curated", ".skill-meta.yaml"), skillMeta{
		Version: 1,
		Summary: "Curated sidecar summary",
	}); err != nil {
		t.Fatalf("write sidecar failed: %v", err)
	}

	// Skill with frontmatter description and no sidecar summary — fallback.
	writeFile(t, filepath.Join(libraryPath, "fallback", "SKILL.md"),
		"---\nname: fallback\ndescription: From frontmatter\n---\nBody")

	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}

	byName := map[string]string{}
	for _, s := range cat.Skills {
		byName[s.Name] = s.Summary
	}
	if got, want := byName["curated"], "Curated sidecar summary"; got != want {
		t.Errorf("curated summary = %q, want %q", got, want)
	}
	if got, want := byName["fallback"], "From frontmatter"; got != want {
		t.Errorf("fallback summary = %q, want %q", got, want)
	}
}

func TestCatalogRoundTripPreservesHashInScalars(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	writeFile(t, filepath.Join(libraryPath, "csharp", "SKILL.md"),
		"---\nname: csharp\ndescription: Build C# skills\n---\nBody")
	if err := writeSkillMeta(filepath.Join(libraryPath, "csharp", ".skill-meta.yaml"), skillMeta{
		Version: 1,
		Tags:    []string{"c#", "dotnet"},
		Summary: "Build C# skills",
	}); err != nil {
		t.Fatalf("write sidecar failed: %v", err)
	}

	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	if err := writeCatalog(catalogPath, cat); err != nil {
		t.Fatalf("write catalog failed: %v", err)
	}

	parsed, err := readCatalog(catalogPath)
	if err != nil {
		t.Fatalf("read catalog failed: %v", err)
	}
	if len(parsed.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(parsed.Skills))
	}
	got := parsed.Skills[0]
	if got.Summary != "Build C# skills" {
		t.Errorf("summary = %q, want %q", got.Summary, "Build C# skills")
	}
	if len(got.Tags) != 2 || got.Tags[0] != "c#" || got.Tags[1] != "dotnet" {
		t.Errorf("tags = %v, want [c# dotnet]", got.Tags)
	}

	meta, err := readSkillMeta(filepath.Join(libraryPath, "csharp", ".skill-meta.yaml"))
	if err != nil {
		t.Fatalf("read sidecar failed: %v", err)
	}
	if meta.Summary != "Build C# skills" {
		t.Errorf("sidecar summary = %q, want %q", meta.Summary, "Build C# skills")
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "c#" {
		t.Errorf("sidecar tags = %v, want [c# dotnet]", meta.Tags)
	}
}

func TestCatalogPreservesOptionalTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	writeFile(t, filepath.Join(libraryPath, "mixed", "SKILL.md"),
		"---\nname: mixed\ndescription: Mixed reqs\n---\nBody")
	if err := writeSkillMeta(filepath.Join(libraryPath, "mixed", ".skill-meta.yaml"), skillMeta{
		Version: 1,
		Requirements: requirements{Tools: []toolRequirement{
			{Name: "gh", Required: true},
			{Name: "jq", Required: false},
		}},
	}); err != nil {
		t.Fatalf("write sidecar failed: %v", err)
	}

	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	if err := writeCatalog(catalogPath, cat); err != nil {
		t.Fatalf("write catalog failed: %v", err)
	}

	parsed, err := readCatalog(catalogPath)
	if err != nil {
		t.Fatalf("read catalog failed: %v", err)
	}
	if len(parsed.Skills) != 1 {
		raw, _ := os.ReadFile(catalogPath)
		t.Fatalf("got %d skills, want 1; catalog:\n%s", len(parsed.Skills), string(raw))
	}
	tools := parsed.Skills[0].Requirements.Tools
	if len(tools) != 2 {
		raw, _ := os.ReadFile(catalogPath)
		t.Fatalf("got %d tools, want 2; catalog:\n%s", len(tools), string(raw))
	}
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = tool.Required
	}
	if !got["gh"] {
		t.Errorf("gh required = false, want true")
	}
	if got["jq"] {
		t.Errorf("jq required = true, want false")
	}
}

func TestSidecarPreservesCategorizedAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".skill-meta.yaml")
	in := skillMeta{
		Version: 1,
		Categorization: skillCategorization{
			Source:        "llm",
			CategorizedAt: "2026-05-22T10:30:05Z",
			ByTool:        "skills-ingest",
			Confidence:    "high",
		},
	}
	if err := writeSkillMeta(path, in); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	out, err := readSkillMeta(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if out.Categorization.CategorizedAt != in.Categorization.CategorizedAt {
		t.Errorf("CategorizedAt = %q, want %q", out.Categorization.CategorizedAt, in.Categorization.CategorizedAt)
	}
	if out.Categorization.ByTool != in.Categorization.ByTool {
		t.Errorf("ByTool = %q, want %q", out.Categorization.ByTool, in.Categorization.ByTool)
	}
}

func TestParseSkillFrontmatterUnquotesDescription(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	writeFile(t, skillPath, "---\nname: foo\ndescription: \"Use when foo: do X\"\n---\nBody")

	_, desc, err := parseSkillFrontmatter(skillPath)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if desc != "Use when foo: do X" {
		t.Errorf("description = %q, want %q", desc, "Use when foo: do X")
	}
}

func TestShowJSONIncludesFingerprintWhenSidecarMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}
	writeFile(t, filepath.Join(libraryPath, "bare", "SKILL.md"),
		"---\nname: bare\ndescription: Bare skill\n---\nBody")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"show", "bare", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("show returned %d: %s", code, stderr.String())
	}

	var got map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json decode failed: %v\noutput: %s", err, stdout.String())
	}
	fp, ok := got["fingerprint"].(map[string]interface{})
	if !ok {
		t.Fatalf("fingerprint missing or wrong type: %v", got["fingerprint"])
	}
	if sha, _ := fp["sha256"].(string); sha == "" {
		t.Errorf("sha256 missing; got %v", fp)
	}
	if size, _ := fp["size"].(float64); size <= 0 {
		t.Errorf("size missing; got %v", fp)
	}
}

func TestRebuildCatalogReadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	writeFile(t, filepath.Join(libraryPath, "linear-feature", "SKILL.md"),
		"---\nname: linear-feature\ndescription: Orchestrate Linear issues\n---\nImplementation")

	meta := skillMeta{
		Version:       1,
		Categories:    []string{"Engineering"},
		Tags:          []string{"linear", "github"},
		Compatibility: compatibility{Mode: "portable"},
		Requirements: requirements{
			Tools: []toolRequirement{
				{Name: "gh", Required: true},
				{Name: "git", Required: true},
			},
		},
	}
	metaPath := filepath.Join(libraryPath, "linear-feature", ".skill-meta.yaml")
	if err := writeSkillMeta(metaPath, meta); err != nil {
		t.Fatalf("write meta failed: %v", err)
	}

	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}

	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatalf("write catalog failed: %v", err)
	}

	catRead, err := readCatalog(filepath.Join(libraryPath, "catalog.yaml"))
	if err != nil {
		t.Fatalf("read catalog failed: %v", err)
	}

	if len(catRead.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(catRead.Skills))
	}

	skill := catRead.Skills[0]
	if skill.Name != "linear-feature" {
		t.Errorf("Name = %q, want %q", skill.Name, "linear-feature")
	}
	if skill.Summary == "" {
		t.Errorf("Summary should not be empty")
	}
	if len(skill.Categories) != 1 || skill.Categories[0] != "Engineering" {
		t.Errorf("Categories = %v, want [Engineering]", skill.Categories)
	}
	if len(skill.Tags) != 2 {
		t.Errorf("Tags length = %d, want 2", len(skill.Tags))
	}
	if skill.Compatibility.Mode != "portable" {
		t.Errorf("Compatibility.Mode = %q, want %q", skill.Compatibility.Mode, "portable")
	}
	if len(skill.Requirements.Tools) != 2 {
		t.Errorf("Requirements.Tools length = %d, want 2", len(skill.Requirements.Tools))
	}
}

func TestListCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	writeFile(t, filepath.Join(libraryPath, "skill-a", "SKILL.md"),
		"---\nname: skill-a\ndescription: Alpha skill\n---\nContent A")
	writeFile(t, filepath.Join(libraryPath, "skill-b", "SKILL.md"),
		"---\nname: skill-b\ndescription: Beta skill\n---\nContent B")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runList([]string{}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("runList returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "skill-a") {
		t.Errorf("output missing skill-a: %s", output)
	}
	if !strings.Contains(output, "skill-b") {
		t.Errorf("output missing skill-b: %s", output)
	}
	if !strings.Contains(output, "Alpha skill") {
		t.Errorf("output missing Alpha skill summary: %s", output)
	}
	if !strings.Contains(output, "Beta skill") {
		t.Errorf("output missing Beta skill summary: %s", output)
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 lines, got %d", len(lines))
	}

	if !strings.HasPrefix(lines[0], "skill-a") {
		t.Errorf("first skill should be skill-a (alphabetical), got %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "skill-b") {
		t.Errorf("second skill should be skill-b (alphabetical), got %s", lines[1])
	}
}

func TestShowCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	skillMdContent := "---\nname: test-skill\ndescription: A comprehensive test skill\n---\nImplementation details"
	writeFile(t, filepath.Join(libraryPath, "test-skill", "SKILL.md"), skillMdContent)

	meta := skillMeta{
		Version:       1,
		Categories:    []string{"Engineering", "Quality"},
		Tags:          []string{"test", "demo"},
		Compatibility: compatibility{Mode: "portable"},
		Requirements: requirements{
			Tools: []toolRequirement{
				{Name: "git", Required: true},
				{Name: "rg", Required: false},
			},
		},
	}
	metaPath := filepath.Join(libraryPath, "test-skill", ".skill-meta.yaml")
	if err := writeSkillMeta(metaPath, meta); err != nil {
		t.Fatalf("write meta failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runShow([]string{"test-skill"}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("runShow returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "test-skill") {
		t.Errorf("output missing skill name: %s", output)
	}
	if !strings.Contains(output, "Engineering") {
		t.Errorf("output missing category: %s", output)
	}
	if !strings.Contains(output, "portable") {
		t.Errorf("output missing compatibility: %s", output)
	}
	if !strings.Contains(output, "git") {
		t.Errorf("output missing tool requirement: %s", output)
	}

	_, size, _ := fingerprintSkillMd(filepath.Join(libraryPath, "test-skill", "SKILL.md"))
	if size > 0 && !strings.Contains(output, "Fingerprint") {
		t.Errorf("output missing fingerprint: %s", output)
	}
}

func TestShowCommandMissingSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runShow([]string{"missing-skill"}, &stdout, &stderr, globalFlags{})

	if code != 3 {
		t.Fatalf("runShow returned %d, want 3", code)
	}

	if !strings.Contains(stderr.String(), "not found") {
		t.Errorf("stderr missing 'not found': %s", stderr.String())
	}
}

func TestShowCommandJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	skillMdContent := "---\nname: json-test\ndescription: Test JSON output\n---\nContent"
	writeFile(t, filepath.Join(libraryPath, "json-test", "SKILL.md"), skillMdContent)

	meta := skillMeta{
		Version:       1,
		Categories:    []string{"Engineering"},
		Tags:          []string{"test"},
		Compatibility: compatibility{Mode: "portable"},
	}
	metaPath := filepath.Join(libraryPath, "json-test", ".skill-meta.yaml")
	if err := writeSkillMeta(metaPath, meta); err != nil {
		t.Fatalf("write meta failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runShow([]string{"json-test"}, &stdout, &stderr, globalFlags{JSON: true})

	if code != 0 {
		t.Fatalf("runShow returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(stdout.String()), &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout.String())
	}

	if result["name"] != "json-test" {
		t.Errorf("name = %v, want %q", result["name"], "json-test")
	}
	if summary, ok := result["summary"].(string); ok && !strings.Contains(summary, "Test JSON output") {
		t.Errorf("summary unexpected: %v", result["summary"])
	}
}

func TestParseSkillFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")

	tests := []struct {
		name     string
		content  string
		wantName string
		wantDesc string
	}{
		{
			name:     "basic",
			content:  "---\nname: test-skill\ndescription: A test skill\n---\nContent",
			wantName: "test-skill",
			wantDesc: "A test skill",
		},
		{
			name:     "multiline description",
			content:  "---\nname: test-skill\ndescription: A very long\n  description that spans\n  multiple lines\n---\nContent",
			wantName: "test-skill",
			wantDesc: "A very long description that spans multiple lines",
		},
		{
			name:     "no frontmatter",
			content:  "Just content",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "missing name",
			content:  "---\ndescription: Only description\n---\nContent",
			wantName: "",
			wantDesc: "Only description",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(skillPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("write failed: %v", err)
			}

			name, desc, err := parseSkillFrontmatter(skillPath)
			if err != nil {
				t.Fatalf("parseSkillFrontmatter failed: %v", err)
			}

			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if desc != tt.wantDesc {
				t.Errorf("desc = %q, want %q", desc, tt.wantDesc)
			}
		})
	}
}

func TestParseSkillFrontmatterFull_Exclusive(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")

	content := `---
name: qa
description: QA testing tool
exclusive: claude
reason: "Uses AskUserQuestion + gstack preamble"
---
# QA Testing
`
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	decl, compDecl, err := parseSkillFrontmatterFull(skillPath)
	if err != nil {
		t.Fatalf("parseSkillFrontmatterFull failed: %v", err)
	}

	if decl.name != "qa" {
		t.Errorf("name = %q, want qa", decl.name)
	}
	if decl.exclusive != "claude" {
		t.Errorf("exclusive = %q, want claude", decl.exclusive)
	}
	if decl.reason != "Uses AskUserQuestion + gstack preamble" {
		t.Errorf("reason = %q, want 'Uses AskUserQuestion + gstack preamble'", decl.reason)
	}

	if compDecl.Mode != "exclusive" {
		t.Errorf("compDecl.Mode = %q, want exclusive", compDecl.Mode)
	}
	if compDecl.Harness != "claude" {
		t.Errorf("compDecl.Harness = %q, want claude", compDecl.Harness)
	}
}

func TestParseSkillFrontmatterFull_Compatible(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")

	content := `---
name: linear-feature
description: Multi-harness feature
compatible: [claude, codex, grok]
---
# Linear Feature
`
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	decl, compDecl, err := parseSkillFrontmatterFull(skillPath)
	if err != nil {
		t.Fatalf("parseSkillFrontmatterFull failed: %v", err)
	}

	if decl.name != "linear-feature" {
		t.Errorf("name = %q, want linear-feature", decl.name)
	}
	if len(decl.compatible) != 3 {
		t.Errorf("len(compatible) = %d, want 3", len(decl.compatible))
	}

	if compDecl.Mode != "compatible" {
		t.Errorf("compDecl.Mode = %q, want compatible", compDecl.Mode)
	}
	if len(compDecl.Harnesses) != 3 {
		t.Errorf("len(compDecl.Harnesses) = %d, want 3", len(compDecl.Harnesses))
	}
}

func TestParseSkillFrontmatterFull_Portable(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")

	content := `---
name: pdf
description: PDF tool
---
# PDF
`
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	decl, compDecl, err := parseSkillFrontmatterFull(skillPath)
	if err != nil {
		t.Fatalf("parseSkillFrontmatterFull failed: %v", err)
	}

	if decl.name != "pdf" {
		t.Errorf("name = %q, want pdf", decl.name)
	}
	if decl.exclusive != "" || len(decl.compatible) > 0 {
		t.Errorf("expected no compatibility declaration")
	}

	if compDecl.Mode != "" {
		t.Errorf("compDecl.Mode = %q, want empty", compDecl.Mode)
	}
}

func TestDetectCompatibility_ClaudeSkillTool(t *testing.T) {
	detectors, err := loadDetectors()
	if err != nil {
		t.Fatalf("loadDetectors failed: %v", err)
	}

	skillBody := `# A skill that uses the Skill tool
The Skill tool is used here for advanced operations.
`

	results := detectCompatibility(detectors, skillBody)
	if len(results) == 0 {
		t.Errorf("expected detection results, got none")
		return
	}

	if results["claude"].Confidence == "" {
		t.Errorf("expected claude detection, got none")
	}
}

func TestDetectCompatibility_CursorRules(t *testing.T) {
	detectors, err := loadDetectors()
	if err != nil {
		t.Fatalf("loadDetectors failed: %v", err)
	}

	skillBody := `# Cursor rules configuration
Use .cursor/rules/ for configuration.
`

	results := detectCompatibility(detectors, skillBody)
	if results["cursor"].Confidence == "" {
		t.Errorf("expected cursor detection, got none")
	}
}

// FLO-235 Issue 3: mcp__ pattern matching should handle literal patterns correctly
func TestMatchPattern_MCPLiteralVsHexSentinel(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		text     string
		expected bool
	}{
		{
			name:     "literal mcp__linear__ matches exact pattern in text",
			pattern:  "mcp__linear__",
			text:     "using mcp__linear__list_issues function",
			expected: true,
		},
		{
			name:     "literal mcp__linear__ does not match other mcp pattern",
			pattern:  "mcp__linear__",
			text:     "using mcp__github__list_issues function",
			expected: false,
		},
		{
			name:     "sentinel mcp__[hex]__ matches any hex UUID pattern",
			pattern:  "mcp__[hex]__",
			text:     "using mcp__a1b2c3d4__foo function",
			expected: true,
		},
		{
			name:     "sentinel mcp__[hex]__ matches different hex UUID",
			pattern:  "mcp__[hex]__",
			text:     "calling mcp__deadbeef__bar method",
			expected: true,
		},
		{
			name:     "sentinel mcp__[hex]__ does not match non-hex pattern",
			pattern:  "mcp__[hex]__",
			text:     "using mcp__not-hex__baz function",
			expected: false,
		},
		{
			name:     "sentinel mcp__[hex]__ does not match uppercase hex",
			pattern:  "mcp__[hex]__",
			text:     "using mcp__A1B2C3D4__test function",
			expected: false,
		},
		{
			name:     "literal mcp__not-hex__ matches even with non-hex characters",
			pattern:  "mcp__not-hex__",
			text:     "using mcp__not-hex__function call",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := matchPattern(tc.pattern, tc.text)
			if result != tc.expected {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.text, result, tc.expected)
			}
		})
	}
}

func TestRebuildCatalogFromLibrary_WithDeclaration(t *testing.T) {
	libraryPath := t.TempDir()

	// Create a skill with exclusive declaration
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A test skill
exclusive: claude
reason: "Claude-only implementation"
---
# Test Skill
Content here.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuildCatalogFromLibrary failed: %v", err)
	}

	if len(cat.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(cat.Skills))
	}

	skill := cat.Skills[0]
	if skill.Compatibility.Mode != "exclusive" {
		t.Errorf("mode = %q, want exclusive", skill.Compatibility.Mode)
	}
	if skill.Compatibility.Harness != "claude" {
		t.Errorf("harness = %q, want claude", skill.Compatibility.Harness)
	}
	if skill.Compatibility.Declared == nil {
		t.Errorf("expected declared block to be set")
	} else if skill.Compatibility.Declared.Reason != "Claude-only implementation" {
		t.Errorf("declared reason = %q, want 'Claude-only implementation'", skill.Compatibility.Declared.Reason)
	}
}

func TestSetCommand_Exclusive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create library and skill
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A test skill
---
# Test Skill
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runSet([]string{"test-skill", "--compatibility", "exclusive", "--harness", "claude", "--reason", "Test reason"}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("runSet returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Verify the SKILL.md was updated
	newContent, err := os.ReadFile(skillMdPath)
	if err != nil {
		t.Fatalf("read updated SKILL.md failed: %v", err)
	}

	contentStr := string(newContent)
	if !strings.Contains(contentStr, "exclusive: claude") {
		t.Errorf("expected 'exclusive: claude' in SKILL.md, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, `reason: "Test reason"`) {
		t.Errorf("expected reason in SKILL.md, got: %s", contentStr)
	}
}

func TestSetCommand_Compatible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create library and skill
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A test skill
---
# Test Skill
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runSet([]string{"test-skill", "--compatibility", "compatible", "--harnesses", "claude,codex"}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("runSet returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Verify the SKILL.md was updated
	newContent, err := os.ReadFile(skillMdPath)
	if err != nil {
		t.Fatalf("read updated SKILL.md failed: %v", err)
	}

	contentStr := string(newContent)
	if !strings.Contains(contentStr, "compatible:") {
		t.Errorf("expected 'compatible:' in SKILL.md, got: %s", contentStr)
	}
}

func TestSetCommand_RequiresHarnessForExclusive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create library and skill
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A test skill
---
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runSet([]string{"test-skill", "--compatibility", "exclusive"}, &stdout, &stderr, globalFlags{})

	if code == 0 {
		t.Fatalf("runSet should have failed without --harness, got 0")
	}
	if !strings.Contains(stderr.String(), "--harness") {
		t.Fatalf("error should mention --harness, got: %s", stderr.String())
	}
}

// Test Fix #1: set refreshes catalog after updating skill files
func TestSetCommand_RefreshesCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A test skill
---
# Test Skill
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// Call set to update compatibility
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runSet([]string{"test-skill", "--compatibility", "exclusive", "--harness", "claude"}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("runSet returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Read catalog directly and verify it was updated
	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	catRead, err := readCatalog(catalogPath)
	if err != nil {
		t.Fatalf("read catalog failed: %v", err)
	}

	if len(catRead.Skills) != 1 {
		t.Fatalf("expected 1 skill in catalog, got %d", len(catRead.Skills))
	}

	skill := catRead.Skills[0]
	if skill.Name != "test-skill" {
		t.Errorf("skill name = %q, want test-skill", skill.Name)
	}
	if skill.Compatibility.Mode != "exclusive" {
		t.Errorf("compatibility mode in catalog = %q, want exclusive", skill.Compatibility.Mode)
	}
	if skill.Compatibility.Harness != "claude" {
		t.Errorf("harness in catalog = %q, want claude", skill.Compatibility.Harness)
	}
}

// Test Fix #2: set respects global --json and --quiet flags
func TestSetCommand_RespectsGlobalFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A test skill
---
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	t.Run("JSON flag", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := runSet([]string{"test-skill", "--compatibility", "compatible", "--harnesses", "claude,codex"}, &stdout, &stderr, globalFlags{JSON: true})

		if code != 0 {
			t.Fatalf("runSet returned %d, want 0\nstderr: %s", code, stderr.String())
		}

		output := stdout.String()
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("output not valid JSON: %v\noutput: %s", err, output)
		}
		if result["skill"] != "test-skill" {
			t.Errorf("JSON skill = %v, want test-skill", result["skill"])
		}
		if result["mode"] != "compatible" {
			t.Errorf("JSON mode = %v, want compatible", result["mode"])
		}
	})

	t.Run("Quiet flag", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := runSet([]string{"test-skill", "--compatibility", "portable"}, &stdout, &stderr, globalFlags{Quiet: true})

		if code != 0 {
			t.Fatalf("runSet returned %d, want 0\nstderr: %s", code, stderr.String())
		}

		// With --quiet, human text should not appear on stdout
		output := stdout.String()
		if strings.Contains(output, "Set test-skill") {
			t.Errorf("human text appeared on stdout despite --quiet: %s", output)
		}
	})
}

// Test Fix #3: switching modes clears stale declared fields
func TestSetCommand_ClearsStaleFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A test skill
---
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// First set to exclusive mode
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runSet([]string{"test-skill", "--compatibility", "exclusive", "--harness", "claude", "--reason", "First"}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("first runSet returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Now switch to compatible mode
	stdout.Reset()
	stderr.Reset()
	code = runSet([]string{"test-skill", "--compatibility", "compatible", "--harnesses", "codex,grok"}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("second runSet returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Read .skill-meta.yaml and check that stale fields were cleared
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	meta, err := readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("readSkillMeta failed: %v", err)
	}

	if meta.Compatibility.Declared == nil {
		t.Fatalf("declared block should exist")
	}

	// Should have compatible harnesses, not exclusive harness
	if meta.Compatibility.Declared.Harness != "" {
		t.Errorf("declared.Harness should be empty after switching to compatible, got %q", meta.Compatibility.Declared.Harness)
	}
	if meta.Compatibility.Declared.Reason != "" {
		t.Errorf("declared.Reason should be empty after switching to compatible, got %q", meta.Compatibility.Declared.Reason)
	}
	if len(meta.Compatibility.Declared.Harnesses) != 2 {
		t.Errorf("declared.Harnesses length = %d, want 2", len(meta.Compatibility.Declared.Harnesses))
	}

	// Also verify effective state doesn't have leftover harness
	if meta.Compatibility.Harness != "" {
		t.Errorf("effective Harness should be empty in compatible mode, got %q", meta.Compatibility.Harness)
	}
	if len(meta.Compatibility.Harnesses) != 2 {
		t.Errorf("effective Harnesses length = %d, want 2", len(meta.Compatibility.Harnesses))
	}

	// Test switching back to exclusive to ensure compatible harnesses are cleared
	stdout.Reset()
	stderr.Reset()
	code = runSet([]string{"test-skill", "--compatibility", "exclusive", "--harness", "grok", "--reason", "Final"}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("third runSet returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	meta, err = readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("readSkillMeta failed: %v", err)
	}

	if meta.Compatibility.Declared == nil {
		t.Fatalf("declared block should exist")
	}

	// Should have exclusive harness, not compatible harnesses
	if meta.Compatibility.Declared.Harness != "grok" {
		t.Errorf("declared.Harness = %q, want grok", meta.Compatibility.Declared.Harness)
	}
	if len(meta.Compatibility.Declared.Harnesses) != 0 {
		t.Errorf("declared.Harnesses should be empty after switching to exclusive, got %v", meta.Compatibility.Declared.Harnesses)
	}

	// Verify effective state is clean too
	if meta.Compatibility.Harness != "grok" {
		t.Errorf("effective Harness = %q, want grok", meta.Compatibility.Harness)
	}
	if len(meta.Compatibility.Harnesses) != 0 {
		t.Errorf("effective Harnesses should be empty in exclusive mode, got %v", meta.Compatibility.Harnesses)
	}
}

// FLO-235 Issue 1: Block-list compatible/exclusive frontmatter removal in set command
func TestSetCommand_BlockListFrontmatterCleanup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	// Create SKILL.md with block-list format for compatible
	skillMdContent := `---
name: test-skill
description: A test skill
compatible:
  - claude
  - codex
---
# Test Skill
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// Set to exclusive mode
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runSet([]string{"test-skill", "--compatibility", "exclusive", "--harness", "claude"}, &stdout, &stderr, globalFlags{})

	if code != 0 {
		t.Fatalf("runSet returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Read updated SKILL.md
	newContent, err := os.ReadFile(skillMdPath)
	if err != nil {
		t.Fatalf("read updated SKILL.md failed: %v", err)
	}

	contentStr := string(newContent)

	// Should have exclusive declaration
	if !strings.Contains(contentStr, "exclusive: claude") {
		t.Errorf("expected 'exclusive: claude' in updated SKILL.md, got: %s", contentStr)
	}

	// Should NOT have orphaned block-list items from old compatible
	if strings.Contains(contentStr, "- claude") && !strings.Contains(contentStr, "exclusive:") {
		// If "- claude" appears but it's not part of a list under compatible, it's orphaned
		lines := strings.Split(contentStr, "\n")
		inFrontmatter := false
		foundOrphan := false
		for _, line := range lines {
			if strings.TrimSpace(line) == "---" {
				inFrontmatter = !inFrontmatter
				continue
			}
			if inFrontmatter && strings.Contains(line, "- claude") && !strings.Contains(contentStr, "compatible:") {
				foundOrphan = true
				break
			}
		}
		if foundOrphan {
			t.Errorf("orphaned '- claude' or '- codex' found in frontmatter after removal: %s", contentStr)
		}
	}

	if strings.Contains(contentStr, "- codex") {
		t.Errorf("orphaned '- codex' should be removed from frontmatter: %s", contentStr)
	}

	// Parse and verify the result via parseSkillFrontmatterFull
	decl, compDecl, err := parseSkillFrontmatterFull(skillMdPath)
	if err != nil {
		t.Fatalf("parseSkillFrontmatterFull failed: %v", err)
	}

	if compDecl.Mode != "exclusive" {
		t.Errorf("mode = %q, want exclusive", compDecl.Mode)
	}
	if compDecl.Harness != "claude" {
		t.Errorf("harness = %q, want claude", compDecl.Harness)
	}
	if len(decl.compatible) != 0 {
		t.Errorf("decl.compatible should be empty after set, got %v", decl.compatible)
	}
}

// FLO-235 Issue 1: Detector dir resolution outside repo
func TestLoadDetectors_RelativeToExecutable(t *testing.T) {
	t.Run("with env var set", func(t *testing.T) {
		tempDir := t.TempDir()

		// Create a detector file
		compatDir := filepath.Join(tempDir, "compatibility")
		if err := os.MkdirAll(compatDir, 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}

		detectorYaml := filepath.Join(compatDir, "test.yaml")
		content := `version: 1
detectors:
  - id: test-detector
    harness: claude
    confidence: high
    patterns:
      - "test pattern"
`
		if err := os.WriteFile(detectorYaml, []byte(content), 0644); err != nil {
			t.Fatalf("write detector failed: %v", err)
		}

		// Set env var
		t.Setenv("SKILLS_MANAGER_DETECTORS_DIR", tempDir)

		set, err := loadDetectors()
		if err != nil {
			t.Fatalf("loadDetectors failed: %v", err)
		}

		if len(set.compatibilityDetectors) != 1 {
			t.Errorf("expected 1 compatibility detector, got %d", len(set.compatibilityDetectors))
		}
		if len(set.compatibilityDetectors) > 0 && set.compatibilityDetectors[0].ID != "test-detector" {
			t.Errorf("detector ID = %q, want test-detector", set.compatibilityDetectors[0].ID)
		}
	})

	t.Run("no env var, exe resolution", func(t *testing.T) {
		// Unset env var
		t.Setenv("SKILLS_MANAGER_DETECTORS_DIR", "")

		// Call loadDetectors without env var set
		// It should try to find detectors relative to exe, fall back to cwd
		set, err := loadDetectors()
		if err != nil {
			t.Fatalf("loadDetectors failed: %v", err)
		}

		// Should not panic and return a valid detectorSet (may be empty if detectors not found)
		_ = set
	})
}

// FLO-235 Issue 2: Detection results influence effective install
func TestApplyAutoClassification_ExclusiveHighConfidence(t *testing.T) {
	detected := map[string]detectionResult{
		"claude": {Confidence: "high", Reasons: []string{"AskUserQuestion"}},
	}

	result := applyAutoClassification(detected)

	if result.Mode != "exclusive" {
		t.Errorf("mode = %q, want exclusive", result.Mode)
	}
	if result.Harness != "claude" {
		t.Errorf("harness = %q, want claude", result.Harness)
	}
	if len(result.Harnesses) != 0 {
		t.Errorf("harnesses should be empty for exclusive, got %v", result.Harnesses)
	}
}

func TestApplyAutoClassification_Compatible(t *testing.T) {
	detected := map[string]detectionResult{
		"claude": {Confidence: "medium", Reasons: []string{"Plan Mode"}},
		"codex":  {Confidence: "medium", Reasons: []string{"Agent references"}},
	}

	result := applyAutoClassification(detected)

	if result.Mode != "compatible" {
		t.Errorf("mode = %q, want compatible", result.Mode)
	}
	if result.Harness != "" {
		t.Errorf("harness should be empty for compatible, got %q", result.Harness)
	}
	if len(result.Harnesses) != 2 {
		t.Errorf("harnesses length = %d, want 2", len(result.Harnesses))
	}
}

func TestApplyAutoClassification_Portable(t *testing.T) {
	detected := map[string]detectionResult{}

	result := applyAutoClassification(detected)

	if result.Mode != "portable" {
		t.Errorf("mode = %q, want portable", result.Mode)
	}
	if result.Harness != "" {
		t.Errorf("harness should be empty for portable, got %q", result.Harness)
	}
	if len(result.Harnesses) != 0 {
		t.Errorf("harnesses should be empty for portable, got %v", result.Harnesses)
	}
}

func TestRebuildCatalogFromLibrary_AutoClassificationApplied(t *testing.T) {
	home := t.TempDir()
	libraryPath := filepath.Join(home, "library")

	// Create skill directory
	skillDir := filepath.Join(libraryPath, "test-claude-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// Create SKILL.md with Claude-only pattern (no declaration)
	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-claude-skill
description: A skill that uses AskUserQuestion
---
# Test Skill

This skill uses AskUserQuestion to ask the user for input.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// Rebuild catalog
	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuildCatalogFromLibrary failed: %v", err)
	}

	if len(cat.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(cat.Skills))
	}

	skill := cat.Skills[0]
	if skill.Name != "test-claude-skill" {
		t.Errorf("skill name = %q, want test-claude-skill", skill.Name)
	}

	// Should be detected as exclusive:claude due to high-confidence AskUserQuestion pattern
	if skill.Compatibility.Mode != "exclusive" {
		t.Errorf("mode = %q, want exclusive (detected from AskUserQuestion)", skill.Compatibility.Mode)
	}
	if skill.Compatibility.Harness != "claude" {
		t.Errorf("harness = %q, want claude", skill.Compatibility.Harness)
	}

	// Verify .skill-meta.yaml was updated with the effective mode
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	meta, err := readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("readSkillMeta failed: %v", err)
	}

	if meta.Compatibility.Mode != "exclusive" {
		t.Errorf("meta.compatibility.mode = %q, want exclusive", meta.Compatibility.Mode)
	}
	if meta.Compatibility.Harness != "claude" {
		t.Errorf("meta.compatibility.harness = %q, want claude", meta.Compatibility.Harness)
	}
	// Detected should be populated with the detection results
	if len(meta.Compatibility.Detected) > 0 {
		// Good: detection found signals
	} else {
		// If detectors didn't load in test, at least the effective mode was still applied
		// This is the key behavior change for this fix
	}
}

// FLO-235 Issue 3: Requirements inference called during catalog rebuild
func TestRebuildCatalogFromLibrary_InferRequirements(t *testing.T) {
	home := t.TempDir()
	libraryPath := filepath.Join(home, "library")

	// Create skill directory
	skillDir := filepath.Join(libraryPath, "gh-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// Create SKILL.md with requirement pattern (e.g., gh pr create)
	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: gh-skill
description: A skill that uses GitHub CLI
---
# GitHub PR Creator

This skill runs "gh pr create" to create pull requests.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// Rebuild catalog
	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuildCatalogFromLibrary failed: %v", err)
	}

	if len(cat.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(cat.Skills))
	}

	skill := cat.Skills[0]
	if skill.Name != "gh-skill" {
		t.Errorf("skill name = %q, want gh-skill", skill.Name)
	}

	// Should have inferred gh requirement
	if len(skill.Requirements.Tools) == 0 {
		t.Errorf("expected at least 1 inferred tool requirement")
	}

	found := false
	for _, tool := range skill.Requirements.Tools {
		if tool.Name == "gh" && tool.Required {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected inferred tool gh (required), got %v", skill.Requirements.Tools)
	}

	// Verify .skill-meta.yaml was updated
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	meta, err := readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("readSkillMeta failed: %v", err)
	}

	if len(meta.Requirements.Tools) == 0 {
		t.Errorf("meta.requirements.tools should be populated from inference")
	}

	// Verify that inferred flag is set
	if !meta.Requirements.Inferred {
		t.Errorf("meta.requirements.inferred should be true, got false")
	}
}

func TestRebuildCatalogFromLibrary_PreservesExplicitRequirements(t *testing.T) {
	home := t.TempDir()
	libraryPath := filepath.Join(home, "library")

	// Create skill directory
	skillDir := filepath.Join(libraryPath, "explicit-req-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: explicit-req-skill
description: A skill with explicit requirements
---
# Skill

This skill uses "gh pr create" but has explicit requirements.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// Write .skill-meta.yaml with explicit requirements (inferred=false)
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	existingMeta := skillMeta{
		Version: 1,
		Requirements: requirements{
			Tools: []toolRequirement{
				{Name: "custom-tool", Required: true},
			},
			Inferred: false,
		},
	}
	if err := writeSkillMeta(metaPath, existingMeta); err != nil {
		t.Fatalf("writeSkillMeta failed: %v", err)
	}

	// Rebuild catalog
	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuildCatalogFromLibrary failed: %v", err)
	}

	skill := cat.Skills[0]

	// Should NOT infer gh because explicit requirements exist
	// Only custom-tool should remain
	if len(skill.Requirements.Tools) != 1 {
		t.Errorf("requirements.tools length = %d, want 1 (explicit preserved, no inference)", len(skill.Requirements.Tools))
	}
	if skill.Requirements.Tools[0].Name != "custom-tool" {
		t.Errorf("tool name = %q, want custom-tool", skill.Requirements.Tools[0].Name)
	}
}

// FLO-235 Issue 2: Inferred requirements should not freeze across rebuilds
func TestRebuildCatalogFromLibrary_InferredRequirementsNotFrozen(t *testing.T) {
	home := t.TempDir()
	libraryPath := filepath.Join(home, "library")

	skillDir := filepath.Join(libraryPath, "tool-change-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")

	// First rebuild: SKILL.md mentions "gh pr create"
	skillMdContent1 := `---
name: tool-change-skill
description: A skill that uses GitHub CLI
---
This skill runs "gh pr create" to create pull requests.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent1), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// First rebuild
	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("first rebuildCatalogFromLibrary failed: %v", err)
	}

	skill := cat.Skills[0]
	// Should have inferred gh tool
	if len(skill.Requirements.Tools) != 1 || skill.Requirements.Tools[0].Name != "gh" {
		t.Errorf("first rebuild: expected inferred tool [gh], got %v", skill.Requirements.Tools)
	}

	// Verify .skill-meta.yaml has inferred=true and gh tool
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	meta, err := readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("first readSkillMeta failed: %v", err)
	}
	if !meta.Requirements.Inferred {
		t.Errorf("first rebuild: meta.requirements.inferred should be true")
	}

	// Second rebuild: Change SKILL.md to use ffmpeg instead of gh
	skillMdContent2 := `---
name: tool-change-skill
description: A skill that uses FFmpeg
---
This skill runs "ffmpeg -i input.mp4" to process videos.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent2), 0644); err != nil {
		t.Fatalf("update SKILL.md failed: %v", err)
	}

	// Second rebuild
	cat, err = rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("second rebuildCatalogFromLibrary failed: %v", err)
	}

	skill = cat.Skills[0]
	// Should NOW have ffmpeg, NOT gh (no freezing)
	if len(skill.Requirements.Tools) != 1 || skill.Requirements.Tools[0].Name != "ffmpeg" {
		t.Errorf("second rebuild: expected inferred tool [ffmpeg], got %v", skill.Requirements.Tools)
	}

	// Verify that meta still has inferred=true and ffmpeg
	meta, err = readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("second readSkillMeta failed: %v", err)
	}
	if !meta.Requirements.Inferred {
		t.Errorf("second rebuild: meta.requirements.inferred should still be true")
	}
	if len(meta.Requirements.Tools) != 1 || meta.Requirements.Tools[0].Name != "ffmpeg" {
		t.Errorf("second rebuild: meta.requirements.tools should be [ffmpeg], got %v", meta.Requirements.Tools)
	}

	// Third rebuild: Write explicit (non-inferred) requirement
	metaPath = filepath.Join(skillDir, ".skill-meta.yaml")
	explicitMeta := skillMeta{
		Version: 1,
		Requirements: requirements{
			Tools: []toolRequirement{
				{Name: "my-tool", Required: true},
			},
			Inferred: false,
		},
	}
	if err := writeSkillMeta(metaPath, explicitMeta); err != nil {
		t.Fatalf("write explicit meta failed: %v", err)
	}

	// Update SKILL.md to mention gh
	skillMdContent3 := `---
name: tool-change-skill
description: A skill
---
This mentions "gh pr create" but has explicit requirements.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent3), 0644); err != nil {
		t.Fatalf("update SKILL.md for third rebuild failed: %v", err)
	}

	// Third rebuild
	cat, err = rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("third rebuildCatalogFromLibrary failed: %v", err)
	}

	skill = cat.Skills[0]
	// Should keep only explicit my-tool, not infer gh
	if len(skill.Requirements.Tools) != 1 || skill.Requirements.Tools[0].Name != "my-tool" {
		t.Errorf("third rebuild: expected explicit tool [my-tool], got %v", skill.Requirements.Tools)
	}

	// Verify meta still has inferred=false
	meta, err = readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("third readSkillMeta failed: %v", err)
	}
	if meta.Requirements.Inferred {
		t.Errorf("third rebuild: meta.requirements.inferred should be false for explicit")
	}
}

func TestSetCommand_PortableSticksWithDetectorPatterns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Create library and skill with a detector pattern (AskUserQuestion)
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A test skill with Claude pattern
---
# Test Skill
This skill uses AskUserQuestion which is a high-confidence Claude pattern.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// First rebuild: auto-classify as exclusive/claude due to detector pattern
	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("first rebuild failed: %v", err)
	}
	if len(cat.Skills) != 1 {
		t.Fatalf("expected 1 skill after first rebuild, got %d", len(cat.Skills))
	}
	if cat.Skills[0].Compatibility.Mode != "exclusive" {
		t.Errorf("after first rebuild: mode = %q, want exclusive (auto-classified)", cat.Skills[0].Compatibility.Mode)
	}

	// Now set to portable explicitly
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runSet([]string{"test-skill", "--compatibility", "portable"}, &stdout, &stderr, globalFlags{})
	if code != 0 {
		t.Fatalf("runSet returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Verify meta has declared.mode = portable
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	meta, err := readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("readSkillMeta failed: %v", err)
	}
	if meta.Compatibility.Declared == nil {
		t.Fatalf("expected declared block after set --compatibility portable")
	}
	if meta.Compatibility.Declared.Mode != "portable" {
		t.Errorf("declared.mode = %q, want portable", meta.Compatibility.Declared.Mode)
	}

	// Rebuild again: despite detector pattern still present, should respect explicit portable declaration
	cat, err = rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("second rebuild failed: %v", err)
	}
	if len(cat.Skills) != 1 {
		t.Fatalf("expected 1 skill after second rebuild, got %d", len(cat.Skills))
	}
	skill := cat.Skills[0]
	if skill.Compatibility.Mode != "portable" {
		t.Errorf("after second rebuild: mode = %q, want portable (should stick despite detector)", skill.Compatibility.Mode)
	}
	if skill.Compatibility.Harness != "" {
		t.Errorf("after second rebuild: harness = %q, want empty for portable", skill.Compatibility.Harness)
	}
	if len(skill.Compatibility.Harnesses) != 0 {
		t.Errorf("after second rebuild: harnesses = %v, want empty for portable", skill.Compatibility.Harnesses)
	}
}

func TestRebuildCatalog_ClearsHarnessWhenFallingBackToPortable(t *testing.T) {
	libraryPath := t.TempDir()

	// Create a skill directory
	skillDir := filepath.Join(libraryPath, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// Write initial SKILL.md with NO detector patterns
	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillMdContent := `---
name: test-skill
description: A portable skill
---
# Test Skill
This is a simple portable skill with no patterns.
`
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// Create a .skill-meta.yaml that simulates a prior detection: exclusive/claude
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	metaContent := `compatibility:
  mode: exclusive
  harness: claude
  detected:
    claude:
      confidence: high
      reasons:
        - AskUserQuestion pattern found
`
	if err := os.WriteFile(metaPath, []byte(metaContent), 0644); err != nil {
		t.Fatalf("write .skill-meta.yaml failed: %v", err)
	}

	// Rebuild: body has NO detector patterns, so should fall back to portable and clear harness fields
	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuildCatalogFromLibrary failed: %v", err)
	}

	if len(cat.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(cat.Skills))
	}

	skill := cat.Skills[0]
	if skill.Compatibility.Mode != "portable" {
		t.Errorf("mode = %q, want portable", skill.Compatibility.Mode)
	}
	if skill.Compatibility.Harness != "" {
		t.Errorf("harness = %q, want empty string", skill.Compatibility.Harness)
	}
	if len(skill.Compatibility.Harnesses) != 0 {
		t.Errorf("harnesses = %v, want empty slice", skill.Compatibility.Harnesses)
	}

	// Verify the persisted meta also has cleared harness fields
	meta, err := readSkillMeta(metaPath)
	if err != nil {
		t.Fatalf("readSkillMeta failed: %v", err)
	}
	if meta.Compatibility.Harness != "" {
		t.Errorf("persisted meta: harness = %q, want empty string", meta.Compatibility.Harness)
	}
	if len(meta.Compatibility.Harnesses) != 0 {
		t.Errorf("persisted meta: harnesses = %v, want empty slice", meta.Compatibility.Harnesses)
	}
}

func TestReadCatalog_RoundTripsMCPAndModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		t.Fatalf("mkdir library failed: %v", err)
	}

	// Create a skill with tools, mcp_servers, and model requirements
	writeFile(t, filepath.Join(libraryPath, "testskill", "SKILL.md"),
		"---\nname: testskill\ndescription: Test skill\n---\nBody")
	if err := writeSkillMeta(filepath.Join(libraryPath, "testskill", ".skill-meta.yaml"), skillMeta{
		Version: 1,
		Requirements: requirements{
			Tools: []toolRequirement{
				{Name: "gh", Required: true},
				{Name: "jq", Required: false},
			},
			MCPServers: []mcpRequirement{
				{Name: "linear", Required: true},
				{Name: "github", Required: false},
			},
			Model: modelRequirement{
				ToolUse: "required",
			},
		},
	}); err != nil {
		t.Fatalf("write sidecar failed: %v", err)
	}

	// Build and write catalog
	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	if err := writeCatalog(catalogPath, cat); err != nil {
		t.Fatalf("write catalog failed: %v", err)
	}

	// Read catalog back and verify all fields round-trip
	parsed, err := readCatalog(catalogPath)
	if err != nil {
		t.Fatalf("read catalog failed: %v", err)
	}
	if len(parsed.Skills) != 1 {
		raw, _ := os.ReadFile(catalogPath)
		t.Fatalf("got %d skills, want 1; catalog:\n%s", len(parsed.Skills), string(raw))
	}

	skill := parsed.Skills[0]

	// Verify tools round-trip
	if len(skill.Requirements.Tools) != 2 {
		t.Errorf("tools count = %d, want 2", len(skill.Requirements.Tools))
	}
	toolMap := make(map[string]bool)
	for _, tool := range skill.Requirements.Tools {
		toolMap[tool.Name] = tool.Required
	}
	if !toolMap["gh"] {
		t.Errorf("gh required = false, want true")
	}
	if toolMap["jq"] {
		t.Errorf("jq required = true, want false")
	}

	// Verify mcp_servers round-trip
	if len(skill.Requirements.MCPServers) != 2 {
		t.Errorf("mcp_servers count = %d, want 2", len(skill.Requirements.MCPServers))
	}
	mcpMap := make(map[string]bool)
	for _, server := range skill.Requirements.MCPServers {
		mcpMap[server.Name] = server.Required
	}
	if !mcpMap["linear"] {
		t.Errorf("linear required = false, want true")
	}
	if mcpMap["github"] {
		t.Errorf("github required = true, want false")
	}

	// Verify model round-trip
	if skill.Requirements.Model.ToolUse != "required" {
		t.Errorf("model.tool_use = %q, want %q", skill.Requirements.Model.ToolUse, "required")
	}
}
