package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

type triageOverview struct {
	LibrarySkills  int              `json:"library_skills"`
	Projects       int              `json:"projects"`
	PendingUpdates int              `json:"pending_updates"`
	Unregistered   int              `json:"unregistered"`
	ScheduledCheck string           `json:"scheduled_check"`
	Home           string           `json:"home"`
	GeneratedAt    string           `json:"generated_at"`
	MostUsed       []triageUsage    `json:"most_used"`
	Activity       []triageActivity `json:"activity"`
}

type triageSkill struct {
	Name               string        `json:"name"`
	Summary            string        `json:"summary,omitempty"`
	Categories         []string      `json:"categories,omitempty"`
	Tags               []string      `json:"tags,omitempty"`
	Compatibility      compatibility `json:"compatibility,omitempty"`
	CompatibilityLabel string        `json:"compatibility_label,omitempty"`
	RequirementsStatus string        `json:"requirements_status"`
	Source             string        `json:"source,omitempty"`
	InstalledProjects  int           `json:"installed_projects"`
	Usage30d           int           `json:"usage_30d"`
	LastActivityAt     string        `json:"last_activity_at,omitempty"`
	PendingUpdate      bool          `json:"pending_update"`
	UpdateBadges       []string      `json:"update_badges,omitempty"`
}

type triageSkillList struct {
	Skills      []triageSkill `json:"skills"`
	Total       int           `json:"total"`
	Page        int           `json:"page"`
	PageSize    int           `json:"page_size"`
	Categories  []string      `json:"categories"`
	Tags        []string      `json:"tags"`
	Sources     []string      `json:"sources"`
	GeneratedAt string        `json:"generated_at"`
}

type triageUsage struct {
	SkillName string `json:"skill_name"`
	Count     int    `json:"count"`
}

type triageActivity struct {
	Kind      string `json:"kind"`
	SkillName string `json:"skill_name,omitempty"`
	Detail    string `json:"detail"`
	At        string `json:"at,omitempty"`
}

type triageProject struct {
	Slug         string   `json:"slug"`
	ProjectPath  string   `json:"project_path"`
	InstalledAt  string   `json:"installed_at,omitempty"`
	SkillCount   int      `json:"skill_count"`
	ManagedPaths int      `json:"managed_paths"`
	Skills       []string `json:"skills,omitempty"`
}

type triageProjectDetail struct {
	triageProject
	Config          projectConfig             `json:"config"`
	DetectedStack   []string                  `json:"detected_stack"`
	PreviewSkills   []triageProjectCandidate  `json:"preview_skills"`
	ManualSkills    []string                  `json:"manual_skills"`
	SuggestedSkills []triageProjectCandidate  `json:"suggested_skills"`
	Warnings        []triageDependencyWarning `json:"warnings"`
	MatchExplain    []triageProjectCandidate  `json:"match_explain"`
}

type triageProjectCandidate struct {
	Name      string   `json:"name"`
	Score     int      `json:"score"`
	Reasons   []string `json:"reasons,omitempty"`
	Harnesses []string `json:"harnesses,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

type triageDriftGroup struct {
	GroupID                 string   `json:"group_id"`
	GroupType               string   `json:"group_type"`
	SkillName               string   `json:"skill_name,omitempty"`
	ContentSHA256           string   `json:"content_sha256,omitempty"`
	Status                  string   `json:"status"`
	Classification          string   `json:"classification,omitempty"`
	ReviewStatus            string   `json:"review_status,omitempty"`
	ReviewReason            string   `json:"review_reason,omitempty"`
	CanonicalInstallationID string   `json:"canonical_installation_id,omitempty"`
	InstallationIDs         []string `json:"installation_ids"`
	LastSeenAt              string   `json:"last_seen_at,omitempty"`
}

type triageAssessment struct {
	GeneratedAt      string                   `json:"generated_at"`
	Summary          discoverSummary          `json:"summary"`
	Tools            []discoverTool           `json:"tools"`
	Projects         []discoverProject        `json:"projects"`
	Installations    []discoverInstallation   `json:"installations"`
	DriftGroups      []discoverDriftGroup     `json:"drift_groups"`
	ReviewFacts      []discoverReportItem     `json:"review_facts"`
	CoverageGaps     []discoverCoverageGap    `json:"coverage_gaps"`
	Recommendations  []discoverRecommendation `json:"recommendations"`
	ActionReviews    []triageActionReview     `json:"action_reviews"`
	DeterministicRun bool                     `json:"deterministic_run"`
}

type triageActionReview struct {
	RecommendationID string `json:"recommendation_id"`
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
	ErrorDetail      string `json:"error_detail,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type triageDependencyWarning struct {
	Skill string   `json:"skill"`
	Kind  string   `json:"kind"`
	Names []string `json:"names"`
}

type triageMatrix struct {
	Skills    []string                     `json:"skills"`
	Projects  []string                     `json:"projects"`
	Cells     map[string][]string          `json:"cells"`      // projectSlug -> skill names installed
	Usage     map[string]map[string]int    `json:"usage"`      // projectSlug -> skill -> invocation count
	SkillInfo map[string]triageMatrixSkill `json:"skill_info"` // skill -> metadata for color-by / filters
}

