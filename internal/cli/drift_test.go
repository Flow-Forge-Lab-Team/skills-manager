package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDriftDiffShowsDiscoveredContentDifference(t *testing.T) {
	home, inventory, group := writeDriftInventory(t)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"drift", "diff", "--inventory", inventory, "--group", group.GroupID}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("drift diff exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "--- "+group.InstallationIDs[0]) || !strings.Contains(got, "+++ "+group.InstallationIDs[1]) {
		t.Fatalf("diff missing installation headers:\n%s", got)
	}
	if !(strings.Contains(got, "-# Review") && strings.Contains(got, "+# Review changed")) &&
		!(strings.Contains(got, "-# Review changed") && strings.Contains(got, "+# Review")) {
		t.Fatalf("diff missing changed lines:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".skills-manager", "state.db")); err != nil {
		t.Fatalf("expected discover state db: %v", err)
	}
}

func TestDriftIgnorePersistsAndSuppressesRecommendation(t *testing.T) {
	home, _, group := writeDriftInventory(t)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"drift", "ignore", "--group", group.GroupID, "--reason", "intentional local variant"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("drift ignore exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	out := runDiscoverGlobalJSON(t, home)
	groupAfter := findTestDriftGroup(t, out, group.GroupID)
	if groupAfter.ReviewStatus != "ignored" || groupAfter.ReviewReason != "intentional local variant" {
		t.Fatalf("review state = %#v", groupAfter)
	}
	for _, rec := range out.Report.Recommendations {
		if rec.Kind == "review_drift" && rec.SkillName == "review" {
			t.Fatalf("ignored group still generated review recommendation: %#v", rec)
		}
	}

	ts := httptest.NewServer(newServeServer(filepath.Join(home, ".skills-manager")))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/drift")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/drift status = %d", resp.StatusCode)
	}
	var apiGroups []triageDriftGroup
	if err := json.NewDecoder(resp.Body).Decode(&apiGroups); err != nil {
		t.Fatalf("decode drift API: %v", err)
	}
	if len(apiGroups) != 1 || apiGroups[0].ReviewStatus != "ignored" || apiGroups[0].ReviewReason == "" {
		t.Fatalf("api groups = %#v", apiGroups)
	}
}

func TestDriftCanonicalPersistsAndPrintsReconciliationPlan(t *testing.T) {
	home, inventory, group := writeDriftInventory(t)
	canonical := group.InstallationIDs[0]

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "drift", "canonical", "--inventory", inventory, "--group", group.GroupID, "--canonical", canonical, "--reason", "keep claude"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("drift canonical exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var planOut actionPlanOutput
	if err := json.Unmarshal(stdout.Bytes(), &planOut); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, stdout.String())
	}
	plan := onlyPlan(t, planOut)
	if plan.Kind != "review_drift" || !containsPlanFile(plan.Files.Preserve, findTestInstall(t, runDiscoverGlobalJSON(t, home), canonical).SourcePath) {
		t.Fatalf("canonical plan = %#v", plan)
	}

	out := runDiscoverGlobalJSON(t, home)
	groupAfter := findTestDriftGroup(t, out, group.GroupID)
	if groupAfter.ReviewStatus != "canonical_selected" || groupAfter.CanonicalInstallationID != canonical {
		t.Fatalf("canonical review state = %#v", groupAfter)
	}
}

func writeDriftInventory(t *testing.T) (string, string, discoverDriftGroup) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Join(home, ".codex", "skills"), "review", "---\nname: review\n---\n# Review changed\n")
	out := runDiscoverGlobalJSON(t, home)
	if len(out.DriftGroups) == 0 {
		t.Fatalf("expected drift group: %#v", out)
	}
	inventory := filepath.Join(home, "discover.json")
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inventory, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return home, inventory, out.DriftGroups[0]
}

func runDiscoverGlobalJSON(t *testing.T, home string) discoverOutput {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("SKILLS_MANAGER_HOME", filepath.Join(home, ".skills-manager"))
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("discover exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var out discoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode discover: %v\n%s", err, stdout.String())
	}
	return out
}

func findTestDriftGroup(t *testing.T, out discoverOutput, groupID string) discoverDriftGroup {
	t.Helper()
	for _, group := range out.DriftGroups {
		if group.GroupID == groupID {
			return group
		}
	}
	t.Fatalf("group not found: %s in %#v", groupID, out.DriftGroups)
	return discoverDriftGroup{}
}

func findTestInstall(t *testing.T, out discoverOutput, installID string) discoverInstallation {
	t.Helper()
	for _, inst := range out.Installations {
		if inst.InstallationID == installID {
			return inst
		}
	}
	t.Fatalf("install not found: %s", installID)
	return discoverInstallation{}
}
