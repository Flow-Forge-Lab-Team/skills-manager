package cli

import (
	"encoding/json"
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
	jsonOut  bool
}

type showOptions struct {
	jsonOut bool
}

func runList(args []string, stdout io.Writer, stderr io.Writer) int {
	opts, err := parseListOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return 3
	}

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return 3
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
			return 3
		}
		if err := writeCatalog(catalogPath, cat); err != nil {
			fmt.Fprintf(stderr, "write catalog: %v\n", err)
			return 3
		}
	} else {
		var err error
		cat, err = readCatalog(catalogPath)
		if err != nil {
			fmt.Fprintf(stderr, "read catalog: %v\n", err)
			return 3
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

	if opts.jsonOut {
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
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "marshal json: %v\n", err)
			return 3
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		for _, skill := range filtered {
			summary := skill.Summary
			if len(summary) > 50 {
				summary = summary[:50]
			}
			fmt.Fprintf(stdout, "%s  %s\n", skill.Name, summary)
		}
	}

	return 0
}

func runShow(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: skills-manager show <skill>")
		return 2
	}

	opts, skillName, err := parseShowOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	if skillName == "" {
		fmt.Fprintln(stderr, "no skill name provided")
		return 2
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return 3
	}

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return 3
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
			return 3
		}
		if err := writeCatalog(catalogPath, cat); err != nil {
			fmt.Fprintf(stderr, "write catalog: %v\n", err)
			return 3
		}
	} else {
		var err error
		cat, err = readCatalog(catalogPath)
		if err != nil {
			fmt.Fprintf(stderr, "read catalog: %v\n", err)
			return 3
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
		return 3
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

	if opts.jsonOut {
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

		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "marshal json: %v\n", err)
			return 3
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprintf(stdout, "Name: %s\n", skill.Name)
		if skill.Summary != "" {
			fmt.Fprintf(stdout, "Summary: %s\n", skill.Summary)
		}

		if len(skill.Categories) > 0 {
			fmt.Fprintf(stdout, "Categories: %s\n", strings.Join(skill.Categories, ", "))
		}

		if len(skill.Tags) > 0 {
			fmt.Fprintf(stdout, "Tags: %s\n", strings.Join(skill.Tags, ", "))
		}

		fmt.Fprintf(stdout, "Compatibility: %s\n", skill.Compatibility.Mode)
		if skill.Compatibility.Harness != "" {
			fmt.Fprintf(stdout, "  Harness: %s\n", skill.Compatibility.Harness)
		}
		if len(skill.Compatibility.Harnesses) > 0 {
			fmt.Fprintf(stdout, "  Harnesses: %s\n", strings.Join(skill.Compatibility.Harnesses, ", "))
		}

		if len(skill.Requirements.Tools) > 0 {
			fmt.Fprint(stdout, "Requirements:\n")
			for _, tool := range skill.Requirements.Tools {
				status := "optional"
				if tool.Required {
					status = "required"
				}
				fmt.Fprintf(stdout, "  - %s (%s)\n", tool.Name, status)
			}
		}

		if meta.Origin.Type != "" {
			fmt.Fprintf(stdout, "Origin: %s\n", meta.Origin.Type)
			if meta.Origin.Source != "" {
				fmt.Fprintf(stdout, "  Source: %s\n", meta.Origin.Source)
			}
			if meta.Origin.Version != "" {
				fmt.Fprintf(stdout, "  Version: %s\n", meta.Origin.Version)
			}
		}

		if displayFp != "" || sz > 0 {
			fmt.Fprint(stdout, "Fingerprint: ")
			if displayFp != "" {
				fmt.Fprint(stdout, displayFp)
			}
			if sz > 0 {
				fmt.Fprintf(stdout, " (%d bytes)", sz)
			}
			fmt.Fprint(stdout, "\n")
		}

		var installPaths []string
		for harness := range harnessProjectPaths {
			installPaths = append(installPaths, harnessProjectPaths[harness]+"/"+skillName)
		}
		sort.Strings(installPaths)
		if len(installPaths) > 0 {
			fmt.Fprint(stdout, "Install Locations:\n")
			for _, path := range installPaths {
				fmt.Fprintf(stdout, "  - %s\n", path)
			}
		}
	}

	return 0
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
		case "--json":
			opts.jsonOut = true
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
		switch args[i] {
		case "--json":
			opts.jsonOut = true
		default:
			if !strings.HasPrefix(args[i], "-") {
				skillName = args[i]
			} else {
				return opts, "", fmt.Errorf("unknown flag: %s", args[i])
			}
		}
	}

	return opts, skillName, nil
}
