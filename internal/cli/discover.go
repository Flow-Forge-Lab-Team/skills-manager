package cli

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
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
	InstallationID    string   `json:"installation_id"`
	SkillName         string   `json:"skill_name"`
	ToolID            string   `json:"tool_id"`
	Scope             string   `json:"scope"`
	ProjectID         string   `json:"project_id,omitempty"`
	SourcePath        string   `json:"source_path"`
	ContentPath       string   `json:"content_path"`
	ContentSHA256     string   `json:"content_sha256"`
	ContentSizeBytes  int64    `json:"content_size_bytes"`
	ModifiedAt        string   `json:"modified_at,omitempty"`
	Managed           bool     `json:"managed"`
	Ownership         string   `json:"ownership"`
	Format            string   `json:"format"`
	CompatibleToolIDs []string `json:"compatible_tool_ids,omitempty"`
	ExclusiveToolID   string   `json:"exclusive_tool_id,omitempty"`
	Present           bool     `json:"present"`
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

type discoverReport struct {
	Facts           discoverSummary          `json:"facts"`
	ReviewFacts     []discoverReportItem     `json:"review_facts"`
	CoverageGaps    []discoverCoverageGap    `json:"coverage_gaps"`
	Recommendations []discoverRecommendation `json:"recommendations"`
}

type discoverReportItem struct {
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	Detail          string   `json:"detail"`
	SkillName       string   `json:"skill_name,omitempty"`
	ContentSHA256   string   `json:"content_sha256,omitempty"`
	InstallationIDs []string `json:"installation_ids,omitempty"`
}

type discoverCoverageGap struct {
	Kind           string   `json:"kind"`
	Title          string   `json:"title"`
	Detail         string   `json:"detail"`
	SkillName      string   `json:"skill_name,omitempty"`
	ToolID         string   `json:"tool_id,omitempty"`
	PresentToolIDs []string `json:"present_tool_ids,omitempty"`
	MissingToolIDs []string `json:"missing_tool_ids,omitempty"`
}

type discoverRecommendation struct {
	RecommendationID      string   `json:"recommendation_id"`
	Kind                  string   `json:"kind"`
	Title                 string   `json:"title"`
	Reason                string   `json:"reason"`
	Confidence            string   `json:"confidence"`
	SkillName             string   `json:"skill_name,omitempty"`
	SourceInstallationIDs []string `json:"source_installation_ids,omitempty"`
	TargetToolID          string   `json:"target_tool_id,omitempty"`
	TargetProjectID       string   `json:"target_project_id,omitempty"`
	RequiresPlan          bool     `json:"requires_plan"`
}

type discoverOutput struct {
	ScannedAt            string                 `json:"scanned_at"`
	ApprovedProjectRoots []string               `json:"approved_project_roots,omitempty"`
	SkippedProjectRoots  []string               `json:"skipped_project_roots,omitempty"`
	Tools                []discoverTool         `json:"tools"`
	Projects             []discoverProject      `json:"projects,omitempty"`
	Installations        []discoverInstallation `json:"installations"`
	DriftGroups          []discoverDriftGroup   `json:"drift_groups,omitempty"`
	Summary              discoverSummary        `json:"summary"`
	Report               discoverReport         `json:"report"`
}

type discoverProjectRootsOutput struct {
	ProjectRoots []string `json:"project_roots"`
	Updated      bool     `json:"updated,omitempty"`
	Removed      bool     `json:"removed,omitempty"`
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
	if err := validateDiscoverOptions(opts); err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	if opts.listProjectRoots {
		return printDiscoverProjectRoots(stdout, stderr, gf, home, false, false)
	}
	if opts.removeProjectRoot != "" {
		removed, err := removeDiscoverProjectRoot(home, opts.removeProjectRoot)
		if err != nil {
			fmt.Fprintf(stderr, "remove project root: %v\n", err)
			return ExitOpError
		}
		return printDiscoverProjectRoots(stdout, stderr, gf, home, true, removed)
	}
	if opts.savedProjectRoots {
		roots, err := loadDiscoverProjectRoots(home)
		if err != nil {
			fmt.Fprintf(stderr, "read project roots: %v\n", err)
			return ExitOpError
		}
		for _, root := range roots {
			if dirExists(root) {
				opts.projectRoots = append(opts.projectRoots, root)
				continue
			}
			opts.skippedProjectRoots = append(opts.skippedProjectRoots, root)
		}
	}
	out, err := collectDiscovery(opts)
	if err != nil {
		fmt.Fprintf(stderr, "discover: %v\n", err)
		return ExitOpError
	}
	if opts.saveProjectRoots {
		if err := saveDiscoverProjectRoots(home, opts.projectRoots); err != nil {
			fmt.Fprintf(stderr, "save project roots: %v\n", err)
			return ExitOpError
		}
	}
	out.ApprovedProjectRoots, _ = loadDiscoverProjectRoots(home)
	out.SkippedProjectRoots = opts.skippedProjectRoots
	if err := persistDiscoverOutput(home, out, opts); err != nil {
		fmt.Fprintf(stderr, "persist discovery: %v\n", err)
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
	global              bool
	projectRoots        []string
	savedProjectRoots   bool
	saveProjectRoots    bool
	listProjectRoots    bool
	removeProjectRoot   string
	skippedProjectRoots []string
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
		case arg == "--saved-project-roots":
			opts.savedProjectRoots = true
		case arg == "--save-project-roots":
			opts.saveProjectRoots = true
		case arg == "--list-project-roots":
			opts.listProjectRoots = true
		case arg == "--remove-project-root":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--remove-project-root requires a path")
			}
			opts.removeProjectRoot = args[i+1]
			i++
		case strings.HasPrefix(arg, "--remove-project-root="):
			opts.removeProjectRoot = strings.TrimPrefix(arg, "--remove-project-root=")
		default:
			return opts, fmt.Errorf("unknown discover flag: %s", arg)
		}
	}
	return opts, nil
}

