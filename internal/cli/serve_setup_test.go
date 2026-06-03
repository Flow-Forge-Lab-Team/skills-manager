package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveSetupState(t *testing.T) {
	// Truth table from the FLO-406 contract: A=inventory, B=managed, C=open.
	cases := []struct {
		a, b, c bool
		want    string
	}{
		{false, false, false, setupStateNoDiscovery},
		{false, true, true, setupStateNoDiscovery}, // A=false dominates
		{true, false, false, setupStateCompleted},
		{true, true, false, setupStateCompleted}, // B is don't-care when C=false
		{true, false, true, setupStateDiscoveredUnmanaged},
		{true, true, true, setupStatePartiallyManaged},
	}
	for _, tc := range cases {
		if got := deriveSetupState(tc.a, tc.b, tc.c); got != tc.want {
			t.Errorf("deriveSetupState(A=%v,B=%v,C=%v) = %q, want %q", tc.a, tc.b, tc.c, got, tc.want)
		}
	}
}

func TestSetupStatusFromAssessment(t *testing.T) {
	unmanaged := discoverInstallation{InstallationID: "i1", SkillName: "review", Ownership: "unmanaged"}
	managed := discoverInstallation{InstallationID: "i2", SkillName: "owned", Ownership: "manager"}
	rec := func(id, kind string) discoverRecommendation {
		return discoverRecommendation{RecommendationID: id, Kind: kind}
	}
	review := func(id, status string) triageActionReview {
		return triageActionReview{RecommendationID: id, Status: status}
	}

	cases := []struct {
		name             string
		assessment       triageAssessment
		managedLibrary   int
		wantState        string
		wantOpenRecs     int
		wantManagerOwned int
	}{
		{
			name:       "no_discovery when there are no installations",
			assessment: triageAssessment{},
			wantState:  setupStateNoDiscovery,
		},
		{
			name:           "no_discovery dominates even with a managed library",
			assessment:     triageAssessment{},
			managedLibrary: 3,
			wantState:      setupStateNoDiscovery,
		},
		{
			name: "discovered_unmanaged with an open install recommendation",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged},
				Recommendations: []discoverRecommendation{rec("r1", "install_global")},
			},
			wantState:    setupStateDiscoveredUnmanaged,
			wantOpenRecs: 1,
		},
		{
			name: "partially_managed via a manager-owned install",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged, managed},
				Recommendations: []discoverRecommendation{rec("r1", "ingest")},
			},
			wantState:        setupStatePartiallyManaged,
			wantOpenRecs:     1,
			wantManagerOwned: 1,
		},
		{
			name: "partially_managed via the managed library",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged},
				Recommendations: []discoverRecommendation{rec("r1", "review_drift")},
			},
			managedLibrary: 2,
			wantState:      setupStatePartiallyManaged,
			wantOpenRecs:   1,
		},
		{
			name: "completed when every actionable recommendation is reviewed",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged},
				Recommendations: []discoverRecommendation{rec("r1", "install_global")},
				ActionReviews:   []triageActionReview{review("r1", "accepted")},
			},
			wantState:    setupStateCompleted,
			wantOpenRecs: 0,
		},
		{
			name: "completed when only ignorable recommendations remain",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged},
				Recommendations: []discoverRecommendation{rec("r1", "ignore"), rec("r2", "ignore")},
			},
			wantState:    setupStateCompleted,
			wantOpenRecs: 0,
		},
		{
			name: "completed when discovery surfaced no recommendations",
			assessment: triageAssessment{
				Installations: []discoverInstallation{unmanaged},
			},
			wantState: setupStateCompleted,
		},
		{
			name: "an actionable recommendation outweighs ignorable ones",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged},
				Recommendations: []discoverRecommendation{rec("r1", "ignore"), rec("r2", "install_global")},
			},
			wantState:    setupStateDiscoveredUnmanaged,
			wantOpenRecs: 1,
		},
		{
			name: "a failed action stays open so setup is not marked complete",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged},
				Recommendations: []discoverRecommendation{rec("r1", "install_global")},
				ActionReviews:   []triageActionReview{review("r1", "failed")},
			},
			wantState:    setupStateDiscoveredUnmanaged,
			wantOpenRecs: 1,
		},
		{
			name: "an ignored review closes the action",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged},
				Recommendations: []discoverRecommendation{rec("r1", "install_global")},
				ActionReviews:   []triageActionReview{review("r1", "ignored")},
			},
			wantState:    setupStateCompleted,
			wantOpenRecs: 0,
		},
		{
			name: "an applied review closes the action",
			assessment: triageAssessment{
				Installations:   []discoverInstallation{unmanaged, managed},
				Recommendations: []discoverRecommendation{rec("r1", "install_global")},
				ActionReviews:   []triageActionReview{review("r1", "applied")},
			},
			wantState:        setupStateCompleted,
			wantOpenRecs:     0,
			wantManagerOwned: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := setupStatusFromAssessment(tc.assessment, tc.managedLibrary)
			if got.State != tc.wantState {
				t.Errorf("state = %q, want %q (%#v)", got.State, tc.wantState, got)
			}
			if got.OpenRecommendations != tc.wantOpenRecs {
				t.Errorf("open recommendations = %d, want %d", got.OpenRecommendations, tc.wantOpenRecs)
			}
			if got.ManagerOwnedInstallations != tc.wantManagerOwned {
				t.Errorf("manager-owned installations = %d, want %d", got.ManagerOwnedInstallations, tc.wantManagerOwned)
			}
			if got.ManagedLibrarySkills != tc.managedLibrary {
				t.Errorf("managed library skills = %d, want %d", got.ManagedLibrarySkills, tc.managedLibrary)
			}
			if got.DiscoveredInstallations != len(tc.assessment.Installations) {
				t.Errorf("discovered installations = %d, want %d", got.DiscoveredInstallations, len(tc.assessment.Installations))
			}
		})
	}
}

