package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

	catalogPath := filepath.Join(libraryPath, "catalog.yaml")
	rebuilt := []string{}

	var cat catalog
	if opts.rebuildCatalog {
		cat, err = rebuildCatalogFromLibrary(libraryPath)
		if err != nil {
			fmt.Fprintf(stderr, "rebuild catalog: %v\n", err)
			return ExitOpError
		}
		if err := writeCatalog(catalogPath, cat); err != nil {
			fmt.Fprintf(stderr, "write catalog: %v\n", err)
			return ExitOpError
		}
		rebuilt = append(rebuilt, "catalog")
		fmt.Fprintln(humanOut, "rebuilt catalog.yaml")
	} else {
		cat, err = readCatalog(catalogPath)
		if err != nil {
			// Fallback for first-run or missing catalog.yaml
			cat, err = rebuildCatalogFromLibrary(libraryPath)
			if err != nil {
				fmt.Fprintf(stderr, "load catalog: %v\n", err)
				return ExitOpError
			}
		}
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
		} else if len(cat.Skills) > 0 {
			// Cheap structural check: names set (catches add/remove even at same count).
			// Pure field drift (summary/compat/reqs) on the same set of skills is
			// exactly what --rebuild-state normalizes; we don't do a full row diff here.
			rows, _ := db.Query("SELECT name FROM skills")
			stateNames := map[string]bool{}
			for rows != nil && rows.Next() {
				var n string
				rows.Scan(&n)
				stateNames[n] = true
			}
			if rows != nil {
				rows.Close()
			}
			for _, s := range cat.Skills {
				if !stateNames[s.Name] {
					problems = append(problems, "catalog/state skill set drift; run `skills-manager doctor --rebuild-state`")
					break
				}
			}
		}
		db.Close()
	} else {
		problems = append(problems, fmt.Sprintf("cannot open state.db (%v); run `skills-manager doctor --rebuild-state`", err))
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
				problems = append(problems, fmt.Sprintf("malformed manifest %s: %v", e.Name(), err))
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
		if m != "linear" {
			problems = append(problems, fmt.Sprintf("MCP %s declared as required; automated check not implemented for non-linear MCPs — verify manually (see COMPATIBILITY.md)", m))
		} else if !mcpCheckPasses(m) {
			problems = append(problems, fmt.Sprintf("missing required MCP %s (configure connector for your harness)", m))
		}
	}

	// stale state.db — only flag if the derived state is older than the canonical
	// catalog.yaml (the source of truth). Absolute wall-clock age (48h) would cause
	// healthy long-stable machines to always report "stale".
	catPath := filepath.Join(libraryPath, "catalog.yaml")
	statePath := filepath.Join(home, "state.db")
	if catFi, err := os.Stat(catPath); err == nil {
		if stateFi, err := os.Stat(statePath); err == nil {
			if stateFi.ModTime().Before(catFi.ModTime()) {
				problems = append(problems, "state.db older than catalog.yaml; run `skills-manager doctor --rebuild-state`")
			}
		}
	}

	return problems
}

func toolCheckPasses(name string) bool {
	switch name {
	case "gh":
		c := exec.Command("gh", "auth", "status")
		return c.Run() == nil
	case "rg":
		c := exec.Command("rg", "--version")
		return c.Run() == nil
	default:
		// Safe: no shell, just presence check. Names from catalog are expected
		// to be simple binaries; LookPath does not evaluate the name as code.
		_, err := exec.LookPath(name)
		return err == nil
	}
}

func mcpCheckPasses(name string) bool {
	// Env opt-in (matches install.go missingRequiredMCPServers convention)
	envVar := "SKILLS_MANAGER_MCP_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	if os.Getenv(envVar) == "available" {
		return true
	}
	if name != "linear" {
		return false // unknown MCPs require the env (or future config) to be considered present
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