// triageMatrixSkill carries the per-skill metadata the Matrix view uses for its
// color-by toggles (usage / recency / compatibility / requirements) and filters
// (category / tag / harness / missing dependency / safety flag).
type triageMatrixSkill struct {
	Categories         []string `json:"categories,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	Harnesses          []string `json:"harnesses,omitempty"`
	CompatibilityLabel string   `json:"compatibility"`
	RequirementsStatus string   `json:"requirements"`
	UsageTotal         int      `json:"usage_total"`
	LastActivity       string   `json:"last_activity,omitempty"`
	MissingDeps        bool     `json:"missing_deps"`
	SafetyFlag         bool     `json:"safety_flag"`
	Hostile            bool     `json:"hostile"`
}

// triageUpdateView is the enriched per-update payload for the Updates triage
// view. Deterministic safety flags and the raw diff (fetched separately) are
// authoritative; summary badges/status are advisory only.
type triageUpdateView struct {
	SkillName        string           `json:"skill_name"`
	FromVersion      string           `json:"from_version"`
	ToVersion        string           `json:"to_version"`
	Source           string           `json:"source"`
	DetectedAt       string           `json:"detected_at,omitempty"`
	SummaryStatus    string           `json:"summary_status,omitempty"`
	SummaryBadges    []string         `json:"summary_badges"`
	SafetyFlags      []safetyFlagJSON `json:"safety_flags"`
	Blocking         bool             `json:"blocking"`
	Hostile          bool             `json:"hostile"`
	AffectedProjects []string         `json:"affected_projects"`
}

func loadTriageCatalog(home string) (catalog, string, error) {
	libraryPath := filepath.Join(home, "library")
	if _, err := os.Stat(libraryPath); err != nil {
		if os.IsNotExist(err) {
			return catalog{}, libraryPath, nil
		}
		return catalog{}, libraryPath, err
	}
	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	if _, err := os.Stat(catalogPath); err != nil {
		cat, err := rebuildCatalogFromLibrary(libraryPath)
		if err != nil {
			return catalog{}, libraryPath, err
		}
		return cat, libraryPath, nil
	}
	cat, err := readCatalog(catalogPath)
	return cat, libraryPath, err
}

func loadTriageOverview(home string) (triageOverview, error) {
	out := triageOverview{
		Home:     home,
		MostUsed: []triageUsage{},
		Activity: []triageActivity{},
	}

	libraryPath := filepath.Join(home, "library")
	out.LibrarySkills = countLibrarySkills(libraryPath)
	out.PendingUpdates = countPendingUpdates(libraryPath)

	if entries, err := os.ReadDir(filepath.Join(home, "manifests")); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				out.Projects++
			}
		}
	}

	// Read-only: never create home, state.db, or library (FLO-423 / FLO-407 contract).
	db, err := state.OpenForRead(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			out.ScheduledCheck = "not configured"
			return out, nil
		}
		return out, err
	}
	defer db.Close()

	_ = db.QueryRow("SELECT COUNT(*) FROM detected WHERE action IS NULL OR action = '' OR action = 'pending'").Scan(&out.Unregistered)
	out.ScheduledCheck = checkScheduledState(db)
	out.MostUsed = loadTriageMostUsed(db, 6)
	out.Activity = loadTriageActivity(db, 8)

	return out, nil
}

func loadTriageSkills(home string) ([]triageSkill, error) {
	cat, libraryPath, err := loadTriageCatalog(home)
	if err != nil {
		return nil, err
	}
	out := make([]triageSkill, 0, len(cat.Skills))
	sources := loadTriageSkillSources(libraryPath)
	installed := loadTriageInstalledCounts(home)
	usage := loadTriageUsage(home)
	lastActivity := loadTriageLastActivity(home)
	updates := loadTriageUpdateBadges(home)
	for _, s := range cat.Skills {
		out = append(out, triageSkill{
			Name:               s.Name,
			Summary:            s.Summary,
			Categories:         s.Categories,
			Tags:               s.Tags,
			Compatibility:      s.Compatibility,
			CompatibilityLabel: compatibilityLabel(s.Compatibility),
			RequirementsStatus: requirementsStatus(s.Requirements),
			Source:             sources[s.Name],
			InstalledProjects:  installed[s.Name],
			Usage30d:           usage[s.Name],
			LastActivityAt:     lastActivity[s.Name],
			PendingUpdate:      len(updates[s.Name]) > 0,
			UpdateBadges:       updates[s.Name],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func loadTriageAssessment(home string) (triageAssessment, error) {
	db, err := state.OpenForRead(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return triageAssessment{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}, nil
		}
		return triageAssessment{}, err
	}
	defer db.Close()
	return loadTriageAssessmentFromDB(db)
}

// loadTriageAssessmentFromDB builds the assessment using an already-open
// database connection. Read-only callers (setup status) supply a read-only
// connection so computing the assessment never mutates persisted state.
func loadTriageAssessmentFromDB(db *state.DB) (triageAssessment, error) {
	out, err := loadDiscoverOutputFromDB(db)
	if err != nil {
		return triageAssessment{}, err
	}
	out.DriftGroups = annotateDiscoverDriftGroupsWithReviews(out.DriftGroups, out.Installations, loadDiscoverDriftReviewsFromDB(db))
	out.Summary = summarizeDiscovery(out)
	out.Report = buildDiscoverReport(out)
	reviews, err := loadTriageActionReviewsFromDB(db)
	if err != nil {
		return triageAssessment{}, err
	}
	return triageAssessment{
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		Summary:          out.Summary,
		Tools:            out.Tools,
		Projects:         out.Projects,
		Installations:    out.Installations,
		DriftGroups:      out.DriftGroups,
		ReviewFacts:      out.Report.ReviewFacts,
		CoverageGaps:     out.Report.CoverageGaps,
		Recommendations:  out.Report.Recommendations,
		ActionReviews:    reviews,
		DeterministicRun: true,
	}, nil
}

func loadDiscoverOutputFromState(home string) (discoverOutput, error) {
	db, err := state.Open(home)
	if err != nil {
		return discoverOutput{}, err
	}
	defer db.Close()
	return loadDiscoverOutputFromDB(db)
}

// loadDiscoverOutputFromDB loads the persisted discovery snapshot using an
// already-open database connection, so read-only callers can supply a read-only
// connection.
func loadDiscoverOutputFromDB(db *state.DB) (discoverOutput, error) {
	out := discoverOutput{
		Tools:         []discoverTool{},
		Projects:      []discoverProject{},
		Installations: []discoverInstallation{},
		DriftGroups:   []discoverDriftGroup{},
	}

	toolRows, err := db.Query(`SELECT tool_id, display_name, detected, status, COALESCE(global_roots, '[]'), COALESCE(project_patterns, '[]')
FROM discovery_tools ORDER BY tool_id`)
	if err != nil {
		return discoverOutput{}, err
	}
	for toolRows.Next() {
		var tool discoverTool
		var detected int
		var rootsJSON, patternsJSON string
		if err := toolRows.Scan(&tool.ToolID, &tool.DisplayName, &detected, &tool.Status, &rootsJSON, &patternsJSON); err != nil {
			toolRows.Close()
			return discoverOutput{}, err
		}
		tool.Detected = detected == 1
		_ = json.Unmarshal([]byte(rootsJSON), &tool.GlobalRoots)
		_ = json.Unmarshal([]byte(patternsJSON), &tool.ProjectPatterns)
		out.Tools = append(out.Tools, tool)
	}
	toolRows.Close()

	projectRows, err := db.Query(`SELECT project_id, root_path, COALESCE(repo_remote, ''), COALESCE(detected_tools, '[]'), COALESCE(last_scanned_at, '')
FROM discovery_projects WHERE present=1 ORDER BY root_path`)
	if err != nil {
		return discoverOutput{}, err
	}
	for projectRows.Next() {
		var project discoverProject
		var toolsJSON string
		if err := projectRows.Scan(&project.ProjectID, &project.RootPath, &project.RepoRemote, &toolsJSON, &project.LastScannedAt); err != nil {
			projectRows.Close()
			return discoverOutput{}, err
		}
		_ = json.Unmarshal([]byte(toolsJSON), &project.DetectedTools)
		out.Projects = append(out.Projects, project)
	}
	projectRows.Close()

	installRows, err := db.Query(`SELECT installation_id, skill_name, tool_id, scope, COALESCE(project_id, ''), source_path,
content_path, content_sha256, content_size_bytes, COALESCE(modified_at, ''), managed, ownership, format
FROM discovery_installations WHERE present=1 ORDER BY skill_name, tool_id, scope, source_path`)
	if err != nil {
		return discoverOutput{}, err
	}
	for installRows.Next() {
		var inst discoverInstallation
		var managed int
		if err := installRows.Scan(
			&inst.InstallationID, &inst.SkillName, &inst.ToolID, &inst.Scope, &inst.ProjectID, &inst.SourcePath,
			&inst.ContentPath, &inst.ContentSHA256, &inst.ContentSizeBytes, &inst.ModifiedAt, &managed, &inst.Ownership, &inst.Format,
		); err != nil {
			installRows.Close()
			return discoverOutput{}, err
		}
		inst.Managed = managed == 1
		inst.Present = true
		out.Installations = append(out.Installations, inst)
	}
	installRows.Close()

	groupRows, err := db.Query(`SELECT group_id, group_type, COALESCE(skill_name, ''), COALESCE(content_sha256, ''), COALESCE(status, '')
FROM discovery_drift_groups WHERE present=1 ORDER BY group_id`)
	if err != nil {
		return discoverOutput{}, err
	}
	for groupRows.Next() {
		var group discoverDriftGroup
		if err := groupRows.Scan(&group.GroupID, &group.GroupType, &group.SkillName, &group.ContentSHA256, &group.Status); err != nil {
			groupRows.Close()
			return discoverOutput{}, err
		}
		group.InstallationIDs = loadTriageDriftGroupInstallIDs(db, group.GroupID)
		out.DriftGroups = append(out.DriftGroups, group)
	}
	groupRows.Close()
	out.Summary = summarizeDiscovery(out)
	out.Report = buildDiscoverReport(out)
	return out, nil
}

func loadTriageActionReviews(home string) ([]triageActionReview, error) {
	db, err := state.Open(home)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return loadTriageActionReviewsFromDB(db)
}

func loadTriageActionReviewsFromDB(db *state.DB) ([]triageActionReview, error) {
	rows, err := db.Query(`SELECT recommendation_id, status, COALESCE(reason, ''), COALESCE(error_detail, ''), updated_at
FROM dashboard_action_reviews ORDER BY recommendation_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reviews := []triageActionReview{}
	for rows.Next() {
		var review triageActionReview
		if err := rows.Scan(&review.RecommendationID, &review.Status, &review.Reason, &review.ErrorDetail, &review.UpdatedAt); err != nil {
			return nil, err
		}
		reviews = append(reviews, review)
	}
	return reviews, rows.Err()
}

