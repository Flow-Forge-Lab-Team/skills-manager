package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// compiledRule is the harness-neutral intermediate produced from a SKILL.md.
type compiledRule struct {
	Name        string
	Description string
	Body        string
	Tags        []string
	AlwaysApply bool
	Globs       []string
}

// runCompile implements `skills-manager compile <harness> [project-path]`,
// translating the project's installed skills into a harness-specific format
// (Cursor `.mdc` today; the framework is shared so other harnesses can be added).
func runCompile(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	var positional []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(stderr, "unknown compile flag: %s\n", a)
			return ExitUsageError
		}
		positional = append(positional, a)
	}
	if len(positional) == 0 {
		fmt.Fprintln(stderr, "usage: skills-manager compile <harness> [project-path]")
		return ExitUsageError
	}
	harness := strings.ToLower(positional[0])
	projectArg := "."
	if len(positional) > 1 {
		projectArg = positional[1]
	}
	stdout := gf.outWriter(realStdout)
	projectPath, err := absoluteProjectPath(projectArg)
	if err != nil {
		fmt.Fprintf(stderr, "project path: %v\n", err)
		return ExitOpError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	written, err := compileForHarness(home, projectPath, harness)
	if err != nil {
		fmt.Fprintf(stderr, "compile %s: %v\n", harness, err)
		if strings.Contains(err.Error(), "unsupported") {
			return ExitUsageError
		}
		return ExitOpError
	}
	if gf.JSON {
		if written == nil {
			written = []string{}
		}
		if err := writeJSON(realStdout, map[string]interface{}{"harness": harness, "written": written}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}
	if len(written) == 0 {
		fmt.Fprintf(stdout, "No skills to compile for %s.\n", harness)
		return ExitSuccess
	}
	for _, w := range written {
		fmt.Fprintf(stdout, "- wrote %s\n", w)
	}
	return ExitSuccess
}

// compileOnlyHarnesses are harnesses that need format translation rather than
// SKILL.md copy. install/sync recompile these when a project opts in.
var compileOnlyHarnesses = map[string]bool{"cursor": true}

// compileHarnessesForProject returns the compile-only harnesses listed in the
// project's config (so install/sync know what to recompile).
func compileHarnessesForProject(projectPath string) []string {
	cfg, err := readProjectConfig(filepath.Join(projectPath, ".skills", "project.yaml"))
	if err != nil {
		return nil
	}
	var out []string
	for _, h := range cfg.Harnesses {
		if compileOnlyHarnesses[strings.ToLower(strings.TrimSpace(h))] {
			out = append(out, strings.ToLower(strings.TrimSpace(h)))
		}
	}
	return out
}

// compileForHarness compiles every installed skill for the given harness and
// returns the project-relative paths written.
func compileForHarness(home, projectPath, harness string) ([]string, error) {
	rules, err := loadCompiledRules(home, projectPath)
	if err != nil {
		return nil, err
	}
	switch harness {
	case "cursor":
		return writeCursorRules(projectPath, rules)
	default:
		return nil, fmt.Errorf("unsupported harness %q (supported: cursor)", harness)
	}
}

// loadCompiledRules reads the project's installed skills and builds the neutral
// intermediate for each.
func loadCompiledRules(home, projectPath string) ([]compiledRule, error) {
	lock, err := readInstallLock(filepath.Join(projectPath, ".skills", "installed.lock"))
	if err != nil {
		return nil, fmt.Errorf("read install lock: %w", err)
	}
	cat, _, err := loadTriageCatalog(home)
	if err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}
	tagsByName := map[string][]string{}
	for _, s := range cat.Skills {
		tagsByName[s.Name] = s.Tags
	}
	libraryPath := filepath.Join(home, "library")
	var rules []compiledRule
	for _, locked := range lock.Skills {
		skillMd := filepath.Join(libraryPath, locked.Name, "SKILL.md")
		rule, err := buildCompiledRule(skillMd, locked.Name, tagsByName[locked.Name])
		if err != nil {
			continue // skip skills we can't read; don't fail the whole compile
		}
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
	return rules, nil
}

// buildCompiledRule parses a SKILL.md into the neutral rule, applying tag-based
// glob inference and any explicit `cursor:` frontmatter override.
func buildCompiledRule(skillMd, name string, catalogTags []string) (compiledRule, error) {
	data, err := os.ReadFile(skillMd)
	if err != nil {
		return compiledRule{}, err
	}
	text := string(data)
	rule := compiledRule{Name: name, Tags: catalogTags}

	var fm struct {
		Description string   `yaml:"description"`
		Tags        []string `yaml:"tags"`
		Cursor      *struct {
			Globs       []string `yaml:"globs"`
			AlwaysApply *bool    `yaml:"alwaysApply"`
			Description string   `yaml:"description"`
		} `yaml:"cursor"`
	}
	if strings.HasPrefix(text, "---") {
		if end := strings.Index(text[3:], "---"); end != -1 {
			_ = yaml.Unmarshal([]byte(text[3:end+3]), &fm)
		}
	}
	body, _ := readSkillBody(skillMd)
	rule.Body = strings.TrimSpace(body)
	rule.Description = strings.TrimSpace(strings.ReplaceAll(fm.Description, "\n", " "))
	if len(fm.Tags) > 0 {
		rule.Tags = unionTags(rule.Tags, fm.Tags)
	}

	rule.AlwaysApply = containsFold(rule.Tags, "always-on")
	rule.Globs = inferGlobsFromTags(rule.Tags)

	// Explicit override wins over heuristics.
	if fm.Cursor != nil {
		if fm.Cursor.Description != "" {
			rule.Description = fm.Cursor.Description
		}
		if fm.Cursor.AlwaysApply != nil {
			rule.AlwaysApply = *fm.Cursor.AlwaysApply
		}
		if fm.Cursor.Globs != nil {
			rule.Globs = fm.Cursor.Globs
		}
	}
	if rule.AlwaysApply {
		rule.Globs = nil // alwaysApply rules don't use globs
	}
	return rule, nil
}

func unionTags(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// inferGlobsFromTags maps common stack tags to file globs so a
// description-driven skill becomes a glob-scoped Cursor rule.
func inferGlobsFromTags(tags []string) []string {
	table := map[string][]string{
		"react":      {"**/*.jsx", "**/*.tsx"},
		"nextjs":     {"app/**", "pages/**", "**/*.tsx"},
		"next":       {"app/**", "pages/**", "**/*.tsx"},
		"vue":        {"**/*.vue"},
		"svelte":     {"**/*.svelte"},
		"typescript": {"**/*.ts", "**/*.tsx"},
		"ts":         {"**/*.ts", "**/*.tsx"},
		"javascript": {"**/*.js", "**/*.jsx"},
		"js":         {"**/*.js", "**/*.jsx"},
		"python":     {"**/*.py"},
		"py":         {"**/*.py"},
		"go":         {"**/*.go"},
		"golang":     {"**/*.go"},
		"rust":       {"**/*.rs"},
		"ruby":       {"**/*.rb"},
		"java":       {"**/*.java"},
		"css":        {"**/*.css", "**/*.scss"},
		"sql":        {"**/*.sql"},
		"terraform":  {"**/*.tf"},
		"docker":     {"Dockerfile", "**/*.dockerfile"},
		"markdown":   {"**/*.md"},
		"docs":       {"**/*.md", "**/*.mdx"},
	}
	seen := map[string]bool{}
	var globs []string
	for _, tag := range tags {
		for _, g := range table[strings.ToLower(strings.TrimSpace(tag))] {
			if !seen[g] {
				seen[g] = true
				globs = append(globs, g)
			}
		}
	}
	sort.Strings(globs)
	return globs
}

// writeCursorRules renders each rule as a Cursor `.mdc` file under
// <project>/.cursor/rules/<name>.mdc.
func writeCursorRules(projectPath string, rules []compiledRule) ([]string, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	dir := filepath.Join(projectPath, ".cursor", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, r := range rules {
		rel := filepath.Join(".cursor", "rules", r.Name+".mdc")
		if err := os.WriteFile(filepath.Join(projectPath, rel), []byte(renderCursorRule(r)), 0o644); err != nil {
			return written, err
		}
		written = append(written, rel)
	}
	return written, nil
}

func renderCursorRule(r compiledRule) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "description: %s\n", yamlScalar(r.Description))
	b.WriteString("globs:")
	if len(r.Globs) == 0 {
		b.WriteString(" []\n")
	} else {
		b.WriteString("\n")
		for _, g := range r.Globs {
			fmt.Fprintf(&b, "  - %s\n", yamlScalar(g))
		}
	}
	fmt.Fprintf(&b, "alwaysApply: %t\n", r.AlwaysApply)
	b.WriteString("---\n\n")
	b.WriteString(r.Body)
	b.WriteString("\n")
	return b.String()
}

// yamlScalar quotes a scalar when needed so the emitted frontmatter is valid.
func yamlScalar(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#\"'[]{}>|*&!%@`") || strings.HasPrefix(s, "- ") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
