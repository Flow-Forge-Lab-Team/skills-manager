package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type installOptions struct {
	projectPath              string
	onlySkill                string
	dryRun                   bool
	allowMissingRequirements bool
	skipMissingLocked        bool
	confirm                  bool
}

type projectConfig struct {
	Name          string
	Categories    []string
	Tags          []string
	Harnesses     []string
	AlwaysInclude []string
	NeverInclude  []string
}

type catalog struct {
	Skills []catalogSkill
}

type catalogSkill struct {
	Name          string
	Categories    []string
	Tags          []string
	Compatibility compatibility
	Requirements  requirements
}

type compatibility struct {
	Mode      string
	Harness   string
	Harnesses []string
}

type requirements struct {
	Tools []toolRequirement
}

type toolRequirement struct {
	Name     string
	Required bool
}

type installManifest struct {
	Version        int               `json:"version"`
	ProjectPath    string            `json:"project_path"`
	ProjectSlug    string            `json:"project_slug"`
	InstalledAt    string            `json:"installed_at"`
	ManagedPaths   []string          `json:"managed_paths"`
	PreservedPaths []string          `json:"preserved_paths"`
	Files          map[string]string `json:"files,omitempty"`
}

type installCandidate struct {
	Skill     catalogSkill
	Harnesses []string
	Reason    string
	Missing   []string
}

type installLockEntry struct {
	Name       string
	Version    string
	Commit     string
	Fingerprint string
	InstalledAt string
	Harnesses  []string
}

type installLock struct {
	Version     int
	GeneratedAt string
	GeneratedBy string
	Skills      []installLockEntry
}

var harnessProjectPaths = map[string]string{
	"claude":      ".claude/skills",
	"codex":       ".codex/skills",
	"grok":        ".grok/skills",
	"antigravity": ".agents/skills",
	"gemini":      ".agents/skills",
	"hermes":      "skills",
	"openclaw":    "skills",
}