func loadTriageDriftGroups(home string) ([]triageDriftGroup, error) {
	db, err := state.OpenForRead(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []triageDriftGroup{}, nil
		}
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT
  g.group_id,
  COALESCE(g.group_type, ''),
  COALESCE(g.skill_name, ''),
  COALESCE(g.content_sha256, ''),
  COALESCE(g.status, ''),
  COALESCE(r.status, ''),
  COALESCE(r.reason, ''),
  COALESCE(r.canonical_installation_id, ''),
  COALESCE(g.last_seen_at, '')
FROM discovery_drift_groups g
LEFT JOIN discovery_drift_reviews r ON r.group_id = g.group_id
WHERE g.present=1
ORDER BY g.group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []triageDriftGroup
	for rows.Next() {
		var group triageDriftGroup
		if err := rows.Scan(
			&group.GroupID,
			&group.GroupType,
			&group.SkillName,
			&group.ContentSHA256,
			&group.Status,
			&group.ReviewStatus,
			&group.ReviewReason,
			&group.CanonicalInstallationID,
			&group.LastSeenAt,
		); err != nil {
			return nil, err
		}
		group.InstallationIDs = loadTriageDriftGroupInstallIDs(db, group.GroupID)
		group.Classification = classifyTriageDriftGroup(db, group)
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func loadTriageDriftGroupInstallIDs(db *state.DB, groupID string) []string {
	rows, err := db.Query(`SELECT installation_id FROM discovery_drift_group_installations WHERE group_id=? ORDER BY installation_id`, groupID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func classifyTriageDriftGroup(db *state.DB, group triageDriftGroup) string {
	installs := make([]discoverInstallation, 0, len(group.InstallationIDs))
	for _, id := range group.InstallationIDs {
		var inst discoverInstallation
		var managed int
		err := db.QueryRow(`SELECT installation_id, skill_name, tool_id, scope, project_id, source_path,
content_path, content_sha256, managed, ownership, format
FROM discovery_installations WHERE installation_id=?`, id).Scan(
			&inst.InstallationID, &inst.SkillName, &inst.ToolID, &inst.Scope, &inst.ProjectID, &inst.SourcePath,
			&inst.ContentPath, &inst.ContentSHA256, &managed, &inst.Ownership, &inst.Format,
		)
		if err != nil {
			continue
		}
		inst.Managed = managed == 1
		installs = append(installs, inst)
	}
	installsByID := discoverInstallationsByID(installs)
	return classifyDiscoverDriftGroup(discoverDriftGroup{
		GroupType:       group.GroupType,
		SkillName:       group.SkillName,
		InstallationIDs: group.InstallationIDs,
	}, installsByID)
}

func loadTriageSkillList(home string, q url.Values) (triageSkillList, error) {
	skills, err := loadTriageSkills(home)
	if err != nil {
		return triageSkillList{}, err
	}
	allCategories := stringSet{}
	allTags := stringSet{}
	allSources := stringSet{}
	for _, skill := range skills {
		allCategories.add(skill.Categories...)
		allTags.add(skill.Tags...)
		if skill.Source != "" {
			allSources.add(skill.Source)
		}
	}

	filtered := make([]triageSkill, 0, len(skills))
	for _, skill := range skills {
		if !matchesSkillFilters(skill, q) {
			continue
		}
		filtered = append(filtered, skill)
	}

	switch q.Get("sort") {
	case "usage":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Usage30d == filtered[j].Usage30d {
				return filtered[i].Name < filtered[j].Name
			}
			return filtered[i].Usage30d > filtered[j].Usage30d
		})
	case "recent":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].LastActivityAt == filtered[j].LastActivityAt {
				return filtered[i].Name < filtered[j].Name
			}
			return filtered[i].LastActivityAt > filtered[j].LastActivityAt
		})
	case "updates":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].PendingUpdate == filtered[j].PendingUpdate {
				return filtered[i].Name < filtered[j].Name
			}
			return filtered[i].PendingUpdate
		})
	default:
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
	}

	page := positiveQueryInt(q, "page", 1)
	pageSize := positiveQueryInt(q, "page_size", 50)
	if pageSize > 200 {
		pageSize = 200
	}
	total := len(filtered)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return triageSkillList{
		Skills:      filtered[start:end],
		Total:       total,
		Page:        page,
		PageSize:    pageSize,
		Categories:  allCategories.sorted(),
		Tags:        allTags.sorted(),
		Sources:     allSources.sorted(),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func loadTriageSkillDetail(home, name string) (map[string]interface{}, error) {
	cat, libraryPath, err := loadTriageCatalog(home)
	if err != nil {
		return nil, err
	}
	var skill *catalogSkill
	for i := range cat.Skills {
		if cat.Skills[i].Name == name {
			skill = &cat.Skills[i]
			break
		}
	}
	if skill == nil {
		return nil, fmt.Errorf("skill %q not found", name)
	}

	skillPath := filepath.Join(libraryPath, name)
	var meta skillMeta
	metaPath := filepath.Join(skillPath, ".skill-meta.yaml")
	if _, err := os.Stat(metaPath); err == nil {
		meta, _ = readSkillMeta(metaPath)
	}

	fp, sz, _ := fingerprintSkillMd(filepath.Join(skillPath, "SKILL.md"))
	fingerprintOut := meta.Fingerprint
	if fp != "" {
		fingerprintOut.SHA256 = fp
		fingerprintOut.Size = sz
	}

	return map[string]interface{}{
		"name":                skill.Name,
		"summary":             skill.Summary,
		"categories":          skill.Categories,
		"tags":                skill.Tags,
		"compatibility":       skill.Compatibility,
		"compatibility_label": compatibilityLabel(skill.Compatibility),
		"requirements":        skill.Requirements,
		"requirements_status": requirementsStatus(skill.Requirements),
		"origin":              meta.Origin,
		"fingerprint":         fingerprintOut,
		"categorization":      meta.Categorization,
		"installed_projects":  installedProjectSlugs(home, name),
		"usage_30d":           usageForSkill(home, name),
		"last_activity_at":    lastActivityForSkill(home, name),
	}, nil
}

func updateTriageSkillMetadata(home, name string, req triageSkillMetadataUpdate) error {
	if err := validateSkillName(name); err != nil {
		return err
	}
	cat, libraryPath, err := loadTriageCatalog(home)
	if err != nil {
		return err
	}
	idx := -1
	for i := range cat.Skills {
		if cat.Skills[i].Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("skill %q not found", name)
	}
	skillPath := filepath.Join(libraryPath, name)
	metaPath := filepath.Join(skillPath, ".skill-meta.yaml")
	if _, err := os.Stat(filepath.Join(skillPath, "SKILL.md")); err != nil {
		return fmt.Errorf("skill %q not found: %w", name, err)
	}

	meta, _ := readSkillMeta(metaPath)
	meta.Version = 1
	if req.Categories != nil {
		clean := normalizeTriageList(*req.Categories)
		cat.Skills[idx].Categories = clean
		meta.Categories = clean
	}
	if req.Tags != nil {
		clean := normalizeTriageList(*req.Tags)
		cat.Skills[idx].Tags = clean
		meta.Tags = clean
	}
	if req.Requirements != nil {
		cat.Skills[idx].Requirements = *req.Requirements
		meta.Requirements = *req.Requirements
	}
	// writeSeedSkillMeta preserves unmodeled requirement keys in the sidecar
	// (it delegates to writeSkillMeta when there are none), so a metadata edit
	// is non-destructive for skills carrying custom requirement sections.
	if err := writeSeedSkillMeta(metaPath, meta); err != nil {
		return err
	}
	return writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat)
}

