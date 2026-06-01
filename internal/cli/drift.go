package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

type driftOptions struct {
	command     string
	inventory   string
	groupID     string
	leftID      string
	rightID     string
	canonicalID string
	reason      string
}

func runDrift(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseDriftOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	stdout := gf.outWriter(realStdout)
	switch opts.command {
	case "diff":
		text, err := buildDriftDiff(opts)
		if err != nil {
			fmt.Fprintf(stderr, "drift diff: %v\n", err)
			return ExitOpError
		}
		fmt.Fprint(stdout, text)
		return ExitSuccess
	case "accept", "ignore":
		status := "accepted"
		if opts.command == "ignore" {
			status = "ignored"
		}
		if err := saveDriftReview(opts.groupID, status, opts.reason, ""); err != nil {
			fmt.Fprintf(stderr, "drift %s: %v\n", opts.command, err)
			return ExitOpError
		}
		if gf.JSON {
			if err := writeJSON(realStdout, map[string]string{"group_id": opts.groupID, "status": status, "reason": opts.reason}); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitOpError
			}
		} else {
			fmt.Fprintf(stdout, "%s marked %s\n", opts.groupID, status)
		}
		return ExitSuccess
	case "canonical":
		inventory, err := readPlanInventory(opts.inventory)
		if err != nil {
			fmt.Fprintf(stderr, "read inventory: %v\n", err)
			return ExitOpError
		}
		group, err := findDriftGroup(inventory, opts.groupID)
		if err != nil {
			fmt.Fprintf(stderr, "drift canonical: %v\n", err)
			return ExitOpError
		}
		if !stringSliceContains(group.InstallationIDs, opts.canonicalID) {
			fmt.Fprintf(stderr, "drift canonical: canonical installation is not in group: %s\n", opts.canonicalID)
			return ExitUsageError
		}
		if err := saveDriftReview(opts.groupID, "canonical_selected", opts.reason, opts.canonicalID); err != nil {
			fmt.Fprintf(stderr, "drift canonical: %v\n", err)
			return ExitOpError
		}
		plan := buildCanonicalDriftPlan(inventory, group, opts.canonicalID)
		out := actionPlanOutput{
			SchemaVersion: 1,
			InventoryPath: opts.inventory,
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
			Plans:         []actionPlan{plan},
		}
		if gf.JSON {
			if err := writeJSON(realStdout, out); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitOpError
			}
			return ExitSuccess
		}
		printActionPlans(stdout, out)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "unknown drift command: %s\n", opts.command)
		return ExitUsageError
	}
}

func parseDriftOptions(args []string) (driftOptions, error) {
	var opts driftOptions
	if len(args) == 0 {
		return opts, errors.New("usage: skills-manager drift <diff|accept|ignore|canonical> [flags]")
	}
	opts.command = args[0]
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--inventory":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return opts, errors.New("--inventory requires a file")
			}
			opts.inventory = args[i]
		case "--group":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return opts, errors.New("--group requires an id")
			}
			opts.groupID = args[i]
		case "--left":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return opts, errors.New("--left requires an installation id")
			}
			opts.leftID = args[i]
		case "--right":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return opts, errors.New("--right requires an installation id")
			}
			opts.rightID = args[i]
		case "--canonical":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return opts, errors.New("--canonical requires an installation id")
			}
			opts.canonicalID = args[i]
		case "--reason":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return opts, errors.New("--reason requires text")
			}
			opts.reason = args[i]
		default:
			return opts, fmt.Errorf("unknown drift argument: %s", args[i])
		}
	}
	if opts.groupID == "" {
		return opts, errors.New("drift requires --group")
	}
	switch opts.command {
	case "diff":
		if opts.inventory == "" {
			return opts, errors.New("drift diff requires --inventory")
		}
	case "accept", "ignore":
		if opts.reason == "" {
			return opts, errors.New("drift review requires --reason")
		}
	case "canonical":
		if opts.inventory == "" || opts.canonicalID == "" || opts.reason == "" {
			return opts, errors.New("drift canonical requires --inventory, --canonical, and --reason")
		}
	default:
		return opts, fmt.Errorf("unknown drift command: %s", opts.command)
	}
	return opts, nil
}

