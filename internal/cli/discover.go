package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type discoverTool struct {
	ToolID          string   `json:"tool_id"`
	DisplayName     string   `json:"display_name"`
	Detected        bool     `json:"detected"`
	GlobalRoots     []string `json:"global_roots,omitempty"`
	ProjectPatterns []string `json:"project_patterns,omitempty"`
	Status          string   `json:"status"`
}

type discoverProject struct {
	ProjectID     string   `json:"project_id"`
	RootPath      string   `json:"root_path"`
	RepoRemote    string   `json:"repo_remote,omitempty"`
	DetectedTools []string `json:"detected_tools,omitempty"`
	LastScannedAt string   `json:"last_scanned_at"`
}

type discoverInstallation struct {
	InstallationID   string `json:"installation_id"`
	SkillName        string `json:"skill_name"`
	ToolID           string `json:"tool_id"`
	Scope            string `json:"scope"`
	ProjectID        string `json:"project_id,omitempty"`
	SourcePath       string `json:"source_path"`
	ContentPath      string `json:"content_path"`
	ContentSHA256    string `json:"content_sha256"`
	ContentSizeBytes int64  `json:"content_size_bytes"`
	ModifiedAt       string `json:"modified_at,omitempty"`
	Managed          bool   `json:"managed"`
	Ownership        string `json:"ownership"`
	Format           string `json:"format"`
	Present          bool   `json:"present"`
}

type discoverDriftGroup struct {
	GroupID         string   `json:"group_id"`
	GroupType       string   `json:"group_type"`
	SkillName       string   `json:"skill_name,omitempty"`
	ContentSHA256   string   `json:"content_sha256,omitempty"`
	InstallationIDs []string `json:"installation_ids"`
	Status          string   `json:"status"`
}

type discoverSummary struct {
	ToolsFound          int `json:"tools_found"`
	ToolsMissing        int `json:"tools_missing"`
	ProjectsFound       int `json:"projects_found"`
	GlobalSkills        int `json:"global_skills"`
	ProjectLocalSkills  int `json:"project_local_skills"`
	DriftGroups         int `json:"drift_groups"`
	DuplicateContent    int `json:"duplicate_content"`
	MissingToolCoverage int `json:"missing_tool_coverage"`
}

type discoverOutput struct {
	ScannedAt     string                 `json:"scanned_at"`
	Tools         []discoverTool         `json:"tools"`
	Projects      []discoverProject      `json:"projects,omitempty"`
	Installations []discoverInstallation `json:"installations"`
	DriftGroups   []discoverDriftGroup   `json:"drift_groups,omitempty"`
	Summary       discoverSummary        `json:"summary"`
}

type discoverRoot struct {
	toolID      string
	displayName string
	path        string
	pattern     string
	format      string
}

func runDiscover(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseDiscoverArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	if !opts.global && len(opts.projectRoots) == 0 {
		fmt.Fprintln(stderr, "discover requires an explicit scope: --global and/or --projects <roots>")
		return ExitUsageError
	}

	out, err := collectDiscovery(opts)
	if err != nil {
		fmt.Fprintf(stderr, "discover: %v\n", err)
		return ExitOpError
	}

	if gf.JSON {
		if err := writeJSON(stdout, out); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitOpError
		}
		return ExitSuccess
	}
	printDiscoverSummary(gf.outWriter(stdout), out)
	return ExitSuccess
}

type discoverOptions struct {
	global       bool
	projectRoots []string
}

func parseDiscoverArgs(args []string) (discoverOptions, error) {
	var opts discoverOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--global":
			opts.global = true
		case arg == "--projects":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--projects requires a comma-separated root list")
			}
			opts.projectRoots = appendSplitPaths(opts.projectRoots, args[i+1])
			i++
		case strings.HasPrefix(arg, "--projects="):
			opts.projectRoots = appendSplitPaths(opts.projectRoots, strings.TrimPrefix(arg, "--projects="))
		default:
			return opts, fmt.Errorf("unknown discover flag: %s", arg)
		}
	}
	return opts, nil
}