func loadTriageProjects(home string) ([]triageProject, error) {
	manifestsDir := filepath.Join(home, "manifests")
	entries, err := os.ReadDir(manifestsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []triageProject{}, nil
		}
		return nil, err
	}

	var out []triageProject
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		m, err := readManifest(filepath.Join(manifestsDir, e.Name()))
		if err != nil {
			continue
		}
		tp := triageProject{
			Slug:         m.ProjectSlug,
			ProjectPath:  m.ProjectPath,
			InstalledAt:  m.InstalledAt,
			ManagedPaths: len(m.ManagedPaths),
		}
		lockPath := filepath.Join(m.ProjectPath, ".skills", "installed.lock")
		if lock, err := readInstallLock(lockPath); err == nil {
			names := make([]string, 0, len(lock.Skills))
			for _, s := range lock.Skills {
				names = append(names, s.Name)
			}
			sort.Strings(names)
			tp.Skills = names
			tp.SkillCount = len(names)
		}
		out = append(out, tp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func loadTriageProjectDetail(home, slug string) (triageProjectDetail, error) {
	projects, err := loadTriageProjects(home)
	if err != nil {
		return triageProjectDetail{}, err
	}
	var project triageProject
	for _, p := range projects {
		if p.Slug == slug {
			project = p
			break
		}
	}
	if project.Slug == "" {
		return triageProjectDetail{}, fmt.Errorf("project %q not found", slug)
	}
	detail := triageProjectDetail{triageProject: project}
	configPath := filepath.Join(project.ProjectPath, ".skills", "project.yaml")
	if cfg, err := readProjectConfig(configPath); err == nil {
		detail.Config = cfg
	}
	detail.DetectedStack = detectedStackTags(detail.Config.Tags)
	cat, _, err := loadTriageCatalog(home)
	if err != nil {
		return detail, err
	}
	installed := set(project.Skills)
	manual := set(detail.Config.AlwaysInclude)
	detail.ManualSkills = append(detail.ManualSkills, detail.Config.AlwaysInclude...)
	sort.Strings(detail.ManualSkills)
	for _, c := range scoredProjectCandidates(cat, detail.Config, installed, false) {
		if manual[c.Name] {
			continue
		}
		detail.MatchExplain = append(detail.MatchExplain, c)
		// Every candidate here is a real match (category/tag/always), so the
		// preview mirrors the install candidate list and suggestions are the
		// not-yet-installed subset, matching `match --suggest`.
		detail.PreviewSkills = append(detail.PreviewSkills, c)
		if !installed[c.Name] {
			detail.SuggestedSkills = append(detail.SuggestedSkills, c)
		}
	}
	for _, skill := range cat.Skills {
		if !installed[skill.Name] {
			continue
		}
		detail.Warnings = append(detail.Warnings, dependencyWarnings(skill)...)
	}
	return detail, nil
}

func loadTriageUpdates(home string) ([]triageUpdateView, error) {
	stateDB, err := state.OpenForRead(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []triageUpdateView{}, nil
		}
		return nil, err
	}
	updates, err := stateDB.ListPendingUpdates()
	stateDB.Close()
	if err != nil {
		return nil, err
	}
	libraryPath := filepath.Join(home, "library")
	view := make([]triageUpdateView, 0, len(updates))
	for _, u := range updates {
		pendingRoot := filepath.Join(libraryPath, u.SkillName, ".update-pending")
		badges := readPendingSummaryBadges(pendingRoot)
		uv := triageUpdateView{
			SkillName:        u.SkillName,
			FromVersion:      u.FromVersion,
			ToVersion:        u.ToVersion,
			Source:           u.Source,
			DetectedAt:       u.DetectedAt,
			SummaryStatus:    u.SummaryStatus,
			SummaryBadges:    badges,
			SafetyFlags:      []safetyFlagJSON{},
			AffectedProjects: installedProjectSlugs(home, u.SkillName),
		}
		// Deterministic safety flags from the staged snapshots are authoritative.
		if pending, err := findPendingUpdate(u.SkillName, pendingRoot); err == nil {
			if report, err := computeSafetyReport(u.SkillName, pending.From, pending.To); err == nil {
				uv.SafetyFlags = safetyReportJSON(report).Flags
				uv.Blocking = hasBlockingFlags(report)
			}
		}
		uv.Hostile = updateIsHostile(uv.SummaryStatus, badges, uv.SafetyFlags)
		if uv.SummaryBadges == nil {
			uv.SummaryBadges = []string{}
		}
		if uv.AffectedProjects == nil {
			uv.AffectedProjects = []string{}
		}
		view = append(view, uv)
	}
	return view, nil
}

