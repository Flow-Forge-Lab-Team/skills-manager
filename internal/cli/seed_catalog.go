package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type seedCatalogRemapEntry struct {
	Name       string   `json:"name"`
	Locs       []string `json:"locs"`
	Categories []string `json:"categories"`
	Tags       []string `json:"tags"`
}

type seedCatalogResult struct {
	Updated  int      `json:"updated"`
	Skipped  int      `json:"skipped"`
	Warnings []string `json:"warnings,omitempty"`
	DryRun   bool     `json:"dry_run,omitempty"`
}

func runSeedCatalog(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	stdout := gf.outWriter(realStdout)
	from := ""
	dryRun := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				fmt.Fprintln(stderr, "usage: --from requires a remap results file")
				return ExitUsageError
			}
			from = args[i+1]
			i++
		case "--dry-run":
			dryRun = true
		case "--help", "-h":
			fmt.Fprintln(stdout, helpText("seed-catalog"))
			return ExitSuccess
		default:
			fmt.Fprintf(stderr, "unknown seed-catalog option: %s\n", args[i])
			return ExitUsageError
		}
	}
	if from == "" {
		fmt.Fprintln(stderr, "usage: skills-manager seed-catalog --from <remap-results.json> [--dry-run]")
		return ExitUsageError
	}

	entries, err := readSeedCatalogRemap(from)
	if err != nil {
		fmt.Fprintf(stderr, "read remap results: %v\n", err)
		return ExitOpError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return ExitOpError
	}
	result, err := applySeedCatalog(libraryPath, entries, dryRun)
	if err != nil {
		fmt.Fprintf(stderr, "seed catalog: %v\n", err)
		return ExitOpError
	}
	if gf.JSON {
		if err := writeJSON(realStdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}
	fmt.Fprintf(stdout, "Seed catalog: %d updated, %d skipped\n", result.Updated, result.Skipped)
	if dryRun {
		fmt.Fprintln(stdout, "Dry run; no files written.")
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", warning)
	}
	return ExitSuccess
}

func readSeedCatalogRemap(path string) ([]seedCatalogRemapEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []seedCatalogRemapEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Name = strings.TrimSpace(entries[i].Name)
	}
	return entries, nil
}

func applySeedCatalog(libraryPath string, entries []seedCatalogRemapEntry, dryRun bool) (seedCatalogResult, error) {
	remap := map[string]seedCatalogRemapEntry{}
	for _, entry := range entries {
		if entry.Name == "" {
			continue
		}
		remap[entry.Name] = entry
	}
	detectors, _ := loadDetectors()
	libraryEntries, err := os.ReadDir(libraryPath)
	if err != nil {
		return seedCatalogResult{}, err
	}

	result := seedCatalogResult{DryRun: dryRun}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, dirEntry := range libraryEntries {
		if !dirEntry.IsDir() || strings.HasPrefix(dirEntry.Name(), ".") {
			continue
		}
		skillDir := filepath.Join(libraryPath, dirEntry.Name())
		skillPath := filepath.Join(skillDir, "SKILL.md")
		if !pathExists(skillPath) {
			result.Skipped++
			continue
		}
		decl, compDecl, _ := parseSkillFrontmatterFull(skillPath)
		name := decl.name
		if name == "" {
			name = dirEntry.Name()
		}
		entry, ok := remap[name]
		if !ok {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: no remap entry", name))
			result.Skipped++
			continue
		}
		metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
		meta, _ := readSkillMeta(metaPath)
		before := meta

		meta.Categories = normalizeCategories(entry.Categories)
		meta.Tags = normalizeTags(entry.Tags)
		meta.Categorization.Source = "seed-remap"
		meta.Categorization.ByTool = "seed-catalog"
		if meta.Categorization.CategorizedAt == "" {
			meta.Categorization.CategorizedAt = now
		}
		meta.Categorization.Confidence = seedCategorizationConfidence(meta.Categories)

		if warnings := validateSeedTaxonomy(name, meta.Categories, meta.Tags); len(warnings) > 0 {
			result.Warnings = append(result.Warnings, warnings...)
		}

		body, _ := readSkillBody(skillPath)
		detected := detectCompatibility(detectors, body)
		applySeedCompatibility(&meta, compDecl, entry, detected)
		mergeSeedRequirements(&meta.Requirements, inferRequirements(detectors, body))

		if !reflect.DeepEqual(before, meta) {
			result.Updated++
			if !dryRun {
				if err := writeSeedSkillMeta(metaPath, meta); err != nil {
					return result, err
				}
			}
		} else {
			result.Skipped++
		}
	}
	sort.Strings(result.Warnings)
	if !dryRun {
		cat, err := rebuildCatalogFromLibrary(libraryPath)
		if err != nil {
			return result, err
		}
		if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
			return result, err
		}
	}
	return result, nil
}