// TestSetupStatusFreshHomeIsNoDiscoveryWithoutWrites is the manual API check
// from the FLO-407 verification: a fresh/empty state returns the wizard-start
// state, and computing it performs no filesystem writes (the state database is
// never created).
func TestSetupStatusFreshHomeIsNoDiscoveryWithoutWrites(t *testing.T) {
	home := t.TempDir()
	managerHome := filepath.Join(home, ".skills-manager")
	// Intentionally leave managerHome uncreated: a first-run user has nothing.

	ts := httptest.NewServer(newServeServer(managerHome))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/setup")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET setup status = %d, want 200", resp.StatusCode)
	}
	var got triageSetupStatus
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if got.State != setupStateNoDiscovery {
		t.Fatalf("state = %q, want %q", got.State, setupStateNoDiscovery)
	}
	if got.InventoryExists || got.ManagedExists || got.OpenActions {
		t.Fatalf("fresh-state signals should all be false: %#v", got)
	}
	if got.GeneratedAt == "" {
		t.Fatalf("generated_at should be set so the UI can show a next step")
	}
	if got.Home != managerHome {
		t.Fatalf("home = %q, want %q so the first-run wizard can show it", got.Home, managerHome)
	}
	// No filesystem write may occur while computing setup status.
	if _, err := os.Stat(filepath.Join(managerHome, "state.db")); !os.IsNotExist(err) {
		t.Fatalf("computing setup status created state.db (stat err=%v); it must be read-only", err)
	}
}

// TestOverviewFreshHomeWithoutWrites mirrors FLO-407 setup-status contract for
// GET /api/v1/overview (FLO-423).
func TestOverviewFreshHomeWithoutWrites(t *testing.T) {
	home := t.TempDir()
	managerHome := filepath.Join(home, ".skills-manager")

	ts := httptest.NewServer(newServeServer(managerHome))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/overview")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET overview = %d, want 200", resp.StatusCode)
	}
	var got triageOverview
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if got.Home != managerHome {
		t.Fatalf("home = %q, want %q", got.Home, managerHome)
	}
	if got.LibrarySkills != 0 || got.Projects != 0 || got.PendingUpdates != 0 || got.Unregistered != 0 {
		t.Fatalf("fresh overview counts should be zero: %#v", got)
	}
	if got.GeneratedAt == "" {
		t.Fatalf("generated_at should be set")
	}
	if _, err := os.Stat(managerHome); !os.IsNotExist(err) {
		t.Fatalf("GET overview created manager home %q (stat err=%v)", managerHome, err)
	}
}