// updateIsHostile reports whether deterministic flags or advisory signals
// indicate prompt-injection / hostile review instructions, which the UI must
// surface prominently and which can never be cleared by an AI summary.
func updateIsHostile(summaryStatus string, badges []string, flags []safetyFlagJSON) bool {
	if summaryStatus == "tainted" {
		return true
	}
	for _, b := range badges {
		if b == "hostile-instructions" || b == "suspicious-instructions" {
			return true
		}
	}
	for _, f := range flags {
		if f.Name == "suspicious-instructions" {
			return true
		}
	}
	return false
}

func loadTriageMatrix(home string) (triageMatrix, error) {
	cat, _, err := loadTriageCatalog(home)
	if err != nil {
		return triageMatrix{}, err
	}
	projects, err := loadTriageProjects(home)
	if err != nil {
		return triageMatrix{}, err
	}

	m := triageMatrix{
		Skills:    make([]string, 0, len(cat.Skills)),
		Projects:  make([]string, 0, len(projects)),
		Cells:     map[string][]string{},
		Usage:     map[string]map[string]int{},
		SkillInfo: map[string]triageMatrixSkill{},
	}
	for _, p := range projects {
		m.Projects = append(m.Projects, p.Slug)
		m.Cells[p.Slug] = append([]string(nil), p.Skills...)
		m.Usage[p.Slug] = map[string]int{}
	}

	// Per-skill×project usage counts and per-skill totals / last activity.
	usageTotals := map[string]int{}
	lastActivity := map[string]string{}
	if db, err := state.OpenForRead(home); err == nil {
		if cells, err := db.UsageMatrix(); err == nil {
			for _, c := range cells {
				usageTotals[c.SkillName] += c.Count
				if _, ok := m.Usage[c.ProjectSlug]; ok {
					m.Usage[c.ProjectSlug][c.SkillName] += c.Count
				}
			}
		}
		rows, qErr := db.Query(`SELECT skill_name, MAX(invoked_at) FROM invocations GROUP BY skill_name`)
		if qErr == nil {
			for rows.Next() {
				var name, at string
				if rows.Scan(&name, &at) == nil {
					lastActivity[name] = at
				}
			}
			rows.Close()
		}
		db.Close()
	}

	// Skills with a pending update carrying a safety / hostile flag.
	safety := map[string]bool{}
	hostile := map[string]bool{}
	if updates, err := loadTriageUpdates(home); err == nil {
		for _, u := range updates {
			if len(u.SafetyFlags) > 0 || u.Blocking {
				safety[u.SkillName] = true
			}
			if u.Hostile {
				hostile[u.SkillName] = true
			}
		}
	}

	allHarnesses := []string{"claude", "codex", "grok", "antigravity", "gemini", "hermes", "openclaw"}
	for _, skill := range cat.Skills {
		m.Skills = append(m.Skills, skill.Name)
		m.SkillInfo[skill.Name] = triageMatrixSkill{
			Categories:         skill.Categories,
			Tags:               skill.Tags,
			Harnesses:          compatibleHarnesses(skill.Compatibility, allHarnesses),
			CompatibilityLabel: compatibilityLabel(skill.Compatibility),
			RequirementsStatus: requirementsStatus(skill.Requirements),
			UsageTotal:         usageTotals[skill.Name],
			LastActivity:       lastActivity[skill.Name],
			MissingDeps:        len(dependencyWarningStrings(skill)) > 0,
			SafetyFlag:         safety[skill.Name],
			Hostile:            hostile[skill.Name],
		}
	}
	sort.Strings(m.Skills)
	return m, nil
}

