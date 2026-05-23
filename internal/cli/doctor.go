package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

type doctorOptions struct {
	rebuildState   bool
	rebuildCatalog bool
}

func parseDoctorOptions(args []string) (doctorOptions, error) {
	var opts doctorOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--rebuild-state":
			opts.rebuildState = true
		case "--rebuild-catalog":
			opts.rebuildCatalog = true
		default:
			return opts, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return opts, nil
}

func runDoctor(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseDoctorOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	humanOut := gf.outWriter(stdout)

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return ExitOpError
	}

	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		fmt.Fprintf(stderr, "rebuild catalog: %v\n", err)
		return ExitOpError
	}

	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	rebuilt := []string{}

	if opts.rebuildCatalog {
		if err := writeCatalog(catalogPath, cat); err != nil {
			fmt.Fprintf(stderr, "write catalog: %v\n", err)
			return ExitOpError
		}
		rebuilt = append(rebuilt, "catalog")
		fmt.Fprintln(humanOut, "rebuilt catalog.yaml")
	}

	if opts.rebuildState {
		db, err := state.Open(home)
		if err != nil {
			fmt.Fprintf(stderr, "open state for rebuild: %v\n", err)
			return ExitOpError
		}
		snap := catalogToSnapshot(cat)
		if err := db.Rebuild(snap); err != nil {
			db.Close()
			fmt.Fprintf(stderr, "rebuild state: %v\n", err)
			return ExitOpError
		}
		db.Close()
		rebuilt = append(rebuilt, "state")
		fmt.Fprintln(humanOut, "rebuilt state.db")
	}

	problems := collectProblems(home, libraryPath, cat)

	if gf.JSON {
		out := map[string]interface{}{
			"problems": problems,
			"rebuilt":  rebuilt,
		}
		if err := writeJSON(stdout, out); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	} else {
		if len(problems) == 0 {
			fmt.Fprintln(humanOut, "No problems detected.")
		} else {
			for _, p := range problems {
				fmt.Fprintf(humanOut, "%s\n", p)
			}
		}
	}

	if len(problems) > 0 {
		return ExitNotable
	}
	return ExitSuccess
}

func collectProblems(home, libraryPath string, cat catalog) []string {
	var problems []string

	// catalog/state drift (if not just rebuilt)
	db, err := state.Open(home)
	if err == nil {
		var stateCnt int
		db.QueryRow("SELECT COUNT(*) FROM skills").Scan(&stateCnt)
		if stateCnt != len(cat.Skills) {
			problems = append(problems, fmt.Sprintf("catalog/state drift (%d vs %d); run `skills-manager doctor --rebuild-state`", len(cat.Skills), stateCnt))
		}
		db.Close()
	}

	// manifest integrity + fingerprint drift
	manifestsDir := filepath.Join(home, "manifests")
	if entries, err := os.ReadDir(manifestsDir); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			m, err := readManifest(filepath.Join(manifestsDir, e.Name()))
			if err != nil {
				continue
			}
			for _, rel := range m.ManagedPaths {
				target := filepath.Join(m.ProjectPath, filepath.FromSlash(rel))
				if _, err := os.Stat(target); os.IsNotExist(err) {
					problems = append(problems, fmt.Sprintf("manifest integrity: missing %s in %s", rel, m.ProjectPath))
					continue
				}
				if expected := m.Files[rel]; expected != "" {
					if actual, err := fingerprintDir(target); err == nil && actual != expected {
						problems = append(problems, fmt.Sprintf("fingerprint drift: %s modified in %s", rel, m.ProjectPath))
					}
				}
			}
		}
	}

	// requirement execution checks
	reqTools := map[string]bool{}
	reqMCPs := map[string]bool{}
	for _, s := range cat.Skills {
		for _, t := range s.Requirements.Tools {
			if t.Required {
				reqTools[t.Name] = true
			}
		}
		for _, m := range s.Requirements.MCPServers {
			if m.Required {
				reqMCPs[m.Name] = true
			}
		}
	}
	for t := range reqTools {
		if !toolCheckPasses(t) {
			problems = append(problems, fmt.Sprintf("missing required tool %s (see `skills-manager doctor` output and COMPATIBILITY.md)", t))
		}
	}
	for m := range reqMCPs {
		if !mcpCheckPasses(m) {
			problems = append(problems, fmt.Sprintf("missing required MCP %s (configure connector for your harness)", m))
		}
	}

	// stale state.db
	dbPath := filepath.Join(home, "state.db")
	if fi, err := os.Stat(dbPath); err == nil {
		if time.Since(fi.ModTime()) > 48*time.Hour {
			problems = append(problems, "stale state.db; run `skills-manager doctor --rebuild-state`")
		}
	}

	return problems
}

func toolCheckPasses(name string) bool {
	cmdStr := ""
	switch name {
	case "gh":
		cmdStr = "gh auth status"
	case "rg":
		cmdStr = "rg --version"
	default:
		cmdStr = "command -v " + name + " > /dev/null 2>&1 || exit 1"
	}
	c := exec.Command("/bin/sh", "-c", cmdStr)
	return c.Run() == nil
}

func mcpCheckPasses(name string) bool {
	if name != "linear" {
		return true
	}
	h, _ := os.UserHomeDir()
	cands := []string{
		filepath.Join(h, "Library/Application Support/Claude/claude_desktop_config.json"),
		filepath.Join(h, ".config/claude/config.json"),
		filepath.Join(h, ".claude/mcp.json"),
	}
	for _, p := range cands {
		if b, err := os.ReadFile(p); err == nil && (strings.Contains(string(b), "linear") || strings.Contains(string(b), "mcp__linear")) {
			return true
		}
	}
	return false
}

func catalogToSnapshot(cat catalog) state.CatalogSnapshot {
	var snap state.CatalogSnapshot
	for _, s := range cat.Skills {
		cs := state.CatalogSkill{
			Name:              s.Name,
			Summary:           s.Summary,
			Categories:        s.Categories,
			Tags:              s.Tags,
			CompatibilityMode: s.Compatibility.Mode,
		}
		if b, err := json.Marshal(s.Compatibility); err == nil {
			var m map[string]any
			json.Unmarshal(b, &m)
			cs.CompatibilityData = m
		}
		if b, err := json.Marshal(s.Requirements); err == nil {
			var m map[string]any
			json.Unmarshal(b, &m)
			cs.Requirements = m
		}
		snap.Skills = append(snap.Skills, cs)
	}
	return snap
}
