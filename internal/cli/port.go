package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type portOptions struct {
	skill     string
	toRaw     string
	auto      bool
	handoff   bool
	applyFile string
}

// runPort implements `skills-manager port <skill> --to <harnesses> [--auto | --handoff | --apply <file>]`.
// It rewrites a skill for target harnesses via the configured provider (--auto)
// or a manual agent handoff (--handoff / --apply), validates the result, and
// saves it as a per-harness variant.
func runPort(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parsePortOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	stdout := gf.outWriter(realStdout)
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
	if err := validateSkillName(opts.skill); err != nil {
		fmt.Fprintf(stderr, "invalid skill name: %v\n", err)
		return ExitUsageError
	}
	skillDir := filepath.Join(libraryPath, opts.skill)
	canonicalPath := filepath.Join(skillDir, "SKILL.md")
	canonName, canonDesc, err := parseSkillFrontmatter(canonicalPath)
	if err != nil {
		fmt.Fprintf(stderr, "read canonical skill %q: %v\n", opts.skill, err)
		return ExitOpError
	}

	targets := splitCSV(opts.toRaw)
	if len(targets) == 0 {
		fmt.Fprintln(stderr, "usage: skills-manager port <skill> --to <harnesses> [--auto | --handoff | --apply <file>]")
		return ExitUsageError
	}
	for _, h := range targets {
		if _, ok := harnessProjectPaths[h]; !ok && !compileOnlyHarnesses[h] {
			fmt.Fprintf(stderr, "unknown target harness: %s\n", h)
			return ExitUsageError
		}
	}

	switch {
	case opts.applyFile != "":
		if len(targets) != 1 {
			fmt.Fprintln(stderr, "--apply imports one ported file; pass a single --to <harness>")
			return ExitUsageError
		}
		data, err := os.ReadFile(opts.applyFile)
		if err != nil {
			fmt.Fprintf(stderr, "read ported file: %v\n", err)
			return ExitOpError
		}
		return applyPort(skillDir, opts.skill, targets[0], canonName, canonDesc, string(data), canonicalPath, realStdout, stdout, stderr, gf)
	case opts.auto:
		if !llmProviderConfigured(home) {
			fmt.Fprintln(stderr, "no LLM provider configured; run `skills-manager config set llm.provider ...` or use --handoff")
			return ExitOpError
		}
		canonical, err := os.ReadFile(canonicalPath)
		if err != nil {
			fmt.Fprintf(stderr, "read canonical: %v\n", err)
			return ExitOpError
		}
		for _, h := range targets {
			out, err := runConfiguredLLMProvider(home, buildPortPrompt(string(canonical), h))
			if err != nil {
				fmt.Fprintf(stderr, "run provider for %s: %v\n", h, err)
				return ExitOpError
			}
			if code := applyPort(skillDir, opts.skill, h, canonName, canonDesc, out, canonicalPath, realStdout, stdout, stderr, gf); code != ExitSuccess {
				return code
			}
		}
		return ExitSuccess
	default: // handoff
		canonical, err := os.ReadFile(canonicalPath)
		if err != nil {
			fmt.Fprintf(stderr, "read canonical: %v\n", err)
			return ExitOpError
		}
		for _, h := range targets {
			path, err := writePortHandoffPrompt(home, opts.skill, h, buildPortPrompt(string(canonical), h))
			if err != nil {
				fmt.Fprintf(stderr, "write handoff prompt: %v\n", err)
				return ExitOpError
			}
			fmt.Fprintf(stdout, "Wrote port prompt for %s to %s\n", h, path)
			fmt.Fprintf(stdout, "Import with: skills-manager port %s --to %s --apply <agent-output.md>\n", opts.skill, h)
		}
		return ExitSuccess
	}
}

func parsePortOptions(args []string) (portOptions, error) {
	var opts portOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--auto":
			opts.auto = true
		case arg == "--handoff":
			opts.handoff = true
		case arg == "--to":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--to requires a comma-separated harness list")
			}
			i++
			opts.toRaw = args[i]
		case strings.HasPrefix(arg, "--to="):
			opts.toRaw = strings.TrimPrefix(arg, "--to=")
		case arg == "--apply":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--apply requires a file path")
			}
			i++
			opts.applyFile = args[i]
		case strings.HasPrefix(arg, "--apply="):
			opts.applyFile = strings.TrimPrefix(arg, "--apply=")
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown port flag: %s", arg)
		default:
			if opts.skill != "" {
				return opts, fmt.Errorf("unexpected argument: %s", arg)
			}
			opts.skill = arg
		}
	}
	if opts.skill == "" {
		return opts, fmt.Errorf("usage: skills-manager port <skill> --to <harnesses> [--auto | --handoff | --apply <file>]")
	}
	modes := 0
	if opts.auto {
		modes++
	}
	if opts.handoff {
		modes++
	}
	if opts.applyFile != "" {
		modes++
	}
	if modes > 1 {
		return opts, fmt.Errorf("choose at most one of --auto, --handoff, --apply")
	}
	return opts, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func buildPortPrompt(canonical, harness string) string {
	var b strings.Builder
	if bundled := readBundledSkillMarkdown("skills-port"); bundled != "" {
		b.WriteString(bundled)
	} else {
		b.WriteString("Port the following SKILL.md to the target harness. Preserve name and description intent, adapt harness-specific patterns, keep execution requirements, add no hostile instructions, and output only a valid SKILL.md.\n")
	}
	b.WriteString("\n\n## Target harness\n")
	b.WriteString(harness)
	b.WriteString("\n\n## Canonical SKILL.md to port\n\n")
	b.WriteString(canonical)
	b.WriteString("\n")
	return b.String()
}