func loadTriageUpdateDiff(home, skill string) (string, error) {
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		return "", err
	}
	pendingRoot := filepath.Join(libraryPath, skill, ".update-pending")
	if _, err := os.Stat(pendingRoot); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no pending update for %s", skill)
		}
		return "", err
	}
	pending, err := findPendingUpdate(skill, pendingRoot)
	if err != nil {
		return "", err
	}
	fromFiles, err := snapshotFiles(pending.From)
	if err != nil {
		return "", err
	}
	toFiles, err := snapshotFiles(pending.To)
	if err != nil {
		return "", err
	}
	fromSnapshot, ok := fromFiles["SKILL.md"]
	if !ok {
		return "", fmt.Errorf("SKILL.md not found in from snapshot")
	}
	toSnapshot, ok := toFiles["SKILL.md"]
	if !ok {
		return "", fmt.Errorf("SKILL.md not found in to snapshot")
	}
	return gitDiff([]byte(fromSnapshot.Content), []byte(toSnapshot.Content))
}

type stringSet map[string]bool

func (s stringSet) add(values ...string) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			s[value] = true
		}
	}
}

func (s stringSet) sorted() []string {
	out := make([]string, 0, len(s))
	for value := range s {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func matchesSkillFilters(skill triageSkill, q url.Values) bool {
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))
	if search != "" {
		haystack := strings.ToLower(strings.Join(append([]string{skill.Name, skill.Summary}, append(skill.Categories, skill.Tags...)...), " "))
		if !strings.Contains(haystack, search) {
			return false
		}
	}
	if category := q.Get("category"); category != "" && !triageContainsString(skill.Categories, category) {
		return false
	}
	if tag := q.Get("tag"); tag != "" && !triageContainsString(skill.Tags, tag) {
		return false
	}
	if source := q.Get("source"); source != "" && skill.Source != source {
		return false
	}
	if compat := q.Get("compatibility"); compat != "" && skill.CompatibilityLabel != compat {
		return false
	}
	if req := q.Get("requirements"); req != "" && skill.RequirementsStatus != req {
		return false
	}
	return true
}

