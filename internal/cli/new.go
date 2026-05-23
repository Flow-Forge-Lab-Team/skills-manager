package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func runNew(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: skills-manager new <name>")
		return ExitUsageError
	}

	name := args[0]

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

func isValidSkillName(name string) bool {
	if len(name) < 3 || len(name) > 64 {
		return false
	}
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9-]+$`, name)
	return matched
}