func validateDiscoverOptions(opts discoverOptions) error {
	if opts.listProjectRoots && (opts.global || len(opts.projectRoots) > 0 || opts.savedProjectRoots || opts.saveProjectRoots || opts.removeProjectRoot != "") {
		return fmt.Errorf("--list-project-roots cannot be combined with scan or mutation flags")
	}
	if opts.removeProjectRoot != "" && (opts.global || len(opts.projectRoots) > 0 || opts.savedProjectRoots || opts.saveProjectRoots || opts.listProjectRoots) {
		return fmt.Errorf("--remove-project-root cannot be combined with scan flags")
	}
	if opts.saveProjectRoots && len(opts.projectRoots) == 0 {
		return fmt.Errorf("--save-project-roots requires --projects <roots>")
	}
	if opts.global || len(opts.projectRoots) > 0 || opts.savedProjectRoots || opts.listProjectRoots || opts.removeProjectRoot != "" {
		return nil
	}
	return fmt.Errorf("discover requires an explicit scope: --global, --projects <roots>, or --saved-project-roots")
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
	out.Report = buildDiscoverReport(out)
	return out, nil
}

func persistDiscoverOutput(home string, out discoverOutput, opts discoverOptions) error {
	db, err := state.Open(home)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	persistedProjectRoots, err := normalizePersistedProjectRoots(opts.projectRoots)
	if err != nil {
		return err
	}
	scanID := "scan-" + shortHash(out.ScannedAt+"|"+strings.Join(persistedProjectRoots, ",")+"|"+fmt.Sprint(opts.global)+"|"+time.Now().UTC().Format(time.RFC3339Nano))
	projectRootsJSON, err := jsonString(persistedProjectRoots)
	if err != nil {
		return err
	}
	globalScope := 0
	if opts.global {
		globalScope = 1
	}
	if _, err := tx.Exec(`INSERT INTO discovery_scans (scan_id, scanned_at, global_scope, project_roots)
VALUES (?, ?, ?, ?)
ON CONFLICT(scan_id) DO UPDATE SET
  scanned_at=excluded.scanned_at,
  global_scope=excluded.global_scope,
  project_roots=excluded.project_roots`,
		scanID, out.ScannedAt, globalScope, projectRootsJSON); err != nil {
		return err
	}

	for _, tool := range out.Tools {
		globalRoots, err := jsonString(tool.GlobalRoots)
		if err != nil {
			return err
		}
		projectPatterns, err := jsonString(tool.ProjectPatterns)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO discovery_tools
(tool_id, display_name, detected, status, global_roots, project_patterns, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tool_id) DO UPDATE SET
  display_name=excluded.display_name,
  detected=excluded.detected,
  status=excluded.status,
  global_roots=excluded.global_roots,
  project_patterns=excluded.project_patterns,
  last_seen_at=excluded.last_seen_at`,
			tool.ToolID, tool.DisplayName, boolInt(tool.Detected), tool.Status, globalRoots, projectPatterns, out.ScannedAt); err != nil {
			return err
		}
	}

	seenProjects := map[string]bool{}
	for _, project := range out.Projects {
		seenProjects[project.ProjectID] = true
		detectedTools, err := jsonString(project.DetectedTools)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO discovery_projects
(project_id, root_path, repo_remote, detected_tools, last_scanned_at, present, missing_since)
VALUES (?, ?, ?, ?, ?, 1, NULL)
ON CONFLICT(project_id) DO UPDATE SET
  root_path=excluded.root_path,
  repo_remote=excluded.repo_remote,
  detected_tools=excluded.detected_tools,
  last_scanned_at=excluded.last_scanned_at,
  present=1,
  missing_since=NULL`,
			project.ProjectID, project.RootPath, project.RepoRemote, detectedTools, project.LastScannedAt); err != nil {
			return err
		}
	}

	seenGlobalInstalls := map[string]bool{}
	seenProjectInstalls := map[string]bool{}
	for _, inst := range out.Installations {
		if inst.Scope == "global" {
			seenGlobalInstalls[inst.InstallationID] = true
		}
		if inst.Scope == "project" {
			seenProjectInstalls[inst.InstallationID] = true
		}
		if _, err := tx.Exec(`INSERT INTO discovery_installations
(installation_id, skill_name, tool_id, scope, project_id, source_path, content_path,
 content_sha256, content_size_bytes, modified_at, managed, ownership, format, present,
 first_seen_at, last_seen_at, missing_since)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, NULL)
ON CONFLICT(installation_id) DO UPDATE SET
  skill_name=excluded.skill_name,
  tool_id=excluded.tool_id,
  scope=excluded.scope,
  project_id=excluded.project_id,
  source_path=excluded.source_path,
  content_path=excluded.content_path,
  content_sha256=excluded.content_sha256,
  content_size_bytes=excluded.content_size_bytes,
  modified_at=excluded.modified_at,
  managed=excluded.managed,
  ownership=excluded.ownership,
  format=excluded.format,
  present=1,
  first_seen_at=discovery_installations.first_seen_at,
  last_seen_at=excluded.last_seen_at,
  missing_since=NULL`,
			inst.InstallationID, inst.SkillName, inst.ToolID, inst.Scope, inst.ProjectID, inst.SourcePath,
			inst.ContentPath, inst.ContentSHA256, inst.ContentSizeBytes, inst.ModifiedAt, boolInt(inst.Managed),
			inst.Ownership, inst.Format, out.ScannedAt, out.ScannedAt); err != nil {
			return err
		}
	}

	if opts.global {
		if err := markMissingInstallations(tx, "global", nil, seenGlobalInstalls, out.ScannedAt); err != nil {
			return err
		}
	}
	if len(persistedProjectRoots) > 0 {
		if err := markMissingProjects(tx, persistedProjectRoots, seenProjects, out.ScannedAt); err != nil {
			return err
		}
		if err := markMissingInstallations(tx, "project", persistedProjectRoots, seenProjectInstalls, out.ScannedAt); err != nil {
			return err
		}
	}

	seenGroups := map[string]bool{}
	for _, group := range out.DriftGroups {
		seenGroups[group.GroupID] = true
		if _, err := tx.Exec(`INSERT INTO discovery_drift_groups
(group_id, group_type, skill_name, content_sha256, status, present, last_seen_at)
VALUES (?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(group_id) DO UPDATE SET
  group_type=excluded.group_type,
  skill_name=excluded.skill_name,
  content_sha256=excluded.content_sha256,
  status=excluded.status,
  present=1,
  last_seen_at=excluded.last_seen_at`,
			group.GroupID, group.GroupType, group.SkillName, group.ContentSHA256, group.Status, out.ScannedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM discovery_drift_group_installations WHERE group_id=?`, group.GroupID); err != nil {
			return err
		}
		for _, instID := range group.InstallationIDs {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO discovery_drift_group_installations (group_id, installation_id) VALUES (?, ?)`, group.GroupID, instID); err != nil {
				return err
			}
		}
	}
	if err := markMissingDriftGroups(tx, seenGroups, opts.global, persistedProjectRoots); err != nil {
		return err
	}

	return tx.Commit()
}