func runInstall(args []string, stdout io.Writer, stderr io.Writer, syncMode bool) int {
	opts, err := parseInstallOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	projectPath, err := absoluteProjectPath(opts.projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "project path: %v\n", err)
		return 3
	}
	opts.projectPath = projectPath

	project, err := readProjectConfig(filepath.Join(projectPath, ".skills", "project.yaml"))
	if err != nil {
		fmt.Fprintf(stderr, "read project config: %v\n", err)
		return 3
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return 3
	}
	libraryPath := filepath.Join(home, "library")
	catalog, err := readCatalog(filepath.Join(libraryPath, "catalog.yaml"))
	if err != nil {
		fmt.Fprintf(stderr, "read catalog: %v\n", err)
		return 3
	}

	manifestPath := manifestPath(home, projectPath)
	manifest, err := readManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "read manifest: %v\n", err)
		return 3
	}
	if manifest.ProjectPath != "" && manifest.ProjectPath != projectPath {
		fmt.Fprintf(stderr, "manifest belongs to %s, not %s\n", manifest.ProjectPath, projectPath)
		return 3
	}
	if manifest.ProjectPath == "" {
		manifest = newManifest(projectPath)
	}

	lockPath := filepath.Join(projectPath, ".skills", "installed.lock")
	lock, err := readInstallLock(lockPath)
	if err != nil {
		fmt.Fprintf(stderr, "read lock: %v\n", err)
		return 3
	}

	var candidates []installCandidate
	var skippedLockedSkills bool
	if len(lock.Skills) > 0 {
		// Lock exists: use it to determine candidates
		missing := missingLockedSkills(lock, catalog)
		if len(missing) > 0 && !opts.skipMissingLocked {
			fmt.Fprintf(stderr, "the following skills in .skills/installed.lock are missing from the library:\n")
			for _, name := range missing {
				fmt.Fprintf(stderr, "  - %s\n", name)
			}
			fmt.Fprintf(stderr, "suggest:\n")
			fmt.Fprintf(stderr, "  - run: skills-manager sync-library\n")
			fmt.Fprintf(stderr, "  - or: skills-manager install --skip-missing-locked\n")
			return 3
		}

		// Build candidates from lock: skip missing, apply --only filter, apply never_include
		never := set(project.NeverInclude)
		for _, lockSkill := range lock.Skills {
			if opts.onlySkill != "" && lockSkill.Name != opts.onlySkill {
				continue
			}
			if never[lockSkill.Name] {
				fmt.Fprintf(stdout, "- %s: skipped, in never_include list\n", lockSkill.Name)
				continue
			}
			// Find skill in catalog to get the definition
			var catalogSkill *catalogSkill
			for i, s := range catalog.Skills {
				if s.Name == lockSkill.Name {
					catalogSkill = &catalog.Skills[i]
					break
				}
			}
			if catalogSkill == nil {
				if opts.skipMissingLocked {
					skippedLockedSkills = true
					continue
				}
				fmt.Fprintf(stderr, "skill %q in lock but not in catalog\n", lockSkill.Name)
				return 3
			}
			// Recompute compatible harnesses based on current catalog (not locked harnesses)
			// This allows the install to adapt to compatibility changes
			harnesses := compatibleHarnesses(catalogSkill.Compatibility, project.Harnesses)
			candidates = append(candidates, installCandidate{
				Skill:     *catalogSkill,
				Harnesses: harnesses,
				Reason:    "locked skill",
			})
		}
	} else {
		// No lock: use normal selection from catalog + project config
		candidates = selectInstallCandidates(catalog, project, opts.onlySkill)
	}

	if opts.onlySkill != "" && len(candidates) == 0 {
		fmt.Fprintf(stderr, "skill %q was not found or does not match this project\n", opts.onlySkill)
		return 3
	}
	for _, candidate := range candidates {
		if err := validateSkillName(candidate.Skill.Name); err != nil {
			fmt.Fprintf(stderr, "invalid skill name %q: %v\n", candidate.Skill.Name, err)
			return 3
		}
	}

	blocked := 0
	installed := 0
	preserved := mapFromSlice(manifest.PreservedPaths)
	managed := mapFromSlice(manifest.ManagedPaths)
	files := manifest.Files
	if files == nil {
		files = map[string]string{}
	}

	if opts.dryRun {
		fmt.Fprintln(stdout, "Install preview:")
	} else if syncMode {
		fmt.Fprintln(stdout, "Syncing skills:")
	} else {
		fmt.Fprintln(stdout, "Installing skills:")
	}

	desired := desiredManagedPaths(candidates)
	if syncMode && opts.dryRun {
		if err := previewStaleManaged(projectPath, managed, desired, files, stdout); err != nil {
			fmt.Fprintf(stderr, "preview stale installs: %v\n", err)
			return 3
		}
	}

	partial := false
	for _, candidate := range candidates {
		if len(candidate.Harnesses) == 0 {
			fmt.Fprintf(stdout, "- %s: skipped, no compatible active harnesses\n", candidate.Skill.Name)
			continue
		}

		missing := missingRequiredTools(candidate.Skill.Requirements)
		candidate.Missing = missing
		if len(missing) > 0 && !opts.allowMissingRequirements {
			blocked++
			fmt.Fprintf(stdout, "- %s: blocked, missing required tools: %s\n", candidate.Skill.Name, strings.Join(missing, ", "))
			continue
		}
		if len(missing) > 0 {
			fmt.Fprintf(stdout, "- %s: warning, installing despite missing required tools: %s\n", candidate.Skill.Name, strings.Join(missing, ", "))
		}

		fmt.Fprintf(stdout, "- %s: %s; harnesses: %s\n", candidate.Skill.Name, candidate.Reason, strings.Join(candidate.Harnesses, ", "))
		src := filepath.Join(libraryPath, candidate.Skill.Name)
		for _, targetBase := range targetBases(candidate.Harnesses) {
			relTarget := filepath.ToSlash(filepath.Join(targetBase, candidate.Skill.Name))
			target := filepath.Join(projectPath, relTarget)
			if opts.dryRun {
				if _, err := os.Stat(target); err == nil {
					if _, ok := managed[relTarget]; !ok {
						preserved[relTarget] = true
						fmt.Fprintf(stdout, "  preserve %s (already exists, unmanaged)\n", relTarget)
						continue
					}
					expected := files[relTarget]
					if expected != "" {
						actual, err := fingerprintDir(target)
						if err != nil {
							fmt.Fprintf(stderr, "fingerprint %s: %v\n", relTarget, err)
							return 3
						}
						if actual != expected {
							fmt.Fprintf(stdout, "  preserve %s (manager-owned copy has local edits)\n", relTarget)
							continue
						}
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					fmt.Fprintf(stderr, "stat %s: %v\n", relTarget, err)
					return 3
				}
				fmt.Fprintf(stdout, "  copy %s -> %s\n", candidate.Skill.Name, relTarget)
				continue
			}

			wrote, err := installSkillCopy(src, target, relTarget, manifest, managed)
			if err != nil {
				if errors.Is(err, errUnmanagedTarget) || errors.Is(err, errLocallyEditedTarget) {
					preserved[relTarget] = true
					fmt.Fprintf(stdout, "  preserve %s (%v)\n", relTarget, err)
					continue
				}
				if installed > 0 {
					if writeErr := saveInstallManifest(manifestPath, manifest, managed, preserved, files); writeErr != nil {
						fmt.Fprintf(stderr, "write manifest after failed install: %v\n", writeErr)
					}
				}
				fmt.Fprintf(stderr, "install %s: %v\n", relTarget, err)
				return 3
			}
			if wrote {
				installed++
				managed[relTarget] = true
				delete(preserved, relTarget)
				fingerprint, err := fingerprintDir(target)
				if err != nil {
					if writeErr := saveInstallManifest(manifestPath, manifest, managed, preserved, files); writeErr != nil {
						fmt.Fprintf(stderr, "write manifest after failed fingerprint: %v\n", writeErr)
					}
					fmt.Fprintf(stderr, "fingerprint %s: %v\n", relTarget, err)
					return 3
				}
				files[relTarget] = fingerprint
				fmt.Fprintf(stdout, "  copied %s\n", relTarget)
			}
		}
	}

	if !opts.dryRun {
		if syncMode {
			prunePartial, err := pruneStaleManaged(projectPath, managed, desired, preserved, files, stdout)
			if err != nil {
				fmt.Fprintf(stderr, "prune stale installs: %v\n", err)
				return 3
			}
			if prunePartial {
				partial = true
			}
		}
		if err := saveInstallManifest(manifestPath, manifest, managed, preserved, files); err != nil {
			fmt.Fprintf(stderr, "write manifest: %v\n", err)
			return 3
		}

		// Write installed.lock after successful manifest write
		if len(lock.Skills) > 0 || len(candidates) > 0 {
			newLock := buildInstallLock(candidates, files, libraryPath, lock)
			if err := writeInstallLock(lockPath, newLock); err != nil {
				fmt.Fprintf(stderr, "write lock: %v\n", err)
				return 3
			}
		}
	}

	if skippedLockedSkills && installed > 0 {
		partial = true
	}

	if blocked > 0 {
		if installed > 0 {
			return 4
		}
		return 3
	}
	if partial {
		return 4
	}
	return 0
}

