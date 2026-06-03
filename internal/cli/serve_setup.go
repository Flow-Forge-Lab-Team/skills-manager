package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Setup states for the first-run onboarding wizard, per the FLO-406 UX contract
// (docs/SETUP_WIZARD.md). They are derived read-only from already-persisted
// state; computing them never writes to disk or runs an action.
const (
	setupStateNoDiscovery         = "no_discovery"
	setupStateDiscoveredUnmanaged = "discovered_unmanaged"
	setupStatePartiallyManaged    = "partially_managed"
	setupStateCompleted           = "completed"
)

// triageSetupStatus is the read-only setup-status model the web UI uses to
// decide whether to show the dashboard, an empty state, or the guided setup
// wizard. It exposes the four FLO-406 states, the three signals that derive
// them, and the counts that distinguish discovered skills from manager-owned or
// managed skills.
//
// Counts are installation-level (one installation is a skill in a specific tool
// and scope), except ManagedLibrarySkills, which counts skills in the managed
// library on disk.
type triageSetupStatus struct {
	State       string `json:"state"`
	GeneratedAt string `json:"generated_at"`

	// Signals from the FLO-406 contract.
	InventoryExists bool `json:"inventory_exists"` // A: a discovery snapshot is persisted
	ManagedExists   bool `json:"managed_exists"`   // B: the manager owns >=1 skill
	OpenActions     bool `json:"open_actions"`     // C: >=1 actionable recommendation still in review state "new"

	// Counts distinguishing discovered skills from manager-owned/managed skills.
	DiscoveredInstallations   int `json:"discovered_installations"`
	ManagerOwnedInstallations int `json:"manager_owned_installations"`
	ManagedLibrarySkills      int `json:"managed_library_skills"`
	OpenRecommendations       int `json:"open_recommendations"`

	// Error is set when the persisted assessment cannot be loaded. Per the
	// contract, a failed assessment is treated as no_discovery with an error
	// banner so the UI can show a helpful next step, not an empty success.
	Error string `json:"error,omitempty"`
}

func (s *serveServer) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Always 200: even the error case returns a structured no_discovery status
	// so the UI can render a helpful next step rather than a blank screen.
	writeJSONResponse(w, loadTriageSetupStatus(s.home))
}

// loadTriageSetupStatus derives the setup status read-only from persisted state.
// It never creates the state database, runs discovery, or applies an action. A
// fresh home with no persisted discovery snapshot is no_discovery, computed
// without any filesystem write.
func loadTriageSetupStatus(home string) triageSetupStatus {
	generatedAt := time.Now().UTC().Format(time.RFC3339)
	managedLibrarySkills := countLibrarySkills(filepath.Join(home, "library"))

	// Read-only short-circuit: opening the state DB would create it (and its
	// schema) on a fresh home, which the FLO-407 contract forbids. With no
	// persisted state there is nothing discovered yet.
	if !stateDatabaseExists(home) {
		return triageSetupStatus{
			State:                setupStateNoDiscovery,
			GeneratedAt:          generatedAt,
			ManagedLibrarySkills: managedLibrarySkills,
		}
	}

	assessment, err := loadTriageAssessment(home)
	if err != nil {
		return triageSetupStatus{
			State:                setupStateNoDiscovery,
			GeneratedAt:          generatedAt,
			ManagedLibrarySkills: managedLibrarySkills,
			Error:                err.Error(),
		}
	}

	status := setupStatusFromAssessment(assessment, managedLibrarySkills)
	status.GeneratedAt = generatedAt
	return status
}

// stateDatabaseExists reports whether the persisted state database is present,
// without opening it (opening would create it on a fresh home).
func stateDatabaseExists(home string) bool {
	_, err := os.Stat(filepath.Join(home, "state.db"))
	return err == nil
}

// setupStatusFromAssessment is the pure derivation of setup status from a loaded
// assessment and the managed-library skill count. It performs no I/O so each
// state can be unit-tested with fixture data.
func setupStatusFromAssessment(a triageAssessment, managedLibrarySkills int) triageSetupStatus {
	managerOwned := 0
	for _, inst := range a.Installations {
		if inst.Ownership == "manager" {
			managerOwned++
		}
	}

	reviewStatus := make(map[string]string, len(a.ActionReviews))
	for _, rv := range a.ActionReviews {
		reviewStatus[rv.RecommendationID] = rv.Status
	}
	openRecommendations := 0
	for _, rec := range a.Recommendations {
		if !isActionableRecommendation(rec.Kind) {
			continue
		}
		// A recommendation with no review row is implicitly "new".
		if status := reviewStatus[rec.RecommendationID]; status == "" || status == "new" {
			openRecommendations++
		}
	}

	// Signal A: a discovery snapshot is persisted (>=1 discovered installation).
	inventoryExists := len(a.Installations) > 0
	// Signal B: the manager owns >=1 skill (managed library and/or manager-owned install).
	managedExists := managedLibrarySkills > 0 || managerOwned > 0
	// Signal C: >=1 actionable recommendation is still in review state "new".
	openActions := openRecommendations > 0

	return triageSetupStatus{
		State:                     deriveSetupState(inventoryExists, managedExists, openActions),
		InventoryExists:           inventoryExists,
		ManagedExists:             managedExists,
		OpenActions:               openActions,
		DiscoveredInstallations:   len(a.Installations),
		ManagerOwnedInstallations: managerOwned,
		ManagedLibrarySkills:      managedLibrarySkills,
		OpenRecommendations:       openRecommendations,
	}
}

// deriveSetupState maps the three FLO-406 signals to a setup state:
//
//	A=false                  -> no_discovery
//	A=true, C=false          -> completed              (B is don't-care)
//	A=true, B=false, C=true  -> discovered_unmanaged
//	A=true, B=true,  C=true  -> partially_managed
func deriveSetupState(inventoryExists, managedExists, openActions bool) string {
	switch {
	case !inventoryExists:
		return setupStateNoDiscovery
	case !openActions:
		return setupStateCompleted
	case !managedExists:
		return setupStateDiscoveredUnmanaged
	default:
		return setupStatePartiallyManaged
	}
}

// isActionableRecommendation reports whether a recommendation kind requires a
// user decision toward completing setup. Every kind is actionable except
// "ignore", which the recommendation engine emits as an explicitly-ignorable
// suggestion that requires no filesystem action (the contract's "only
// ignorable" case). Unknown kinds are treated as actionable so setup is never
// silently marked complete.
func isActionableRecommendation(kind string) bool {
	return kind != "ignore"
}
