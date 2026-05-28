package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeCrossMachineView(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	t.Setenv("SKILLS_MANAGER_MACHINE", "alpha")

	// init-library --local-only gives a real git repo + .machines.yaml.
	var out, errb bytes.Buffer
	if code := Run([]string{"init-library", "--local-only"}, &out, &errb); code != ExitSuccess {
		t.Fatalf("init-library returned %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	libraryPath := filepath.Join(home, "library")

	// Register a second machine.
	writeFile(t, filepath.Join(libraryPath, ".machines.yaml"), `version: 1
machines:
  alpha:
    last_synced: 2026-05-24T10:00:00Z
    last_commit: abc123
  beta:
    last_synced: 2026-05-24T09:00:00Z
    last_commit: def456
`)
	// Catalog has "present" but not "absent".
	writeFile(t, filepath.Join(libraryPath, "present", "SKILL.md"), "---\nname: present\n---\n")
	cat := catalog{Version: 1, Skills: []catalogSkill{{Name: "present"}}}
	if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
		t.Fatal(err)
	}
	// A project locks both "present" and the missing "absent".
	projectPath := filepath.Join(home, "projects", "proj-a")
	writeFile(t, filepath.Join(projectPath, ".skills", "installed.lock"), "version: 1\nskills:\n  - name: present\n  - name: absent\n")
	mraw, _ := json.Marshal(installManifest{Version: 1, ProjectPath: projectPath, ProjectSlug: "proj-a", ManagedPaths: []string{".claude/skills/present"}})
	writeFile(t, filepath.Join(home, "manifests", "proj-a.json"), string(mraw))

	srv := newServeServer(home)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/machines")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var cm triageCrossMachine
	if err := json.NewDecoder(resp.Body).Decode(&cm); err != nil {
		t.Fatal(err)
	}
	if !cm.IsGitRepo {
		t.Fatal("expected is_git_repo true after init-library")
	}
	if cm.CurrentMachine != "alpha" {
		t.Fatalf("current_machine = %q, want alpha", cm.CurrentMachine)
	}
	names := map[string]triageMachine{}
	for _, m := range cm.Machines {
		names[m.Name] = m
	}
	if _, ok := names["alpha"]; !ok {
		t.Fatalf("alpha missing from machines: %+v", cm.Machines)
	}
	if !names["alpha"].Current {
		t.Fatal("alpha should be flagged current")
	}
	if _, ok := names["beta"]; !ok {
		t.Fatalf("beta missing from machines: %+v", cm.Machines)
	}
	// "absent" is locked by proj-a but not in the catalog → flagged missing.
	var foundMissing bool
	for _, m := range cm.MissingLockedSkills {
		if m.Project == "proj-a" && triageContainsString(m.Skills, "absent") {
			foundMissing = true
		}
		if triageContainsString(m.Skills, "present") {
			t.Fatalf("present should not be flagged missing: %+v", m)
		}
	}
	if !foundMissing {
		t.Fatalf("expected proj-a to flag missing locked skill 'absent': %+v", cm.MissingLockedSkills)
	}
}

func TestServeSettingsGetAndPatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	srv := newServeServer(home)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// GET defaults.
	resp, err := http.Get(ts.URL + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	var s triageSettings
	json.NewDecoder(resp.Body).Decode(&s)
	resp.Body.Close()
	if s.UpdateFrequencyHours != defaultUpdateFrequencyHours {
		t.Fatalf("default update frequency = %d, want %d", s.UpdateFrequencyHours, defaultUpdateFrequencyHours)
	}

	// PATCH without token is rejected.
	noTok, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/settings", strings.NewReader(`{"mode":"symlink"}`))
	r0, _ := http.DefaultClient.Do(noTok)
	if r0.StatusCode != http.StatusForbidden {
		t.Fatalf("patch without token = %d, want 403", r0.StatusCode)
	}
	r0.Body.Close()

	// PATCH mode + frequency.
	sess := serveSessionToken(t, ts.URL)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/settings", strings.NewReader(`{"mode":"symlink","update_frequency_hours":6}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Skills-Manager-Token", sess)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != http.StatusOK {
		t.Fatalf("patch = %d, want 200", r.StatusCode)
	}
	var updated triageSettings
	json.NewDecoder(r.Body).Decode(&updated)
	r.Body.Close()
	if updated.Mode != "symlink" || updated.UpdateFrequencyHours != 6 {
		t.Fatalf("patched settings = %+v, want mode=symlink freq=6", updated)
	}

	// An invalid value is rejected with 400.
	bad, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/settings", strings.NewReader(`{"mode":"nonsense"}`))
	bad.Header.Set("X-Skills-Manager-Token", sess)
	rb, _ := http.DefaultClient.Do(bad)
	if rb.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid mode patch = %d, want 400", rb.StatusCode)
	}
	rb.Body.Close()
}