func writeSeedSkillMeta(path string, meta skillMeta) error {
	if !sidecarHasUnmodeledRequirements(path) {
		return writeSkillMeta(path, meta)
	}

	original, err := os.ReadFile(path)
	if err != nil {
		return writeSkillMeta(path, meta)
	}
	requirementsBlock, ok := topLevelYAMLBlock(string(original), "requirements")
	if !ok {
		return writeSkillMeta(path, meta)
	}
	extras := parseRequirementExtras(requirementsBlock)

	tmp, err := os.CreateTemp(filepath.Dir(path), ".skill-meta-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	defer os.Remove(tmpPath)

	if err := writeSkillMeta(tmpPath, meta); err != nil {
		return err
	}
	generated, err := os.ReadFile(tmpPath)
	if err != nil {
		return err
	}
	preservedRequirements := renderSeedRequirements(meta.Requirements, extras)
	preserved := replaceTopLevelYAMLBlock(string(generated), "requirements", preservedRequirements)
	return os.WriteFile(path, []byte(preserved), 0644)
}

type seedRequirementExtras struct {
	Tools       map[string][]string
	MCPServers  map[string][]string
	Model       []string
	RawSections []string
}

func parseRequirementExtras(block string) seedRequirementExtras {
	lines := strings.Split(block, "\n")
	extras := seedRequirementExtras{
		Tools:      map[string][]string{},
		MCPServers: map[string][]string{},
	}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		key, _, ok := splitYAMLKey(line)
		if !ok || indent(line) != 2 {
			continue
		}
		switch key {
		case "tools":
			i = collectRequirementItemExtras(lines, i, extras.Tools)
		case "mcp_servers":
			i = collectRequirementItemExtras(lines, i, extras.MCPServers)
		case "model":
			section, next := collectIndentedBlock(lines, i, 2)
			for _, modelLine := range strings.Split(section, "\n") {
				modelKey, _, ok := splitYAMLKey(modelLine)
				if !ok || modelKey == "model" || modelKey == "tool_use" {
					continue
				}
				extras.Model = append(extras.Model, modelLine)
			}
			i = next
		case "inferred":
			continue
		default:
			section, next := collectIndentedBlock(lines, i, 2)
			extras.RawSections = append(extras.RawSections, section)
			i = next
		}
	}
	return extras
}

func collectRequirementItemExtras(lines []string, index int, out map[string][]string) int {
	for i := index + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if indent(line) <= 2 {
			return i - 1
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- name:") {
			continue
		}
		name := unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")))
		item, next := collectIndentedBlock(lines, i, 4)
		for _, itemLine := range strings.Split(item, "\n") {
			key, _, ok := splitYAMLKey(itemLine)
			if !ok || key == "name" || key == "required" {
				continue
			}
			out[name] = append(out[name], itemLine)
		}
		i = next
	}
	return len(lines) - 1
}

func collectIndentedBlock(lines []string, index int, baseIndent int) (string, int) {
	end := len(lines)
	for i := index + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" && indent(lines[i]) <= baseIndent {
			end = i
			break
		}
	}
	return strings.Join(lines[index:end], "\n"), end - 1
}

