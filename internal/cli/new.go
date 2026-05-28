package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

func runNew(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	var name string
	var guided, auto, handoff bool
	var applyFile string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--guided":
			guided = true
		case arg == "--auto":
			auto = true
		case arg == "--handoff":
			handoff = true
		case arg == "--apply":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "--apply requires a file path")
				return ExitUsageError
			}
			i++
			applyFile = args[i]
		case strings.HasPrefix(arg, "--apply="):
			applyFile = strings.TrimPrefix(arg, "--apply=")
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "unknown new flag: %s\n", arg)
			return ExitUsageError
		default:
			if name != "" {
				fmt.Fprintln(stderr, "usage: skills-manager new <name> [--guided [--auto|--handoff|--apply <file>]]")
				return ExitUsageError
			}
			name = arg
		}
	}
	if name == "" {
		fmt.Fprintln(stderr, "usage: skills-manager new <name> [--guided [--auto|--handoff|--apply <file>]]")
		return ExitUsageError
	}
	if (auto || handoff || applyFile != "") && !guided {
		guided = true // --auto/--handoff/--apply imply guided authoring
	}
	modes := 0
	for _, on := range []bool{auto, handoff, applyFile != ""} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		fmt.Fprintln(stderr, "choose at most one of --auto, --handoff, --apply")
		return ExitUsageError
	}

	// Validate name: alphanumeric + dashes, 3..64 chars
	if !isValidSkillName(name) {
		fmt.Fprintf(stderr, "invalid skill name: %q (must be 3-64 alphanumeric characters and dashes)\n", name)
		return ExitUsageError
	}

	// Check if already in library
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

	targetDir := filepath.Join(libraryPath, name)
	if _, err := os.Stat(targetDir); err == nil {
		fmt.Fprintf(stderr, "skill %q already in library\n", name)
		return ExitUsageError
	}

	if guided {
		return runNewGuided(name, home, auto, applyFile, stdout, stderr, gf)
	}

	// Create temp dir with starter SKILL.md
	tmpDir, err := os.MkdirTemp("", "sm-new-*")
	if err != nil {
		fmt.Fprintf(stderr, "create temp dir: %v\n", err)
		return ExitOpError
	}
	defer os.RemoveAll(tmpDir)

	skillMdPath := filepath.Join(tmpDir, "SKILL.md")
	starter := fmt.Sprintf(`---
name: %s
description: TODO — one sentence about when to invoke this skill
---

# %s

TODO body
`, name, name)

	if err := os.WriteFile(skillMdPath, []byte(starter), 0644); err != nil {
		fmt.Fprintf(stderr, "write starter SKILL.md: %v\n", err)
		return ExitOpError
	}

	// Determine editor
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}

	// Execute editor using sh -c to handle editor values with arguments (e.g., "code --wait")
	// Single-quote the path and escape internal single quotes: 'path'\''with'\''quotes'
	shellEscapedPath := "'" + strings.ReplaceAll(skillMdPath, "'", "'\\''") + "'"
	cmd := exec.Command("sh", "-c", editor+" "+shellEscapedPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "editor exited with error\n")
		return ExitUsageError
	}

	// Re-read the file
	content, err := os.ReadFile(skillMdPath)
	if err != nil {
		fmt.Fprintf(stderr, "read edited SKILL.md: %v\n", err)
		return ExitOpError
	}

	contentStr := string(content)
	if strings.Contains(contentStr, "TODO — one sentence about when to invoke this skill") {
		fmt.Fprintln(stderr, "description unchanged; refusing to ingest stub")
		return ExitUsageError
	}

	// Ingest with opts.yes = true (user hand-edited)
	src := ingestSource{
		kind:  "authored",
		raw:   name,
		path:  tmpDir,
		label: name,
	}

	opts := ingestOptions{
		yes:         true,
		interactive: false,
	}

	humanOut := gf.outWriter(stdout)
	result := ingestFromSource(src, opts, home, humanOut)

	if gf.JSON {
		writeJSON(stdout, result)
	} else if !result.Skipped {
		fmt.Fprintf(humanOut, "Created and ingested %s to %s\n", result.Name, result.LibraryPath)
	} else {
		fmt.Fprintf(humanOut, "Failed to ingest %s: %s\n", result.Name, result.Reason)
		return ExitNotable
	}

	return ExitSuccess
}