func appendSplitPaths(paths []string, value string) []string {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			paths = append(paths, part)
		}
	}
	return paths
}

func collectDiscovery(opts discoverOptions) (discoverOutput, error) {
	scannedAt := time.Now().UTC().Format(time.RFC3339)
	var out discoverOutput
	out.ScannedAt = scannedAt
	out.Tools = []discoverTool{}
	out.Projects = []discoverProject{}
	out.Installations = []discoverInstallation{}
	out.DriftGroups = []discoverDriftGroup{}

	home, err := os.UserHomeDir()
	if err != nil {
		return out, err
	}

	if opts.global {
		tools, installs := discoverGlobal(home)
		out.Tools = append(out.Tools, tools...)
		out.Installations = append(out.Installations, installs...)
	}

	if len(opts.projectRoots) > 0 {
		projectRoots, err := discoverProjectRoots(opts.projectRoots)
		if err != nil {
			return out, err
		}
		for _, root := range projectRoots {
			project, installs := collectProjectDiscovery(root, scannedAt)
			out.Projects = append(out.Projects, project)
			out.Installations = append(out.Installations, installs...)
		}
	}
	out.Tools = mergeProjectTools(out.Tools, out.Projects)

	sort.Slice(out.Tools, func(i, j int) bool { return out.Tools[i].ToolID < out.Tools[j].ToolID })
	sort.Slice(out.Projects, func(i, j int) bool { return out.Projects[i].RootPath < out.Projects[j].RootPath })
	sort.Slice(out.Installations, func(i, j int) bool {
		return out.Installations[i].InstallationID < out.Installations[j].InstallationID
	})
	out.DriftGroups = buildDiscoverDriftGroups(out.Installations)
	out.Summary = summarizeDiscovery(out)
	return out, nil
}

func discoverGlobal(home string) ([]discoverTool, []discoverInstallation) {
	roots := globalDiscoverRoots(home)
	var tools []discoverTool
	var installs []discoverInstallation
	for _, root := range roots {
		present := dirExists(root.path)
		tool := discoverTool{
			ToolID:      root.toolID,
			DisplayName: root.displayName,
			Detected:    present,
			GlobalRoots: []string{root.path},
			Status:      "missing",
		}
		if present {
			tool.Status = "present"
			installs = append(installs, discoverSkillDir(root, "global", "", root.path)...)
		}
		tools = append(tools, tool)
	}
	return tools, installs
}

func globalDiscoverRoots(home string) []discoverRoot {
	return []discoverRoot{
		{"antigravity", "Antigravity", filepath.Join(home, ".gemini", "antigravity", "skills"), "~/.gemini/antigravity/skills", "skill_md"},
		{"claude", "Claude Code", filepath.Join(home, ".claude", "skills"), "~/.claude/skills", "skill_md"},
		{"codex", "Codex", filepath.Join(home, ".codex", "skills"), "~/.codex/skills", "skill_md"},
		{"gemini", "Gemini CLI", filepath.Join(home, ".gemini", "skills"), "~/.gemini/skills", "skill_md"},
		{"grok", "Grok", filepath.Join(home, ".grok", "skills"), "~/.grok/skills", "skill_md"},
		{"hermes", "Hermes", filepath.Join(home, ".hermes", "skills"), "~/.hermes/skills", "skill_md"},
		{"openclaw", "OpenClaw", filepath.Join(home, ".openclaw", "skills"), "~/.openclaw/skills", "skill_md"},
	}
}