func renderSeedRequirements(req requirements, extras seedRequirementExtras) string {
	var buf strings.Builder
	fmt.Fprint(&buf, "requirements:\n")
	if req.Model.ToolUse != "" {
		fmt.Fprint(&buf, "  model:\n")
		fmt.Fprintf(&buf, "    tool_use: %q\n", req.Model.ToolUse)
		for _, extra := range extras.Model {
			fmt.Fprintf(&buf, "%s\n", extra)
		}
	} else if len(extras.Model) > 0 {
		fmt.Fprint(&buf, "  model:\n")
		for _, extra := range extras.Model {
			fmt.Fprintf(&buf, "%s\n", extra)
		}
	}
	if len(req.Tools) > 0 {
		fmt.Fprint(&buf, "  tools:\n")
		for _, tool := range req.Tools {
			fmt.Fprintf(&buf, "    - name: %q\n", tool.Name)
			fmt.Fprintf(&buf, "      required: %t\n", tool.Required)
			for _, extra := range extras.Tools[tool.Name] {
				fmt.Fprintf(&buf, "%s\n", extra)
			}
		}
	}
	if len(req.MCPServers) > 0 {
		fmt.Fprint(&buf, "  mcp_servers:\n")
		for _, server := range req.MCPServers {
			fmt.Fprintf(&buf, "    - name: %q\n", server.Name)
			fmt.Fprintf(&buf, "      required: %t\n", server.Required)
			for _, extra := range extras.MCPServers[server.Name] {
				fmt.Fprintf(&buf, "%s\n", extra)
			}
		}
	}
	for _, section := range extras.RawSections {
		fmt.Fprintf(&buf, "%s\n", section)
	}
	if req.Inferred {
		fmt.Fprint(&buf, "  inferred: true\n")
	}
	return strings.TrimRight(buf.String(), "\n")
}

func sidecarHasUnmodeledRequirements(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	block, ok := topLevelYAMLBlock(string(data), "requirements")
	if !ok {
		return false
	}
	for _, line := range strings.Split(block, "\n") {
		key, _, ok := splitYAMLKey(line)
		if !ok {
			continue
		}
		switch key {
		case "scripts", "credentials", "check", "config_hint", "source", "required_runtimes", "allow_auto_run", "min_context_tokens", "reasoning", "notes":
			return true
		}
	}
	return false
}

func topLevelYAMLBlock(content string, key string) (string, bool) {
	lines := strings.Split(content, "\n")
	start := -1
	end := len(lines)
	prefix := key + ":"
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			start = i
			continue
		}
		if start != -1 && strings.TrimSpace(line) != "" && indent(line) == 0 {
			end = i
			break
		}
	}
	if start == -1 {
		return "", false
	}
	return strings.Join(lines[start:end], "\n"), true
}

func replaceTopLevelYAMLBlock(content string, key string, replacement string) string {
	lines := strings.Split(content, "\n")
	start := -1
	end := len(lines)
	prefix := key + ":"
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			start = i
			continue
		}
		if start != -1 && strings.TrimSpace(line) != "" && indent(line) == 0 {
			end = i
			break
		}
	}
	if start == -1 {
		if strings.HasSuffix(content, "\n") {
			return content + replacement + "\n"
		}
		return content + "\n" + replacement + "\n"
	}
	var out strings.Builder
	out.WriteString(strings.Join(lines[:start], "\n"))
	if start > 0 {
		out.WriteString("\n")
	}
	out.WriteString(replacement)
	if !strings.HasSuffix(replacement, "\n") {
		out.WriteString("\n")
	}
	if end < len(lines) {
		out.WriteString(strings.Join(lines[end:], "\n"))
	}
	return out.String()
}

