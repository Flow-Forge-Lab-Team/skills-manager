package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// updateActionJSON is the machine-readable result of a per-skill update action.
type updateActionJSON struct {
	Skill   string `json:"skill"`
	Action  string `json:"action"`
	Status  string `json:"status"`
	Pinned  string `json:"pinned,omitempty"`
	Message string `json:"message,omitempty"`
}

// runUpdateAccept applies a single pending update. Unlike --accept-all-safe,
// this is the manual override path: blocking safety flags do not prevent the
// accept (the operator is expected to have reviewed the raw diff), but the
// divergence guard still refuses to clobber local edits.
func runUpdateAccept(skill string, realStdout, stdout, stderr io.Writer, gf globalFlags) int {
	home, libraryPath, stateDB, code := openUpdateContext(skill, stderr)
	if code != ExitSuccess {
		return code
	}
	defer stateDB.Close()
	_ = home

	pendingRoot := filepath.Join(libraryPath, skill, ".update-pending")
	if _, err := os.Stat(pendingRoot); err != nil {
		fmt.Fprintf(stderr, "no pending update for %s\n", skill)
		return ExitNoPending
	}
	pending, err := findPendingUpdate(skill, pendingRoot)
	if err != nil {
		fmt.Fprintf(stderr, "pending update for %s: %v\n", skill, err)
		return ExitOpError
	}

	skillDir := filepath.Join(libraryPath, skill)
	diverged, reason, err := liveDivergesFromBase(skillDir, pending.From, pending.To)
	if err != nil {
		fmt.Fprintf(stderr, "check divergence for %s: %v\n", skill, err)
		return ExitOpError
	}
	if diverged {
		fmt.Fprintf(stdout, "Refusing --accept: %s diverged from the staged base (%s)\n", skill, reason)
		fmt.Fprintln(stdout, "Re-stage the update or revert the local change before retrying.")
		emitUpdateActionJSON(realStdout, gf, updateActionJSON{Skill: skill, Action: "accept", Status: "conflict", Message: reason})
		return ExitPartial
	}

	if err := applyPendingUpdate(pending); err != nil {
		fmt.Fprintf(stderr, "accept update for %s: %v\n", skill, err)
		return ExitOpError
	}
	if err := stateDB.MarkUpdateAccepted(skill); err != nil {
		fmt.Fprintf(stderr, "mark update accepted for %s: %v\n", skill, err)
		return ExitOpError
	}
	if code := rebuildCatalogAfterUpdate(libraryPath, stderr); code != ExitSuccess {
		return code
	}
	fmt.Fprintf(stdout, "✓ %s: accepted\n", skill)
	emitUpdateActionJSON(realStdout, gf, updateActionJSON{Skill: skill, Action: "accept", Status: "accepted"})
	return ExitSuccess
}

// runUpdateReject discards a pending update without applying it. The rejection
// is recorded in the state DB so the version is treated as seen rather than
// re-detected on the next poll.
func runUpdateReject(skill string, realStdout, stdout, stderr io.Writer, gf globalFlags) int {
	_, libraryPath, stateDB, code := openUpdateContext(skill, stderr)
	if code != ExitSuccess {
		return code
	}
	defer stateDB.Close()

	pendingRoot := filepath.Join(libraryPath, skill, ".update-pending")
	if _, err := os.Stat(pendingRoot); err != nil {
		fmt.Fprintf(stderr, "no pending update for %s\n", skill)
		return ExitNoPending
	}
	if err := stateDB.MarkUpdateRejected(skill); err != nil {
		fmt.Fprintf(stderr, "mark update rejected for %s: %v\n", skill, err)
		return ExitOpError
	}
	if err := os.RemoveAll(pendingRoot); err != nil {
		fmt.Fprintf(stderr, "remove pending update for %s: %v\n", skill, err)
		return ExitOpError
	}
	fmt.Fprintf(stdout, "✓ %s: update rejected\n", skill)
	emitUpdateActionJSON(realStdout, gf, updateActionJSON{Skill: skill, Action: "reject", Status: "rejected"})
	return ExitSuccess
}