func discoverProjectRoots(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var repos []string
	for _, raw := range paths {
		root, err := absoluteProjectPath(raw)
		if err != nil {
			return nil, err
		}
		root = discoverWalkRoot(root)
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if path == root {
					return err
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if path != root && shouldPruneDiscoverDir(name, path) {
				return filepath.SkipDir
			}
			if isGitRepositoryRoot(path) {
				if !seen[path] {
					seen[path] = true
					repos = append(repos, path)
				}
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(repos)
	return repos, nil
}

func shouldPruneDiscoverDir(name, path string) bool {
	switch name {
	case ".git", "node_modules", ".next", "dist", "build", "vendor", ".venv", ".cache", ".turbo":
		return true
	}
	clean := filepath.ToSlash(path)
	return strings.Contains(clean, "/Documents/Codex/") && strings.Contains(clean, "/work/")
}

func collectProjectDiscovery(root string, scannedAt string) (discoverProject, []discoverInstallation) {
	projectID := "project-" + shortHash(root)
	project := discoverProject{
		ProjectID:     projectID,
		RootPath:      root,
		RepoRemote:    discoverGitRemote(root),
		LastScannedAt: scannedAt,
	}
	projectRoots := projectDiscoverRoots(root)

	toolSeen := map[string]bool{}
	var installs []discoverInstallation
	for _, pr := range projectRoots {
		switch pr.format {
		case "skill_md":
			if dirExists(pr.path) {
				found := discoverSkillDir(pr, "project", projectID, root)
				if len(found) > 0 {
					toolSeen[pr.toolID] = true
				}
				installs = append(installs, found...)
			}
		case "cursor_rule", "github_instruction":
			if dirExists(pr.path) {
				found := discoverFlatFiles(pr, "project", projectID, root)
				if len(found) > 0 {
					toolSeen[pr.toolID] = true
				}
				installs = append(installs, found...)
			}
		case "agents_md":
			if fileExists(pr.path) {
				if inst, ok := discoverOneFile(pr, "project", projectID, root, "AGENTS.md"); ok {
					toolSeen[pr.toolID] = true
					installs = append(installs, inst)
				}
			}
		}
	}
	for tool := range toolSeen {
		project.DetectedTools = append(project.DetectedTools, tool)
	}
	installs = applyDiscoverOwnership(root, installs)
	sort.Strings(project.DetectedTools)
	return project, installs
}

func applyDiscoverOwnership(projectRoot string, installs []discoverInstallation) []discoverInstallation {
	home, err := managerHome()
	if err != nil {
		return installs
	}
	manifest, err := readDiscoverManifest(home, projectRoot)
	if err != nil {
		return installs
	}
	managed := map[string]bool{}
	for _, rel := range manifest.ManagedPaths {
		managed[filepath.ToSlash(rel)] = true
	}
	for i := range installs {
		rel, err := filepath.Rel(projectRoot, installs[i].SourcePath)
		if err != nil {
			continue
		}
		if managed[filepath.ToSlash(rel)] {
			installs[i].Managed = true
			installs[i].Ownership = "manager"
		}
	}
	return installs
}

func readDiscoverManifest(home, projectRoot string) (installManifest, error) {
	manifest, err := readManifest(manifestPath(home, projectRoot))
	if err != nil || manifest.ProjectPath != "" || len(manifest.ManagedPaths) > 0 {
		return manifest, err
	}
	homeRoots := []string{home}
	if rawHome := os.Getenv("SKILLS_MANAGER_HOME"); rawHome != "" && rawHome != home {
		if absRawHome, err := filepath.Abs(rawHome); err == nil {
			homeRoots = append(homeRoots, absRawHome)
		}
	}
	if resolvedHome := discoverWalkRoot(home); resolvedHome != home {
		homeRoots = append(homeRoots, resolvedHome)
	}
	for _, homeRoot := range homeRoots {
		entries, err := os.ReadDir(filepath.Join(homeRoot, "manifests"))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			candidate, err := readManifest(filepath.Join(homeRoot, "manifests", entry.Name()))
			if err != nil {
				continue
			}
			if sameDiscoverPath(candidate.ProjectPath, projectRoot) {
				return candidate, nil
			}
		}
	}
	return manifest, nil
}

func sameDiscoverPath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	if discoverWalkRoot(a) == discoverWalkRoot(b) {
		return true
	}
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo)
}