func applySeedCompatibility(meta *skillMeta, compDecl compatibilityDeclaration, entry seedCatalogRemapEntry, detected map[string]detectionResult) {
	if compDecl.Mode != "" {
		meta.Compatibility.Declared = &compDecl
		meta.Compatibility.Mode = compDecl.Mode
		meta.Compatibility.Harness = compDecl.Harness
		meta.Compatibility.Harnesses = compDecl.Harnesses
		meta.Compatibility.ExplicitPortable = false
		return
	}
	if meta.Compatibility.Declared != nil {
		decl := *meta.Compatibility.Declared
		meta.Compatibility.Mode = decl.Mode
		meta.Compatibility.Harness = decl.Harness
		meta.Compatibility.Harnesses = decl.Harnesses
		meta.Compatibility.ExplicitPortable = false
		return
	}
	if class := applyAutoClassification(detected); class.Mode != "" && class.Mode != "portable" {
		meta.Compatibility.Mode = class.Mode
		meta.Compatibility.Harness = class.Harness
		meta.Compatibility.Harnesses = class.Harnesses
		meta.Compatibility.ExplicitPortable = false
		return
	}
	locs := normalizeHarnesses(entry.Locs)
	if len(locs) == 1 && stringSliceContains(normalizeTags(entry.Tags), "gstack") {
		meta.Compatibility.Mode = "exclusive"
		meta.Compatibility.Harness = locs[0]
		meta.Compatibility.Harnesses = nil
		meta.Compatibility.ExplicitPortable = false
		return
	}
	if len(locs) > 0 {
		meta.Compatibility.Mode = "compatible"
		meta.Compatibility.Harness = ""
		meta.Compatibility.Harnesses = locs
		meta.Compatibility.ExplicitPortable = false
		return
	}
	meta.Compatibility.Mode = "portable"
	meta.Compatibility.Harness = ""
	meta.Compatibility.Harnesses = nil
	meta.Compatibility.ExplicitPortable = true
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func mergeSeedRequirements(existing *requirements, inferred requirements) {
	changed := false
	tools := map[string]toolRequirement{}
	for _, tool := range existing.Tools {
		tools[tool.Name] = tool
	}
	for _, tool := range inferred.Tools {
		if _, ok := tools[tool.Name]; !ok {
			tools[tool.Name] = tool
			changed = true
		}
	}
	existing.Tools = sortedToolRequirements(tools)

	servers := map[string]mcpRequirement{}
	for _, server := range existing.MCPServers {
		servers[server.Name] = server
	}
	for _, server := range inferred.MCPServers {
		if _, ok := servers[server.Name]; !ok {
			servers[server.Name] = server
			changed = true
		}
	}
	existing.MCPServers = sortedMCPRequirements(servers)

	if existing.Model.ToolUse == "" && inferred.Model.ToolUse != "" {
		existing.Model = inferred.Model
		changed = true
	}
	if changed {
		existing.Inferred = true
	}
}

func sortedToolRequirements(values map[string]toolRequirement) []toolRequirement {
	var keys []string
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]toolRequirement, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

func sortedMCPRequirements(values map[string]mcpRequirement) []mcpRequirement {
	var keys []string
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]mcpRequirement, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

func normalizeCategories(values []string) []string {
	allowed := map[string]string{
		"engineering":   "Engineering",
		"quality":       "Quality",
		"operations":    "Operations",
		"data":          "Data",
		"design":        "Design",
		"documents":     "Documents",
		"writing":       "Writing",
		"business":      "Business",
		"productivity":  "Productivity",
		"agent-tooling": "Agent-tooling",
	}
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		canonical := allowed[strings.ToLower(strings.TrimSpace(value))]
		if canonical == "" || seen[canonical] {
			continue
		}
		seen[canonical] = true
		out = append(out, canonical)
	}
	return out
}

func normalizeTags(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		tag := strings.ToLower(strings.TrimSpace(value))
		tag = strings.ReplaceAll(tag, "_", "-")
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func normalizeHarnesses(values []string) []string {
	known := map[string]bool{"claude": true, "codex": true, "grok": true, "antigravity": true, "gemini": true, "hermes": true, "openclaw": true}
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		harness := strings.ToLower(strings.TrimSpace(value))
		if !known[harness] || seen[harness] {
			continue
		}
		seen[harness] = true
		out = append(out, harness)
	}
	sort.Strings(out)
	return out
}

func seedCategorizationConfidence(categories []string) string {
	if len(categories) == 0 || len(categories) > 2 {
		return "low"
	}
	return "high"
}

func validateSeedTaxonomy(skill string, categories []string, tags []string) []string {
	var warnings []string
	if len(categories) == 0 {
		warnings = append(warnings, fmt.Sprintf("%s: no category", skill))
	}
	if len(categories) > 2 {
		warnings = append(warnings, fmt.Sprintf("%s: %d categories; review central categories", skill, len(categories)))
	}
	categoryTags := map[string]bool{
		"engineering": true, "quality": true, "operations": true, "data": true, "design": true,
		"documents": true, "writing": true, "business": true, "productivity": true, "agent-tooling": true,
		"coding": true, "dev": true,
	}
	for _, tag := range tags {
		if categoryTags[tag] {
			warnings = append(warnings, fmt.Sprintf("%s: broad tag %q should be a category or removed", skill, tag))
		}
	}
	return warnings
}
