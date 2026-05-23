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