func TestAssessmentFreshHomeWithoutWrites(t *testing.T) {
	home := t.TempDir()
	managerHome := filepath.Join(home, ".skills-manager")

	ts := httptest.NewServer(newServeServer(managerHome))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/assessment")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET assessment = %d, want 200", resp.StatusCode)
	}
	if _, err := os.Stat(managerHome); !os.IsNotExist(err) {
		t.Fatalf("GET assessment created manager home (stat err=%v)", err)
	}
}

func TestProjectsFreshHomeWithoutWrites(t *testing.T) {
	home := t.TempDir()
	managerHome := filepath.Join(home, ".skills-manager")

	ts := httptest.NewServer(newServeServer(managerHome))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET projects = %d, want 200", resp.StatusCode)
	}
	if _, err := os.Stat(managerHome); !os.IsNotExist(err) {
		t.Fatalf("GET projects created manager home (stat err=%v)", err)
	}
}

func TestMatrixFreshHomeWithoutWrites(t *testing.T) {
	home := t.TempDir()
	managerHome := filepath.Join(home, ".skills-manager")

	ts := httptest.NewServer(newServeServer(managerHome))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/matrix")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET matrix = %d, want 200", resp.StatusCode)
	}
	if _, err := os.Stat(managerHome); !os.IsNotExist(err) {
		t.Fatalf("GET matrix created manager home (stat err=%v)", err)
	}
}

// TestSetupStatusDiscoveredUnmanaged drives real discovery end-to-end and
// confirms the endpoint reports inventory that is discovered but not yet
// manager-owned.
func TestSetupStatusDiscoveredUnmanaged(t *testing.T) {
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

	// The read-only contract also applies to an existing database: computing
	// setup status must not write to state.db.
	dbPath := filepath.Join(managerHome, "state.db")
	dbHashBefore := fileSHA256(t, dbPath)

	ts := httptest.NewServer(newServeServer(managerHome))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/setup")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET setup status = %d, want 200", resp.StatusCode)
	}
	var got triageSetupStatus
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if dbHashAfter := fileSHA256(t, dbPath); dbHashAfter != dbHashBefore {
		t.Fatalf("computing setup status modified state.db; the read must be read-only")
	}
	if !got.InventoryExists || got.DiscoveredInstallations == 0 {
		t.Fatalf("inventory should exist after discover: %#v", got)
	}
	if got.ManagedExists || got.ManagerOwnedInstallations != 0 {
		t.Fatalf("freshly discovered skills should be unmanaged: %#v", got)
	}
	if got.State != setupStateDiscoveredUnmanaged {
		t.Fatalf("state = %q, want %q (%#v)", got.State, setupStateDiscoveredUnmanaged, got)
	}
}

// TestServeRunCLIDiscover verifies the setup wizard can start discovery through
// the guarded /api/v1/run endpoint (FLO-409).
func TestServeRunCLIDiscover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	managerHome := filepath.Join(home, ".skills-manager")
	t.Setenv("SKILLS_MANAGER_HOME", managerHome)
	writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")

	ts := httptest.NewServer(newServeServer(managerHome))
	defer ts.Close()
	token := serveSessionToken(t, ts.URL)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/run",
		bytes.NewReader([]byte(`{"args":["discover","--global"]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Skills-Manager-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover run status = %d, want 200", resp.StatusCode)
	}
	var run cliRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if run.ExitCode != ExitSuccess {
		t.Fatalf("discover exit = %d, stderr=%q", run.ExitCode, run.Stderr)
	}

	statusResp, err := http.Get(ts.URL + "/api/v1/setup")
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var got triageSetupStatus
	if err := json.NewDecoder(statusResp.Body).Decode(&got); err != nil {
		t.Fatalf("decode setup status: %v", err)
	}
	if !got.InventoryExists || got.DiscoveredInstallations == 0 {
		t.Fatalf("inventory should exist after UI discover run: %#v", got)
	}
}

func TestServeRunCLIDiscoverRejectsMutatingFlags(t *testing.T) {
	home := t.TempDir()
	ts := httptest.NewServer(newServeServer(filepath.Join(home, ".skills-manager")))
	defer ts.Close()
	token := serveSessionToken(t, ts.URL)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/run",
		bytes.NewReader([]byte(`{"args":["discover","--projects","/tmp","--save-project-roots"]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Skills-Manager-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mutating discover status = %d, want 400", resp.StatusCode)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