func runUninstall(args []string, stdout io.Writer, stderr io.Writer) int {
	opts, err := parseUninstallOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	projectPath, err := absoluteProjectPath(opts.projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "project path: %v\n", err)
		return 3
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return 3
	}
	path := manifestPath(home, projectPath)
	manifest, err := readManifest(path)
	if err != nil {
		fmt.Fprintf(stderr, "read manifest: %v\n", err)
		return 3
	}
	if manifest.ProjectPath == "" {
		fmt.Fprintln(stderr, "no manifest found for project")
		return 3
	}
	if manifest.ProjectPath != projectPath {
		fmt.Fprintf(stderr, "manifest belongs to %s, not %s\n", manifest.ProjectPath, projectPath)
		return 3
	}

	fmt.Fprintln(stdout, "Uninstall preview:")
	for _, rel := range manifest.ManagedPaths {
		fmt.Fprintf(stdout, "- remove %s\n", rel)
	}
	if !opts.confirm {
		fmt.Fprintln(stderr, "refusing to uninstall without --confirm")
		return 2
	}

	remaining := map[string]bool{}
	for i := len(manifest.ManagedPaths) - 1; i >= 0; i-- {
		rel := manifest.ManagedPaths[i]
		target := filepath.Join(projectPath, filepath.FromSlash(rel))
		expected := manifest.Files[rel]
		if expected != "" {
			actual, err := fingerprintDir(target)
			if err == nil && actual != expected {
				fmt.Fprintf(stdout, "- preserve %s (manager-owned copy has local edits)\n", rel)
				remaining[rel] = true
				continue
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(stderr, "fingerprint %s: %v\n", rel, err)
				return 3
			}
		}
		if err := os.RemoveAll(target); err != nil {
			fmt.Fprintf(stderr, "remove %s: %v\n", rel, err)
			return 3
		}
		pruneEmptyParents(projectPath, filepath.Dir(target))
		delete(manifest.Files, rel)
	}
	if len(remaining) > 0 {
		manifest.ManagedPaths = sortedKeys(remaining)
		manifest.PreservedPaths = unionSorted(manifest.PreservedPaths, manifest.ManagedPaths)
		if err := writeManifest(path, manifest); err != nil {
			fmt.Fprintf(stderr, "write manifest: %v\n", err)
			return 3
		}
		return 4
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "remove manifest: %v\n", err)
		return 3
	}
	return 0
}