func buildDriftDiff(opts driftOptions) (string, error) {
	inventory, err := readPlanInventory(opts.inventory)
	if err != nil {
		return "", err
	}
	group, err := findDriftGroup(inventory, opts.groupID)
	if err != nil {
		return "", err
	}
	leftID, rightID := opts.leftID, opts.rightID
	if leftID == "" || rightID == "" {
		if len(group.InstallationIDs) < 2 {
			return "", fmt.Errorf("group needs at least two installations")
		}
		leftID, rightID = group.InstallationIDs[0], group.InstallationIDs[1]
	}
	installs := discoverInstallationsByID(inventory.Installations)
	left, ok := installs[leftID]
	if !ok {
		return "", fmt.Errorf("left installation not found: %s", leftID)
	}
	right, ok := installs[rightID]
	if !ok {
		return "", fmt.Errorf("right installation not found: %s", rightID)
	}
	leftText, err := readDriftInstallationText(left)
	if err != nil {
		return "", err
	}
	rightText, err := readDriftInstallationText(right)
	if err != nil {
		return "", err
	}
	return formatSimpleUnifiedDiff(left, right, leftText, rightText), nil
}

func findDriftGroup(inventory discoverOutput, groupID string) (discoverDriftGroup, error) {
	for _, group := range inventory.DriftGroups {
		if group.GroupID == groupID {
			return group, nil
		}
	}
	return discoverDriftGroup{}, fmt.Errorf("drift group not found: %s", groupID)
}

func discoverInstallationsByID(installs []discoverInstallation) map[string]discoverInstallation {
	out := map[string]discoverInstallation{}
	for _, inst := range installs {
		out[inst.InstallationID] = inst
	}
	return out
}

func readDriftInstallationText(inst discoverInstallation) (string, error) {
	path := strings.TrimSpace(inst.ContentPath)
	if path == "" {
		path = inst.SourcePath
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, "SKILL.md")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

func formatSimpleUnifiedDiff(left, right discoverInstallation, leftText, rightText string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s %s\n", left.InstallationID, left.SourcePath)
	fmt.Fprintf(&b, "+++ %s %s\n", right.InstallationID, right.SourcePath)
	b.WriteString("@@\n")
	leftLines := strings.Split(strings.TrimRight(leftText, "\n"), "\n")
	rightLines := strings.Split(strings.TrimRight(rightText, "\n"), "\n")
	max := len(leftLines)
	if len(rightLines) > max {
		max = len(rightLines)
	}
	for i := 0; i < max; i++ {
		var l, r string
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		switch {
		case i >= len(leftLines):
			fmt.Fprintf(&b, "+%s\n", r)
		case i >= len(rightLines):
			fmt.Fprintf(&b, "-%s\n", l)
		case l == r:
			fmt.Fprintf(&b, " %s\n", l)
		default:
			fmt.Fprintf(&b, "-%s\n", l)
			fmt.Fprintf(&b, "+%s\n", r)
		}
	}
	return b.String()
}

func saveDriftReview(groupID, status, reason, canonicalID string) error {
	home, err := managerHome()
	if err != nil {
		return err
	}
	db, err := state.Open(home)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO discovery_drift_reviews
(group_id, status, reason, canonical_installation_id, reviewed_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(group_id) DO UPDATE SET
  status=excluded.status,
  reason=excluded.reason,
  canonical_installation_id=excluded.canonical_installation_id,
  reviewed_at=excluded.reviewed_at`,
		groupID, status, reason, canonicalID, time.Now().UTC().Format(time.RFC3339))
	return err
}

func buildCanonicalDriftPlan(inventory discoverOutput, group discoverDriftGroup, canonicalID string) actionPlan {
	installs := discoverInstallationsByID(inventory.Installations)
	canonical := installs[canonicalID]
	plan := actionPlan{
		RecommendationID:      "drift-" + strings.TrimPrefix(group.GroupID, "group-"),
		Kind:                  "review_drift",
		Title:                 "Reconcile drift: " + group.SkillName,
		Reason:                "canonical source selected for reviewed drift group",
		Confidence:            "medium",
		SkillName:             group.SkillName,
		SourceInstallationIDs: append([]string{}, group.InstallationIDs...),
		Status:                "ready",
		Files:                 emptyActionPlanFiles(),
	}
	for _, id := range group.InstallationIDs {
		inst := installs[id]
		if id == canonicalID {
			plan.addPreserve(inst.SourcePath, inst.SourcePath, ownershipLabel(inst), "selected canonical source")
			continue
		}
		if inst.InstallationID == "" {
			plan.addBlocker("installation not found: " + id)
			continue
		}
		if inst.Managed {
			plan.addUpdate(inst.SourcePath, canonical.SourcePath, "manager", "replace manager-owned drift with selected canonical source")
			continue
		}
		plan.addPreserve(inst.SourcePath, canonical.SourcePath, ownershipLabel(inst), "unmanaged drift requires explicit conflict resolution")
		plan.addBlocker("unmanaged drift target requires explicit conflict resolution: " + inst.SourcePath)
	}
	plan.sortFiles()
	if len(plan.Blockers) > 0 {
		plan.Status = "blocked"
	}
	return plan
}
