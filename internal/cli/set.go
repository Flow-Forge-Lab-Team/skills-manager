package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type setOptions struct {
	skillName         string
	compatibilityMode string
	harness           string
	harnesses         string
	reason            string
	json              bool
}

func runSet(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts := setOptions{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}

		if arg == "--help" || arg == "-h" {
			fmt.Fprintln(gf.outWriter(realStdout), helpText("set"))
			return ExitSuccess
		}

		if arg == "--json" {
			// Global --json is handled by gf, skip local parsing
			continue
		}

		if arg == "--compatibility" {
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: --compatibility requires a value")
				return ExitUsageError
			}
			i++
			opts.compatibilityMode = args[i]
			continue
		}

		if arg == "--harness" {
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: --harness requires a value")
				return ExitUsageError
			}
			i++
			opts.harness = args[i]
			continue
		}

		if arg == "--harnesses" {
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: --harnesses requires a value")
				return ExitUsageError
			}
			i++
			opts.harnesses = args[i]
			continue
		}

		if arg == "--reason" {
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: --reason requires a value")
				return ExitUsageError
			}
			i++
			opts.reason = args[i]
			continue
		}

		if !strings.HasPrefix(arg, "-") {
			if opts.skillName == "" {
				opts.skillName = arg
				continue
			}
		}

		fmt.Fprintf(stderr, "error: unknown flag or argument: %s\n", arg)
		return ExitUsageError
	}

	if opts.skillName == "" {
		fmt.Fprintln(stderr, "error: skill name is required")
		return ExitUsageError
	}

	if opts.compatibilityMode == "" {
		fmt.Fprintln(stderr, "error: --compatibility is required")
		return ExitUsageError
	}

	// Validate mode-specific requirements
	switch opts.compatibilityMode {
	case "portable":
		// No requirements
	case "exclusive":
		if opts.harness == "" {
			fmt.Fprintln(stderr, "error: --harness is required for exclusive mode")
			return ExitUsageError
		}
	case "compatible":
		if opts.harnesses == "" {
			fmt.Fprintln(stderr, "error: --harnesses is required for compatible mode")
			return ExitUsageError
		}
	default:
		fmt.Fprintf(stderr, "error: invalid compatibility mode: %s (must be portable, compatible, or exclusive)\n", opts.compatibilityMode)
		return ExitUsageError
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "error: cannot determine skills-manager home: %v\n", err)
		return 1
	}
	libraryPath := filepath.Join(home, "library")

	// Find the skill directory
	skillPath := filepath.Join(libraryPath, opts.skillName)
	skillMdPath := filepath.Join(skillPath, "SKILL.md")
	metaPath := filepath.Join(skillPath, ".skill-meta.yaml")

	if _, err := os.Stat(skillMdPath); err != nil {
		fmt.Fprintf(stderr, "error: skill %q not found in library\n", opts.skillName)
		return ExitUsageError
	}

	// Read current SKILL.md
	skillContent, err := os.ReadFile(skillMdPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: cannot read %s: %v\n", skillMdPath, err)
		return 1
	}

	// Parse frontmatter and body
	text := string(skillContent)
	var frontmatter, body string

	if strings.HasPrefix(text, "---") {
		endIdx := strings.Index(text[3:], "---")
		if endIdx != -1 {
			frontmatter = text[3 : endIdx+3]
			body = text[endIdx+6:]
		} else {
			frontmatter = text
			body = ""
		}
	} else {
		body = text
	}

	// Parse frontmatter lines and rebuild with new compatibility
	lines := strings.Split(frontmatter, "\n")
	var newLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "compatible:") || strings.HasPrefix(trimmed, "exclusive:") || strings.HasPrefix(trimmed, "reason:") {
			continue
		}
		newLines = append(newLines, line)
	}

	// Add new compatibility declaration at the end of frontmatter
	// Find the last non-empty line
	for len(newLines) > 0 && strings.TrimSpace(newLines[len(newLines)-1]) == "" {
		newLines = newLines[:len(newLines)-1]
	}

	switch opts.compatibilityMode {
	case "exclusive":
		newLines = append(newLines, fmt.Sprintf("exclusive: %s", opts.harness))
		if opts.reason != "" {
			newLines = append(newLines, fmt.Sprintf("reason: %q", opts.reason))
		}
	case "compatible":
		harnessesStr := strings.Join(strings.Split(opts.harnesses, ","), ", ")
		newLines = append(newLines, fmt.Sprintf("compatible: [%s]", harnessesStr))
	case "portable":
		// No declaration needed
	}

	// Reconstruct frontmatter
	newFrontmatter := strings.Join(newLines, "\n")
	newSkillContent := "---\n" + newFrontmatter + "\n---\n" + body

	// Write back SKILL.md
	if err := os.WriteFile(skillMdPath, []byte(newSkillContent), 0644); err != nil {
		fmt.Fprintf(stderr, "error: cannot write %s: %v\n", skillMdPath, err)
		return 1
	}

	// Update .skill-meta.yaml (create if missing)
	meta, _ := readSkillMeta(metaPath)

	// Clear declared block for portable mode, otherwise ensure it exists and zero all fields
	if opts.compatibilityMode == "portable" {
		meta.Compatibility.Declared = nil
	} else {
		if meta.Compatibility.Declared == nil {
			meta.Compatibility.Declared = &compatibilityDeclaration{}
		}
		// Zero out all mode-specific fields before setting new ones (issue #3)
		meta.Compatibility.Declared.Mode = ""
		meta.Compatibility.Declared.Harness = ""
		meta.Compatibility.Declared.Harnesses = nil
		meta.Compatibility.Declared.Reason = ""

		// Set fields appropriate for the new mode
		meta.Compatibility.Declared.Mode = opts.compatibilityMode
		if opts.compatibilityMode == "exclusive" {
			meta.Compatibility.Declared.Harness = opts.harness
			meta.Compatibility.Declared.Reason = opts.reason
		} else if opts.compatibilityMode == "compatible" {
			meta.Compatibility.Declared.Harnesses = strings.Split(opts.harnesses, ",")
			for i := range meta.Compatibility.Declared.Harnesses {
				meta.Compatibility.Declared.Harnesses[i] = strings.TrimSpace(meta.Compatibility.Declared.Harnesses[i])
			}
		}
	}

	// Update effective state, zeroing out mode-specific fields
	meta.Compatibility.Mode = opts.compatibilityMode
	meta.Compatibility.Harness = ""
	meta.Compatibility.Harnesses = nil

	if opts.compatibilityMode == "exclusive" {
		meta.Compatibility.Harness = opts.harness
	} else if opts.compatibilityMode == "compatible" {
		meta.Compatibility.Harnesses = strings.Split(opts.harnesses, ",")
		for i := range meta.Compatibility.Harnesses {
			meta.Compatibility.Harnesses[i] = strings.TrimSpace(meta.Compatibility.Harnesses[i])
		}
	}

	writeSkillMeta(metaPath, meta)

	// Rebuild catalog to refresh library/catalog.yaml (issue #1)
	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	updatedCatalog, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		fmt.Fprintf(stderr, "warning: failed to rebuild catalog: %v\n", err)
	} else {
		if err := writeCatalog(catalogPath, updatedCatalog); err != nil {
			fmt.Fprintf(stderr, "warning: failed to write catalog: %v\n", err)
		}
	}

	// Use global flags for JSON and quiet (issue #2)
	humanOut := gf.outWriter(realStdout)
	if gf.JSON {
		fmt.Fprintf(realStdout, "{\"skill\":%q,\"mode\":%q}\n", opts.skillName, opts.compatibilityMode)
	} else {
		fmt.Fprintf(humanOut, "Set %s compatibility to %s\n", opts.skillName, opts.compatibilityMode)
	}

	return ExitSuccess
}
