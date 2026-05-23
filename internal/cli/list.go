package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type listOptions struct {
	category string
	tag      string
	rebuild  bool
}

type showOptions struct{}

func runList(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseListOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	humanOut := gf.outWriter(stdout)

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

	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	var cat catalog
	catalogExists := true

	if _, err := os.Stat(catalogPath); err != nil {
		catalogExists = false
	}

	if opts.rebuild || !catalogExists {
		var err error
		cat, err = rebuildCatalogFromLibrary(libraryPath)
		if err != nil {
			fmt.Fprintf(stderr, "rebuild catalog: %v\n", err)
			return ExitOpError
		}
		if err := writeCatalog(catalogPath, cat); err != nil {
			fmt.Fprintf(stderr, "write catalog: %v\n", err)
			return ExitOpError
		}
	} else {
		var err error
		cat, err = readCatalog(catalogPath)
		if err != nil {
			fmt.Fprintf(stderr, "read catalog: %v\n", err)
			return ExitOpError
		}
	}

	var filtered []catalogSkill
	for _, skill := range cat.Skills {
		if opts.category != "" {
			found := false
			for _, c := range skill.Categories {
				if c == opts.category {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		if opts.tag != "" {
			found := false
			for _, t := range skill.Tags {
				if t == opts.tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		filtered = append(filtered, skill)
	}

	if gf.JSON {
		output := make([]map[string]interface{}, len(filtered))
		for i, skill := range filtered {
			output[i] = map[string]interface{}{
				"name":          skill.Name,
				"summary":       skill.Summary,
				"categories":    skill.Categories,
				"tags":          skill.Tags,
				"compatibility": skill.Compatibility,
			}
		}
		if err := writeJSON(stdout, output); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	} else {
		for _, skill := range filtered {
			summary := skill.Summary
			if len(summary) > 50 {
				summary = summary[:50]
			}
			fmt.Fprintf(humanOut, "%s  %s\n", skill.Name, summary)
		}
	}

	return ExitSuccess
}

func runShow(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: skills-manager show <skill>")
		return ExitUsageError
	}

	_, skillName, err := parseShowOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	if skillName == "" {
		fmt.Fprintln(stderr, "no skill name provided")
		return ExitUsageError
	}
	humanOut := gf.outWriter(stdout)

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

	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	var cat catalog
	catalogExists := true

	if _, err := os.Stat(catalogPath); err != nil {
		catalogExists = false
	}

	if !catalogExists {
		var err error
		cat, err = rebuildCatalogFromLibrary(libraryPath)
		if err != nil {
			fmt.Fprintf(stderr, "rebuild catalog: %v\n", err)
			return ExitOpError
		}
		if err := writeCatalog(catalogPath, cat); err != nil {
			fmt.Fprintf(stderr, "write catalog: %v\n", err)
			return ExitOpError
		}
	} else {
		var err error
		cat, err = readCatalog(catalogPath)
		if err != nil {
			fmt.Fprintf(stderr, "read catalog: %v\n", err)
			return ExitOpError
		}
	}

	var skill *catalogSkill
	for i, s := range cat.Skills {
		if s.Name == skillName {
			skill = &cat.Skills[i]
			break
		}
	}

	if skill == nil {
		fmt.Fprintf(stderr, "skill %q not found\n", skillName)
		return ExitOpError
	}

	skillPath := filepath.Join(libraryPath, skillName)
	var meta skillMeta
	metaPath := filepath.Join(skillPath, ".skill-meta.yaml")
	if _, err := os.Stat(metaPath); err == nil {
		meta, _ = readSkillMeta(metaPath)
	}

	var fp string
	var sz int64
	skillMdPath := filepath.Join(skillPath, "SKILL.md")
	if _, err := os.Stat(skillMdPath); err == nil {
		hash, size, _ := fingerprintSkillMd(skillMdPath)
		fp = hash
		sz = size
	}
	displayFp := fp
	if len(displayFp) > 12 {
		displayFp = displayFp[:12]
	}

	if gf.JSON {
		fingerprintOut := meta.Fingerprint
		if fp != "" {
			fingerprintOut.SHA256 = fp
			fingerprintOut.Size = sz
		}
		output := map[string]interface{}{
			"name":          skill.Name,
			"summary":       skill.Summary,
			"categories":    skill.Categories,
			"tags":          skill.Tags,
			"compatibility": skill.Compatibility,
			"requirements":  skill.Requirements,
			"origin":        meta.Origin,
			"fingerprint":   fingerprintOut,
		}

		var installPaths []string
		for harness := range harnessProjectPaths {
			installPaths = append(installPaths, filepath.Join(harnessProjectPaths[harness], skillName))
		}
		sort.Strings(installPaths)
		output["install_locations"] = installPaths

		if err := writeJSON(stdout, output); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	} else {
		fmt.Fprintf(humanOut, "Name: %s\n", skill.Name)
		if skill.Summary != "" {
			fmt.Fprintf(humanOut, "Summary: %s\n", skill.Summary)
		}

		if len(skill.Categories) > 0 {
			fmt.Fprintf(humanOut, "Categories: %s\n", strings.Join(skill.Categories, ", "))
		}

		if len(skill.Tags) > 0 {
			fmt.Fprintf(humanOut, "Tags: %s\n", strings.Join(skill.Tags, ", "))
		}

		fmt.Fprintf(humanOut, "Compatibility: %s\n", skill.Compatibility.Mode)
		if skill.Compatibility.Harness != "" {
			fmt.Fprintf(humanOut, "  Harness: %s\n", skill.Compatibility.Harness)
		}
		if len(skill.Compatibility.Harnesses) > 0 {
			fmt.Fprintf(humanOut, "  Harnesses: %s\n", strings.Join(skill.Compatibility.Harnesses, ", "))
		}

		// Render all requirement kinds (tools, MCP servers, model) when any are present.
		// Previously only Tools were shown, hiding MCP/model requirements (Codex P2).
		hasReq := len(skill.Requirements.Tools) > 0 ||
			len(skill.Requirements.MCPServers) > 0 ||
			skill.Requirements.Model.ToolUse != ""

		if hasReq {
			fmt.Fprint(humanOut, "Requirements:\n")

			for _, tool := range skill.Requirements.Tools {
				status := "optional"
				if tool.Required {
					status = "required"
				}
				fmt.Fprintf(humanOut, "  - tool %s (%s)\n", tool.Name, status)
			}
			for _, mcp := range skill.Requirements.MCPServers {
				status := "optional"
				if mcp.Required {
					status = "required"
				}
				fmt.Fprintf(humanOut, "  - mcp %s (%s)\n", mcp.Name, status)
			}
			if skill.Requirements.Model.ToolUse != "" {
				fmt.Fprintf(humanOut, "  - model tool_use: %s\n", skill.Requirements.Model.ToolUse)
			}
		}

		if meta.Origin.Type != "" {
			fmt.Fprintf(humanOut, "Origin: %s\n", meta.Origin.Type)
			if meta.Origin.Source != "" {
				fmt.Fprintf(humanOut, "  Source: %s\n", meta.Origin.Source)
			}
			if meta.Origin.Version != "" {
				fmt.Fprintf(humanOut, "  Version: %s\n", meta.Origin.Version)
			}
		}

		if displayFp != "" || sz > 0 {
			fmt.Fprint(humanOut, "Fingerprint: ")
			if displayFp != "" {
				fmt.Fprint(humanOut, displayFp)
			}
			if sz > 0 {
				fmt.Fprintf(humanOut, " (%d bytes)", sz)
			}
			fmt.Fprint(humanOut, "\n")
		}

		var installPaths []string
		for harness := range harnessProjectPaths {
			installPaths = append(installPaths, harnessProjectPaths[harness]+"/"+skillName)
		}
		sort.Strings(installPaths)
		if len(installPaths) > 0 {
			fmt.Fprint(humanOut, "Install Locations:\n")
			for _, path := range installPaths {
				fmt.Fprintf(humanOut, "  - %s\n", path)
			}
		}
	}

	return ExitSuccess
}

func parseListOptions(args []string) (listOptions, error) {
	opts := listOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--category":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--category requires a value")
			}
			opts.category = args[i+1]
			i++
		case "--tag":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--tag requires a value")
			}
			opts.tag = args[i+1]
			i++
		case "--rebuild":
			opts.rebuild = true
		default:
			return opts, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return opts, nil
}

func parseShowOptions(args []string) (showOptions, string, error) {
	opts := showOptions{}
	var skillName string

	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			skillName = args[i]
			continue
		}
		return opts, "", fmt.Errorf("unknown flag: %s", args[i])
	}

	return opts, skillName, nil
}