// runNewGuided creates a skill via the skills-author bundled skill: --auto runs
// the configured provider, --apply imports an agent draft, and the default
// writes a handoff prompt. Drafts are validated before ingest.
func runNewGuided(name, home string, auto bool, applyFile string, stdout, stderr io.Writer, gf globalFlags) int {
	humanOut := gf.outWriter(stdout)
	switch {
	case applyFile != "":
		data, err := os.ReadFile(applyFile)
		if err != nil {
			fmt.Fprintf(stderr, "read authored draft: %v\n", err)
			return ExitOpError
		}
		return ingestAuthoredSkill(name, home, string(data), stdout, stderr, gf)
	case auto:
		if !llmProviderConfigured(home) {
			fmt.Fprintln(stderr, "no LLM provider configured; run `skills-manager config set llm.provider ...` or use --handoff")
			return ExitOpError
		}
		out, err := runConfiguredLLMProvider(home, buildAuthorPrompt(name))
		if err != nil {
			fmt.Fprintf(stderr, "run provider: %v\n", err)
			return ExitOpError
		}
		return ingestAuthoredSkill(name, home, out, stdout, stderr, gf)
	default: // handoff
		dir := filepath.Join(home, "authoring")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "create authoring dir: %v\n", err)
			return ExitOpError
		}
		path := filepath.Join(dir, name+".prompt.md")
		if err := os.WriteFile(path, []byte(buildAuthorPrompt(name)), 0o644); err != nil {
			fmt.Fprintf(stderr, "write authoring prompt: %v\n", err)
			return ExitOpError
		}
		fmt.Fprintf(humanOut, "Wrote authoring prompt to %s\n", path)
		fmt.Fprintf(humanOut, "Import the agent's draft with: skills-manager new %s --apply <draft.md>\n", name)
		if gf.JSON {
			_ = writeJSON(stdout, map[string]interface{}{"skill": name, "prompt_path": path})
		}
		return ExitSuccess
	}
}

func buildAuthorPrompt(name string) string {
	var b strings.Builder
	if bundled := readBundledSkillMarkdown("skills-author"); bundled != "" {
		b.WriteString(bundled)
	} else {
		b.WriteString("Author a complete SKILL.md (frontmatter + body) including an activation-safe description, compatibility declaration, and execution requirements. Output only the SKILL.md.\n")
	}
	b.WriteString("\n\n## Skill name\n")
	b.WriteString(name)
	b.WriteString("\n")
	return b.String()
}

// ingestAuthoredSkill validates a generated/applied SKILL.md draft and ingests it.
func ingestAuthoredSkill(name, home, draft string, stdout, stderr io.Writer, gf globalFlags) int {
	draft = stripCodeFences(draft)
	if err := validateAuthoredSkill(name, draft); err != nil {
		fmt.Fprintf(stderr, "rejected authored skill: %v\n", err)
		return ExitOpError
	}
	tmpDir, err := os.MkdirTemp("", "sm-new-*")
	if err != nil {
		fmt.Fprintf(stderr, "create temp dir: %v\n", err)
		return ExitOpError
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(ensureTrailingNewline(draft)), 0o644); err != nil {
		fmt.Fprintf(stderr, "write draft: %v\n", err)
		return ExitOpError
	}
	src := ingestSource{kind: "authored", raw: name, path: tmpDir, label: name}
	result := ingestFromSource(src, ingestOptions{yes: true, interactive: false}, home, gf.outWriter(stdout))
	if !result.Skipped {
		// The local detectors are coarse; preserve any execution requirements
		// the author explicitly declared in the draft frontmatter so they
		// aren't silently dropped from the sidecar.
		if err := preserveAuthoredRequirements(draft, result.LibraryPath, home); err != nil {
			fmt.Fprintf(stderr, "warning: preserve authored requirements: %v\n", err)
		}
	}
	if gf.JSON {
		writeJSON(stdout, result)
		if result.Skipped {
			return ExitNotable
		}
		return ExitSuccess
	}
	if result.Skipped {
		fmt.Fprintf(gf.outWriter(stdout), "Failed to ingest %s: %s\n", result.Name, result.Reason)
		return ExitNotable
	}
	fmt.Fprintf(gf.outWriter(stdout), "Authored and ingested %s to %s\n", result.Name, result.LibraryPath)
	return ExitSuccess
}

