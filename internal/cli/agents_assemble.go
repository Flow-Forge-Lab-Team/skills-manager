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

const (
	agentsBeginMarker = "<!-- skills-manager:begin (generated block — edit skills in your library, not here) -->"
	agentsEndMarker   = "<!-- skills-manager:end -->"
	agentsDefaultRank = 1000
)

type agentsSkillEntry struct {
	name  string
	order int
	body  string
}

// runAssemble is the explicit `skills-manager assemble [path]` entrypoint.
func runAssemble(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	projectArg := "."
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(stderr, "unknown assemble flag: %s\n", a)
			return ExitUsageError
		}
		projectArg = a
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
	wrote, path, err := assembleAgentsMd(home, projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "assemble AGENTS.md: %v\n", err)
		return ExitOpError
	}
	if gf.JSON {
		if err := writeJSON(realStdout, map[string]interface{}{"wrote": wrote, "path": path}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}
	if wrote {
		fmt.Fprintf(stdout, "Wrote %s\n", path)
	} else {
		fmt.Fprintln(stdout, "No always-on / AGENTS.md-marked skills installed; AGENTS.md left unchanged.")
	}
	return ExitSuccess
}

// assembleAgentsMd regenerates the generated block of <project>/AGENTS.md from
// the project's installed skills that are tagged always-on or opt in via an
// `agents_md: true` frontmatter flag. User-authored content outside the
// generated markers is preserved. Returns whether a file was written.
func assembleAgentsMd(home, projectPath string) (bool, string, error) {
	agentsPath := filepath.Join(projectPath, "AGENTS.md")
	libraryPath := filepath.Join(home, "library")

	lock, err := readInstallLock(filepath.Join(projectPath, ".skills", "installed.lock"))
	if err != nil {
		return false, agentsPath, nil // no install lock → nothing to assemble
	}
	cat, _, _ := loadTriageCatalog(home)
	tagsByName := map[string][]string{}
	for _, s := range cat.Skills {
		tagsByName[s.Name] = s.Tags
	}

	var entries []agentsSkillEntry
	for _, locked := range lock.Skills {
		skillMd := filepath.Join(libraryPath, locked.Name, "SKILL.md")
		include, order := agentsInclusion(skillMd, tagsByName[locked.Name])
		if !include {
			continue
		}
		body, err := readSkillBody(skillMd)
		if err != nil {
			continue
		}
		entries = append(entries, agentsSkillEntry{name: locked.Name, order: order, body: strings.TrimSpace(body)})
	}
	if len(entries) == 0 {
		// Nothing to include: leave any existing AGENTS.md untouched.
		return false, agentsPath, nil
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].order != entries[j].order {
			return entries[i].order < entries[j].order
		}
		return entries[i].name < entries[j].name
	})

	cfg, _ := readProjectConfig(filepath.Join(projectPath, ".skills", "project.yaml"))
	generated := buildAgentsBlock(cfg, entries)

	var existing string
	if data, err := os.ReadFile(agentsPath); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return false, agentsPath, err
	}
	merged := mergeAgentsBlock(existing, generated)
	if merged == existing {
		return false, agentsPath, nil
	}
	if err := os.WriteFile(agentsPath, []byte(merged), 0o644); err != nil {
		return false, agentsPath, err
	}
	return true, agentsPath, nil
}

// agentsInclusion reports whether a skill should be included in AGENTS.md and
// its ordering rank. A skill is included if its frontmatter sets
// `agents_md: true` or it carries the `always-on` tag (frontmatter or catalog).
func agentsInclusion(skillMd string, catalogTags []string) (bool, int) {
	data, err := os.ReadFile(skillMd)
	if err != nil {
		return false, agentsDefaultRank
	}
	text := string(data)
	include := containsFold(catalogTags, "always-on")
	order := agentsDefaultRank
	if strings.HasPrefix(text, "---") {
		if end := strings.Index(text[3:], "---"); end != -1 {
			var fm struct {
				AgentsMd *bool    `yaml:"agents_md"`
				Order    *int     `yaml:"order"`
				Tags     []string `yaml:"tags"`
			}
			if yaml.Unmarshal([]byte(text[3:end+3]), &fm) == nil {
				if fm.AgentsMd != nil && *fm.AgentsMd {
					include = true
				}
				if containsFold(fm.Tags, "always-on") {
					include = true
				}
				if fm.Order != nil {
					order = *fm.Order
				}
			}
		}
	}
	return include, order
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

func buildAgentsBlock(cfg projectConfig, entries []agentsSkillEntry) string {
	var b strings.Builder
	b.WriteString(agentsBeginMarker)
	b.WriteString("\n# Project agent guide\n\n")
	b.WriteString("_Generated by skills-manager from your installed skills. Edit the skills in your library; manual edits inside this block are overwritten. Content outside the block is preserved._\n\n")
	if name := strings.TrimSpace(cfg.Name); name != "" {
		fmt.Fprintf(&b, "**Project:** %s\n\n", name)
	}
	if len(cfg.Categories) > 0 {
		fmt.Fprintf(&b, "**Categories:** %s\n\n", strings.Join(cfg.Categories, ", "))
	}
	if len(cfg.Tags) > 0 {
		fmt.Fprintf(&b, "**Tags:** %s\n\n", strings.Join(cfg.Tags, ", "))
	}
	if stack := detectedStackTags(cfg.Tags); len(stack) > 0 {
		fmt.Fprintf(&b, "**Stack:** %s\n\n", strings.Join(stack, ", "))
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", e.name, e.body)
	}
	out := strings.TrimRight(b.String(), "\n")
	return out + "\n" + agentsEndMarker
}

// mergeAgentsBlock replaces the generated region in existing content with the
// freshly generated block, preserving user-authored content outside it. When no
// markers are present, the block is appended below any existing content.
func mergeAgentsBlock(existing, generated string) string {
	start := strings.Index(existing, agentsBeginMarker)
	end := strings.Index(existing, agentsEndMarker)
	if start != -1 && end != -1 && end > start {
		before := existing[:start]
		after := existing[end+len(agentsEndMarker):]
		return strings.TrimRight(before, " \t") + generated + after
	}
	trimmed := strings.TrimRight(existing, "\n")
	if trimmed == "" {
		return generated + "\n"
	}
	return trimmed + "\n\n" + generated + "\n"
}