func discoverSkillDir(root discoverRoot, scope, projectID, basePath string) []discoverInstallation {
	walkRoot := discoverWalkRoot(root.path)
	var installs []discoverInstallation
	err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		sourcePath := discoverSourcePath(root.path, walkRoot, path)
		if path != walkRoot && shouldPruneDiscoverDir(d.Name(), sourcePath) {
			return filepath.SkipDir
		}
		contentPath := filepath.Join(sourcePath, "SKILL.md")
		if !fileExists(contentPath) {
			if !d.IsDir() {
				return nil
			}
			return nil
		}
		skillName := filepath.Base(path)
		if decl, _, err := parseSkillFrontmatterFull(contentPath); err == nil && decl.name != "" {
			skillName = decl.name
		}
		if inst, ok := discoverInstallFromFile(root, scope, projectID, basePath, skillName, sourcePath, contentPath); ok {
			installs = append(installs, inst)
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Slice(installs, func(i, j int) bool { return installs[i].SourcePath < installs[j].SourcePath })
	return installs
}

func discoverWalkRoot(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	if dirExists(resolved) {
		return resolved
	}
	return path
}

func discoverSourcePath(sourceRoot, walkRoot, path string) string {
	if sourceRoot == walkRoot {
		return path
	}
	rel, err := filepath.Rel(walkRoot, path)
	if err != nil || rel == "." {
		return sourceRoot
	}
	return filepath.Join(sourceRoot, rel)
}

func projectDiscoverRoots(projectRoot string) []discoverRoot {
	return []discoverRoot{
		{"agents", "Agents", filepath.Join(projectRoot, ".agents", "skills"), ".agents/skills", "skill_md"},
		{"claude", "Claude Code", filepath.Join(projectRoot, ".claude", "skills"), ".claude/skills", "skill_md"},
		{"codex", "Codex", filepath.Join(projectRoot, ".codex", "skills"), ".codex/skills", "skill_md"},
		{"cursor", "Cursor", filepath.Join(projectRoot, ".cursor", "rules"), ".cursor/rules", "cursor_rule"},
		{"github_instructions", "GitHub Instructions", filepath.Join(projectRoot, ".github", "instructions"), ".github/instructions", "github_instruction"},
		{"grok", "Grok", filepath.Join(projectRoot, ".grok", "skills"), ".grok/skills", "skill_md"},
		{"agents_md", "AGENTS.md", filepath.Join(projectRoot, "AGENTS.md"), "AGENTS.md", "agents_md"},
	}
}

func mergeProjectTools(tools []discoverTool, projects []discoverProject) []discoverTool {
	byID := map[string]discoverTool{}
	for _, tool := range tools {
		byID[tool.ToolID] = tool
	}
	metadata := discoverToolMetadata()
	for _, project := range projects {
		patternByTool := map[string][]string{}
		for _, root := range projectDiscoverRoots(project.RootPath) {
			patternByTool[root.toolID] = append(patternByTool[root.toolID], root.pattern)
		}
		for _, toolID := range project.DetectedTools {
			tool := byID[toolID]
			if tool.ToolID == "" {
				tool = metadata[toolID]
				if tool.ToolID == "" {
					tool = discoverTool{ToolID: toolID, DisplayName: toolID}
				}
			}
			tool.Detected = true
			tool.Status = "present"
			tool.ProjectPatterns = appendUniqueStrings(tool.ProjectPatterns, patternByTool[toolID]...)
			byID[toolID] = tool
		}
	}
	merged := make([]discoverTool, 0, len(byID))
	for _, tool := range byID {
		sort.Strings(tool.ProjectPatterns)
		merged = append(merged, tool)
	}
	return merged
}

func discoverToolMetadata() map[string]discoverTool {
	meta := map[string]discoverTool{}
	for _, root := range globalDiscoverRoots("") {
		meta[root.toolID] = discoverTool{ToolID: root.toolID, DisplayName: root.displayName, Status: "missing"}
	}
	for _, root := range projectDiscoverRoots("") {
		if _, ok := meta[root.toolID]; !ok {
			meta[root.toolID] = discoverTool{ToolID: root.toolID, DisplayName: root.displayName, Status: "missing"}
		}
	}
	return meta
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return values
}

func discoverFlatFiles(root discoverRoot, scope, projectID, basePath string) []discoverInstallation {
	entries, err := os.ReadDir(root.path)
	if err != nil {
		return nil
	}
	var installs []discoverInstallation
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".mdc") {
			continue
		}
		contentPath := filepath.Join(root.path, name)
		skillName := strings.TrimSuffix(strings.TrimSuffix(name, ".mdc"), ".md")
		if inst, ok := discoverInstallFromFile(root, scope, projectID, basePath, skillName, contentPath, contentPath); ok {
			installs = append(installs, inst)
		}
	}
	return installs
}

