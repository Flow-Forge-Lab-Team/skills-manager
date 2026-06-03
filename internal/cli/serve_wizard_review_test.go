package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWizardDryRunPlanMatchesCLI verifies wizard dry-run previews use the same
// action planner output as `skills-manager plan` for a persisted fixture.
func TestWizardDryRunPlanMatchesCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Join(home, ".codex", "skills"), "review", "---\nname: review\n---\n# Review changed\n")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("discover exit = %d\nstderr:\n%s", code, stderr.String())
	}

	inventory, err := loadDiscoverOutputFromState(managerHome)
	if err != nil {
		t.Fatalf("load inventory: %v", err)
	}
	rec := findTestRecommendationFromInventory(t, inventory, "review", "ingest")
	invPath := filepath.Join(t.TempDir(), "discover.json")
	raw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invPath, raw, 0644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--json", "plan", "--inventory", invPath, "--recommendation", rec.RecommendationID}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("plan exit = %d\nstderr:\n%s", code, stderr.String())
	}
	var cliOut actionPlanOutput
	if err := json.Unmarshal(stdout.Bytes(), &cliOut); err != nil {
		t.Fatalf("decode cli plan: %v", err)
	}
	if len(cliOut.Plans) != 1 {
		t.Fatalf("cli plans = %d, want 1", len(cliOut.Plans))
	}

	srv := newServeServer(managerHome)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	apiPreview := postTestAction[triageActionPlanResponse](t, ts.URL, srv.token, "plan", map[string]string{
		"recommendation_id": rec.RecommendationID,
	})
	if !sameActionPlan(cliOut.Plans[0], apiPreview.Plan) {
		t.Fatalf("api plan differs from cli plan\ncli=%#v\napi=%#v", cliOut.Plans[0], apiPreview.Plan)
	}
}

// TestServeWizardReviewFixtureDryRunNoApply mirrors the setup-wizard review step:
// recommendations are loaded, dry-run plans are previewed, and no install writes occur.
func TestServeWizardReviewFixtureDryRunNoApply(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
	writeScanSkill(t, filepath.Join(home, ".codex", "skills"), "review", "---\nname: review\n---\n# Review changed\n")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--json", "discover", "--global"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("discover exit = %d\nstderr:\n%s", code, stderr.String())
	}

	srv := newServeServer(managerHome)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	assessment := getTestAssessment(t, ts.URL)
	if len(assessment.Recommendations) == 0 {
		t.Fatalf("expected recommendations: %#v", assessment)
	}
	rec := findTestRecommendation(t, assessment, "review", "ingest")
	libraryTarget := filepath.Join(managerHome, "library", "review", "SKILL.md")
	if _, err := os.Stat(libraryTarget); err == nil {
		t.Fatalf("library target should not exist before preview: %s", libraryTarget)
	}

	preview := postTestAction[triageActionPlanResponse](t, ts.URL, srv.token, "plan", map[string]string{
		"recommendation_id": rec.RecommendationID,
	})
	if preview.Plan.Status != "ready" || len(preview.Plan.Files.Create) == 0 {
		t.Fatalf("preview plan = %#v", preview.Plan)
	}
	if _, err := os.Stat(libraryTarget); err == nil {
		t.Fatalf("preview must not write library target: %s", libraryTarget)
	}

	status, body := postTestActionRaw(t, ts.URL, srv.token, "apply", map[string]any{
		"recommendation_id": rec.RecommendationID,
		"plan":              preview.Plan,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("apply without confirm status = %d body=%s, want 400", status, body)
	}
	if _, err := os.Stat(libraryTarget); err == nil {
		t.Fatalf("blocked apply must not write library target: %s", libraryTarget)
	}
}

// TestServeEmbeddedWizardReviewUIContract checks the embedded web UI exposes the
// wizard review step markers used by the fixture-driven browser flow.
func TestServeEmbeddedWizardReviewUIContract(t *testing.T) {
	home := t.TempDir()
	ts := httptest.NewServer(newServeServer(filepath.Join(home, ".skills-manager")))
	defer ts.Close()

	for _, path := range []string{"/app.js", "/styles.css"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, resp.StatusCode)
		}
		text := string(body)
		switch path {
		case "/app.js":
			for _, marker := range []string{
				"renderWizardReviewPanel",
				"wizard-review-group",
				"Preview dry-run",
				"wizardReviewStepCanAdvance",
			} {
				if !strings.Contains(text, marker) {
					t.Fatalf("%s missing marker %q", path, marker)
				}
			}
		case "/styles.css":
			if !strings.Contains(text, ".wizard-review-card") {
				t.Fatalf("%s missing wizard review styles", path)
			}
		}
	}
}

func findTestRecommendationFromInventory(t *testing.T, inventory discoverOutput, skillName, kind string) discoverRecommendation {
	t.Helper()
	for _, rec := range inventory.Report.Recommendations {
		if rec.SkillName == skillName && rec.Kind == kind {
			return rec
		}
	}
	t.Fatalf("recommendation not found for %s/%s: %#v", skillName, kind, inventory.Report.Recommendations)
	return discoverRecommendation{}
}