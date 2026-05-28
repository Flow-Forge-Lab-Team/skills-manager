package cli

import (
	"encoding/json"
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
	// CopilotApplyTo, when non-empty, is an explicit Copilot applyTo override
	// from the skill's `copilot:` frontmatter block.
	CopilotApplyTo string
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

	written, err := compileForHarness(home, projectPath, harness, gf.Config)
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
var compileOnlyHarnesses = map[string]bool{"cursor": true, "copilot": true}

// compileHarnessesForProject returns the compile-only harnesses listed in the
// project's config (so install/sync know what to recompile). configPath may
// override the default <project>/.skills/project.yaml (e.g. via --config).
func compileHarnessesForProject(projectPath, configPath string) []string {
	cfg, err := readProjectConfig(projectConfigPathOrDefault(projectPath, configPath))
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

// reconcileCompileHarnesses brings every compile-only harness's output in sync
// with the project: active harnesses are (re)compiled, inactive ones have their
// previously-generated artifacts pruned. Used by install/sync so disabling a
// harness clears its stale generated rules. Best-effort; returns the first error.
func reconcileCompileHarnesses(home, projectPath, configPath string) error {
	active := map[string]bool{}
	for _, h := range compileHarnessesForProject(projectPath, configPath) {
		active[h] = true
	}
	var firstErr error
	for h := range compileOnlyHarnesses {
		if active[h] {
			if _, err := compileForHarness(home, projectPath, h, configPath); err != nil && firstErr == nil {
				firstErr = err
			}
		} else if err := pruneCompileHarness(projectPath, h); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// pruneCompileHarness removes any artifacts a compile-only harness previously
// generated for the project (used when the harness is disabled or uninstalled).
func pruneCompileHarness(projectPath, harness string) error {
	switch harness {
	case "cursor":
		_, err := writeCursorRules(projectPath, nil) // nil rules → prune-only
		return err
	case "copilot":
		_, err := writeCopilotInstructions(projectPath, nil)
		return err
	default:
		return nil
	}
}

// pruneAllCompileHarnesses removes generated artifacts for every compile-only
// harness (used by uninstall, which doesn't track compile outputs in the
// install manifest).
func pruneAllCompileHarnesses(projectPath string) {
	for h := range compileOnlyHarnesses {
		_ = pruneCompileHarness(projectPath, h)
	}
}

// compileForHarness compiles every installed skill for the given harness and
// returns the project-relative paths written.
func compileForHarness(home, projectPath, harness, configPath string) ([]string, error) {
	if harness != "cursor" && harness != "copilot" {
		return nil, fmt.Errorf("unsupported harness %q (supported: cursor, copilot)", harness)
	}
	rules, err := loadCompiledRules(home, projectPath, harness, configPath)
	if err != nil {
		return nil, err
	}
	if harness == "copilot" {
		return writeCopilotInstructions(projectPath, rules)
	}
	return writeCursorRules(projectPath, rules)
}

func projectConfigPathOrDefault(projectPath, configPath string) string {
	if strings.TrimSpace(configPath) != "" {
		return configPath
	}
	return filepath.Join(projectPath, ".skills", "project.yaml")
}

// loadCompiledRules resolves the project's skills for the harness and builds the
// neutral intermediate for each.
func loadCompiledRules(home, projectPath, harness, configPath string) ([]compiledRule, error) {
	cat, _, err := loadTriageCatalog(home)
	if err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}
	tagsByName := map[string][]string{}
	for _, s := range cat.Skills {
		tagsByName[s.Name] = s.Tags
	}
	names, err := projectCompileSkills(projectPath, configPath, cat, harness)
	if err != nil {
		return nil, err
	}
	libraryPath := filepath.Join(home, "library")
	var rules []compiledRule
	for _, name := range names {
		skillMd := filepath.Join(libraryPath, name, "SKILL.md")
		rule, err := buildCompiledRule(skillMd, name, tagsByName[name], harness)
		if err != nil {
			// A skill whose canonical SKILL.md is unreadable is skipped for
			// robustness, but a declared-but-missing variant is a real
			// misconfiguration we surface.
			if !pathExists(skillMd) {
				continue
			}
			return nil, err
		}
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
	return rules, nil
}

// projectCompileSkills returns the skills to compile for a harness. It prefers
// the install lock (the concrete installed set), but falls back to the project
// match for compile-only harnesses like cursor, which have no copy target and
// therefore record nothing in the lock.
func projectCompileSkills(projectPath, configPath string, cat catalog, harness string) ([]string, error) {
	lock, err := readInstallLock(filepath.Join(projectPath, ".skills", "installed.lock"))
	if err != nil {
		return nil, fmt.Errorf("read install lock: %w", err)
	}
	skillByName := map[string]catalogSkill{}
	for _, s := range cat.Skills {
		skillByName[s.Name] = s
	}
	if len(lock.Skills) > 0 {
		// A lock can include skills installed only for other harnesses (e.g.
		// exclusive:claude) or a synthesized entry for a skill install blocked
		// on missing requirements. Only compile those compatible with this
		// harness and whose requirements are met.
		var names []string
		for _, e := range lock.Skills {
			s := skillByName[e.Name]
			if len(compatibleHarnesses(s.Compatibility, []string{harness})) == 0 {
				continue
			}
			if skillHasUnmetRequirements(s.Requirements) {
				continue
			}
			names = append(names, e.Name)
		}
		return names, nil
	}
	// Fallback: matched candidates compatible with this harness.
	cfg, err := readProjectConfig(projectConfigPathOrDefault(projectPath, configPath))
	if err != nil {
		return nil, nil
	}
	var names []string
	for _, c := range selectInstallCandidates(cat, cfg, "") {
		if len(compatibleHarnesses(c.Skill.Compatibility, []string{harness})) == 0 {
			continue
		}
		// Mirror install's blocking: don't compile rules for a skill whose
		// required tools/MCP/credentials/model/runtimes are unmet — install
		// would have refused it.
		if skillHasUnmetRequirements(c.Skill.Requirements) {
			continue
		}
		names = append(names, c.Skill.Name)
	}
	return names, nil
}

// skillHasUnmetRequirements reports whether any required dependency is missing,
// matching the conditions under which install blocks a skill.
func skillHasUnmetRequirements(req requirements) bool {
	return len(missingRequiredTools(req)) > 0 ||
		len(missingRequiredMCPServers(req)) > 0 ||
		len(missingModelCapabilities(req)) > 0 ||
		len(missingRequiredCredentials(req)) > 0 ||
		len(missingRequiredScriptRuntimes(req)) > 0
}

// buildCompiledRule parses a SKILL.md into the neutral rule, applying tag-based
// glob inference. Harness-specific frontmatter overrides are applied only for
// the harness being compiled, so a `cursor:` block never reshapes Copilot
// output (and vice versa).
func buildCompiledRule(skillMd, name string, catalogTags []string, harness string) (compiledRule, error) {
	// Honor a per-harness ported variant: if the skill declares an override for
	// this harness, compile from the ported file instead of the canonical.
	skillDir := filepath.Dir(skillMd)
	vf, ok, verr := readVariants(skillDir)
	if verr != nil {
		return compiledRule{}, fmt.Errorf("read variants for %q: %w", name, verr)
	}
	if ok {
		if chosen := selectVariantFile(vf, []string{harness}); chosen != "" && chosen != "SKILL.md" {
			candidate := filepath.Join(skillDir, chosen)
			if !pathExists(candidate) {
				// A declared variant that's gone (bad sync / deletion) must fail
				// loudly rather than silently emitting the unported canonical.
				return compiledRule{}, fmt.Errorf("declared %s variant %q for %q is missing", harness, chosen, name)
			}
			skillMd = candidate
		}
	}
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
		Copilot *struct {
			ApplyTo string `yaml:"applyTo"`
		} `yaml:"copilot"`
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

	// Cursor overrides reshape Globs/AlwaysApply, so only apply them when
	// compiling for cursor — otherwise they would leak into other harnesses.
	if harness == "cursor" && fm.Cursor != nil {
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
	if fm.Copilot != nil {
		rule.CopilotApplyTo = strings.TrimSpace(fm.Copilot.ApplyTo)
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

// cursorManifestName tracks which .mdc files skills-manager generated, so stale
// rules for removed skills can be pruned without touching user-authored rules.
const cursorManifestName = ".skills-manager.json"

// writeCursorRules renders each rule as a Cursor `.mdc` file under
// <project>/.cursor/rules/<name>.mdc, and prunes previously-generated rules that
// are no longer present (without deleting user-authored .mdc files).
func writeCursorRules(projectPath string, rules []compiledRule) ([]string, error) {
	dir := filepath.Join(projectPath, ".cursor", "rules")
	manifestPath := filepath.Join(dir, cursorManifestName)
	prior := readCursorManifest(manifestPath)

	current := map[string]bool{}
	for _, r := range rules {
		current[r.Name+".mdc"] = true
	}

	// Prune previously-generated rules that are no longer compiled.
	for _, oldFile := range prior {
		if !current[oldFile] {
			_ = os.Remove(filepath.Join(dir, oldFile))
		}
	}

	if len(rules) == 0 {
		// Nothing to generate: drop the manifest (and the dir if now empty).
		_ = os.Remove(manifestPath)
		_ = os.Remove(dir)
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	var names []string
	for _, r := range rules {
		base := r.Name + ".mdc"
		rel := filepath.Join(".cursor", "rules", base)
		if err := os.WriteFile(filepath.Join(projectPath, rel), []byte(renderCursorRule(r)), 0o644); err != nil {
			return written, err
		}
		written = append(written, rel)
		names = append(names, base)
	}
	if err := writeCursorManifest(manifestPath, names); err != nil {
		return written, err
	}
	return written, nil
}

func readCursorManifest(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var names []string
	_ = json.Unmarshal(data, &names)
	return names
}

func writeCursorManifest(path string, names []string) error {
	sort.Strings(names)
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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

const (
	copilotBeginMarker = "<!-- skills-manager:begin (generated — managed by skills-manager) -->"
	copilotEndMarker   = "<!-- skills-manager:end -->"
)

// copilotApplyTo derives a Copilot `applyTo` glob for a rule: an explicit
// override wins, then always-on maps to `**`, then inferred globs are joined.
// An empty result means the skill has no glob scope and belongs in the
// single-file fallback (.github/copilot-instructions.md).
func copilotApplyTo(r compiledRule) string {
	if r.CopilotApplyTo != "" {
		return r.CopilotApplyTo
	}
	if r.AlwaysApply {
		return "**"
	}
	if len(r.Globs) > 0 {
		// Copilot's applyTo is a comma-separated glob list; a space after the
		// comma would make the next pattern start with a space and never match.
		return strings.Join(r.Globs, ",")
	}
	return ""
}

// writeCopilotInstructions writes glob-scoped skills to
// .github/instructions/<name>.instructions.md and folds skills without a glob
// scope into .github/copilot-instructions.md (single-file fallback). Generated
// per-file instructions are tracked/pruned via a manifest; the single-file
// fallback uses a marker block so user-authored content is preserved.
func writeCopilotInstructions(projectPath string, rules []compiledRule) ([]string, error) {
	instrDir := filepath.Join(projectPath, ".github", "instructions")
	manifestPath := filepath.Join(instrDir, cursorManifestName)
	prior := readCursorManifest(manifestPath)

	var perFile, general []compiledRule
	for _, r := range rules {
		if copilotApplyTo(r) != "" {
			perFile = append(perFile, r)
		} else {
			general = append(general, r)
		}
	}

	current := map[string]bool{}
	for _, r := range perFile {
		current[r.Name+".instructions.md"] = true
	}
	for _, old := range prior {
		if !current[old] {
			_ = os.Remove(filepath.Join(instrDir, old))
		}
	}

	var written []string
	if len(perFile) > 0 {
		if err := os.MkdirAll(instrDir, 0o755); err != nil {
			return nil, err
		}
		var names []string
		for _, r := range perFile {
			base := r.Name + ".instructions.md"
			rel := filepath.Join(".github", "instructions", base)
			if err := os.WriteFile(filepath.Join(projectPath, rel), []byte(renderCopilotInstruction(r)), 0o644); err != nil {
				return written, err
			}
			written = append(written, rel)
			names = append(names, base)
		}
		if err := writeCursorManifest(manifestPath, names); err != nil {
			return written, err
		}
	} else {
		_ = os.Remove(manifestPath)
		_ = os.Remove(instrDir)
	}

	// Single-file fallback for skills with no glob scope.
	singleRel := filepath.Join(".github", "copilot-instructions.md")
	singlePath := filepath.Join(projectPath, singleRel)
	existing := ""
	if data, err := os.ReadFile(singlePath); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return written, err
	}
	if len(general) == 0 {
		if strings.Contains(existing, copilotBeginMarker) {
			cleaned := removeMarkedBlock(existing, copilotBeginMarker, copilotEndMarker)
			if cleaned != existing {
				if strings.TrimSpace(cleaned) == "" {
					// File held only our generated block — remove it entirely
					// rather than leaving an empty file behind.
					_ = os.Remove(singlePath)
				} else if err := os.WriteFile(singlePath, []byte(cleaned), 0o644); err != nil {
					return written, err
				}
				written = append(written, singleRel)
			}
		}
		return written, nil
	}
	block := buildCopilotGeneralBlock(general)
	merged := mergeMarkedBlock(existing, block, copilotBeginMarker, copilotEndMarker)
	if merged != existing {
		if err := os.MkdirAll(filepath.Join(projectPath, ".github"), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(singlePath, []byte(merged), 0o644); err != nil {
			return written, err
		}
		written = append(written, singleRel)
	}
	return written, nil
}

func renderCopilotInstruction(r compiledRule) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "applyTo: %s\n", yamlScalar(copilotApplyTo(r)))
	b.WriteString("---\n\n")
	b.WriteString(r.Body)
	b.WriteString("\n")
	return b.String()
}

func buildCopilotGeneralBlock(rules []compiledRule) string {
	var b strings.Builder
	b.WriteString(copilotBeginMarker)
	b.WriteString("\n_General instructions assembled by skills-manager. Edit the skills in your library; content outside this block is preserved._\n\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", r.Name, r.Body)
	}
	return strings.TrimRight(b.String(), "\n") + "\n" + copilotEndMarker
}

// mergeMarkedBlock replaces the begin/end-delimited region in existing with
// generated, preserving content outside it; appends if no markers are present.
func mergeMarkedBlock(existing, generated, begin, end string) string {
	start := strings.Index(existing, begin)
	endIdx := strings.Index(existing, end)
	if start != -1 && endIdx != -1 && endIdx > start {
		before := existing[:start]
		after := existing[endIdx+len(end):]
		return strings.TrimRight(before, " \t") + generated + after
	}
	trimmed := strings.TrimRight(existing, "\n")
	if trimmed == "" {
		return generated + "\n"
	}
	return trimmed + "\n\n" + generated + "\n"
}

// removeMarkedBlock strips the begin/end-delimited region, preserving content
// around it.
func removeMarkedBlock(existing, begin, end string) string {
	start := strings.Index(existing, begin)
	endIdx := strings.Index(existing, end)
	if start == -1 || endIdx == -1 || endIdx < start {
		return existing
	}
	before := strings.TrimRight(existing[:start], " \t\n")
	after := strings.TrimLeft(existing[endIdx+len(end):], "\n")
	switch {
	case before == "" && after == "":
		return ""
	case before == "":
		return after
	case after == "":
		return before + "\n"
	default:
		return before + "\n\n" + after
	}
}

// yamlScalar quotes a scalar when needed so the emitted frontmatter is valid.
func yamlScalar(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#\"'[]{}>|*&!%@`\\") || strings.HasPrefix(s, "- ") {
		// Escape backslashes first, then quotes, so YAML double-quoted escape
		// sequences stay valid.
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return `"` + s + `"`
	}
	return s
}