func discoverOneFile(root discoverRoot, scope, projectID, basePath, skillName string) (discoverInstallation, bool) {
	return discoverInstallFromFile(root, scope, projectID, basePath, skillName, root.path, root.path)
}

func discoverInstallFromFile(root discoverRoot, scope, projectID, basePath, skillName, sourcePath, contentPath string) (discoverInstallation, bool) {
	hash, size, hashedPath, err := hashDiscoverContent(sourcePath, contentPath)
	if err != nil {
		return discoverInstallation{}, false
	}
	modified := ""
	if info, err := os.Stat(contentPath); err == nil {
		modified = info.ModTime().UTC().Format(time.RFC3339)
	}
	idSeed := strings.Join([]string{scope, projectID, root.toolID, sourcePath}, "|")
	return discoverInstallation{
		InstallationID:   "inst-" + shortHash(idSeed),
		SkillName:        skillName,
		ToolID:           root.toolID,
		Scope:            scope,
		ProjectID:        projectID,
		SourcePath:       sourcePath,
		ContentPath:      hashedPath,
		ContentSHA256:    hash,
		ContentSizeBytes: size,
		ModifiedAt:       modified,
		Managed:          false,
		Ownership:        "unmanaged",
		Format:           root.format,
		Present:          true,
	}, true
}

func hashDiscoverContent(sourcePath, contentPath string) (string, int64, string, error) {
	if info, err := os.Stat(sourcePath); err == nil && info.IsDir() {
		hash, size, err := hashDirContent(sourcePath)
		return hash, size, sourcePath, err
	}
	hash, size, err := hashFile(contentPath)
	return hash, size, contentPath, err
}

func buildDiscoverDriftGroups(installs []discoverInstallation) []discoverDriftGroup {
	byName := map[string]map[string][]string{}
	byHash := map[string]map[string][]string{}
	byNameScope := map[string]map[string]bool{}
	for _, inst := range installs {
		if byName[inst.SkillName] == nil {
			byName[inst.SkillName] = map[string][]string{}
		}
		byName[inst.SkillName][inst.ContentSHA256] = append(byName[inst.SkillName][inst.ContentSHA256], inst.InstallationID)
		if byHash[inst.ContentSHA256] == nil {
			byHash[inst.ContentSHA256] = map[string][]string{}
		}
		byHash[inst.ContentSHA256][inst.SkillName] = append(byHash[inst.ContentSHA256][inst.SkillName], inst.InstallationID)
		if byNameScope[inst.SkillName] == nil {
			byNameScope[inst.SkillName] = map[string]bool{}
		}
		byNameScope[inst.SkillName][inst.Scope] = true
	}

	var groups []discoverDriftGroup
	for name, hashes := range byName {
		if len(hashes) <= 1 {
			continue
		}
		var ids []string
		for _, hashIDs := range hashes {
			ids = append(ids, hashIDs...)
		}
		groups = append(groups, newDiscoverGroup("same_name_different_hash", name, "", ids))
	}
	for hash, names := range byHash {
		if len(names) <= 1 {
			continue
		}
		var ids []string
		for _, nameIDs := range names {
			ids = append(ids, nameIDs...)
		}
		groups = append(groups, newDiscoverGroup("same_hash_different_name", "", hash, ids))
	}
	for name, scopes := range byNameScope {
		if scopes["global"] && scopes["project"] {
			var ids []string
			for _, hashIDs := range byName[name] {
				ids = append(ids, hashIDs...)
			}
			groups = append(groups, newDiscoverGroup("global_project_overlap", name, "", ids))
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].GroupID < groups[j].GroupID })
	return groups
}

