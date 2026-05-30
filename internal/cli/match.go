package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type matchOptions struct {
	projectPath string
	explain     bool
	suggest     bool
}

func parseMatchOptions(args []string) (matchOptions, error) {
	opts := matchOptions{projectPath: "."}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			i++
			if i >= len(args) {
				return opts, errors.New("--project requires a path")
			}
			opts.projectPath = args[i]
		case "--explain":
			opts.explain = true
		case "--suggest":
			opts.suggest = true
		default:
			return opts, fmt.Errorf("unknown match argument: %s", args[i])
		}
	}
	return opts, nil
}

type scoredCandidate struct {
	Skill      catalogSkill
	Score      int
	Reasons    []string
	Rejections []string
	Warnings   []string
	Harnesses  []string
}

func computeMatchScore(skill catalogSkill, project projectConfig) (int, []string, []string) {
	always := set(project.AlwaysInclude)
	never := set(project.NeverInclude)
	if never[skill.Name] {
		return 0, nil, nil
	}
	var reasons, warnings []string
	if always[skill.Name] {
		return 999, []string{"always_include"}, warnings
	}
	pCats := set(project.Categories)
	catO := 0
	for _, c := range skill.Categories {
		if pCats[c] {
			catO++
		}
	}
	pTags := set(project.Tags)
	tagO := 0
	for _, t := range skill.Tags {
		if pTags[t] {
			tagO++
		}
	}
	score := 1*catO + 2*tagO
	if catO > 0 {
		reasons = append(reasons, fmt.Sprintf("category overlap: %d", catO))
	}
	if tagO > 0 {
		reasons = append(reasons, fmt.Sprintf("tag overlap: %d", tagO))
	}
	// small stack negative signal
	stackTags := []string{"nodejs", "python", "go", "rust", "typescript", "java", "php", "csharp"}
	projStack := ""
	for _, t := range project.Tags {
		for _, s := range stackTags {
			if t == s {
				projStack = t
				break
			}
		}
		if projStack != "" {
			break
		}
	}
	skillStack := ""
	for _, t := range skill.Tags {
		for _, s := range stackTags {
			if t == s {
				skillStack = t
				break
			}
		}
		if skillStack != "" {
			break
		}
	}
	if projStack != "" && skillStack != "" && skillStack != projStack {
		score -= 3
		reasons = append(reasons, "stack mismatch penalty")
	}
	return score, reasons, warnings
}

func matchRejectionReasons(skill catalogSkill, project projectConfig) []string {
	never := set(project.NeverInclude)
	if never[skill.Name] {
		return []string{"listed in project never_include"}
	}
	always := set(project.AlwaysInclude)
	if always[skill.Name] {
		return nil
	}
	pCats := set(project.Categories)
	pTags := set(project.Tags)
	if !intersects(set(skill.Categories), pCats) && !intersects(set(skill.Tags), pTags) {
		return []string{"no category or tag overlap"}
	}
	return nil
}

