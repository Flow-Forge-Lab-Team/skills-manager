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
	"strconv"
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
	noBackup                 bool
}

type lockProblem struct {
	name   string
	kind   string // "missing" or "divergence"
	detail string
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
	Skills []catalogSkill `json:"skills"`
}

type catalogSkill struct {
	Name          string        `json:"name"`
	Summary       string        `json:"summary,omitempty"`
	Categories    []string      `json:"categories,omitempty"`
	Tags          []string      `json:"tags,omitempty"`
	Compatibility compatibility `json:"compatibility"`
	Requirements  requirements  `json:"requirements,omitempty"`
}

type compatibility struct {
	Mode             string                     `json:"mode,omitempty"`
	Harness          string                     `json:"harness,omitempty"`
	Harnesses        []string                   `json:"harnesses,omitempty"`
	Declared         *compatibilityDeclaration  `json:"declared,omitempty"`
	Detected         map[string]detectionResult `json:"detected,omitempty"`
	ExplicitPortable bool                       `json:"explicit_portable,omitempty"`
}

type compatibilityDeclaration struct {
	Mode      string   `json:"mode,omitempty"`
	Harness   string   `json:"harness,omitempty"`
	Harnesses []string `json:"harnesses,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

type detectionResult struct {
	Confidence string   `json:"confidence,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
}

type requirements struct {
	Tools      []toolRequirement `json:"tools,omitempty"`
	MCPServers []mcpRequirement  `json:"mcp_servers,omitempty"`
	Model      modelRequirement  `json:"model,omitempty"`
	Inferred   bool              `json:"inferred,omitempty"`
}

type toolRequirement struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

type mcpRequirement struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