func normalizePersistedProjectRoots(roots []string) ([]string, error) {
	normalized := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		root, err := normalizeDiscoverProjectRoot(root)
		if err != nil {
			return nil, err
		}
		if seen[root] {
			continue
		}
		seen[root] = true
		normalized = append(normalized, root)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func markMissingProjects(tx *sql.Tx, roots []string, seen map[string]bool, missingSince string) error {
	rows, err := tx.Query(`SELECT project_id, root_path FROM discovery_projects WHERE present=1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var projectID, rootPath string
		if err := rows.Scan(&projectID, &rootPath); err != nil {
			return err
		}
		if seen[projectID] || !pathUnderAny(rootPath, roots) {
			continue
		}
		if _, err := tx.Exec(`UPDATE discovery_projects
SET present=0, missing_since=COALESCE(missing_since, ?)
WHERE project_id=?`, missingSince, projectID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func markMissingInstallations(tx *sql.Tx, scope string, roots []string, seen map[string]bool, missingSince string) error {
	rows, err := tx.Query(`SELECT installation_id, source_path FROM discovery_installations WHERE scope=? AND present=1`, scope)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var installID, sourcePath string
		if err := rows.Scan(&installID, &sourcePath); err != nil {
			return err
		}
		if seen[installID] {
			continue
		}
		if len(roots) > 0 && !pathUnderAny(sourcePath, roots) {
			continue
		}
		if _, err := tx.Exec(`UPDATE discovery_installations
SET present=0, missing_since=COALESCE(missing_since, ?)
WHERE installation_id=?`, missingSince, installID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func markMissingDriftGroups(tx *sql.Tx, seen map[string]bool, includeGlobal bool, projectRoots []string) error {
	rows, err := tx.Query(`SELECT group_id FROM discovery_drift_groups WHERE present=1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var groupID string
		if err := rows.Scan(&groupID); err != nil {
			return err
		}
		if seen[groupID] {
			continue
		}
		inScope, err := driftGroupWithinScanScope(tx, groupID, includeGlobal, projectRoots)
		if err != nil {
			return err
		}
		if !inScope {
			continue
		}
		if _, err := tx.Exec(`UPDATE discovery_drift_groups SET present=0 WHERE group_id=?`, groupID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func driftGroupWithinScanScope(tx *sql.Tx, groupID string, includeGlobal bool, projectRoots []string) (bool, error) {
	rows, err := tx.Query(`
SELECT i.scope, i.source_path
FROM discovery_drift_group_installations gi
JOIN discovery_installations i ON i.installation_id = gi.installation_id
WHERE gi.group_id = ?`, groupID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	checked := false
	for rows.Next() {
		checked = true
		var scope, sourcePath string
		if err := rows.Scan(&scope, &sourcePath); err != nil {
			return false, err
		}
		switch {
		case scope == "global" && includeGlobal:
		case scope == "project" && len(projectRoots) > 0 && pathUnderAny(sourcePath, projectRoots):
		default:
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return checked, nil
}

func pathUnderAny(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
			return true
		}
	}
	return false
}

func jsonString(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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

func discoverProjectRootsPath(home string) string {
	return filepath.Join(home, "discover-project-roots.txt")
}

func loadDiscoverProjectRoots(home string) ([]string, error) {
	data, err := os.ReadFile(discoverProjectRootsPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	roots := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		root := strings.TrimSpace(line)
		if root == "" || strings.HasPrefix(root, "#") || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots, nil
}

func saveDiscoverProjectRoots(home string, rawRoots []string) error {
	existing, err := loadDiscoverProjectRoots(home)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, root := range existing {
		seen[root] = true
	}
	for _, raw := range rawRoots {
		root, err := normalizeDiscoverProjectRoot(raw)
		if err != nil {
			return err
		}
		seen[root] = true
	}
	roots := make([]string, 0, len(seen))
	for root := range seen {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return writeDiscoverProjectRoots(home, roots)
}

func removeDiscoverProjectRoot(home, rawRoot string) (bool, error) {
	target, err := normalizeDiscoverProjectRoot(rawRoot)
	if err != nil {
		return false, err
	}
	existing, err := loadDiscoverProjectRoots(home)
	if err != nil {
		return false, err
	}
	var kept []string
	removed := false
	for _, root := range existing {
		if sameDiscoverPath(root, target) {
			removed = true
			continue
		}
		kept = append(kept, root)
	}
	if !removed {
		return false, nil
	}
	return true, writeDiscoverProjectRoots(home, kept)
}

func writeDiscoverProjectRoots(home string, roots []string) error {
	if err := os.MkdirAll(home, 0755); err != nil {
		return err
	}
	var b strings.Builder
	for _, root := range roots {
		fmt.Fprintln(&b, root)
	}
	return os.WriteFile(discoverProjectRootsPath(home), []byte(b.String()), 0600)
}

func printDiscoverProjectRoots(stdout io.Writer, stderr io.Writer, gf globalFlags, home string, updated bool, removed bool) int {
	roots, err := loadDiscoverProjectRoots(home)
	if err != nil {
		fmt.Fprintf(stderr, "read project roots: %v\n", err)
		return ExitOpError
	}
	if gf.JSON {
		if err := writeJSON(stdout, discoverProjectRootsOutput{ProjectRoots: roots, Updated: updated, Removed: removed}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}
	out := gf.outWriter(stdout)
	if updated {
		if removed {
			fmt.Fprintln(out, "Removed approved project root.")
		} else {
			fmt.Fprintln(out, "No matching approved project root was saved.")
		}
	}
	if len(roots) == 0 {
		fmt.Fprintln(out, "Approved project roots: none")
		return ExitSuccess
	}
	fmt.Fprintln(out, "Approved project roots:")
	for _, root := range roots {
		fmt.Fprintf(out, "  - %s\n", root)
	}
	return ExitSuccess
}

func discoverProjectRoots(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var repos []string
	for _, raw := range paths {
		root, err := normalizeDiscoverProjectRoot(raw)
		if err != nil {
			return nil, err
		}
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

func normalizeDiscoverProjectRoot(raw string) (string, error) {
	root, err := absoluteProjectPath(raw)
	if err != nil {
		return "", err
	}
	return discoverWalkRoot(root), nil
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
		contentPath := filepath.Join(sourcePath, "SKILL.md")
		if !fileExists(contentPath) {
			if path != walkRoot && shouldPruneDiscoverDir(d.Name(), sourcePath) {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				return nil
			}
			return nil
		}
		skillName := filepath.Base(path)
		var compatible []string
		exclusive := ""
		if decl, _, err := parseSkillFrontmatterFull(contentPath); err == nil {
			if decl.name != "" {
				skillName = decl.name
			}
			compatible = normalizedDiscoverToolIDs(decl.compatible)
			exclusive = normalizedDiscoverToolID(decl.exclusive)
		}
		if inst, ok := discoverInstallFromFile(root, scope, projectID, basePath, skillName, sourcePath, contentPath); ok {
			inst.CompatibleToolIDs = compatible
			inst.ExclusiveToolID = exclusive
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
		hashPath := sourcePath
		if resolved, err := filepath.EvalSymlinks(sourcePath); err == nil {
			if resolvedInfo, err := os.Stat(resolved); err == nil && resolvedInfo.IsDir() {
				hashPath = resolved
			}
		}
		hash, size, err := hashDirContent(hashPath)
		return hash, size, hashPath, err
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

func buildDiscoverReport(out discoverOutput) discoverReport {
	report := discoverReport{
		Facts:           out.Summary,
		ReviewFacts:     []discoverReportItem{},
		CoverageGaps:    []discoverCoverageGap{},
		Recommendations: []discoverRecommendation{},
	}
	installsByID := map[string]discoverInstallation{}
	for _, inst := range out.Installations {
		installsByID[inst.InstallationID] = inst
	}
	for _, group := range out.DriftGroups {
		report.ReviewFacts = append(report.ReviewFacts, discoverReportItem{
			Kind:            group.GroupType,
			Title:           discoverGroupTitle(group),
			Detail:          discoverGroupDetail(group, installsByID),
			SkillName:       group.SkillName,
			ContentSHA256:   group.ContentSHA256,
			InstallationIDs: append([]string{}, group.InstallationIDs...),
		})
	}
	for _, tool := range out.Tools {
		if tool.Detected {
			continue
		}
		report.CoverageGaps = append(report.CoverageGaps, discoverCoverageGap{
			Kind:   "missing_tool",
			Title:  "Missing tool coverage: " + tool.DisplayName,
			Detail: fmt.Sprintf("%s was not detected in this scan scope.", tool.DisplayName),
			ToolID: tool.ToolID,
		})
	}
	report.CoverageGaps = append(report.CoverageGaps, discoverGlobalSkillCoverageGaps(out)...)
	report.Recommendations = buildDiscoverRecommendations(out, installsByID, report.CoverageGaps)
	sort.Slice(report.ReviewFacts, func(i, j int) bool {
		if report.ReviewFacts[i].Kind != report.ReviewFacts[j].Kind {
			return report.ReviewFacts[i].Kind < report.ReviewFacts[j].Kind
		}
		if report.ReviewFacts[i].SkillName != report.ReviewFacts[j].SkillName {
			return report.ReviewFacts[i].SkillName < report.ReviewFacts[j].SkillName
		}
		return report.ReviewFacts[i].ContentSHA256 < report.ReviewFacts[j].ContentSHA256
	})
	sort.Slice(report.CoverageGaps, func(i, j int) bool {
		if report.CoverageGaps[i].Kind != report.CoverageGaps[j].Kind {
			return report.CoverageGaps[i].Kind < report.CoverageGaps[j].Kind
		}
		if report.CoverageGaps[i].SkillName != report.CoverageGaps[j].SkillName {
			return report.CoverageGaps[i].SkillName < report.CoverageGaps[j].SkillName
		}
		return report.CoverageGaps[i].ToolID < report.CoverageGaps[j].ToolID
	})
	sortDiscoverRecommendations(report.Recommendations)
	return report
}

func buildDiscoverRecommendations(out discoverOutput, installsByID map[string]discoverInstallation, gaps []discoverCoverageGap) []discoverRecommendation {
	var recs []discoverRecommendation
	globalInstallBlocked := map[string]bool{}
	for _, group := range out.DriftGroups {
		if group.SkillName != "" && (group.GroupType == "same_name_different_hash" || group.GroupType == "global_project_overlap") {
			globalInstallBlocked[group.SkillName] = true
		}
		recs = append(recs, newDiscoverRecommendation("review_drift", group.SkillName, "", "", group.InstallationIDs, "high", discoverRecommendationTitle("review_drift", group.SkillName, ""), discoverGroupDetail(group, installsByID)))
		if group.GroupType == "same_hash_different_name" {
			recs = append(recs, newDiscoverRecommendation("remove", group.SkillName, "", "", group.InstallationIDs, "low", "Review duplicate removal: "+shortDisplayHash(group.ContentSHA256), "Exact bytes are installed under different names; generate a dry-run plan before removing any copy."))
		}
	}

	for _, inst := range out.Installations {
		if !inst.Managed {
			confidence := discoverContextConfidence(inst.ToolID, inst.Format)
			reason := fmt.Sprintf("%s/%s is unmanaged inventory; ingest would first create a dry-run plan and preserve the source path.", inst.ToolID, inst.Scope)
			recs = append(recs, newDiscoverRecommendation("ingest", inst.SkillName, "", inst.ProjectID, []string{inst.InstallationID}, confidence, "Ingest unmanaged skill: "+inst.SkillName, reason))
		}
	}

	for _, gap := range gaps {
		if gap.Kind != "global_skill_absent_from_detected_tool" {
			continue
		}
		if globalInstallBlocked[gap.SkillName] {
			continue
		}
		sources := sourceInstallationsForSkill(out.Installations, gap.SkillName, gap.PresentToolIDs, "global")
		sourceIDs := installationIDs(sources)
		if len(sourceIDs) == 0 {
			continue
		}
		for _, targetTool := range gap.MissingToolIDs {
			source := bestSourceForTarget(sources, targetTool)
			if source.InstallationID == "" {
				continue
			}
			if !installationCompatibleWithTool(source, targetTool) {
				reason := fmt.Sprintf("%s declares compatibility that does not include %s; port and validate before install.", gap.SkillName, targetTool)
				recs = append(recs, newDiscoverRecommendation("needs_port", gap.SkillName, targetTool, "", []string{source.InstallationID}, "high", discoverRecommendationTitle("needs_port", gap.SkillName, targetTool), reason))
				continue
			}
			if targetTool == "codex" {
				reason := fmt.Sprintf("%s is absent from codex, but the discovered Codex global root is legacy ~/.codex/skills; ignore until the documented .agents/skills target is modeled.", gap.SkillName)
				recs = append(recs, newDiscoverRecommendation("ignore", gap.SkillName, targetTool, "", []string{source.InstallationID}, "low", discoverRecommendationTitle("ignore", gap.SkillName, targetTool), reason))
				continue
			}
			if discoverToolContextCost(targetTool, source.Format) != "low" {
				reason := fmt.Sprintf("%s is absent from %s, but %s has unknown or always-loaded context cost; ignore until manually validated.", gap.SkillName, targetTool, targetTool)
				recs = append(recs, newDiscoverRecommendation("ignore", gap.SkillName, targetTool, "", []string{source.InstallationID}, "low", discoverRecommendationTitle("ignore", gap.SkillName, targetTool), reason))
				continue
			}
			confidence := discoverContextConfidence(targetTool, source.Format)
			reason := fmt.Sprintf("%s is visible in %s and absent from %s; %s has requested/on-demand loading cost, so a global install can be planned safely.", gap.SkillName, strings.Join(gap.PresentToolIDs, ", "), targetTool, targetTool)
			recs = append(recs, newDiscoverRecommendation("install_global", gap.SkillName, targetTool, "", []string{source.InstallationID}, confidence, discoverRecommendationTitle("install_global", gap.SkillName, targetTool), reason))
		}
	}

	recs = append(recs, discoverProjectInstallRecommendations(out.Installations)...)
	return dedupeDiscoverRecommendations(recs)
}

func discoverProjectInstallRecommendations(installs []discoverInstallation) []discoverRecommendation {
	byProjectSkill := map[string][]discoverInstallation{}
	for _, inst := range installs {
		if inst.Scope != "project" {
			continue
		}
		key := inst.ProjectID + "|" + inst.SkillName
		byProjectSkill[key] = append(byProjectSkill[key], inst)
	}
	var recs []discoverRecommendation
	for _, group := range byProjectSkill {
		toolIDs := map[string]bool{}
		for _, inst := range group {
			toolIDs[inst.ToolID] = true
		}
		if len(toolIDs) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].InstallationID < group[j].InstallationID })
		sourceIDs := installationIDs(group)
		targets := sortedBoolKeys(toolIDs)
		reason := fmt.Sprintf("%s appears in multiple project-local harnesses (%s); keep future installs project-local unless a dry-run plan proves global utility.", group[0].SkillName, strings.Join(targets, ", "))
		recs = append(recs, newDiscoverRecommendation("install_project", group[0].SkillName, "", group[0].ProjectID, sourceIDs, "medium", "Keep project-local coverage: "+group[0].SkillName, reason))
	}
	return recs
}

func newDiscoverRecommendation(kind, skillName, targetToolID, targetProjectID string, sourceIDs []string, confidence, title, reason string) discoverRecommendation {
	sourceIDs = append([]string{}, sourceIDs...)
	sort.Strings(sourceIDs)
	seed := strings.Join([]string{kind, skillName, targetToolID, targetProjectID, strings.Join(sourceIDs, ","), reason}, "|")
	return discoverRecommendation{
		RecommendationID:      "rec-" + shortHash(seed),
		Kind:                  kind,
		Title:                 title,
		Reason:                reason,
		Confidence:            confidence,
		SkillName:             skillName,
		SourceInstallationIDs: sourceIDs,
		TargetToolID:          targetToolID,
		TargetProjectID:       targetProjectID,
		RequiresPlan:          true,
	}
}

func discoverRecommendationTitle(kind, skillName, targetToolID string) string {
	switch kind {
	case "install_global":
		return fmt.Sprintf("Install globally: %s -> %s", skillName, targetToolID)
	case "needs_port":
		return fmt.Sprintf("Port before install: %s -> %s", skillName, targetToolID)
	case "ignore":
		return fmt.Sprintf("Ignore global gap: %s -> %s", skillName, targetToolID)
	case "review_drift":
		if skillName == "" {
			return "Review duplicate content"
		}
		return "Review drift: " + skillName
	default:
		return kind + ": " + skillName
	}
}

func sourceInstallationsForSkill(installs []discoverInstallation, skillName string, toolIDs []string, scope string) []discoverInstallation {
	tools := map[string]bool{}
	for _, toolID := range toolIDs {
		tools[toolID] = true
	}
	var sources []discoverInstallation
	for _, inst := range installs {
		if inst.SkillName == skillName && inst.Scope == scope && tools[inst.ToolID] {
			sources = append(sources, inst)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].InstallationID < sources[j].InstallationID })
	return sources
}

func bestSourceForTarget(sources []discoverInstallation, targetTool string) discoverInstallation {
	for _, source := range sources {
		if installationCompatibleWithTool(source, targetTool) {
			return source
		}
	}
	if len(sources) > 0 {
		return sources[0]
	}
	return discoverInstallation{}
}

func installationCompatibleWithTool(inst discoverInstallation, targetTool string) bool {
	if inst.ExclusiveToolID != "" {
		return inst.ExclusiveToolID == targetTool
	}
	if len(inst.CompatibleToolIDs) == 0 {
		return true
	}
	for _, toolID := range inst.CompatibleToolIDs {
		if toolID == targetTool {
			return true
		}
	}
	return false
}

func installationIDs(installs []discoverInstallation) []string {
	ids := make([]string, 0, len(installs))
	for _, inst := range installs {
		ids = append(ids, inst.InstallationID)
	}
	sort.Strings(ids)
	return ids
}

func discoverContextConfidence(toolID, format string) string {
	switch discoverToolContextCost(toolID, format) {
	case "low":
		return "medium"
	case "high":
		return "low"
	default:
		return "low"
	}
}

func discoverToolContextCost(toolID, format string) string {
	if format == "agents_md" || format == "cursor_rule" || format == "github_instruction" {
		return "high"
	}
	if format != "skill_md" {
		return "unknown"
	}
	switch toolID {
	case "antigravity", "claude", "codex", "grok", "openclaw", "agents":
		return "low"
	case "gemini", "hermes":
		return "unknown"
	default:
		return "unknown"
	}
}

func normalizedDiscoverToolIDs(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = normalizedDiscoverToolID(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizedDiscoverToolID(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func dedupeDiscoverRecommendations(recs []discoverRecommendation) []discoverRecommendation {
	seen := map[string]bool{}
	var out []discoverRecommendation
	sortDiscoverRecommendations(recs)
	for _, rec := range recs {
		if seen[rec.RecommendationID] {
			continue
		}
		seen[rec.RecommendationID] = true
		out = append(out, rec)
	}
	return out
}

func sortDiscoverRecommendations(recs []discoverRecommendation) {
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Kind != recs[j].Kind {
			return recs[i].Kind < recs[j].Kind
		}
		if recs[i].SkillName != recs[j].SkillName {
			return recs[i].SkillName < recs[j].SkillName
		}
		if recs[i].TargetToolID != recs[j].TargetToolID {
			return recs[i].TargetToolID < recs[j].TargetToolID
		}
		if recs[i].Reason != recs[j].Reason {
			return recs[i].Reason < recs[j].Reason
		}
		leftSources := strings.Join(recs[i].SourceInstallationIDs, ",")
		rightSources := strings.Join(recs[j].SourceInstallationIDs, ",")
		if leftSources != rightSources {
			return leftSources < rightSources
		}
		return recs[i].RecommendationID < recs[j].RecommendationID
	})
}

func discoverGroupTitle(group discoverDriftGroup) string {
	switch group.GroupType {
	case "same_name_different_hash":
		return "Same-name drift: " + group.SkillName
	case "same_hash_different_name":
		return "Duplicate content: " + shortDisplayHash(group.ContentSHA256)
	case "global_project_overlap":
		return "Project override: " + group.SkillName
	default:
		return group.GroupType
	}
}

func discoverGroupDetail(group discoverDriftGroup, installsByID map[string]discoverInstallation) string {
	locations := make([]string, 0, len(group.InstallationIDs))
	for _, id := range group.InstallationIDs {
		inst, ok := installsByID[id]
		if !ok {
			continue
		}
		locations = append(locations, inst.ToolID+"/"+inst.Scope)
	}
	locationText := compactLocationSummary(locations, 6)
	switch group.GroupType {
	case "same_name_different_hash":
		return fmt.Sprintf("%s has different content across %s.", group.SkillName, locationText)
	case "same_hash_different_name":
		return fmt.Sprintf("Identical content appears under different names across %s.", locationText)
	case "global_project_overlap":
		return fmt.Sprintf("%s exists both globally and in a project scope.", group.SkillName)
	default:
		return locationText
	}
}

func discoverGlobalSkillCoverageGaps(out discoverOutput) []discoverCoverageGap {
	detectedGlobalTools := map[string]bool{}
	for _, tool := range out.Tools {
		if tool.Detected && len(tool.GlobalRoots) > 0 {
			detectedGlobalTools[tool.ToolID] = true
		}
	}
	if len(detectedGlobalTools) <= 1 {
		return nil
	}
	bySkill := map[string]map[string]bool{}
	installsBySkill := map[string][]discoverInstallation{}
	for _, inst := range out.Installations {
		if inst.Scope != "global" || !detectedGlobalTools[inst.ToolID] {
			continue
		}
		if bySkill[inst.SkillName] == nil {
			bySkill[inst.SkillName] = map[string]bool{}
		}
		bySkill[inst.SkillName][inst.ToolID] = true
		installsBySkill[inst.SkillName] = append(installsBySkill[inst.SkillName], inst)
	}
	var gaps []discoverCoverageGap
	for skillName, physicalPresent := range bySkill {
		present := effectiveGlobalSkillPresence(physicalPresent, detectedGlobalTools, installsBySkill[skillName])
		presentIDs := sortedBoolKeys(present)
		var missingIDs []string
		for toolID := range detectedGlobalTools {
			if !present[toolID] {
				missingIDs = append(missingIDs, toolID)
			}
		}
		if len(missingIDs) == 0 {
			continue
		}
		sort.Strings(missingIDs)
		gaps = append(gaps, discoverCoverageGap{
			Kind:           "global_skill_absent_from_detected_tool",
			Title:          "Global skill coverage gap: " + skillName,
			Detail:         fmt.Sprintf("%s is visible in %s but absent from %s.", skillName, strings.Join(presentIDs, ", "), strings.Join(missingIDs, ", ")),
			SkillName:      skillName,
			PresentToolIDs: presentIDs,
			MissingToolIDs: missingIDs,
		})
	}
	return gaps
}

func effectiveGlobalSkillPresence(physicalPresent, detectedGlobalTools map[string]bool, installs []discoverInstallation) map[string]bool {
	present := map[string]bool{}
	for toolID, ok := range physicalPresent {
		if ok {
			present[toolID] = true
		}
	}
	if detectedGlobalTools["grok"] {
		for _, inst := range installs {
			if inst.ToolID == "claude" && installationCompatibleWithTool(inst, "grok") {
				// Grok global roots include compatible Claude skills, so those
				// do not need a duplicate ~/.grok install.
				present["grok"] = true
				break
			}
		}
	}
	return present
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func compactStringList(values []string, limit int) string {
	if len(values) <= limit {
		return strings.Join(values, ", ")
	}
	return fmt.Sprintf("%s, ... %d more", strings.Join(values[:limit], ", "), len(values)-limit)
}

func compactLocationSummary(locations []string, limit int) string {
	counts := map[string]int{}
	for _, location := range locations {
		counts[location]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if counts[key] == 1 {
			parts = append(parts, key)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s x%d", key, counts[key]))
	}
	return compactStringList(parts, limit)
}

func shortDisplayHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func printDiscoverSummary(out io.Writer, result discoverOutput) {
	if out == io.Discard {
		return
	}
	fmt.Fprintln(out, "Discover assessment")
	fmt.Fprintf(out, "Facts: %d tools present, %d tools missing, %d global skills, %d project-local skills, %d projects\n",
		result.Summary.ToolsFound, result.Summary.ToolsMissing, result.Summary.GlobalSkills, result.Summary.ProjectLocalSkills, result.Summary.ProjectsFound)
	fmt.Fprintf(out, "Review facts: %d drift/overlap, %d duplicate-content\n", result.Summary.DriftGroups, result.Summary.DuplicateContent)
	fmt.Fprintf(out, "Coverage gaps: %d\n", len(result.Report.CoverageGaps))
	if len(result.SkippedProjectRoots) > 0 {
		fmt.Fprintf(out, "Skipped saved project roots: %d\n", len(result.SkippedProjectRoots))
	}
	printDiscoverReportSection(out, "Exact review facts", reportItemLines(result.Report.ReviewFacts))
	printDiscoverReportSection(out, "Coverage gaps", coverageGapLines(result.Report.CoverageGaps))
	if len(result.Report.Recommendations) == 0 {
		fmt.Fprintln(out, "\nRecommendations: none generated by discover; review facts are exact inventory signals.")
	} else {
		printDiscoverReportSection(out, "Recommendations", recommendationLines(result.Report.Recommendations))
	}
	if len(result.Installations) > 0 {
		fmt.Fprintln(out, "\nInstallations:")
		installs := append([]discoverInstallation{}, result.Installations...)
		sort.Slice(installs, func(i, j int) bool {
			if installs[i].SkillName != installs[j].SkillName {
				return installs[i].SkillName < installs[j].SkillName
			}
			if installs[i].ToolID != installs[j].ToolID {
				return installs[i].ToolID < installs[j].ToolID
			}
			if installs[i].Scope != installs[j].Scope {
				return installs[i].Scope < installs[j].Scope
			}
			return installs[i].SourcePath < installs[j].SourcePath
		})
		const maxHumanInstallLines = 20
		displayed := installs
		if len(displayed) > maxHumanInstallLines {
			displayed = displayed[:maxHumanInstallLines]
		}
		for _, inst := range displayed {
			fmt.Fprintf(out, "  - %-24s %-12s %-8s %s\n", inst.SkillName, inst.ToolID, inst.Scope, inst.SourcePath)
		}
		if remaining := len(installs) - len(displayed); remaining > 0 {
			fmt.Fprintf(out, "  - ... %d more\n", remaining)
		}
	}
}

func printDiscoverReportSection(out io.Writer, title string, lines []string) {
	if len(lines) == 0 {
		fmt.Fprintf(out, "\n%s: none\n", title)
		return
	}
	fmt.Fprintf(out, "\n%s:\n", title)
	const maxHumanReportLines = 12
	displayed := lines
	if len(displayed) > maxHumanReportLines {
		displayed = displayed[:maxHumanReportLines]
	}
	for _, line := range displayed {
		fmt.Fprintf(out, "  - %s\n", line)
	}
	if remaining := len(lines) - len(displayed); remaining > 0 {
		fmt.Fprintf(out, "  - ... %d more\n", remaining)
	}
}

func reportItemLines(items []discoverReportItem) []string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, item.Title+" - "+item.Detail)
	}
	return lines
}

func coverageGapLines(gaps []discoverCoverageGap) []string {
	lines := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		lines = append(lines, gap.Title+" - "+gap.Detail)
	}
	return lines
}

func recommendationLines(recs []discoverRecommendation) []string {
	lines := make([]string, 0, len(recs))
	for _, rec := range recs {
		lines = append(lines, fmt.Sprintf("%s (confidence: %s) - %s", rec.Title, rec.Confidence, rec.Reason))
	}
	return lines
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