// runUpdatePin freezes a skill at a version. If no version is given it defaults
// to the incoming version of the pending update (or the skill's current commit).
// Any pending update is rejected, since pinning means "do not move past here".
func runUpdatePin(skill, version string, realStdout, stdout, stderr io.Writer, gf globalFlags) int {
	_, libraryPath, stateDB, code := openUpdateContext(skill, stderr)
	if code != ExitSuccess {
		return code
	}
	defer stateDB.Close()

	skillDir := filepath.Join(libraryPath, skill)
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	meta, _ := readSkillMeta(metaPath)

	pendingRoot := filepath.Join(skillDir, ".update-pending")
	hasPending := false
	if _, err := os.Stat(pendingRoot); err == nil {
		hasPending = true
	}

	if version == "" {
		if pu, err := stateDB.GetPendingUpdate(skill); err == nil && pu != nil && pu.ToVersion != "" {
			version = pu.ToVersion
		} else if meta.Origin.Commit != "" {
			version = meta.Origin.Commit
		} else {
			version = "current"
		}
	}

	meta.Pinned = version
	if err := writeSeedSkillMeta(metaPath, meta); err != nil {
		fmt.Fprintf(stderr, "write skill metadata for %s: %v\n", skill, err)
		return ExitOpError
	}

	if hasPending {
		if err := stateDB.MarkUpdateRejected(skill); err != nil {
			fmt.Fprintf(stderr, "mark update rejected for %s: %v\n", skill, err)
			return ExitOpError
		}
		if err := os.RemoveAll(pendingRoot); err != nil {
			fmt.Fprintf(stderr, "remove pending update for %s: %v\n", skill, err)
			return ExitOpError
		}
	}
	fmt.Fprintf(stdout, "✓ %s pinned to %s (won't auto-update past this)\n", skill, version)
	fmt.Fprintf(stdout, "  Remove pin with: skills-manager update --unpin %s\n", skill)
	emitUpdateActionJSON(realStdout, gf, updateActionJSON{Skill: skill, Action: "pin", Status: "pinned", Pinned: version})
	return ExitSuccess
}

// runUpdateUnpin removes a version pin so updates resume on the next poll.
func runUpdateUnpin(skill string, realStdout, stdout, stderr io.Writer, gf globalFlags) int {
	_, libraryPath, stateDB, code := openUpdateContext(skill, stderr)
	if code != ExitSuccess {
		return code
	}
	defer stateDB.Close()

	metaPath := filepath.Join(libraryPath, skill, ".skill-meta.yaml")
	meta, _ := readSkillMeta(metaPath)
	if meta.Pinned == "" {
		fmt.Fprintf(stdout, "%s is not pinned\n", skill)
		emitUpdateActionJSON(realStdout, gf, updateActionJSON{Skill: skill, Action: "unpin", Status: "not-pinned"})
		return ExitSuccess
	}
	meta.Pinned = ""
	if err := writeSeedSkillMeta(metaPath, meta); err != nil {
		fmt.Fprintf(stderr, "write skill metadata for %s: %v\n", skill, err)
		return ExitOpError
	}
	fmt.Fprintf(stdout, "✓ %s unpinned\n", skill)
	emitUpdateActionJSON(realStdout, gf, updateActionJSON{Skill: skill, Action: "unpin", Status: "unpinned"})
	return ExitSuccess
}

// openUpdateContext validates the skill name and opens the library + state DB.
// The caller owns closing the returned DB.
func openUpdateContext(skill string, stderr io.Writer) (string, string, *state.DB, int) {
	if err := validateSkillName(skill); err != nil {
		fmt.Fprintf(stderr, "invalid skill name: %v\n", err)
		return "", "", nil, ExitUsageError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return "", "", nil, ExitOpError
	}
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return "", "", nil, ExitOpError
	}
	stateDB, err := state.Open(home)
	if err != nil {
		fmt.Fprintf(stderr, "open state: %v\n", err)
		return "", "", nil, ExitOpError
	}
	return home, libraryPath, stateDB, ExitSuccess
}

func rebuildCatalogAfterUpdate(libraryPath string, stderr io.Writer) int {
	cat, err := rebuildCatalogFromLibrary(libraryPath)
	if err != nil {
		fmt.Fprintf(stderr, "rebuild catalog: %v\n", err)
		return ExitOpError
	}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		fmt.Fprintf(stderr, "write catalog: %v\n", err)
		return ExitOpError
	}
	return ExitSuccess
}

func emitUpdateActionJSON(realStdout io.Writer, gf globalFlags, payload updateActionJSON) {
	if gf.JSON {
		_ = writeJSON(realStdout, payload)
	}
}
