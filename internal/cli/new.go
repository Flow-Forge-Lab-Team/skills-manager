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

// validateAuthoredSkill enforces the skills-author rules: name match, an
// activation-safe (non-stub, specific) description, valid frontmatter, and no
// hostile instructions.
func validateAuthoredSkill(name, draft string) error {
	trimmed := strings.TrimSpace(draft)
	if !strings.HasPrefix(trimmed, "---") {
		return fmt.Errorf("draft is not a SKILL.md (missing frontmatter)")
	}
	end := strings.Index(trimmed[3:], "---")
	if end == -1 {
		return fmt.Errorf("unterminated frontmatter")
	}
	var fm struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Compatible  []string `yaml:"compatible"`
		Exclusive   string   `yaml:"exclusive"`
	}
	if err := yaml.Unmarshal([]byte(trimmed[3:end+3]), &fm); err != nil {
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
