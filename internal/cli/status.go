package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

	// v0.1 ground truth for "registered projects" is the set of manifests we have
	// written. The `projects` table in state.db is not yet populated on install/sync
	// (manifests/*.json are the durable record), so we count those for an accurate
	// number instead of always reporting 0.
	projCount := 0
	if entries, err := os.ReadDir(filepath.Join(home, "manifests")); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				projCount++
			}
		}
	}

	var unregCount int
	db.QueryRow("SELECT COUNT(*) FROM detected WHERE action IS NULL OR action = '' OR action = 'pending'").Scan(&unregCount)

	watcherCount := countWatcherNotifications(home)

	sched := formatScheduledCheckStatus(db, home)

	if gf.JSON {
		out := map[string]interface{}{
			"library_skills":        libCount,
			"projects":              projCount,
			"pending_updates":       pendingCount,
			"unregistered":          unregCount,
			"watcher_notifications": watcherCount,
			"scheduled_check":       sched,
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
			fmt.Fprintf(humanOut, " (outside library; `scan` will be added with ingest)")
		}
		fmt.Fprintln(humanOut)
		if watcherCount > 0 {
			fmt.Fprintf(humanOut, "Watcher alerts:   %d (in ~/.skills-manager/notifications/; run `scan --ingest` to review)\n", watcherCount)
		}
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

// watcherNotificationView is one unresolved watcher notification plus the
// basename of the file it was read from, so the UI can target it for dismissal.
type watcherNotificationView struct {
	watchNotification
	File string `json:"file"`
}

// loadWatcherNotifications returns the unresolved watcher detections written
// under ~/.skills-manager/notifications/ by `skills-manager watch`, newest
// first. A notification is resolved once its fingerprint appears in the library
// (e.g. after ingest), so resolved files are pruned and not returned —
// otherwise the UI would surface handled alerts forever.
func loadWatcherNotifications(home string) []watcherNotificationView {
	dir := filepath.Join(home, "notifications")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []watcherNotificationView{}
	}
	inLibrary := buildFingerprintIndex(filepath.Join(home, "library"))
	out := []watcherNotificationView{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "watch-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var n watchNotification
		if json.Unmarshal(data, &n) != nil {
			continue
		}
		if n.Fingerprint != "" {
			if _, ok := inLibrary[n.Fingerprint]; ok {
				_ = os.Remove(path) // resolved (now in library) — prune
				continue
			}
		}
		out = append(out, watcherNotificationView{watchNotification: n, File: e.Name()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DetectedAt > out[j].DetectedAt })
	return out
}

// countWatcherNotifications counts unresolved watcher detections. It shares the
// read/prune logic with loadWatcherNotifications so the status count and the UI
// list never disagree.
func countWatcherNotifications(home string) int {
	return len(loadWatcherNotifications(home))
}

func checkScheduledState(db *state.DB) string {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='skill_polls'`).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows || strings.Contains(err.Error(), "no such table") {
			return "not configured"
		}
		return "unknown"
	}
	var tracked, stale int
	db.QueryRow(`SELECT COUNT(*) FROM skill_polls WHERE last_checked_at != ''`).Scan(&tracked)
	db.QueryRow(`SELECT COUNT(*) FROM skill_polls WHERE last_checked_at != '' AND datetime(last_checked_at) < datetime('now', '-24 hours')`).Scan(&stale)
	if tracked == 0 {
		return "0 tracked (run `skills-manager check`)"
	}
	return fmt.Sprintf("%d tracked, %d stale (>24h)", tracked, stale)
}