type modelRequirement struct {
	ToolUse string `json:"tool_use,omitempty"`
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
	Name        string
	Version     string
	Commit      string
	Fingerprint string
	InstalledAt string
	Harnesses   []string
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

func runInstall(args []string, realStdout io.Writer, stderr io.Writer, syncMode bool, gf globalFlags) int {
	opts, err := parseInstallOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	// stdout below is the human-text sink: io.Discard when --quiet, real stdout
	// otherwise. JSON output, when requested, is written to realStdout at the end.
	stdout := gf.outWriter(realStdout)

	projectPath, err := absoluteProjectPath(opts.projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "project path: %v\n", err)
		return 3
	}
	opts.projectPath = projectPath

	projectConfigPath := filepath.Join(projectPath, ".skills", "project.yaml")
	if gf.Config != "" {
		projectConfigPath = gf.Config
	}
	project, err := readProjectConfig(projectConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "read project config: %v\n", err)
		return ExitOpError
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

	// Lock contract:
	//
	//   installed.lock is the committed desired install set for this project.
	//   It represents "this is the full set of skills that should be installed
	//   for full reproducibility across machines." It is not a record of what
	//   any individual install run happened to copy.
	//
	// Modes:
	//
	//   1. lock-driven install (lock exists, not sync): candidates come from
	//      the lock; the lock is authoritative. --only filters which entries
	//      get re-evaluated on disk this run; non-matching entries are
	//      preserved unchanged in the rewritten lock. Blocked/incompatible
	//      entries also preserve their old lock entry.
	//
	//   2. bootstrap install (no lock, not sync, no --only): candidates come
	//      from project.yaml matches. The new lock contains the full desired
	//      set, including blocked-this-run skills (with library fingerprint +
	//      compatible harnesses), so a teammate with the tool gets them.
	//      Zero-compatible-harness skills are not added to the lock.
	//
	//   3. surgical install (no lock, not sync, --only): act on the named
	//      skill only; do NOT bootstrap a lock. The user's intent is "install
	//      this one thing", not "freeze a project-wide desired set."
	//
	//   4. sync (with or without --only): always recomputes desired set from
	//      project.yaml and rewrites the lock to match. --only still filters
	//      which entries get touched on disk; the rest of the desired set is
	//      written to the lock from catalog + library.
	//
	// authoritativeLock controls whether buildInstallLock can synthesize
	// entries for blocked/incompatible candidates from the library, vs.
	// only writing entries we actually installed this run. True for
	// bootstrap and sync (modes 2 and 4); false for surgical (mode 3).
	authoritativeLock := !(opts.onlySkill != "" && len(lock.Skills) == 0 && !syncMode)

	var candidates []installCandidate
	var skippedLockedSkills bool
	preserveLockEntries := map[string]bool{}
	if len(lock.Skills) > 0 && !syncMode {
		// Mode 1: lock-driven install.
		var lockProblems []lockProblem

		never := set(project.NeverInclude)
		for _, lockSkill := range lock.Skills {
			if opts.onlySkill != "" && lockSkill.Name != opts.onlySkill {
				preserveLockEntries[lockSkill.Name] = true
				continue
			}
			if never[lockSkill.Name] {
				fmt.Fprintf(stdout, "- %s: skipped, in never_include list\n", lockSkill.Name)
				preserveLockEntries[lockSkill.Name] = true
				continue
			}
			var catalogSkill *catalogSkill
			for i, s := range catalog.Skills {
				if s.Name == lockSkill.Name {
					catalogSkill = &catalog.Skills[i]
					break
				}
			}
			if catalogSkill == nil {
				lockProblems = append(lockProblems, lockProblem{
					name:   lockSkill.Name,
					kind:   "missing",
					detail: "skill in lock but not in catalog",
				})
				if opts.skipMissingLocked {
					skippedLockedSkills = true
					preserveLockEntries[lockSkill.Name] = true
				}
				continue
			}

			if lockSkill.Fingerprint != "" {
				libFP, err := fingerprintDir(filepath.Join(libraryPath, lockSkill.Name))
				if err != nil {
					fmt.Fprintf(stderr, "fingerprint library skill %q: %v\n", lockSkill.Name, err)
					return 3
				}
				if libFP != lockSkill.Fingerprint {
					prefix1 := lockSkill.Fingerprint
					if len(prefix1) > 12 {
						prefix1 = prefix1[:12]
					}
					prefix2 := libFP
					if len(prefix2) > 12 {
						prefix2 = prefix2[:12]
					}
					lockProblems = append(lockProblems, lockProblem{
						name:   lockSkill.Name,
						kind:   "divergence",
						detail: fmt.Sprintf("lock=%s, library=%s", prefix1, prefix2),
					})
					if opts.skipMissingLocked {
						skippedLockedSkills = true
						preserveLockEntries[lockSkill.Name] = true
					}
					continue
				}
			} else {
				fmt.Fprintf(stderr, "lock entry for %s has no fingerprint; accepting library content as-is\n", lockSkill.Name)
			}

			// Locked harnesses intersected with active project harnesses.
			// (Legacy locks without harness data fall back to catalog.)
			var harnesses []string
			if len(lockSkill.Harnesses) > 0 {
				projectHarnessSet := set(project.Harnesses)
				for _, h := range lockSkill.Harnesses {
					if projectHarnessSet[h] {
						harnesses = append(harnesses, h)
					}
				}
				if len(harnesses) == 0 {
					fmt.Fprintf(stdout, "- %s: skipped, no locked harnesses are active in project.yaml\n", lockSkill.Name)
					skippedLockedSkills = true
					preserveLockEntries[lockSkill.Name] = true
					continue
				}
			} else {
				harnesses = compatibleHarnesses(catalogSkill.Compatibility, project.Harnesses)
			}

			candidates = append(candidates, installCandidate{
				Skill:     *catalogSkill,
				Harnesses: harnesses,
				Reason:    "locked skill",
			})
		}

		if len(lockProblems) > 0 && !opts.skipMissingLocked {
			fmt.Fprintf(stderr, "problems with skills in .skills/installed.lock:\n")
			for _, prob := range lockProblems {
				if prob.kind == "missing" {
					fmt.Fprintf(stderr, "  - %s: missing from library\n", prob.name)
				} else {
					fmt.Fprintf(stderr, "  - %s: library content differs from lock (%s)\n", prob.name, prob.detail)
				}
			}
			fmt.Fprintf(stderr, "suggest:\n")
			fmt.Fprintf(stderr, "  - run: skills-manager sync-library\n")
			fmt.Fprintf(stderr, "  - or: skills-manager install --skip-missing-locked\n")
			return 3
		}
	} else {
		// Modes 2, 3, 4: candidates derive from project.yaml match.
		candidates = selectInstallCandidates(catalog, project, opts.onlySkill)
	}

	// fullDesired is the unfiltered project.yaml-match set; used as the lock
	// content in modes 2 (bootstrap) and 4 (sync) so --only or blocked tools
	// don't silently shrink the committed desired set.
	var fullDesired []installCandidate
	if authoritativeLock && (len(lock.Skills) == 0 || syncMode) {
		if opts.onlySkill != "" {
			fullDesired = selectInstallCandidates(catalog, project, "")
		} else {
			fullDesired = candidates
		}
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
	var blockedSkills []string
	var skippedSkills []string
	installedSkillSet := map[string]bool{}
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

	// For stale-pruning during sync we want every desired skill's target paths,
	// not just the ones we acted on this run (--only would otherwise prune
	// untouched managed skills).
	desiredSource := candidates
	if len(fullDesired) > 0 {
		desiredSource = fullDesired
	}
	desired := desiredManagedPaths(desiredSource)
	// The lock file is always desired when we're installing; it will be written/updated
	if len(lock.Skills) > 0 || len(candidates) > 0 {
		desired[".skills/installed.lock"] = true
	}
	if syncMode && opts.dryRun {
		if err := previewStaleManaged(projectPath, managed, desired, files, stdout); err != nil {
			fmt.Fprintf(stderr, "preview stale installs: %v\n", err)
			return 3
		}
	}

	partial := false
	skippedCandidates := map[string]bool{}
	for _, candidate := range candidates {
		if len(candidate.Harnesses) == 0 {
			fmt.Fprintf(stdout, "- %s: skipped, no compatible active harnesses\n", candidate.Skill.Name)
			skippedCandidates[candidate.Skill.Name] = true
			skippedSkills = append(skippedSkills, candidate.Skill.Name)
			continue
		}

		missing := missingRequiredTools(candidate.Skill.Requirements)
		missingMCP := missingRequiredMCPServers(candidate.Skill.Requirements)
		missingModel := missingModelCapabilities(candidate.Skill.Requirements)
		candidate.Missing = missing

		allMissing := append(append(missing, missingMCP...), missingModel...)
		if len(allMissing) > 0 && !opts.allowMissingRequirements {
			blocked++
			var parts []string
			if len(missing) > 0 {
				parts = append(parts, "tools="+strings.Join(missing, ","))
			}
			if len(missingMCP) > 0 {
				parts = append(parts, "mcp_servers="+strings.Join(missingMCP, ","))
			}
			if len(missingModel) > 0 {
				parts = append(parts, "model="+strings.Join(missingModel, ","))
			}
			fmt.Fprintf(stdout, "- %s: blocked, missing required: %s\n", candidate.Skill.Name, strings.Join(parts, ", "))
			skippedCandidates[candidate.Skill.Name] = true
			blockedSkills = append(blockedSkills, candidate.Skill.Name)
			continue
		}
		if len(allMissing) > 0 {
			var parts []string
			if len(missing) > 0 {
				parts = append(parts, "tools="+strings.Join(missing, ","))
			}
			if len(missingMCP) > 0 {
				parts = append(parts, "mcp_servers="+strings.Join(missingMCP, ","))
			}
			if len(missingModel) > 0 {
				parts = append(parts, "model="+strings.Join(missingModel, ","))
			}
			fmt.Fprintf(stdout, "- %s: warning, installing despite missing required: %s\n", candidate.Skill.Name, strings.Join(parts, ", "))
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
					// Drop from managed so the lock writer doesn't record
					// the library fingerprint for a preserved local-edited copy.
					delete(managed, relTarget)
					delete(files, relTarget)
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
				installedSkillSet[candidate.Skill.Name] = true
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

		// Build and write installed.lock before saving manifest, so on a
		// failure the manifest doesn't claim a lock we didn't write.
		//
		// Lock-content shape depends on the mode:
		//   - mode 1 (lock-driven): lockInstalled is just candidates that ran;
		//     skipped/blocked entries get their old lock entry preserved via
		//     preserveLockEntries. The lock writer reuses managed[] to decide
		//     which harnesses count as "installed" for fingerprint advance.
		//   - mode 2 (bootstrap) + mode 4 (sync): the lock content is the full
		//     desired set from project.yaml (fullDesired). Entries for skills
		//     blocked this run get library fingerprint + compatible harnesses
		//     synthesized — the lock is the desired set, not the action log.
		//   - mode 3 (surgical --only with no existing lock): skip writing the
		//     lock entirely; this run is not authoritative for the project.
		var lockSkipped map[string]bool
		var lockSource []installCandidate
		actedOn := map[string]bool{}
		for _, c := range candidates {
			actedOn[c.Skill.Name] = true
		}
		if authoritativeLock && (len(lock.Skills) == 0 || syncMode) {
			lockSource = fullDesired
			lockSkipped = map[string]bool{}
			for name := range skippedCandidates {
				lockSkipped[name] = true
			}
		} else if authoritativeLock {
			// Mode 1.
			lockSource = candidates
			lockSkipped = skippedCandidates
			for name := range skippedCandidates {
				preserveLockEntries[name] = true
			}
		}

		writeLock := authoritativeLock && (len(lock.Skills) > 0 || len(lockSource) > 0)
		if writeLock {
			newLock, err := buildInstallLock(lockSource, lockSkipped, actedOn, libraryPath, lock, managed, preserveLockEntries)
			if err != nil {
				fmt.Fprintf(stderr, "build lock: %v\n", err)
				return 3
			}
			if err := writeInstallLock(lockPath, newLock); err != nil {
				fmt.Fprintf(stderr, "write lock: %v\n", err)
				return 3
			}
			managed[".skills/installed.lock"] = true
			lockFP, err := fingerprintDir(lockPath)
			if err != nil {
				fmt.Fprintf(stderr, "fingerprint lock: %v\n", err)
				return 3
			}
			files[".skills/installed.lock"] = lockFP
		}

		// Save manifest with the lock file (if any) now recorded as managed
		if err := saveInstallManifest(manifestPath, manifest, managed, preserved, files); err != nil {
			fmt.Fprintf(stderr, "write manifest: %v\n", err)
			return 3
		}
	}

	if skippedLockedSkills {
		partial = true
	}

	exit := ExitSuccess
	switch {
	case blocked > 0 && installed > 0:
		exit = ExitPartial
	case blocked > 0:
		exit = ExitOpError
	case partial:
		exit = ExitPartial
	}

	if gf.JSON {
		installedNames := sortedKeys(installedSkillSet)
		if installedNames == nil {
			installedNames = []string{}
		}
		if blockedSkills == nil {
			blockedSkills = []string{}
		}
		if skippedSkills == nil {
			skippedSkills = []string{}
		}
		mode := "install"
		if syncMode {
			mode = "sync"
		}
		result := map[string]interface{}{
			"mode":      mode,
			"project":   projectPath,
			"dry_run":   opts.dryRun,
			"installed": installedNames,
			"blocked":   blockedSkills,
			"skipped":   skippedSkills,
			"partial":   partial,
			"exit_code": exit,
		}
		if err := writeJSON(realStdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	}

	return exit
}

func runUninstall(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseUninstallOptions(args)
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
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	path := manifestPath(home, projectPath)
	manifest, err := readManifest(path)
	if err != nil {
		fmt.Fprintf(stderr, "read manifest: %v\n", err)
		return ExitOpError
	}
	if manifest.ProjectPath == "" {
		fmt.Fprintln(stderr, "no manifest found for project")
		return ExitOpError
	}
	if manifest.ProjectPath != projectPath {
		fmt.Fprintf(stderr, "manifest belongs to %s, not %s\n", manifest.ProjectPath, projectPath)
		return ExitOpError
	}

	type uninstallPlanEntry struct {
		rel      string
		preserve bool
		reason   string
	}
	var plan []uninstallPlanEntry
	for _, rel := range manifest.ManagedPaths {
		target := filepath.Join(projectPath, filepath.FromSlash(rel))
		expected := manifest.Files[rel]
		entry := uninstallPlanEntry{rel: rel}
		if expected != "" {
			actual, ferr := fingerprintDir(target)
			if ferr == nil && actual != expected {
				entry.preserve = true
				entry.reason = "manager-owned copy has local edits"
			} else if ferr != nil && !errors.Is(ferr, os.ErrNotExist) {
				fmt.Fprintf(stderr, "fingerprint %s: %v\n", rel, ferr)
				return ExitOpError
			}
		}
		plan = append(plan, entry)
	}

	if opts.dryRun {
		fmt.Fprintln(stdout, "Uninstall preview (dry-run):")
	} else {
		fmt.Fprintln(stdout, "Uninstall preview:")
	}
	for _, e := range plan {
		if e.preserve {
			fmt.Fprintf(stdout, "- preserve %s (%s)\n", e.rel, e.reason)
		} else {
			fmt.Fprintf(stdout, "- remove %s\n", e.rel)
		}
	}
	for _, rel := range manifest.PreservedPaths {
		fmt.Fprintf(stdout, "- preserve %s (already preserved)\n", rel)
	}
	if opts.dryRun {
		return ExitSuccess
	}
	if !opts.confirm {
		fmt.Fprintln(stderr, "refusing to uninstall without --confirm")
		return ExitUsageError
	}

	var backupDir string
	if !opts.noBackup {
		needBackup := false
		for _, e := range plan {
			if !e.preserve {
				needBackup = true
				break
			}
		}
		if needBackup {
			backupDir = filepath.Join(home, "backups", projectSlug(projectPath), time.Now().UTC().Format("20060102T150405Z"))
			if err := os.MkdirAll(backupDir, 0o755); err != nil {
				fmt.Fprintf(stderr, "create backup dir: %v\n", err)
				return ExitOpError
			}
			fmt.Fprintf(stdout, "Backing up removed paths to %s\n", backupDir)
		}
	}

	remaining := map[string]bool{}
	var removed []string
	for _, e := range plan {
		rel := e.rel
		target := filepath.Join(projectPath, filepath.FromSlash(rel))
		if e.preserve {
			fmt.Fprintf(stdout, "- preserved %s (%s)\n", rel, e.reason)
			remaining[rel] = true
			continue
		}
		if backupDir != "" {
			info, statErr := os.Stat(target)
			if statErr == nil {
				dst := filepath.Join(backupDir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					fmt.Fprintf(stderr, "backup %s: %v\n", rel, err)
					return ExitOpError
				}
				if info.IsDir() {
					if err := copyDir(target, dst); err != nil {
						fmt.Fprintf(stderr, "backup %s: %v\n", rel, err)
						return ExitOpError
					}
				} else {
					if err := copyFile(target, dst, info.Mode()); err != nil {
						fmt.Fprintf(stderr, "backup %s: %v\n", rel, err)
						return ExitOpError
					}
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				fmt.Fprintf(stderr, "stat %s: %v\n", rel, statErr)
				return ExitOpError
			}
		}
		if err := os.RemoveAll(target); err != nil {
			fmt.Fprintf(stderr, "remove %s: %v\n", rel, err)
			return ExitOpError
		}
		pruneEmptyParents(projectPath, filepath.Dir(target))
		delete(manifest.Files, rel)
		removed = append(removed, rel)
		fmt.Fprintf(stdout, "- removed %s\n", rel)
	}

	exit := ExitSuccess
	if len(remaining) > 0 {
		manifest.ManagedPaths = sortedKeys(remaining)
		manifest.PreservedPaths = unionSorted(manifest.PreservedPaths, manifest.ManagedPaths)
		if err := writeManifest(path, manifest); err != nil {
			fmt.Fprintf(stderr, "write manifest: %v\n", err)
			return ExitOpError
		}
		exit = ExitPartial
	} else {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "remove manifest: %v\n", err)
			return ExitOpError
		}
	}

	if gf.JSON {
		if removed == nil {
			removed = []string{}
		}
		preservedPaths := sortedKeys(remaining)
		if preservedPaths == nil {
			preservedPaths = []string{}
		}
		result := map[string]interface{}{
			"project":   projectPath,
			"removed":   removed,
			"preserved": preservedPaths,
			"exit_code": exit,
		}
		if err := writeJSON(realStdout, result); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	}
	return exit
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
		case "--dry-run":
			opts.dryRun = true
		case "--no-backup":
			opts.noBackup = true
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
				v := unquote(value)
				if v != "~" {
					currentSkill.Version = v
				}
			case "commit":
				v := unquote(value)
				if v != "~" {
					currentSkill.Commit = v
				}
			case "fingerprint":
				fp := unquote(value)
				if fp != "~" {
					currentSkill.Fingerprint = fp
				}
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

// buildInstallLock produces the lock content to write after this run.
//
//	desired: the full set of skills that should appear in the new lock.
//	  - mode 1 (lock-driven): only the candidates we re-evaluated this run.
//	    preserveOld carries the skills we left alone (--only-excluded,
//	    never_include, blocked, etc.)
//	  - modes 2/4 (bootstrap/sync): the full project.yaml-matched set.
//	skipped: subset of desired we did NOT install this run (blocked tools,
//	  no compatible harness). For these we either preserve the old lock
//	  entry (honest: bytes didn't change) or synthesize from library+catalog
//	  if there's no old entry to preserve. Synthesizing with library
//	  fingerprint is correct because the lock describes the desired set;
//	  a teammate with the tool will install the library content.
//	managed: target paths the manager currently owns. Used to decide which
//	  of a skill's harnesses count as "managed" — only those advance to the
//	  current library fingerprint.
//	preserveOld: names whose old lock entry should survive unchanged even
//	  if the skill isn't in desired (--only excludes, never_include, etc.).
func buildInstallLock(desired []installCandidate, skipped map[string]bool, actedOn map[string]bool, libraryPath string, oldLock installLock, managed map[string]bool, preserveOld map[string]bool) (installLock, error) {
	newLock := installLock{
		Version:     1,
		GeneratedBy: fmt.Sprintf("skills-manager %s", Version),
		Skills:      []installLockEntry{},
	}
	now := time.Now().UTC().Format(time.RFC3339)

	oldByName := map[string]installLockEntry{}
	for _, e := range oldLock.Skills {
		oldByName[e.Name] = e
	}

	written := map[string]bool{}

	for _, candidate := range desired {
		name := candidate.Skill.Name
		if skipped[name] {
			// Bytes didn't change on disk for this skill this run.
			if old, ok := oldByName[name]; ok {
				newLock.Skills = append(newLock.Skills, old)
				written[name] = true
				continue
			}
			// First-time bootstrap, blocked or no-harness this run.
			// Skip lock entries that have no harnesses (incompatible) —
			// nothing to install anywhere.
			if len(candidate.Harnesses) == 0 {
				continue
			}
			libFP, err := fingerprintDir(filepath.Join(libraryPath, name))
			if err != nil {
				return newLock, fmt.Errorf("fingerprint library skill %q: %w", name, err)
			}
			newLock.Skills = append(newLock.Skills, installLockEntry{
				Name:        name,
				Fingerprint: libFP,
				InstalledAt: now,
				Harnesses:   candidate.Harnesses,
			})
			written[name] = true
			continue
		}

		libFP, err := fingerprintDir(filepath.Join(libraryPath, name))
		if err != nil {
			return newLock, fmt.Errorf("fingerprint library skill %q: %w", name, err)
		}

		var managedHarnesses []string
		for _, harness := range candidate.Harnesses {
			targetBase, ok := harnessProjectPaths[harness]
			if !ok {
				continue
			}
			relTarget := filepath.ToSlash(filepath.Join(targetBase, name))
			if managed[relTarget] {
				managedHarnesses = append(managedHarnesses, harness)
			}
		}

		if len(managedHarnesses) > 0 {
			old, hadOld := oldByName[name]
			installedAt := now
			if hadOld && old.Fingerprint == libFP && stringSlicesEqual(old.Harnesses, managedHarnesses) {
				installedAt = old.InstalledAt
			}
			newLock.Skills = append(newLock.Skills, installLockEntry{
				Name:        name,
				Fingerprint: libFP,
				InstalledAt: installedAt,
				Harnesses:   managedHarnesses,
			})
			written[name] = true
			continue
		}

		// All target harnesses were preserved-as-unmanaged (existing
		// committed copies). Don't silently drop the locked entry.
		if old, ok := oldByName[name]; ok {
			newLock.Skills = append(newLock.Skills, old)
			written[name] = true
		}
	}

	// Desired skills we didn't act on this run (filtered out by --only in
	// modes 2 or 4): keep the old entry if any, else synthesize from library
	// + catalog-compatible harnesses. This is what makes the bootstrap lock
	// represent the full project.yaml-matched set even when bootstrapped via
	// `sync --only one-thing`.
	//
	// We intentionally skip skills that WERE acted on but ended up with no
	// managed harnesses (target preserved as unmanaged / locally edited
	// without an old lock entry). Those represent a real conflict between
	// project.yaml's desired set and the user's local override; we respect
	// the local override by leaving them out of the lock.
	for _, c := range desired {
		if written[c.Skill.Name] {
			continue
		}
		if actedOn[c.Skill.Name] {
			continue
		}
		if old, ok := oldByName[c.Skill.Name]; ok {
			newLock.Skills = append(newLock.Skills, old)
			written[c.Skill.Name] = true
			continue
		}
		if len(c.Harnesses) == 0 {
			continue
		}
		libFP, err := fingerprintDir(filepath.Join(libraryPath, c.Skill.Name))
		if err != nil {
			return newLock, fmt.Errorf("fingerprint library skill %q: %w", c.Skill.Name, err)
		}
		newLock.Skills = append(newLock.Skills, installLockEntry{
			Name:        c.Skill.Name,
			Fingerprint: libFP,
			InstalledAt: now,
			Harnesses:   c.Harnesses,
		})
		written[c.Skill.Name] = true
	}

	for _, oldEntry := range oldLock.Skills {
		if written[oldEntry.Name] {
			continue
		}
		if preserveOld[oldEntry.Name] {
			newLock.Skills = append(newLock.Skills, oldEntry)
		}
	}

	sort.Slice(newLock.Skills, func(i, j int) bool {
		return newLock.Skills[i].Name < newLock.Skills[j].Name
	})

	if oldLock.GeneratedAt != "" && lockEntriesEqual(newLock.Skills, oldLock.Skills) {
		newLock.GeneratedAt = oldLock.GeneratedAt
	} else {
		newLock.GeneratedAt = now
	}

	return newLock, nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lockEntriesEqual(a, b []installLockEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Version != b[i].Version ||
			a[i].Commit != b[i].Commit || a[i].Fingerprint != b[i].Fingerprint ||
			a[i].InstalledAt != b[i].InstalledAt ||
			!stringSlicesEqual(a[i].Harnesses, b[i].Harnesses) {
			return false
		}
	}
	return true
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
	var skillBaseIndent int = -1
	for i := 0; i < len(lines); i++ {
		raw := stripComment(lines[i])
		if strings.TrimSpace(raw) == "" {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		lineIndent := indent(raw)

		// A leading "- name:" at the same or lower indent as the first skill we saw
		// is the start of a new top-level catalog skill. This is more robust than
		// checking section == "" (which can be left non-empty after processing
		// a skill's requirements/compatibility block).
		isTopLevelSkillStart := strings.HasPrefix(trimmed, "- name:") &&
			(skillBaseIndent == -1 || lineIndent <= skillBaseIndent)

		if isTopLevelSkillStart {
			if current != nil {
				if tool != nil {
					current.Requirements.Tools = append(current.Requirements.Tools, *tool)
				}
				out.Skills = append(out.Skills, *current)
			}
			current = &catalogSkill{Name: unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")))}
			section = ""
			tool = nil
			if skillBaseIndent == -1 {
				skillBaseIndent = lineIndent
			}
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
		case "summary":
			current.Summary = unquote(value)
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
			if section == "requirements" || section == "requirements:model" {
				section = "requirements"
				if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
					current.Requirements.Tools = toolRequirementsFromNames(parseInlineList(value))
				} else {
					items, next := readToolRequirements(lines, i)
					i = next
					current.Requirements.Tools = items
				}
			}
		case "mcp_servers":
			if section == "requirements" || section == "requirements:model" {
				section = "requirements"
				if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
					current.Requirements.MCPServers = mcpRequirementsFromNames(parseInlineList(value))
				} else {
					items, next := readMCPServerRequirements(lines, i)
					i = next
					current.Requirements.MCPServers = items
				}
			}
		case "model":
			if section == "requirements" {
				section = "requirements:model"
				// Support the documented inline form used in catalog entries:
				//   model: {tool_use: required}
				// Previously this dropped the value on the same line.
				if strings.Contains(value, "tool_use") {
					parts := strings.SplitN(value, "tool_use:", 2)
					if len(parts) == 2 {
						v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), "}"))
						current.Requirements.Model.ToolUse = unquote(v)
					}
				}
			}
		case "tool_use":
			if section == "requirements:model" {
				current.Requirements.Model.ToolUse = unquote(value)
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

func readMCPServerRequirements(lines []string, index int) ([]mcpRequirement, int) {
	var servers []mcpRequirement
	baseIndent := indent(lines[index])
	var current *mcpRequirement
	for next := index + 1; next < len(lines); next++ {
		line := stripComment(lines[next])
		if strings.TrimSpace(line) == "" {
			continue
		}
		if indent(line) <= baseIndent {
			if current != nil {
				servers = append(servers, *current)
			}
			return servers, next - 1
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- name:") {
			if current != nil {
				servers = append(servers, *current)
			}
			current = &mcpRequirement{Name: unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:")))}
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if current != nil {
				servers = append(servers, *current)
				current = nil
			}
			servers = append(servers, mcpRequirement{
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
		servers = append(servers, *current)
	}
	return servers, len(lines) - 1
}

func mcpRequirementsFromNames(names []string) []mcpRequirement {
	requirements := make([]mcpRequirement, 0, len(names))
	for _, name := range names {
		requirements = append(requirements, mcpRequirement{Name: name, Required: true})
	}
	return requirements
}

func stripComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(line) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '#' {
			return line[:i]
		}
	}
	return line
}

func indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func unquote(value string) string {
	v := strings.TrimSpace(value)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if unquoted, err := strconv.Unquote(v); err == nil {
			return unquoted
		}
	}
	return strings.Trim(v, `"'`)
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

func missingRequiredMCPServers(req requirements) []string {
	var missing []string
	for _, server := range req.MCPServers {
		if !server.Required {
			continue
		}
		// Stub MCP server presence check:
		// - Env var SKILLS_MANAGER_MCP_<NAME_UPPERCASE>=available opts in (for testing/dev)
		// - Config file ~/.skills-manager/mcp/<name>.yaml presence check (future)
		// - Default: missing (blocks install unless --allow-missing-requirements)
		envVar := "SKILLS_MANAGER_MCP_" + strings.ToUpper(strings.ReplaceAll(server.Name, "-", "_"))
		if os.Getenv(envVar) == "available" {
			continue
		}
		missing = append(missing, server.Name)
	}
	sort.Strings(missing)
	return missing
}

func missingModelCapabilities(req requirements) []string {
	var missing []string
	if req.Model.ToolUse == "required" {
		// Stub model capability check:
		// - Env var SKILLS_MANAGER_MODEL_TOOL_USE=available opts in (for testing/dev)
		// - Default: missing (blocks install unless --allow-missing-requirements)
		if os.Getenv("SKILLS_MANAGER_MODEL_TOOL_USE") != "available" {
			missing = append(missing, "tool_use")
		}
	}
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
		// Skip symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
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
		// Skip symlinks
		if entry.Type()&os.ModeSymlink != 0 {
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
