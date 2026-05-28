package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

type triageOverview struct {
	LibrarySkills  int    `json:"library_skills"`
	Projects       int    `json:"projects"`
	PendingUpdates int    `json:"pending_updates"`
	Unregistered   int    `json:"unregistered"`
	ScheduledCheck string `json:"scheduled_check"`
	Home           string `json:"home"`
	GeneratedAt    string `json:"generated_at"`
}

type triageSkill struct {
	Name          string        `json:"name"`
	Summary       string        `json:"summary,omitempty"`
	Categories    []string      `json:"categories,omitempty"`
	Tags          []string      `json:"tags,omitempty"`
	Compatibility compatibility `json:"compatibility,omitempty"`
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

	return out, nil
}

func loadTriageSkills(home string) ([]triageSkill, error) {
	cat, _, err := loadTriageCatalog(home)
	if err != nil {
		return nil, err
	}
	out := make([]triageSkill, 0, len(cat.Skills))
	for _, s := range cat.Skills {
		out = append(out, triageSkill{
			Name:          s.Name,
			Summary:       s.Summary,
			Categories:    s.Categories,
			Tags:          s.Tags,
			Compatibility: s.Compatibility,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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
	skills, err := loadTriageSkills(home)
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
		m.Skills = append(m.Skills, s.Name)
	}
	for _, p := range projects {
		m.Projects = append(m.Projects, p.Slug)
		m.Cells[p.Slug] = append([]string(nil), p.Skills...)
	}
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