func newDiscoverGroup(groupType, name, hash string, ids []string) discoverDriftGroup {
	sort.Strings(ids)
	seed := groupType + "|" + name + "|" + hash + "|" + strings.Join(ids, ",")
	return discoverDriftGroup{
		GroupID:         "group-" + shortHash(seed),
		GroupType:       groupType,
		SkillName:       name,
		ContentSHA256:   hash,
		InstallationIDs: ids,
		Status:          "new",
	}
}

func summarizeDiscovery(out discoverOutput) discoverSummary {
	var summary discoverSummary
	for _, tool := range out.Tools {
		if tool.Detected {
			summary.ToolsFound++
		} else {
			summary.ToolsMissing++
		}
	}
	summary.ProjectsFound = len(out.Projects)
	for _, inst := range out.Installations {
		switch inst.Scope {
		case "global":
			summary.GlobalSkills++
		case "project":
			summary.ProjectLocalSkills++
		}
	}
	for _, group := range out.DriftGroups {
		switch group.GroupType {
		case "same_hash_different_name":
			summary.DuplicateContent++
		default:
			summary.DriftGroups++
		}
	}
	summary.MissingToolCoverage = summary.ToolsMissing
	return summary
}

func printDiscoverSummary(out io.Writer, result discoverOutput) {
	if out == io.Discard {
		return
	}
	fmt.Fprintf(out, "Discovered tools: %d present, %d missing\n", result.Summary.ToolsFound, result.Summary.ToolsMissing)
	fmt.Fprintf(out, "Skills: %d global, %d project-local\n", result.Summary.GlobalSkills, result.Summary.ProjectLocalSkills)
	fmt.Fprintf(out, "Projects: %d\n", result.Summary.ProjectsFound)
	fmt.Fprintf(out, "Review groups: %d drift, %d duplicate-content\n", result.Summary.DriftGroups, result.Summary.DuplicateContent)
	if len(result.Installations) > 0 {
		fmt.Fprintln(out, "\nInstallations:")
		for _, inst := range result.Installations {
			fmt.Fprintf(out, "  - %-24s %-12s %-8s %s\n", inst.SkillName, inst.ToolID, inst.Scope, inst.SourcePath)
		}
	}
}

func discoverGitRemote(root string) string {
	configPath := gitConfigPath(root)
	if configPath == "" {
		return ""
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	inOrigin := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inOrigin = trimmed == `[remote "origin"]`
			continue
		}
		if inOrigin && strings.HasPrefix(trimmed, "url = ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "url = "))
		}
	}
	return ""
}

func gitConfigPath(root string) string {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return filepath.Join(gitPath, "config")
	}

	data, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if gitDir == "" || gitDir == string(data) {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	commonDir := gitDir
	if commonData, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimSpace(string(commonData))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	}
	return filepath.Clean(filepath.Join(commonDir, "config"))
}

func isGitRepositoryRoot(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func hashDirContent(path string) (string, int64, error) {
	hash := sha256.New()
	var total int64
	err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
		total += int64(len(data))
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), total, nil
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
