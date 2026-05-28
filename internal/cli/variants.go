package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// variantsFile is the per-skill .variants.yaml: a canonical SKILL.md plus
// per-harness ported overrides, with a fingerprint of the canonical so drift
// can flag the ports as stale.
type variantsFile struct {
	Version              int               `yaml:"version"`
	Default              string            `yaml:"default,omitempty"`
	Overrides            map[string]string `yaml:"overrides,omitempty"`
	CanonicalFingerprint string            `yaml:"canonical_fingerprint,omitempty"`
	LastPorted           string            `yaml:"last_ported,omitempty"`
	PortedBy             string            `yaml:"ported_by,omitempty"`
}

func variantsPath(skillDir string) string { return filepath.Join(skillDir, ".variants.yaml") }

// readVariants loads a skill's .variants.yaml. The bool reports whether the file
// exists; a missing file is not an error.
func readVariants(skillDir string) (variantsFile, bool, error) {
	data, err := os.ReadFile(variantsPath(skillDir))
	if err != nil {
		if os.IsNotExist(err) {
			return variantsFile{}, false, nil
		}
		return variantsFile{}, false, err
	}
	var vf variantsFile
	if err := yaml.Unmarshal(data, &vf); err != nil {
		return variantsFile{}, true, err
	}
	if vf.Version == 0 {
		vf.Version = 1
	}
	return vf, true, nil
}

func writeVariants(skillDir string, vf variantsFile) error {
	if vf.Version == 0 {
		vf.Version = 1
	}
	return writeYAMLFile(variantsPath(skillDir), vf)
}

// isLocalFile reports whether name is a single local filename (no directory
// separators, no traversal). Variant files must live inside the skill directory,
// so synced/hand-edited metadata can't read or delete files elsewhere.
func isLocalFile(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	return filepath.Base(name) == name
}

// selectVariantFile returns the override file for the first harness (in the
// given order) that has one, or "" to mean the canonical SKILL.md. Unsafe
// (non-local) filenames are ignored.
func selectVariantFile(vf variantsFile, harnesses []string) string {
	for _, h := range harnesses {
		if file, ok := vf.Overrides[h]; ok && isLocalFile(file) {
			return file
		}
	}
	return ""
}

// applyVariantToTarget rewrites an installed skill copy so the harness sees only
// a single SKILL.md — the ported variant for one of the given harnesses when
// present, else the canonical — and never the variant source files or
// .variants.yaml. Called after the skill dir is copied to target.
func applyVariantToTarget(srcSkillDir, target string, harnesses []string) error {
	vf, ok, err := readVariants(srcSkillDir)
	if err != nil {
		return err
	}
	if !ok {
		return nil // no variants → copied dir is already correct
	}
	if chosen := selectVariantFile(vf, harnesses); chosen != "" && chosen != "SKILL.md" {
		srcFile := filepath.Join(srcSkillDir, chosen)
		info, statErr := os.Stat(srcFile)
		if statErr != nil {
			return fmt.Errorf("variant %q for skill: %w", chosen, statErr)
		}
		if err := copyFile(srcFile, filepath.Join(target, "SKILL.md"), info.Mode()); err != nil {
			return err
		}
	}
	// Strip variant sources + the variants manifest from the harness copy.
	// Only touch safe local filenames so malformed metadata can't delete
	// siblings outside the copied skill directory.
	_ = os.Remove(filepath.Join(target, ".variants.yaml"))
	for _, file := range vf.Overrides {
		if file != "SKILL.md" && isLocalFile(file) {
			_ = os.Remove(filepath.Join(target, file))
		}
	}
	if vf.Default != "" && vf.Default != "SKILL.md" && isLocalFile(vf.Default) {
		_ = os.Remove(filepath.Join(target, vf.Default))
	}
	return nil
}

// validateVariantForHarnesses checks, before the install destructively replaces
// a target, that the skill's variant metadata is readable and that any selected
// override file actually exists. This avoids leaving a managed install as a raw
// library copy when a declared variant is missing.
func validateVariantForHarnesses(srcSkillDir string, harnesses []string) error {
	vf, ok, err := readVariants(srcSkillDir)
	if err != nil {
		return fmt.Errorf("read variants: %w", err)
	}
	if !ok {
		return nil
	}
	chosen := selectVariantFile(vf, harnesses)
	if chosen == "" || chosen == "SKILL.md" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(srcSkillDir, chosen)); err != nil {
		return fmt.Errorf("declared variant %q is missing: %w", chosen, err)
	}
	return nil
}

// harnessesForBase returns the subset of harnesses (in canonical install order)
// that install into the given project target base, so variant selection for a
// shared path (e.g. .agents/skills) is deterministic.
func harnessesForBase(harnesses []string, base string) []string {
	order := []string{"claude", "codex", "grok", "antigravity", "gemini", "hermes", "openclaw"}
	inSet := map[string]bool{}
	for _, h := range harnesses {
		inSet[h] = true
	}
	var out []string
	for _, h := range order {
		if inSet[h] && harnessProjectPaths[h] == base {
			out = append(out, h)
		}
	}
	return out
}

// variantsStale reports whether the canonical SKILL.md fingerprint differs from
// the fingerprint recorded when the variants were last ported.
func variantsStale(skillDir string) (bool, error) {
	vf, ok, err := readVariants(skillDir)
	if err != nil || !ok {
		return false, err
	}
	if vf.CanonicalFingerprint == "" || len(vf.Overrides) == 0 {
		return false, nil
	}
	fp, _, err := fingerprintSkillMd(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return false, err
	}
	return fp != vf.CanonicalFingerprint, nil
}