func positiveQueryInt(q url.Values, key string, fallback int) int {
	n, err := strconv.Atoi(q.Get(key))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func triageContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func compatibilityLabel(c compatibility) string {
	if c.Mode != "" {
		return c.Mode
	}
	if c.ExplicitPortable {
		return "portable"
	}
	if c.Harness != "" {
		return "exclusive"
	}
	if len(c.Harnesses) > 0 {
		return "compatible"
	}
	return "unknown"
}

func requirementsStatus(r requirements) string {
	if !r.hasExplicitFields() {
		return "none"
	}
	if r.Inferred {
		return "inferred"
	}
	return "declared"
}

func loadTriageSkillSources(libraryPath string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() {
			meta, err := readSkillMeta(filepath.Join(libraryPath, entry.Name(), ".skill-meta.yaml"))
			if err != nil {
				continue
			}
			source := meta.Origin.Source
			if source == "" {
				source = meta.Origin.Type
			}
			if source != "" {
				out[entry.Name()] = source
			}
		}
	}
	return out
}

func loadTriageInstalledCounts(home string) map[string]int {
	out := map[string]int{}
	projects, err := loadTriageProjects(home)
	if err != nil {
		return out
	}
	for _, project := range projects {
		for _, skill := range project.Skills {
			out[skill]++
		}
	}
	return out
}

func loadTriageUsage(home string) map[string]int {
	out := map[string]int{}
	db, err := state.Open(home)
	if err != nil {
		return out
	}
	defer db.Close()
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	rows, err := db.Query(`SELECT skill_name, COUNT(*) FROM invocations WHERE invoked_at >= ? GROUP BY skill_name`, cutoff)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var skill string
		var count int
		if err := rows.Scan(&skill, &count); err == nil {
			out[skill] = count
		}
	}
	return out
}

func loadTriageLastActivity(home string) map[string]string {
	out := map[string]string{}
	db, err := state.Open(home)
	if err != nil {
		return out
	}
	defer db.Close()
	rows, err := db.Query(`SELECT skill_name, MAX(invoked_at) FROM invocations GROUP BY skill_name`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var skill, at string
		if err := rows.Scan(&skill, &at); err == nil {
			out[skill] = at
		}
	}
	return out
}