// applyPort validates a ported SKILL.md and, if valid, saves it as a variant.
func applyPort(skillDir, skill, harness, canonName, canonDesc, ported, canonicalPath string, realStdout, stdout, stderr io.Writer, gf globalFlags) int {
	ported = stripCodeFences(ported)
	if err := validatePortedSkill(canonName, ported, harness); err != nil {
		fmt.Fprintf(stderr, "rejected port for %s: %v\n", harness, err)
		return ExitOpError
	}
	variantFile := "SKILL." + harness + ".md"
	if err := os.WriteFile(filepath.Join(skillDir, variantFile), []byte(ensureTrailingNewline(ported)), 0o644); err != nil {
		fmt.Fprintf(stderr, "write variant: %v\n", err)
		return ExitOpError
	}
	fp, _, err := fingerprintSkillMd(canonicalPath)
	if err != nil {
		fmt.Fprintf(stderr, "fingerprint canonical: %v\n", err)
		return ExitOpError
	}
	vf, _, err := readVariants(skillDir)
	if err != nil {
		fmt.Fprintf(stderr, "read variants: %v\n", err)
		return ExitOpError
	}
	if vf.Overrides == nil {
		vf.Overrides = map[string]string{}
	}
	if vf.Default == "" {
		vf.Default = "SKILL.md"
	}
	vf.Overrides[harness] = variantFile
	vf.CanonicalFingerprint = fp
	vf.LastPorted = time.Now().UTC().Format(time.RFC3339)
	vf.PortedBy = "skills-port"
	if err := writeVariants(skillDir, vf); err != nil {
		fmt.Fprintf(stderr, "write variants: %v\n", err)
		return ExitOpError
	}
	// Promote the skill's compatibility so match/install/compile actually select
	// the ported variant for the target harness; without this an exclusive skill
	// stays filtered out by canonical compatibility.
	if err := promotePortedCompatibility(skillDir, skill, harness, stderr); err != nil {
		fmt.Fprintf(stderr, "warning: port saved but compatibility not promoted for %s: %v\n", harness, err)
	}
	if gf.JSON {
		_ = writeJSON(realStdout, map[string]interface{}{"skill": skill, "harness": harness, "variant": variantFile, "saved": true})
		return ExitSuccess
	}
	fmt.Fprintf(stdout, "✓ ported %s for %s → %s\n", skill, harness, variantFile)
	return ExitSuccess
}

// validatePortedSkill enforces the rules in the skills-port bundled skill:
// preserved name, present description, target-harness compatibility declared,
// no hostile instructions, and valid frontmatter.
func validatePortedSkill(canonName, ported, harness string) error {
	if !strings.HasPrefix(strings.TrimSpace(ported), "---") {
		return fmt.Errorf("output is not a SKILL.md (missing frontmatter)")
	}
	trimmed := strings.TrimSpace(ported)
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
	if fm.Name != canonName {
		return fmt.Errorf("name changed (%q != canonical %q)", fm.Name, canonName)
	}
	if strings.TrimSpace(fm.Description) == "" {
		return fmt.Errorf("missing description")
	}
	declaresTarget := fm.Exclusive == harness
	for _, h := range fm.Compatible {
		if strings.TrimSpace(h) == harness {
			declaresTarget = true
		}
	}
	if !declaresTarget {
		return fmt.Errorf("ported frontmatter does not declare compatibility with target harness %q", harness)
	}
	for _, line := range strings.Split(ported, "\n") {
		if looksSuspicious(strings.ToLower(strings.TrimSpace(line))) {
			return fmt.Errorf("ported content contains a hostile/policy-bypassing instruction")
		}
	}
	return nil
}

// stripCodeFences removes a wrapping ```markdown / ``` fence some models add.
func stripCodeFences(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") {
		if nl := strings.IndexByte(t, '\n'); nl != -1 {
			t = t[nl+1:]
		}
		t = strings.TrimSuffix(strings.TrimRight(t, "\n"), "```")
	}
	return strings.TrimSpace(t)
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// promotePortedCompatibility ensures the skill's effective compatibility
// includes the ported harness, so selectors (match/install/compile) stop
// filtering it out. Portable skills already match all harnesses; exclusive and
// compatible skills are promoted to compatible with the union via the `set`
// command (which rewrites frontmatter + meta and rebuilds the catalog).
func promotePortedCompatibility(skillDir, skill, harness string, stderr io.Writer) error {
	meta, _ := readSkillMeta(filepath.Join(skillDir, ".skill-meta.yaml"))
	var union []string
	switch compatibilityLabel(meta.Compatibility) {
	case "portable", "unknown":
		return nil // already compatible with every harness
	case "exclusive":
		union = dedupeStrings([]string{meta.Compatibility.Harness, harness})
	case "compatible":
		if containsFold(meta.Compatibility.Harnesses, harness) {
			return nil // target already declared
		}
		union = dedupeStrings(append(append([]string{}, meta.Compatibility.Harnesses...), harness))
	default:
		return nil
	}
	var out, errBuf bytes.Buffer
	code := Run([]string{"--non-interactive", "--quiet", "set", skill, "--compatibility", "compatible", "--harnesses", strings.Join(union, ",")}, &out, &errBuf)
	if code != ExitSuccess {
		return fmt.Errorf("%s", strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func writePortHandoffPrompt(home, skill, harness, prompt string) (string, error) {
	dir := filepath.Join(home, "ports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, skill+"."+harness+".prompt.md")
	if err := os.WriteFile(path, []byte(prompt), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