// preserveAuthoredRequirements merges execution requirements declared in the
// authored draft's frontmatter into the ingested skill's sidecar (the local
// detectors may not catch explicit tools/credentials), then refreshes the catalog.
func preserveAuthoredRequirements(draft, libraryPath, home string) error {
	block, ok := splitFrontmatterBlock(draft)
	if !ok {
		return nil
	}
	var fm struct {
		Requirements requirements `yaml:"requirements"`
	}
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return nil // requirements are optional; ignore parse issues
	}
	if !fm.Requirements.hasExplicitFields() || libraryPath == "" {
		return nil
	}
	metaPath := filepath.Join(libraryPath, ".skill-meta.yaml")
	meta, _ := readSkillMeta(metaPath)
	// Merge: authored explicit requirements take precedence, but inferred
	// requirements the detectors found (e.g. a `gh pr` in the body) are kept.
	merged := fm.Requirements
	authoredModel := fm.Requirements.Model
	mergeSeedRequirements(&merged, meta.Requirements) // adds inferred tools/mcp/model not already present
	// mergeSeedRequirements can replace the whole model block; instead merge
	// model fields individually so authored constraints survive.
	merged.Model = mergeModelRequirements(authoredModel, meta.Requirements.Model)
	credSeen := map[string]bool{}
	for _, c := range merged.Credentials {
		credSeen[c.Name] = true
	}
	for _, c := range meta.Requirements.Credentials {
		if !credSeen[c.Name] {
			merged.Credentials = append(merged.Credentials, c)
		}
	}
	merged.Inferred = false // contains explicit authored requirements
	meta.Requirements = merged
	if err := writeSeedSkillMeta(metaPath, meta); err != nil {
		return err
	}
	cat, err := rebuildCatalogFromLibrary(filepath.Join(home, "library"))
	if err != nil {
		return err
	}
	return writeCatalog(filepath.Join(home, "library", "catalog.yaml"), cat)
}

// splitFrontmatterBlock returns the YAML between the opening `---` and the next
// `---` that appears on its own line, so a `---` inside a quoted/block scalar
// (e.g. a skill that documents frontmatter) is not mistaken for the closing
// fence. The bool is false when there is no well-formed frontmatter.
func splitFrontmatterBlock(content string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return strings.Join(lines[1:i], "\n"), true
		}
	}
	return "", false
}

// mergeModelRequirements prefers authored model fields, filling empty ones from
// inferred (so an inferred tool_use augments rather than replaces authored
// reasoning / context constraints).
func mergeModelRequirements(authored, inferred modelRequirement) modelRequirement {
	out := authored
	if out.ToolUse == "" {
		out.ToolUse = inferred.ToolUse
	}
	if out.MinContextTokens == 0 {
		out.MinContextTokens = inferred.MinContextTokens
	}
	if out.Reasoning == "" {
		out.Reasoning = inferred.Reasoning
	}
	if out.Notes == "" {
		out.Notes = inferred.Notes
	}
	return out
}

// validateAuthoredSkill enforces the skills-author rules: name match, an
// activation-safe (non-stub, specific) description, valid frontmatter, and no
// hostile instructions.
func validateAuthoredSkill(name, draft string) error {
	block, ok := splitFrontmatterBlock(draft)
	if !ok {
		return fmt.Errorf("draft is not a SKILL.md (missing or unterminated frontmatter)")
	}
	var fm struct {
		Name         string       `yaml:"name"`
		Description  string       `yaml:"description"`
		Compatible   []string     `yaml:"compatible"`
		Exclusive    string       `yaml:"exclusive"`
		Requirements requirements `yaml:"requirements"`
	}
	// Decoding requirements here means a malformed requirements block (wrong
	// shape) is rejected rather than silently dropped during preservation.
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return fmt.Errorf("invalid frontmatter: %w", err)
	}
	if fm.Name != name {
		return fmt.Errorf("name %q does not match requested %q", fm.Name, name)
	}
	desc := strings.TrimSpace(fm.Description)
	if desc == "" || strings.Contains(desc, "TODO") {
		return fmt.Errorf("missing or placeholder description")
	}
	if len(desc) < 15 {
		return fmt.Errorf("description too short to be activation-safe")
	}
	// Descriptions drive activation: require an explicit "when to use" trigger
	// (per the skills-author rules) rather than a vague summary.
	if !strings.Contains(strings.ToLower(desc), "when") {
		return fmt.Errorf("description must state when to use the skill (e.g. \"Use when …\")")
	}
	// Guided authoring must declare compatibility, not leave it implicitly portable.
	if len(fm.Compatible) == 0 && strings.TrimSpace(fm.Exclusive) == "" {
		return fmt.Errorf("missing compatibility declaration (compatible: [...] or exclusive: <harness>)")
	}
	for _, line := range strings.Split(draft, "\n") {
		if looksSuspicious(strings.ToLower(strings.TrimSpace(line))) {
			return fmt.Errorf("draft contains a hostile/policy-bypassing instruction")
		}
	}
	return nil
}

func isValidSkillName(name string) bool {
	if len(name) < 3 || len(name) > 64 {
		return false
	}
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9-]+$`, name)
	return matched
}
