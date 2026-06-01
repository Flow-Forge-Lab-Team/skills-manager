package cli

import (
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

	"gopkg.in/yaml.v3"
)

type machinesFile struct {
	Version  int                     `json:"version" yaml:"version"`
	Machines map[string]machineEntry `json:"machines" yaml:"machines"`
}

type machineEntry struct {
	LastSynced string                  `json:"last_synced,omitempty" yaml:"last_synced,omitempty"`
	LastCommit string                  `json:"last_commit,omitempty" yaml:"last_commit,omitempty"`
	Inventory  machineInventorySummary `json:"inventory,omitempty" yaml:"inventory,omitempty"`
}

type machineInventorySummary struct {
	LastScan           string `json:"last_scan,omitempty" yaml:"last_scan,omitempty"`
	SnapshotPath       string `json:"snapshot_path,omitempty" yaml:"snapshot_path,omitempty"`
	InventoryDigest    string `json:"inventory_digest,omitempty" yaml:"inventory_digest,omitempty"`
	ToolsFound         int    `json:"tools_found,omitempty" yaml:"tools_found,omitempty"`
	GlobalSkills       int    `json:"global_skills,omitempty" yaml:"global_skills,omitempty"`
	ProjectLocalSkills int    `json:"project_local_skills,omitempty" yaml:"project_local_skills,omitempty"`
	DriftGroups        int    `json:"drift_groups,omitempty" yaml:"drift_groups,omitempty"`
}

type machineInventorySnapshot struct {
	SchemaVersion int                       `json:"schema_version"`
	Machine       string                    `json:"machine"`
	GeneratedAt   string                    `json:"generated_at"`
	Summary       machineInventorySummary   `json:"summary"`
	Installations []machineInventoryInstall `json:"installations"`
	Tools         []machineInventoryTool    `json:"tools"`
	Projects      []machineInventoryProject `json:"projects,omitempty"`
}

type machineInventoryInstall struct {
	SkillName     string `json:"skill_name"`
	ToolID        string `json:"tool_id"`
	Scope         string `json:"scope"`
	ProjectID     string `json:"project_id,omitempty"`
	SourcePath    string `json:"source_path"`
	ContentSHA256 string `json:"content_sha256"`
	Managed       bool   `json:"managed"`
	Ownership     string `json:"ownership"`
}

type machineInventoryTool struct {
	ToolID   string `json:"tool_id"`
	Detected bool   `json:"detected"`
}

type machineInventoryProject struct {
	ProjectID string `json:"project_id"`
	RootPath  string `json:"root_path"`
}

