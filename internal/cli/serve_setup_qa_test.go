package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupWizardFixtureMatrixAndLandingInvariant is the FLO-413 end-to-end QA
// matrix. It builds each of the four FLO-406 setup states (docs/SETUP_WIZARD.md)
// from isolated HOME fixtures and asserts, per state:
//
//   - the derived state returned by /api/v1/setup matches the fixture, and
//   - the fresh-user landing invariant holds: no_discovery and
//     discovered_unmanaged are "fresh" users routed to the setup wizard, while
//     partially_managed and completed are "returning" users routed to the
//     dashboard. A fresh user must never land on an empty Overview.
//
// The landing half is checked against the real client routing predicate
// (isFreshSetupState in the served app.js), not a table duplicated in this test,
// so a routing change in app.js that left /api/v1/setup unchanged still fails the
// invariant here.
//
// Single-state endpoint tests already cover no_discovery
// (TestSetupStatusFreshHomeIsNoDiscoveryWithoutWrites), discovered_unmanaged
// (TestSetupStatusDiscoveredUnmanaged), and completed (the apply-completion
// tests). This test ties all four into one matrix and adds the partially_managed
// endpoint case, which no other test exercises.
func TestSetupWizardFixtureMatrixAndLandingInvariant(t *testing.T) {
	cases := []struct {
		name       string
		wantState  string
		wantWizard bool // expected landing per the FLO-406 routing table
		build      func(t *testing.T) (managerHome string)
	}{
		{
			name:       "no_discovery enters the wizard",
			wantState:  setupStateNoDiscovery,
			wantWizard: true,
			build: func(t *testing.T) string {
				// A first-run user: nothing discovered, no managed library.
				return filepath.Join(t.TempDir(), ".skills-manager")
			},
		},
		{
			name:       "discovered_unmanaged enters the wizard",
			wantState:  setupStateDiscoveredUnmanaged,
			wantWizard: true,
			build: func(t *testing.T) string {
				home := t.TempDir()
				writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
				runDiscoverGlobalJSON(t, home) // inventory exists, but nothing managed yet
				return filepath.Join(home, ".skills-manager")
			},
		},
		{
			name:       "partially_managed lands on the dashboard",
			wantState:  setupStatePartiallyManaged,
			wantWizard: false,
			build: func(t *testing.T) string {
				home := t.TempDir()
				// An unmanaged skill on disk leaves an open recommendation (signal C).
				writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
				runDiscoverGlobalJSON(t, home)
				managerHome := filepath.Join(home, ".skills-manager")
				// The manager already owns a skill (signal B): setup began but the
				// open recommendation above means it is not finished.
				seedManagedLibrarySkill(t, managerHome, "owned")
				return managerHome
			},
		},
		{
			name:       "completed lands on the dashboard",
			wantState:  setupStateCompleted,
			wantWizard: false,
			build: func(t *testing.T) string {
				home := t.TempDir()
				writeScanSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\nname: review\n---\n# Review\n")
				runDiscoverGlobalJSON(t, home)
				managerHome := filepath.Join(home, ".skills-manager")
				// Resolve every recommendation so none stays open (signal C false).
				srv := newServeServer(managerHome)
				ts := httptest.NewServer(srv)
				defer ts.Close()
				for _, rec := range getTestAssessment(t, ts.URL).Recommendations {
					_ = postTestAction[triageActionReview](t, ts.URL, srv.token, "review", map[string]string{
						"recommendation_id": rec.RecommendationID,
						"status":            "ignored",
						"reason":            "deferred in setup wizard QA",
					})
				}
				return managerHome
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			managerHome := tc.build(t)
			ts := httptest.NewServer(newServeServer(managerHome))
			defer ts.Close()

			setup := getTestSetupStatus(t, ts.URL)
			if setup.State != tc.wantState {
				t.Fatalf("setup state = %q, want %q (%#v)", setup.State, tc.wantState, setup)
			}

			// Headline invariant, checked against the real client routing predicate
			// served as app.js: exactly the two fresh states route to the wizard, so
			// a fresh user is never sent to an empty Overview and a returning user is
			// never trapped in the wizard.
			fresh := freshSetupStatesFromServedAppJS(t, ts.URL)
			if !fresh[setupStateNoDiscovery] || !fresh[setupStateDiscoveredUnmanaged] ||
				fresh[setupStatePartiallyManaged] || fresh[setupStateCompleted] {
				t.Fatalf("served app.js routes fresh states = %v; want only no_discovery and discovered_unmanaged to the wizard", fresh)
			}
			// This fixture's actual derived state lands where the served routing
			// predicate sends it, matching the QA expectation.
			if fresh[setup.State] != tc.wantWizard {
				t.Fatalf("served routing sends state %q to wizard=%v, want %v", setup.State, fresh[setup.State], tc.wantWizard)
			}

			// Read-only contract (FLO-407): computing the no_discovery status must
			// not create state.db. The populated fixtures already wrote state.db via
			// discover; their read-only-ness is asserted by TestSetupStatus* tests.
			if tc.wantState == setupStateNoDiscovery {
				if _, err := os.Stat(filepath.Join(managerHome, "state.db")); !os.IsNotExist(err) {
					t.Fatalf("computing no_discovery status created state.db (err=%v); it must be read-only", err)
				}
			}
		})
	}
}

// freshSetupStatesFromServedAppJS extracts, from the app.js served by the running
// server, the set of setup states that the real client routing predicate
// (isFreshSetupState) sends to the wizard. The QA matrix asserts each fixture's
// landing against this served routing source rather than a table duplicated in
// the test, so a divergence between app.js routing and /api/v1/setup is caught.
func freshSetupStatesFromServedAppJS(t *testing.T, baseURL string) map[string]bool {
	t.Helper()
	resp, err := http.Get(baseURL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /app.js = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)

	const marker = "function isFreshSetupState"
	start := strings.Index(js, marker)
	if start < 0 {
		t.Fatalf("served app.js no longer defines %q; the client wizard-routing predicate moved or was renamed", marker)
	}
	open := strings.Index(js[start:], "{")
	end := strings.Index(js[start:], "}")
	if open < 0 || end < 0 || end < open {
		t.Fatalf("could not locate the isFreshSetupState body in served app.js")
	}
	predicate := js[start+open : start+end]

	fresh := map[string]bool{}
	for _, state := range []string{
		setupStateNoDiscovery, setupStateDiscoveredUnmanaged,
		setupStatePartiallyManaged, setupStateCompleted,
	} {
		if strings.Contains(predicate, `"`+state+`"`) {
			fresh[state] = true
		}
	}
	return fresh
}

// seedManagedLibrarySkill writes a minimal managed-library skill so countLibrarySkills
// reports a manager-owned skill (FLO-406 signal B) without running an install.
func seedManagedLibrarySkill(t *testing.T, managerHome, name string) {
	t.Helper()
	dir := filepath.Join(managerHome, "library", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
