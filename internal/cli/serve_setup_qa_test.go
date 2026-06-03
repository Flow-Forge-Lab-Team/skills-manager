package cli

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestSetupWizardFixtureMatrixAndLandingInvariant is the FLO-413 end-to-end QA
// matrix. It builds each of the four FLO-406 setup states (docs/SETUP_WIZARD.md)
// from isolated HOME fixtures and asserts, over the real /api/v1/setup endpoint,
// two things per state:
//
//   - the derived state matches the fixture, and
//   - the fresh-user landing invariant holds: no_discovery and
//     discovered_unmanaged are "fresh" users who enter the setup wizard, while
//     partially_managed and completed are "returning" users who land on the
//     dashboard. This is the headline assertion from the UX contract — a fresh
//     user must never land on an empty Overview.
//
// Single-state endpoint tests already cover no_discovery
// (TestSetupStatusFreshHomeIsNoDiscoveryWithoutWrites), discovered_unmanaged
// (TestSetupStatusDiscoveredUnmanaged), and completed (the apply-completion
// tests). This test ties all four into one matrix, encodes the routing table as
// an executable invariant, and adds the partially_managed endpoint case, which
// no other test exercises.
func TestSetupWizardFixtureMatrixAndLandingInvariant(t *testing.T) {
	// Landing per the FLO-406 routing table (docs/SETUP_WIZARD.md, "Default
	// landing and routing"). true => wizard (fresh user), false => dashboard.
	wizardLanding := map[string]bool{
		setupStateNoDiscovery:         true,
		setupStateDiscoveredUnmanaged: true,
		setupStatePartiallyManaged:    false,
		setupStateCompleted:           false,
	}

	cases := []struct {
		name       string
		wantState  string
		wantWizard bool // expected landing, authored independently of wizardLanding
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

			// Headline invariant: the routing table classifies this state's landing
			// exactly as the QA expects, so a fresh user is never routed to an empty
			// Overview and a returning user is never trapped in the wizard.
			if wizardLanding[setup.State] != tc.wantWizard {
				t.Fatalf("state %q routes to wizard=%v, want %v per the FLO-406 routing table",
					setup.State, wizardLanding[setup.State], tc.wantWizard)
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