func runInitLibrary(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	remote, localOnly, err := parseInitLibraryOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	if remote != "" && localOnly {
		fmt.Fprintln(stderr, "init-library accepts either --remote or --local-only, not both")
		return ExitUsageError
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	library, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "create library: %v\n", err)
		return ExitOpError
	}
	if err := ensureLibraryFiles(library); err != nil {
		fmt.Fprintf(stderr, "initialize library files: %v\n", err)
		return ExitOpError
	}
	if err := ensureGitRepo(library); err != nil {
		fmt.Fprintf(stderr, "initialize git repo: %v\n", err)
		return ExitOpError
	}
	if remote != "" {
		if err := setGitRemote(library, remote); err != nil {
			fmt.Fprintf(stderr, "set remote: %v\n", err)
			return ExitOpError
		}
	}
	if err := commitLibraryIfDirty(library, "Initialize skills library"); err != nil {
		fmt.Fprintf(stderr, "commit library: %v\n", err)
		return ExitOpError
	}
	if err := updateMachine(library); err != nil {
		fmt.Fprintf(stderr, "update machines: %v\n", err)
		return ExitOpError
	}
	if err := commitLibraryIfDirty(library, "Update machine sync status"); err != nil {
		fmt.Fprintf(stderr, "commit machine sync status: %v\n", err)
		return ExitOpError
	}
	if remote != "" {
		if _, err := runGit(library, "push", "-u", "origin", "main"); err != nil {
			fmt.Fprintf(stderr, "push library: %v\n", err)
			return ExitOpError
		}
	}

	if gf.JSON {
		if err := writeJSON(realStdout, map[string]interface{}{
			"library": library,
			"remote":  remote,
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}
	stdout := gf.outWriter(realStdout)
	fmt.Fprintf(stdout, "Initialized library at %s\n", library)
	if remote != "" {
		fmt.Fprintf(stdout, "Remote: %s\n", remote)
	}
	return ExitSuccess
}

func runJoin(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: skills-manager join <remote>")
		return ExitUsageError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	library := filepath.Join(home, "library")
	empty, err := dirMissingOrEmpty(library)
	if err != nil {
		fmt.Fprintf(stderr, "stat library: %v\n", err)
		return ExitOpError
	}
	if !empty {
		fmt.Fprintf(stderr, "%s already exists and is not empty\n", library)
		return ExitUsageError
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		fmt.Fprintf(stderr, "create manager home: %v\n", err)
		return ExitOpError
	}
	if _, err := runCmd("", "git", "clone", args[0], library); err != nil {
		fmt.Fprintf(stderr, "clone library: %v\n", err)
		return ExitOpError
	}
	if _, err := runGit(library, "checkout", "-B", "main", "origin/main"); err != nil {
		fmt.Fprintf(stderr, "checkout library main: %v\n", err)
		return ExitOpError
	}
	if err := ensureLibraryFiles(library); err != nil {
		fmt.Fprintf(stderr, "initialize library files: %v\n", err)
		return ExitOpError
	}
	if err := commitLibraryIfDirty(library, "Initialize library files"); err != nil {
		fmt.Fprintf(stderr, "commit library files: %v\n", err)
		return ExitOpError
	}
	if err := updateMachine(library); err != nil {
		fmt.Fprintf(stderr, "update machines: %v\n", err)
		return ExitOpError
	}
	if err := commitLibraryIfDirty(library, "Register machine"); err != nil {
		fmt.Fprintf(stderr, "commit library: %v\n", err)
		return ExitOpError
	}
	if _, err := runGit(library, "push", "origin", "HEAD:main"); err != nil {
		fmt.Fprintf(stderr, "push machine registration: %v\n", err)
		return ExitOpError
	}

	if gf.JSON {
		if err := writeJSON(realStdout, map[string]interface{}{
			"library": library,
			"remote":  args[0],
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}
	fmt.Fprintf(gf.outWriter(realStdout), "Joined library at %s\n", library)
	return ExitSuccess
}

func runSyncLibrary(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	mode, err := parseSyncLibraryOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	library := filepath.Join(home, "library")
	if _, err := os.Stat(filepath.Join(library, ".git")); err != nil {
		fmt.Fprintf(stderr, "library is not a git repository; run skills-manager init-library or join first\n")
		return ExitOpError
	}

	stdout := gf.outWriter(realStdout)
	switch mode {
	case "status":
		if err := fetchLibrary(library); err != nil {
			fmt.Fprintf(stderr, "fetch library status: %v\n", err)
			return ExitOpError
		}
		status, err := runGit(library, "status", "--short", "--branch")
		if err != nil {
			fmt.Fprintf(stderr, "git status: %v\n", err)
			return ExitOpError
		}
		if gf.JSON {
			if err := writeJSON(realStdout, map[string]interface{}{
				"library": library,
				"mode":    "status",
				"status":  status,
			}); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
			return ExitSuccess
		}
		fmt.Fprint(stdout, status)
		return ExitSuccess
	case "push":
		if err := commitLibraryIfDirty(library, "Update skills library"); err != nil {
			fmt.Fprintf(stderr, "commit library: %v\n", err)
			return ExitOpError
		}
		if err := pullLibrary(library); err != nil {
			fmt.Fprintf(stderr, "pull library before push: %v\n", err)
			return ExitOpError
		}
		if err := updateMachine(library); err != nil {
			fmt.Fprintf(stderr, "update machines: %v\n", err)
			return ExitOpError
		}
		if err := commitLibraryIfDirty(library, "Update machine sync status"); err != nil {
			fmt.Fprintf(stderr, "commit library: %v\n", err)
			return ExitOpError
		}
		if _, err := runGit(library, "push", "origin", "HEAD:main"); err != nil {
			fmt.Fprintf(stderr, "push library: %v\n", err)
			return ExitOpError
		}
		if gf.JSON {
			if err := writeJSON(realStdout, map[string]interface{}{
				"library": library,
				"mode":    "push",
				"pushed":  true,
			}); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
			return ExitSuccess
		}
		fmt.Fprintln(stdout, "Pushed library")
		return ExitSuccess
	default:
		if err := pullLibrary(library); err != nil {
			fmt.Fprintf(stderr, "pull library: %v\n", err)
			return ExitOpError
		}
		if err := updateMachine(library); err != nil {
			fmt.Fprintf(stderr, "update machines: %v\n", err)
			return ExitOpError
		}
		if err := commitLibraryIfDirty(library, "Update machine sync status"); err != nil {
			fmt.Fprintf(stderr, "commit library: %v\n", err)
			return ExitOpError
		}
		if err := pushLibraryIfRemote(library); err != nil {
			fmt.Fprintf(stderr, "push machine sync status: %v\n", err)
			return ExitOpError
		}
		if gf.JSON {
			if err := writeJSON(realStdout, map[string]interface{}{
				"library": library,
				"mode":    "pull",
				"pulled":  true,
			}); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
			return ExitSuccess
		}
		fmt.Fprintln(stdout, "Pulled library")
		return ExitSuccess
	}
}

func fetchLibrary(library string) error {
	if !hasGitRemote(library) {
		return nil
	}
	_, err := runGit(library, "fetch", "origin", "main")
	return err
}

func pullLibrary(library string) error {
	if !hasGitRemote(library) {
		return nil
	}
	if _, err := runGit(library, "pull", "--no-rebase", "origin", "main"); err != nil {
		if mergeErr := resolveGeneratedLibraryConflicts(library); mergeErr != nil {
			return err
		}
	}
	return nil
}

func pushLibraryIfRemote(library string) error {
	if !hasGitRemote(library) {
		return nil
	}
	_, err := runGit(library, "push", "origin", "HEAD:main")
	return err
}

func hasGitRemote(library string) bool {
	_, err := runGit(library, "remote", "get-url", "origin")
	return err == nil
}

func runMachines(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: skills-manager machines")
		return ExitUsageError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	library := filepath.Join(home, "library")
	machines, err := readMachines(filepath.Join(library, ".machines.yaml"))
	if err != nil {
		fmt.Fprintf(stderr, "read machines: %v\n", err)
		return ExitOpError
	}
	current := currentMachineName()
	names := make([]string, 0, len(machines.Machines))
	for name := range machines.Machines {
		names = append(names, name)
	}
	sort.Strings(names)
	if gf.JSON {
		if err := writeJSON(realStdout, machines); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}
	stdout := gf.outWriter(realStdout)
	for _, name := range names {
		entry := machines.Machines[name]
		label := name
		if name == current {
			label += " (this)"
		}
		inventory := ""
		if entry.Inventory.InventoryDigest != "" {
			inventory = fmt.Sprintf("  tools:%d global:%d project:%d drift:%d", entry.Inventory.ToolsFound, entry.Inventory.GlobalSkills, entry.Inventory.ProjectLocalSkills, entry.Inventory.DriftGroups)
		}
		fmt.Fprintf(stdout, "%s  %s  %s%s\n", label, entry.LastCommit, entry.LastSynced, inventory)
	}
	return ExitSuccess
}

func parseInitLibraryOptions(args []string) (remote string, localOnly bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--remote":
			if i+1 >= len(args) {
				return "", false, errors.New("--remote requires a URL or path")
			}
			remote = args[i+1]
			i++
		case "--local-only":
			localOnly = true
		case "--provider":
			if i+1 >= len(args) {
				return "", false, errors.New("--provider requires a value")
			}
			if args[i+1] != "local" {
				return "", false, fmt.Errorf("unsupported provider %q; use --remote for custom git remotes", args[i+1])
			}
			localOnly = true
			i++
		default:
			return "", false, fmt.Errorf("unknown init-library argument: %s", args[i])
		}
	}
	return remote, localOnly, nil
}

func parseSyncLibraryOptions(args []string) (string, error) {
	mode := "pull"
	seen := false
	for _, arg := range args {
		switch arg {
		case "--pull":
			if seen {
				return "", errors.New("sync-library accepts only one of --pull, --push, or --status")
			}
			seen = true
			mode = "pull"
		case "--push":
			if seen {
				return "", errors.New("sync-library accepts only one of --pull, --push, or --status")
			}
			seen = true
			mode = "push"
		case "--status":
			if seen {
				return "", errors.New("sync-library accepts only one of --pull, --push, or --status")
			}
			seen = true
			mode = "status"
		default:
			return "", fmt.Errorf("unknown sync-library argument: %s", arg)
		}
	}
	return mode, nil
}

func ensureLibraryFiles(library string) error {
	if err := os.MkdirAll(library, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(library, "catalog.yaml")); errors.Is(err, os.ErrNotExist) {
		if err := writeCatalog(filepath.Join(library, "catalog.yaml"), catalog{Skills: []catalogSkill{}}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(library, ".gitignore")); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(filepath.Join(library, ".gitignore"), []byte("notifications/\nlogs/\ncache/\n"), 0o644); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(library, ".machines.yaml")); errors.Is(err, os.ErrNotExist) {
		return writeMachines(filepath.Join(library, ".machines.yaml"), machinesFile{Version: 1, Machines: map[string]machineEntry{}})
	} else if err != nil {
		return err
	}
	return nil
}

func ensureGitRepo(library string) error {
	if _, err := os.Stat(filepath.Join(library, ".git")); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := runCmd(library, "git", "init", "-b", "main"); err != nil {
		if _, fallbackErr := runCmd(library, "git", "init"); fallbackErr != nil {
			return err
		}
		if _, branchErr := runGit(library, "checkout", "-B", "main"); branchErr != nil {
			return branchErr
		}
	}
	return configureGitIdentity(library)
}

func setGitRemote(library string, remote string) error {
	if _, err := runGit(library, "remote", "get-url", "origin"); err == nil {
		_, err = runGit(library, "remote", "set-url", "origin", remote)
		return err
	}
	_, err := runGit(library, "remote", "add", "origin", remote)
	return err
}

func configureGitIdentity(library string) error {
	if _, err := runGit(library, "config", "user.email", "skills-manager@example.local"); err != nil {
		return err
	}
	_, err := runGit(library, "config", "user.name", "skills-manager")
	return err
}

func commitLibraryIfDirty(library string, message string) error {
	if err := configureGitIdentity(library); err != nil {
		return err
	}
	status, err := runGit(library, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	if _, err := runGit(library, "add", "-A"); err != nil {
		return err
	}
	_, err = runGit(library, "commit", "-m", message)
	return err
}

func updateMachine(library string) error {
	path := filepath.Join(library, ".machines.yaml")
	machines, err := readMachines(path)
	if err != nil {
		return err
	}
	if machines.Machines == nil {
		machines.Machines = map[string]machineEntry{}
	}
	commit := ""
	if out, err := runGit(library, "rev-parse", "--short", "HEAD"); err == nil {
		commit = strings.TrimSpace(out)
	}
	inventory, err := writeMachineInventorySnapshot(library)
	if err != nil {
		return err
	}
	machines.Machines[currentMachineName()] = machineEntry{
		LastSynced: time.Now().UTC().Format(time.RFC3339),
		LastCommit: commit,
		Inventory:  inventory,
	}
	return writeMachines(path, machines)
}

func writeMachineInventorySnapshot(library string) (machineInventorySummary, error) {
	home := filepath.Dir(library)
	inventory, err := loadDiscoverOutputFromState(home)
	if err != nil {
		return machineInventorySummary{}, err
	}
	machine := currentMachineName()
	snapshot := machineInventorySnapshot{
		SchemaVersion: 1,
		Machine:       machine,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Installations: []machineInventoryInstall{},
		Tools:         []machineInventoryTool{},
		Projects:      []machineInventoryProject{},
	}
	for _, inst := range inventory.Installations {
		snapshot.Installations = append(snapshot.Installations, machineInventoryInstall{
			SkillName:     inst.SkillName,
			ToolID:        inst.ToolID,
			Scope:         inst.Scope,
			ProjectID:     inst.ProjectID,
			SourcePath:    normalizeSnapshotPath(home, inst.SourcePath),
			ContentSHA256: inst.ContentSHA256,
			Managed:       inst.Managed,
			Ownership:     inst.Ownership,
		})
	}
	for _, tool := range inventory.Tools {
		snapshot.Tools = append(snapshot.Tools, machineInventoryTool{ToolID: tool.ToolID, Detected: tool.Detected})
	}
	for _, project := range inventory.Projects {
		snapshot.Projects = append(snapshot.Projects, machineInventoryProject{
			ProjectID: project.ProjectID,
			RootPath:  normalizeSnapshotPath(home, project.RootPath),
		})
	}
	sort.Slice(snapshot.Installations, func(i, j int) bool {
		if snapshot.Installations[i].SkillName != snapshot.Installations[j].SkillName {
			return snapshot.Installations[i].SkillName < snapshot.Installations[j].SkillName
		}
		if snapshot.Installations[i].ToolID != snapshot.Installations[j].ToolID {
			return snapshot.Installations[i].ToolID < snapshot.Installations[j].ToolID
		}
		return snapshot.Installations[i].SourcePath < snapshot.Installations[j].SourcePath
	})
	sort.Slice(snapshot.Tools, func(i, j int) bool { return snapshot.Tools[i].ToolID < snapshot.Tools[j].ToolID })
	sort.Slice(snapshot.Projects, func(i, j int) bool { return snapshot.Projects[i].ProjectID < snapshot.Projects[j].ProjectID })
	summary := machineInventorySummary{
		LastScan:           inventory.ScannedAt,
		SnapshotPath:       filepath.ToSlash(filepath.Join("inventory-snapshots", machine+".json")),
		ToolsFound:         inventory.Summary.ToolsFound,
		GlobalSkills:       inventory.Summary.GlobalSkills,
		ProjectLocalSkills: inventory.Summary.ProjectLocalSkills,
		DriftGroups:        inventory.Summary.DriftGroups,
	}
	raw, err := json.Marshal(snapshot.Installations)
	if err != nil {
		return machineInventorySummary{}, err
	}
	sum := sha256.Sum256(raw)
	summary.InventoryDigest = hex.EncodeToString(sum[:])
	snapshot.Summary = summary
	target := filepath.Join(library, summary.SnapshotPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return machineInventorySummary{}, err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return machineInventorySummary{}, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return machineInventorySummary{}, err
	}
	return summary, nil
}

func normalizeSnapshotPath(home, raw string) string {
	if raw == "" {
		return ""
	}
	absHome, homeErr := filepath.Abs(home)
	absRaw, rawErr := filepath.Abs(raw)
	if homeErr == nil && rawErr == nil {
		if rel, err := filepath.Rel(absHome, absRaw); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return filepath.ToSlash(filepath.Join("$HOME", rel))
		}
		if absRaw == absHome {
			return "$HOME"
		}
	}
	return filepath.ToSlash(filepath.Join("$PATH", filepath.Base(raw)))
}

func readMachines(path string) (machinesFile, error) {
	var machines machinesFile
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return machinesFile{Version: 1, Machines: map[string]machineEntry{}}, nil
	}
	if err != nil {
		return machines, err
	}
	if err := yaml.Unmarshal(data, &machines); err != nil {
		return machines, err
	}
	if machines.Version == 0 {
		machines.Version = 1
	}
	if machines.Machines == nil {
		machines.Machines = map[string]machineEntry{}
	}
	return machines, nil
}