func runMatch(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseMatchOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	stdout := gf.outWriter(realStdout)

	projectPath, err := absoluteProjectPath(opts.projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "project path: %v\n", err)
		return ExitOpError
	}

	projectConfigPath := filepath.Join(projectPath, ".skills", "project.yaml")
	if gf.Config != "" {
		projectConfigPath = gf.Config
	}

	project, err := readProjectConfig(projectConfigPath)
	noConfig := false
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "read project config: %v\n", err)
			return ExitOpError
		}
		noConfig = true
		project = projectConfig{}
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	libraryPath := filepath.Join(home, "library")
	catalog, err := readCatalog(filepath.Join(libraryPath, "catalog.yaml"))
	if err != nil {
		fmt.Fprintf(stderr, "read catalog: %v\n", err)
		return ExitOpError
	}

	if len(project.Categories) == 0 && len(project.Tags) == 0 {
		if noConfig {
			fmt.Fprintln(stdout, "no project config; candidates: none (or only always)")
		} else {
			fmt.Fprintln(stdout, "no categories/tags in project.yaml; use set or edit to configure")
		}
	}

	var scored []scoredCandidate
	never := set(project.NeverInclude)
	if opts.explain {
		for _, skill := range catalog.Skills {
			score, reasons, warnings := computeMatchScore(skill, project)
			rejections := matchRejectionReasons(skill, project)
			hs := compatibleHarnesses(skill.Compatibility, project.Harnesses)
			if len(rejections) == 0 && len(hs) == 0 {
				warnings = append(warnings, "no compatible harness in project")
			}
			mt := missingRequiredTools(skill.Requirements)
			mm := missingRequiredMCPServers(skill.Requirements)
			md := missingModelCapabilities(skill.Requirements)
			mc := missingRequiredCredentials(skill.Requirements)
			mr := missingRequiredScriptRuntimes(skill.Requirements)
			if len(mt)+len(mm)+len(md)+len(mc)+len(mr) > 0 {
				var parts []string
				if len(mt) > 0 {
					parts = append(parts, "tools="+strings.Join(mt, ","))
				}
				if len(mm) > 0 {
					parts = append(parts, "mcp_servers="+strings.Join(mm, ","))
				}
				if len(md) > 0 {
					parts = append(parts, "model="+strings.Join(md, ","))
				}
				if len(mc) > 0 {
					parts = append(parts, "credentials="+strings.Join(mc, ","))
				}
				if len(mr) > 0 {
					parts = append(parts, "runtimes="+strings.Join(mr, ","))
				}
				warnings = append(warnings, "missing required: "+strings.Join(parts, ", "))
			}
			scored = append(scored, scoredCandidate{
				Skill: skill, Score: score, Reasons: reasons, Rejections: rejections, Warnings: warnings, Harnesses: hs,
			})
		}
	} else {
		baseCands := selectInstallCandidates(catalog, project, "")
		for _, c := range baseCands {
			if never[c.Skill.Name] {
				continue
			}
			score, reasons, warnings := computeMatchScore(c.Skill, project)
			hs := compatibleHarnesses(c.Skill.Compatibility, project.Harnesses)
			if len(hs) == 0 {
				warnings = append(warnings, "no compatible harness in project")
			}
			scored = append(scored, scoredCandidate{
				Skill: c.Skill, Score: score, Reasons: reasons, Warnings: warnings, Harnesses: hs,
			})
		}
	}

	if opts.suggest {
		lock, _ := readInstallLock(filepath.Join(projectPath, ".skills", "installed.lock"))
		installed := map[string]bool{}
		for _, e := range lock.Skills {
			installed[e.Name] = true
		}
		var filtered []scoredCandidate
		for _, sc := range scored {
			if !installed[sc.Skill.Name] {
				filtered = append(filtered, sc)
			}
		}
		scored = filtered
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Skill.Name < scored[j].Skill.Name
	})

	for _, sc := range scored {
		if len(sc.Rejections) > 0 {
			fmt.Fprintf(stdout, "%s (rejected) - %s\n", sc.Skill.Name, strings.Join(sc.Rejections, ", "))
			continue
		}
		reasonStr := strings.Join(sc.Reasons, ", ")
		fmt.Fprintf(stdout, "%s (score: %d) - %s\n", sc.Skill.Name, sc.Score, reasonStr)
		if len(sc.Harnesses) > 0 {
			fmt.Fprintf(stdout, "  harnesses: %s\n", strings.Join(sc.Harnesses, ", "))
		}
		if len(sc.Warnings) > 0 {
			fmt.Fprintf(stdout, "  warnings: %s\n", strings.Join(sc.Warnings, "; "))
		}
	}

	if gf.JSON {
		type jsonCand struct {
			Name      string   `json:"name"`
			Score     int      `json:"score"`
			Reasons   []string `json:"reasons,omitempty"`
			Rejected  bool     `json:"rejected,omitempty"`
			Rejection []string `json:"rejection,omitempty"`
			Harnesses []string `json:"harnesses,omitempty"`
			Warnings  []string `json:"warnings,omitempty"`
		}
		var js []jsonCand
		for _, sc := range scored {
			js = append(js, jsonCand{
				Name: sc.Skill.Name, Score: sc.Score, Reasons: sc.Reasons,
				Rejected: len(sc.Rejections) > 0, Rejection: sc.Rejections,
				Harnesses: sc.Harnesses, Warnings: sc.Warnings,
			})
		}
		result := map[string]interface{}{
			"project":    projectPath,
			"candidates": js,
		}
		if err := writeJSON(realStdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	}
	return ExitSuccess
}
