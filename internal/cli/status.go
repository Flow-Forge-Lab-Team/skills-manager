package cli

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

func runStatus(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	for _, a := range args {
		fmt.Fprintf(stderr, "unknown flag: %s\n", a)
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

	libCount := countLibrarySkills(libraryPath)
	pendingCount := countPendingUpdates(libraryPath)

	db, err := state.Open(home)
	if err != nil {
		fmt.Fprintf(stderr, "open state: %v\n", err)
		return ExitOpError
	}
	defer db.Close()

	var projCount int
	db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&projCount)

	var unregCount int
	db.QueryRow("SELECT COUNT(*) FROM detected").Scan(&unregCount)

	sched := checkScheduledState(db)

	if gf.JSON {
		out := map[string]interface{}{
			"library_skills":   libCount,
			"projects":         projCount,
			"pending_updates":  pendingCount,
			"unregistered":     unregCount,
			"scheduled_check":  sched,
		}
		if err := writeJSON(stdout, out); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	} else {
		fmt.Fprintf(humanOut, "Library:          %d skills\n", libCount)
		fmt.Fprintf(humanOut, "Projects:         %d\n", projCount)
		fmt.Fprintf(humanOut, "Pending updates:  %d", pendingCount)
		if pendingCount > 0 {
			fmt.Fprintf(humanOut, " (run `skills-manager update`)")
		}
		fmt.Fprintln(humanOut)
		fmt.Fprintf(humanOut, "Unregistered:     %d detected", unregCount)
		if unregCount > 0 {
			fmt.Fprintf(humanOut, " (run `skills-manager scan`)")
		}
		fmt.Fprintln(humanOut)
		fmt.Fprintf(humanOut, "Scheduled checks: %s\n", sched)
	}

	return ExitSuccess
}

func countLibrarySkills(libraryPath string) int {
	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			if _, err := os.Stat(filepath.Join(libraryPath, e.Name(), "SKILL.md")); err == nil {
				count++
			}
		}
	}
	return count
}

func countPendingUpdates(libraryPath string) int {
	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			if _, err := os.Stat(filepath.Join(libraryPath, e.Name(), ".update-pending")); err == nil {
				count++
			}
		}
	}
	return count
}

func checkScheduledState(db *state.DB) string {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='skill_polls'`).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows || strings.Contains(err.Error(), "no such table") {
			return "not configured (pending FLO-238)"
		}
		return "unknown (pending FLO-238)"
	}
	return "table present (stale detection in FLO-238)"
}
