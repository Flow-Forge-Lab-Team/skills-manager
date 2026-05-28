package cli

import (
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

type triageMatrix struct {
	Skills   []string            `json:"skills"`
	Projects []string            `json:"projects"`
	Cells    map[string][]string `json:"cells"` // projectSlug -> skill names installed
}

func loadTriageCatalog(home string) (catalog, string, error) {
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		return catalog{}, "", err
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
	out := triageOverview{Home: home}

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		return out, err
	}
	out.LibrarySkills = countLibrarySkills(libraryPath)
	out.PendingUpdates = countPendingUpdates(libraryPath)

	db, err := state.Open(home)
	if err != nil {
		return out, err
	}
	defer db.Close()

	if entries, err := os.ReadDir(filepath.Join(home, "manifests")); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				out.Projects++
			}
		}
	}

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
		"name":           skill.Name,
		"summary":        skill.Summary,
		"categories":     skill.Categories,
		"tags":           skill.Tags,
		"compatibility":  skill.Compatibility,
		"requirements":   skill.Requirements,
		"origin":         meta.Origin,
		"fingerprint":    fingerprintOut,
		"categorization": meta.Categorization,
	}, nil
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

func loadTriageUpdates(home string) ([]pendingUpdateView, error) {
	stateDB, err := state.Open(home)
	if err != nil {
		return nil, err
	}
	defer stateDB.Close()

	updates, err := stateDB.ListPendingUpdates()
	if err != nil {
		return nil, err
	}
	libraryPath := filepath.Join(home, "library")
	view := make([]pendingUpdateView, 0, len(updates))
	for _, u := range updates {
		view = append(view, pendingUpdateView{
			PendingUpdate: u,
			SummaryBadges: readPendingSummaryBadges(filepath.Join(libraryPath, u.SkillName, ".update-pending")),
		})
	}
	return view, nil
}

func loadTriageMatrix(home string) (triageMatrix, error) {
	skills, err := loadTriageSkillNames(home)
	if err != nil {
		return triageMatrix{}, err
	}
	projects, err := loadTriageProjects(home)
	if err != nil {
		return triageMatrix{}, err
	}

	m := triageMatrix{
		Skills:   make([]string, 0, len(skills)),
		Projects: make([]string, 0, len(projects)),
		Cells:    map[string][]string{},
	}
	for _, s := range skills {
		m.Skills = append(m.Skills, s)
	}
	for _, p := range projects {
		m.Projects = append(m.Projects, p.Slug)
		m.Cells[p.Slug] = append([]string(nil), p.Skills...)
	}
	return m, nil
}

func loadTriageSkillNames(home string) ([]string, error) {
	cat, _, err := loadTriageCatalog(home)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cat.Skills))
	for _, skill := range cat.Skills {
		names = append(names, skill.Name)
	}
	sort.Strings(names)
	return names, nil
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