func loadTriageUpdateBadges(home string) map[string][]string {
	out := map[string][]string{}
	updates, err := loadTriageUpdates(home)
	if err != nil {
		return out
	}
	for _, update := range updates {
		badges := append([]string(nil), update.SummaryBadges...)
		if update.SummaryStatus != "" {
			badges = append(badges, update.SummaryStatus)
		}
		out[update.SkillName] = badges
	}
	return out
}

func installedProjectSlugs(home, skillName string) []string {
	projects, err := loadTriageProjects(home)
	if err != nil {
		return []string{}
	}
	var out []string
	for _, project := range projects {
		if triageContainsString(project.Skills, skillName) {
			out = append(out, project.Slug)
		}
	}
	sort.Strings(out)
	return out
}

func usageForSkill(home, skillName string) int {
	db, err := state.Open(home)
	if err != nil {
		return 0
	}
	defer db.Close()
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM invocations WHERE skill_name = ? AND invoked_at >= ?`, skillName, cutoff).Scan(&count)
	return count
}

func lastActivityForSkill(home, skillName string) string {
	db, err := state.Open(home)
	if err != nil {
		return ""
	}
	defer db.Close()
	var at string
	_ = db.QueryRow(`SELECT MAX(invoked_at) FROM invocations WHERE skill_name = ?`, skillName).Scan(&at)
	return at
}

func scoredProjectCandidates(cat catalog, project projectConfig, installed map[string]bool, suggestOnly bool) []triageProjectCandidate {
	var out []triageProjectCandidate
	// Mirror `skills-manager match`: only always/category/tag matches are
	// candidates. Scoring every catalog skill would leak unrelated portable
	// skills (compatible with all harnesses, score 0) into the preview.
	for _, c := range selectInstallCandidates(cat, project, "") {
		skill := c.Skill
		if suggestOnly && installed[skill.Name] {
			continue
		}
		score, reasons, warnings := computeMatchScore(skill, project)
		harnesses := compatibleHarnesses(skill.Compatibility, project.Harnesses)
		if len(harnesses) == 0 {
			warnings = append(warnings, "no compatible harness in project")
		}
		warnings = append(warnings, dependencyWarningStrings(skill)...)
		out = append(out, triageProjectCandidate{
			Name:      skill.Name,
			Score:     score,
			Reasons:   reasons,
			Harnesses: harnesses,
			Warnings:  warnings,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func dependencyWarnings(skill catalogSkill) []triageDependencyWarning {
	var out []triageDependencyWarning
	add := func(kind string, names []string) {
		if len(names) > 0 {
			out = append(out, triageDependencyWarning{Skill: skill.Name, Kind: kind, Names: names})
		}
	}
	add("tools", missingRequiredTools(skill.Requirements))
	add("mcp_servers", missingRequiredMCPServers(skill.Requirements))
	add("model", missingModelCapabilities(skill.Requirements))
	add("credentials", missingRequiredCredentials(skill.Requirements))
	add("runtimes", missingRequiredScriptRuntimes(skill.Requirements))
	return out
}

func dependencyWarningStrings(skill catalogSkill) []string {
	var out []string
	for _, warning := range dependencyWarnings(skill) {
		out = append(out, fmt.Sprintf("missing %s: %s", warning.Kind, strings.Join(warning.Names, ", ")))
	}
	return out
}

func detectedStackTags(tags []string) []string {
	stackTags := set([]string{"nodejs", "python", "go", "rust", "typescript", "java", "php", "csharp"})
	var out []string
	for _, tag := range tags {
		if stackTags[tag] {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeTriageList(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func loadTriageMostUsed(db *state.DB, limit int) []triageUsage {
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	rows, err := db.Query(`SELECT skill_name, COUNT(*) AS c FROM invocations WHERE invoked_at >= ? GROUP BY skill_name ORDER BY c DESC, skill_name LIMIT ?`, cutoff, limit)
	if err != nil {
		return []triageUsage{}
	}
	defer rows.Close()
	var out []triageUsage
	for rows.Next() {
		var item triageUsage
		if err := rows.Scan(&item.SkillName, &item.Count); err == nil {
			out = append(out, item)
		}
	}
	return out
}

func loadTriageActivity(db *state.DB, limit int) []triageActivity {
	rows, err := db.Query(`
		SELECT kind, skill_name, detail, at
		FROM (
			SELECT 'update' AS kind, skill_name, source AS detail, detected_at AS at, 0 AS priority
			FROM updates
			WHERE status = 'pending'
			UNION ALL
			SELECT 'detected' AS kind, skill_name, source_guess AS detail, detected_at AS at, 1 AS priority
			FROM detected
			WHERE action IS NULL OR action = '' OR action = 'pending'
			UNION ALL
			SELECT 'invocation' AS kind, skill_name, COALESCE(project_slug, '') AS detail, invoked_at AS at, 2 AS priority
			FROM invocations
		)
		ORDER BY priority, at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return []triageActivity{}
	}
	defer rows.Close()
	var out []triageActivity
	for rows.Next() {
		var item triageActivity
		if err := rows.Scan(&item.Kind, &item.SkillName, &item.Detail, &item.At); err == nil {
			out = append(out, item)
		}
	}
	if out == nil {
		return []triageActivity{}
	}
	return out
}