func writeMachines(path string, machines machinesFile) error {
	if machines.Version == 0 {
		machines.Version = 1
	}
	if machines.Machines == nil {
		machines.Machines = map[string]machineEntry{}
	}
	return writeYAMLFile(path, machines)
}

func currentMachineName() string {
	if value := os.Getenv("SKILLS_MANAGER_MACHINE"); value != "" {
		return slug(value)
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "machine"
	}
	return slug(host)
}

func dirMissingOrEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func resolveGeneratedLibraryConflicts(library string) error {
	paths, err := unmergedPaths(library)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("merge conflict requires manual resolution")
	}

	hasMachines := false
	hasCatalog := false
	for _, path := range paths {
		switch path {
		case ".machines.yaml":
			hasMachines = true
		case "catalog.yaml":
			hasCatalog = true
		default:
			return errors.New("merge conflict requires manual resolution")
		}
	}

	if hasMachines {
		ours, oursErr := runGit(library, "show", ":2:.machines.yaml")
		theirs, theirsErr := runGit(library, "show", ":3:.machines.yaml")
		if oursErr != nil || theirsErr != nil {
			return errors.New("read .machines.yaml conflict stages")
		}
		merged, err := mergeMachinesYAML([]byte(ours), []byte(theirs))
		if err != nil {
			return err
		}
		if err := writeMachines(filepath.Join(library, ".machines.yaml"), merged); err != nil {
			return err
		}
	}

	if hasCatalog {
		cat, err := rebuildCatalogFromLibrary(library)
		if err != nil {
			return err
		}
		if err := writeCatalog(filepath.Join(library, "catalog.yaml"), cat); err != nil {
			return err
		}
	}

	if _, err := runGit(library, "add", "-A"); err != nil {
		return err
	}
	_, err = runGit(library, "commit", "-m", "Merge generated library metadata")
	return err
}

func unmergedPaths(library string) ([]string, error) {
	out, err := runGit(library, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

func mergeMachinesYAML(a []byte, b []byte) (machinesFile, error) {
	left := machinesFile{Version: 1, Machines: map[string]machineEntry{}}
	right := machinesFile{Version: 1, Machines: map[string]machineEntry{}}
	if err := yaml.Unmarshal(a, &left); err != nil {
		return machinesFile{}, err
	}
	if err := yaml.Unmarshal(b, &right); err != nil {
		return machinesFile{}, err
	}
	if left.Machines == nil {
		left.Machines = map[string]machineEntry{}
	}
	for name, entry := range right.Machines {
		current, ok := left.Machines[name]
		if !ok || entry.LastSynced > current.LastSynced {
			left.Machines[name] = entry
		}
	}
	return left, nil
}

func runGit(dir string, args ...string) (string, error) {
	return runCmd(dir, "git", args...)
}

func runCmd(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}