// staleVariantSkills returns library skill names whose variants are stale.
func staleVariantSkills(libraryPath string) []string {
	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		return nil
	}
	var stale []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if ok, err := variantsStale(filepath.Join(libraryPath, e.Name())); err == nil && ok {
			stale = append(stale, e.Name())
		}
	}
	sort.Strings(stale)
	return stale
}

// runVariants implements `skills-manager variants [skill] [--refresh]`.
//
//	(no args)          list skills that have variants, flagging stale ones
//	<skill>            show that skill's variants and staleness
//	<skill> --refresh  re-stamp canonical_fingerprint to the current SKILL.md
//	                   (acknowledging the ports were refreshed; content
//	                   re-porting itself is done via the skills-port skill)
func runVariants(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	var skill string
	var refresh bool
	for _, a := range args {
		switch {
		case a == "--refresh":
			refresh = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "unknown variants flag: %s\n", a)
			return ExitUsageError
		default:
			skill = a
		}
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

	if refresh {
		if skill == "" {
			fmt.Fprintln(stderr, "usage: skills-manager variants <skill> --refresh")
			return ExitUsageError
		}
		return refreshVariants(libraryPath, skill, realStdout, stdout, stderr, gf)
	}

	if skill != "" {
		return showVariants(libraryPath, skill, realStdout, stdout, stderr, gf)
	}
	return listVariants(libraryPath, realStdout, stdout, stderr, gf)
}

func refreshVariants(libraryPath, skill string, realStdout, stdout, stderr io.Writer, gf globalFlags) int {
	if err := validateSkillName(skill); err != nil {
		fmt.Fprintf(stderr, "invalid skill name: %v\n", err)
		return ExitUsageError
	}
	skillDir := filepath.Join(libraryPath, skill)
	vf, ok, err := readVariants(skillDir)
	if err != nil {
		fmt.Fprintf(stderr, "read variants: %v\n", err)
		return ExitOpError
	}
	if !ok {
		fmt.Fprintf(stderr, "%s has no .variants.yaml\n", skill)
		return ExitOpError
	}
	fp, _, err := fingerprintSkillMd(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		fmt.Fprintf(stderr, "fingerprint canonical: %v\n", err)
		return ExitOpError
	}
	vf.CanonicalFingerprint = fp
	vf.LastPorted = time.Now().UTC().Format(time.RFC3339)
	if err := writeVariants(skillDir, vf); err != nil {
		fmt.Fprintf(stderr, "write variants: %v\n", err)
		return ExitOpError
	}
	if gf.JSON {
		_ = writeJSON(realStdout, map[string]interface{}{"skill": skill, "refreshed": true, "canonical_fingerprint": fp})
		return ExitSuccess
	}
	fmt.Fprintf(stdout, "✓ %s variants marked current (canonical fingerprint re-stamped)\n", skill)
	return ExitSuccess
}

func showVariants(libraryPath, skill string, realStdout, stdout, stderr io.Writer, gf globalFlags) int {
	skillDir := filepath.Join(libraryPath, skill)
	vf, ok, err := readVariants(skillDir)
	if err != nil {
		fmt.Fprintf(stderr, "read variants: %v\n", err)
		return ExitOpError
	}
	if !ok {
		fmt.Fprintf(stderr, "%s has no variants\n", skill)
		return ExitOpError
	}
	stale, _ := variantsStale(skillDir)
	if gf.JSON {
		_ = writeJSON(realStdout, map[string]interface{}{
			"skill": skill, "overrides": vf.Overrides, "stale": stale,
			"canonical_fingerprint": vf.CanonicalFingerprint, "last_ported": vf.LastPorted,
		})
		return ExitSuccess
	}
	fmt.Fprintf(stdout, "%s%s\n", skill, staleSuffix(stale))
	harnesses := make([]string, 0, len(vf.Overrides))
	for h := range vf.Overrides {
		harnesses = append(harnesses, h)
	}
	sort.Strings(harnesses)
	for _, h := range harnesses {
		fmt.Fprintf(stdout, "  %s -> %s\n", h, vf.Overrides[h])
	}
	return ExitSuccess
}

func listVariants(libraryPath string, realStdout, stdout, stderr io.Writer, gf globalFlags) int {
	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		fmt.Fprintf(stderr, "read library: %v\n", err)
		return ExitOpError
	}
	type row struct {
		Skill string `json:"skill"`
		Stale bool   `json:"stale"`
	}
	var rows []row
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok, _ := readVariants(filepath.Join(libraryPath, e.Name())); !ok {
			continue
		}
		stale, _ := variantsStale(filepath.Join(libraryPath, e.Name()))
		rows = append(rows, row{Skill: e.Name(), Stale: stale})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Skill < rows[j].Skill })
	if gf.JSON {
		if rows == nil {
			rows = []row{}
		}
		_ = writeJSON(realStdout, rows)
		return ExitSuccess
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "No skills with variants.")
		return ExitSuccess
	}
	for _, r := range rows {
		fmt.Fprintf(stdout, "%s%s\n", r.Skill, staleSuffix(r.Stale))
	}
	return ExitSuccess
}

func staleSuffix(stale bool) string {
	if stale {
		return "  (stale — canonical changed; re-port with the skills-port skill, then `variants <skill> --refresh`)"
	}
	return ""
}