func parseInstallOptions(args []string) (installOptions, error) {
	opts := installOptions{projectPath: "."}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			i++
			if i >= len(args) {
				return opts, errors.New("--project requires a path")
			}
			opts.projectPath = args[i]
		case "--dry-run":
			opts.dryRun = true
		case "--only":
			i++
			if i >= len(args) {
				return opts, errors.New("--only requires a skill name")
			}
			opts.onlySkill = args[i]
		case "--allow-missing-requirements":
			opts.allowMissingRequirements = true
		case "--skip-missing-locked":
			opts.skipMissingLocked = true
		default:
			return opts, fmt.Errorf("unknown install argument: %s", args[i])
		}
	}
	return opts, nil
}

func parseUninstallOptions(args []string) (installOptions, error) {
	opts := installOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			i++
			if i >= len(args) {
				return opts, errors.New("--project requires a path")
			}
			opts.projectPath = args[i]
		case "--confirm":
			opts.confirm = true
		default:
			return opts, fmt.Errorf("unknown uninstall argument: %s", args[i])
		}
	}
	if opts.projectPath == "" {
		return opts, errors.New("uninstall requires --project")
	}
	return opts, nil
}

func absoluteProjectPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return filepath.Abs(path)
}

func managerHome() (string, error) {
	if value := os.Getenv("SKILLS_MANAGER_HOME"); value != "" {
		return filepath.Abs(value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".skills-manager"), nil
}

func manifestPath(home string, projectPath string) string {
	return filepath.Join(home, "manifests", projectSlug(projectPath)+".json")
}

func projectSlug(projectPath string) string {
	sum := sha256.Sum256([]byte(projectPath))
	return slug(filepath.Base(projectPath)) + "-" + hex.EncodeToString(sum[:])[:12]
}

func slug(value string) string {
	value = strings.ToLower(value)
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func newManifest(projectPath string) installManifest {
	return installManifest{
		Version:      1,
		ProjectPath:  projectPath,
		ProjectSlug:  projectSlug(projectPath),
		InstalledAt:  time.Now().UTC().Format(time.RFC3339),
		ManagedPaths: []string{},
		Files:        map[string]string{},
	}
}

func readManifest(path string) (installManifest, error) {
	var manifest installManifest
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return manifest, nil
	}
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func writeManifest(path string, manifest installManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func saveInstallManifest(path string, manifest installManifest, managed map[string]bool, preserved map[string]bool, files map[string]string) error {
	manifest.ManagedPaths = sortedKeys(managed)
	manifest.PreservedPaths = sortedKeys(preserved)
	manifest.Files = files
	manifest.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	return writeManifest(path, manifest)
}

func readInstallLock(path string) (installLock, error) {
	lines, err := readLines(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return installLock{}, nil
		}
		return installLock{}, err
	}
	var lock installLock
	var currentSkill *installLockEntry
	var section string
	for i := 0; i < len(lines); i++ {
		raw := stripComment(lines[i])
		if strings.TrimSpace(raw) == "" {
			continue
		}
		trimmed := strings.TrimSpace(raw)

		// Handle "skills:" section header
		if strings.HasPrefix(trimmed, "skills:") {
			section = "skills"
			continue
		}

		// Handle list item "- name: ..." for new skill entry
		if section == "skills" && strings.HasPrefix(trimmed, "- name:") {
			if currentSkill != nil {
				lock.Skills = append(lock.Skills, *currentSkill)
			}
			nameValue := strings.TrimPrefix(trimmed, "- name:")
			currentSkill = &installLockEntry{Name: unquote(strings.TrimSpace(nameValue))}
			continue
		}

		// Parse key-value pairs
		key, value, ok := splitYAMLKey(raw)
		if !ok {
			continue
		}

		if section == "" {
			// Top-level keys
			switch key {
			case "version":
				if value == "1" {
					lock.Version = 1
				}
			case "generated_at":
				lock.GeneratedAt = unquote(value)
			case "generated_by":
				lock.GeneratedBy = unquote(value)
			}
		} else if section == "skills" && currentSkill != nil {
			// Per-skill keys
			switch key {
			case "version":
				currentSkill.Version = unquote(value)
			case "commit":
				currentSkill.Commit = unquote(value)
			case "fingerprint":
				currentSkill.Fingerprint = unquote(value)
			case "installed_at":
				currentSkill.InstalledAt = unquote(value)
			case "harnesses":
				items, next := readYAMLStringList(lines, i, value)
				i = next
				currentSkill.Harnesses = items
			}
		}
	}
	if currentSkill != nil {
		lock.Skills = append(lock.Skills, *currentSkill)
	}
	return lock, nil
}

func writeInstallLock(path string, lock installLock) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf strings.Builder
	buf.WriteString("version: 1\n")
	buf.WriteString(fmt.Sprintf("generated_at: %q\n", lock.GeneratedAt))
	buf.WriteString(fmt.Sprintf("generated_by: %q\n", lock.GeneratedBy))
	buf.WriteString("skills:\n")
	for _, skill := range lock.Skills {
		buf.WriteString("  - name: " + skill.Name + "\n")
		if skill.Version != "" {
			buf.WriteString(fmt.Sprintf("    version: %q\n", skill.Version))
		} else {
			buf.WriteString("    version: ~\n")
		}
		if skill.Commit != "" {
			buf.WriteString(fmt.Sprintf("    commit: %q\n", skill.Commit))
		} else {
			buf.WriteString("    commit: ~\n")
		}
		buf.WriteString(fmt.Sprintf("    fingerprint: %q\n", skill.Fingerprint))
		buf.WriteString(fmt.Sprintf("    installed_at: %q\n", skill.InstalledAt))
		if len(skill.Harnesses) > 0 {
			buf.WriteString("    harnesses:\n")
			for _, h := range skill.Harnesses {
				buf.WriteString(fmt.Sprintf("      - %s\n", h))
			}
		} else {
			buf.WriteString("    harnesses: []\n")
		}
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

func buildInstallLock(candidates []installCandidate, files map[string]string, libraryPath string, oldLock installLock) installLock {
	newLock := installLock{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		GeneratedBy: fmt.Sprintf("skills-manager %s", Version),
		Skills:      []installLockEntry{},
	}

	// Track which skills are in the new candidates
	newSkillNames := map[string]bool{}

	for _, candidate := range candidates {
		newSkillNames[candidate.Skill.Name] = true

		// Get fingerprint from the first target base (they should all be identical)
		targetBases := targetBases(candidate.Harnesses)
		var fingerprint string
		if len(targetBases) > 0 {
			relTarget := filepath.ToSlash(filepath.Join(targetBases[0], candidate.Skill.Name))
			fingerprint = files[relTarget]
		}

		// Harnesses are already sorted from compatibleHarnesses
		entry := installLockEntry{
			Name:        candidate.Skill.Name,
			Version:     "",
			Commit:      "",
			Fingerprint: fingerprint,
			InstalledAt: time.Now().UTC().Format(time.RFC3339),
			Harnesses:   candidate.Harnesses,
		}
		newLock.Skills = append(newLock.Skills, entry)
	}

	// Preserve entries from the old lock that were skipped (not in new candidates)
	for _, oldEntry := range oldLock.Skills {
		if !newSkillNames[oldEntry.Name] {
			newLock.Skills = append(newLock.Skills, oldEntry)
		}
	}

	// Sort by skill name for deterministic output
	sort.Slice(newLock.Skills, func(i, j int) bool {
		return newLock.Skills[i].Name < newLock.Skills[j].Name
	})

	return newLock
}

func readProjectConfig(path string) (projectConfig, error) {
	lines, err := readLines(path)
	if err != nil {
		return projectConfig{}, err
	}
	var project projectConfig
	var section string
	for i := 0; i < len(lines); i++ {
		key, value, ok := splitYAMLKey(lines[i])
		if !ok {
			continue
		}
		switch key {
		case "name":
			project.Name = unquote(value)
		case "categories", "tags", "harnesses":
			items, next := readYAMLStringList(lines, i, value)
			i = next
			assignProjectList(&project, key, items)
		case "skills":
			section = "skills"
		case "always_include", "never_include":
			if section != "skills" {
				continue
			}
			items, next := readYAMLStringList(lines, i, value)
			i = next
			assignProjectList(&project, key, items)
		}
	}
	if len(project.Harnesses) == 0 {
		project.Harnesses = []string{"claude", "codex", "grok", "antigravity", "gemini", "hermes", "openclaw"}
	}
	return project, nil
}

func assignProjectList(project *projectConfig, key string, items []string) {
	switch key {
	case "categories":
		project.Categories = items
	case "tags":
		project.Tags = items
	case "harnesses":
		project.Harnesses = items
	case "always_include":
		project.AlwaysInclude = items
	case "never_include":
		project.NeverInclude = items
	}
}

func readCatalog(path string) (catalog, error) {
	lines, err := readLines(path)
	if err != nil {
		return catalog{}, err
	}
	var out catalog
	var current *catalogSkill
	var section string
	var tool *toolRequirement
	for i := 0; i < len(lines); i++ {
		raw := stripComment(lines[i])
		if strings.TrimSpace(raw) == "" {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "- name:") {
			if current != nil {
				if tool != nil {
					current.Requirements.Tools = append(current.Requirements.Tools, *tool)
				}
				out.Skills = append(out.Skills, *current)
			}
			current = &catalogSkill{Name: unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")))}
			section = ""
			tool = nil
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := splitYAMLKey(raw)
		if !ok {
			continue
		}
		switch key {
		case "categories", "tags":
			items, next := readYAMLStringList(lines, i, value)
			i = next
			if key == "categories" {
				current.Categories = items
			} else {
				current.Tags = items
			}
		case "compatibility", "requirements":
			section = key
		case "mode":
			if section == "compatibility" {
				current.Compatibility.Mode = unquote(value)
			}
		case "harness":
			if section == "compatibility" {
				current.Compatibility.Harness = unquote(value)
			}
		case "harnesses":
			if section == "compatibility" {
				items, next := readYAMLStringList(lines, i, value)
				i = next
				current.Compatibility.Harnesses = items
			}
		case "tools":
			if section == "requirements" {
				if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
					current.Requirements.Tools = toolRequirementsFromNames(parseInlineList(value))
				} else {
					items, next := readToolRequirements(lines, i)
					i = next
					current.Requirements.Tools = items
				}
			}
		}
	}
	if current != nil {
		if tool != nil {
			current.Requirements.Tools = append(current.Requirements.Tools, *tool)
		}
		out.Skills = append(out.Skills, *current)
	}
	return out, nil
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func splitYAMLKey(line string) (string, string, bool) {
	line = stripComment(line)
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "- ") {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func readYAMLStringList(lines []string, index int, inline string) ([]string, int) {
	if strings.HasPrefix(inline, "[") && strings.HasSuffix(inline, "]") {
		return parseInlineList(inline), index
	}
	var items []string
	baseIndent := indent(lines[index])
	for next := index + 1; next < len(lines); next++ {
		line := stripComment(lines[next])
		if strings.TrimSpace(line) == "" {
			continue
		}
		if indent(line) <= baseIndent || !strings.HasPrefix(strings.TrimSpace(line), "- ") {
			return items, next - 1
		}
		items = append(items, unquote(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))))
	}
	return items, len(lines) - 1
}

func readToolRequirements(lines []string, index int) ([]toolRequirement, int) {
	var tools []toolRequirement
	baseIndent := indent(lines[index])
	var current *toolRequirement
	for next := index + 1; next < len(lines); next++ {
		line := stripComment(lines[next])
		if strings.TrimSpace(line) == "" {
			continue
		}
		if indent(line) <= baseIndent {
			if current != nil {
				tools = append(tools, *current)
			}
			return tools, next - 1
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- name:") {
			if current != nil {
				tools = append(tools, *current)
			}
			current = &toolRequirement{Name: unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")))}
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if current != nil {
				tools = append(tools, *current)
				current = nil
			}
			tools = append(tools, toolRequirement{
				Name:     unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))),
				Required: true,
			})
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := splitYAMLKey(line)
		if ok && key == "required" {
			current.Required = value == "true"
		}
	}
	if current != nil {
		tools = append(tools, *current)
	}
	return tools, len(lines) - 1
}

func parseInlineList(value string) []string {
	value = strings.TrimPrefix(strings.TrimSuffix(value, "]"), "[")
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var items []string
	for _, item := range strings.Split(value, ",") {
		items = append(items, unquote(strings.TrimSpace(item)))
	}
	return items
}

func toolRequirementsFromNames(names []string) []toolRequirement {
	requirements := make([]toolRequirement, 0, len(names))
	for _, name := range names {
		requirements = append(requirements, toolRequirement{Name: name, Required: true})
	}
	return requirements
}

func stripComment(line string) string {
	if before, _, ok := strings.Cut(line, "#"); ok {
		return before
	}
	return line
}

func indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func unquote(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"'`)
}

func missingLockedSkills(lock installLock, catalog catalog) []string {
	catalogNames := map[string]bool{}
	for _, skill := range catalog.Skills {
		catalogNames[skill.Name] = true
	}
	var missing []string
	for _, lockSkill := range lock.Skills {
		if !catalogNames[lockSkill.Name] {
			missing = append(missing, lockSkill.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

func selectInstallCandidates(catalog catalog, project projectConfig, only string) []installCandidate {
	projectCategories := set(project.Categories)
	projectTags := set(project.Tags)
	always := set(project.AlwaysInclude)
	never := set(project.NeverInclude)
	var candidates []installCandidate
	for _, skill := range catalog.Skills {
		if only != "" && skill.Name != only {
			continue
		}
		if never[skill.Name] {
			continue
		}
		reason := ""
		if always[skill.Name] {
			reason = "always included"
		} else if intersects(set(skill.Categories), projectCategories) {
			reason = "category match"
		} else if intersects(set(skill.Tags), projectTags) {
			reason = "tag match"
		} else {
			continue
		}
		harnesses := compatibleHarnesses(skill.Compatibility, project.Harnesses)
		candidates = append(candidates, installCandidate{Skill: skill, Harnesses: harnesses, Reason: reason})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Skill.Name < candidates[j].Skill.Name
	})
	return candidates
}

func compatibleHarnesses(compat compatibility, active []string) []string {
	activeSet := set(active)
	mode := compat.Mode
	if mode == "" {
		mode = "portable"
	}
	var out []string
	switch mode {
	case "exclusive":
		if activeSet[compat.Harness] {
			out = append(out, compat.Harness)
		}
	case "compatible":
		for _, harness := range compat.Harnesses {
			if activeSet[harness] {
				out = append(out, harness)
			}
		}
	default:
		out = append(out, active...)
	}
	sort.Strings(out)
	return out
}

func targetBases(harnesses []string) []string {
	seen := map[string]bool{}
	var bases []string
	for _, harness := range harnesses {
		base, ok := harnessProjectPaths[harness]
		if !ok || seen[base] {
			continue
		}
		seen[base] = true
		bases = append(bases, base)
	}
	sort.Strings(bases)
	return bases
}

func desiredManagedPaths(candidates []installCandidate) map[string]bool {
	desired := map[string]bool{}
	for _, candidate := range candidates {
		for _, targetBase := range targetBases(candidate.Harnesses) {
			relTarget := filepath.ToSlash(filepath.Join(targetBase, candidate.Skill.Name))
			desired[relTarget] = true
		}
	}
	return desired
}

func staleManagedPaths(managed map[string]bool, desired map[string]bool) []string {
	var stale []string
	for rel := range managed {
		if !desired[rel] {
			stale = append(stale, rel)
		}
	}
	sort.Strings(stale)
	return stale
}

func pruneStaleManaged(projectPath string, managed map[string]bool, desired map[string]bool, preserved map[string]bool, files map[string]string, stdout io.Writer) (bool, error) {
	partial := false
	for _, rel := range staleManagedPaths(managed, desired) {
		target := filepath.Join(projectPath, filepath.FromSlash(rel))
		expected := files[rel]
		if expected != "" {
			actual, err := fingerprintDir(target)
			if err == nil && actual != expected {
				delete(managed, rel)
				preserved[rel] = true
				partial = true
				fmt.Fprintf(stdout, "- preserve stale %s (manager-owned copy has local edits)\n", rel)
				continue
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return partial, fmt.Errorf("fingerprint %s: %w", rel, err)
			}
		}
		if err := os.RemoveAll(target); err != nil {
			return partial, fmt.Errorf("remove %s: %w", rel, err)
		}
		pruneEmptyParents(projectPath, filepath.Dir(target))
		delete(managed, rel)
		delete(files, rel)
		fmt.Fprintf(stdout, "- removed stale %s\n", rel)
	}
	return partial, nil
}

func previewStaleManaged(projectPath string, managed map[string]bool, desired map[string]bool, files map[string]string, stdout io.Writer) error {
	for _, rel := range staleManagedPaths(managed, desired) {
		target := filepath.Join(projectPath, filepath.FromSlash(rel))
		expected := files[rel]
		if expected != "" {
			actual, err := fingerprintDir(target)
			if err == nil && actual != expected {
				fmt.Fprintf(stdout, "- preserve stale %s (manager-owned copy has local edits)\n", rel)
				continue
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("fingerprint %s: %w", rel, err)
			}
		}
		fmt.Fprintf(stdout, "- remove stale %s\n", rel)
	}
	return nil
}

func missingRequiredTools(req requirements) []string {
	var missing []string
	for _, tool := range req.Tools {
		if !tool.Required {
			continue
		}
		if _, err := exec.LookPath(tool.Name); err != nil {
			missing = append(missing, tool.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

func validateSkillName(name string) error {
	if name == "" {
		return errors.New("must not be empty")
	}
	if name == "." || name == ".." || filepath.Base(name) != name || filepath.Clean(name) != name {
		return errors.New("must be a single path component")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("contains unsupported character %q", r)
	}
	return nil
}

var (
	errUnmanagedTarget     = errors.New("already exists, unmanaged")
	errLocallyEditedTarget = errors.New("manager-owned copy has local edits")
)

func installSkillCopy(src string, target string, relTarget string, manifest installManifest, managed map[string]bool) (bool, error) {
	if _, err := os.Stat(src); err != nil {
		return false, err
	}
	if _, err := os.Stat(target); err == nil {
		if !managed[relTarget] {
			return false, errUnmanagedTarget
		}
		expected := manifest.Files[relTarget]
		if expected != "" {
			actual, err := fingerprintDir(target)
			if err != nil {
				return false, err
			}
			if actual != expected {
				return false, errLocallyEditedTarget
			}
		}
		if err := os.RemoveAll(target); err != nil {
			return false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := copyDir(src, target); err != nil {
		return false, err
	}
	return true, nil
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func fingerprintDir(path string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		hash.Write(data)
		hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func pruneEmptyParents(projectPath string, dir string) {
	for {
		if dir == projectPath || !strings.HasPrefix(dir, projectPath) {
			return
		}
		err := os.Remove(dir)
		if err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func set(items []string) map[string]bool {
	out := map[string]bool{}
	for _, item := range items {
		out[item] = true
	}
	return out
}

func intersects(left, right map[string]bool) bool {
	for item := range left {
		if right[item] {
			return true
		}
	}
	return false
}

func mapFromSlice(items []string) map[string]bool {
	out := map[string]bool{}
	for _, item := range items {
		out[item] = true
	}
	return out
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func unionSorted(left []string, right []string) []string {
	values := mapFromSlice(left)
	for _, item := range right {
		values[item] = true
	}
	return sortedKeys(values)
}
