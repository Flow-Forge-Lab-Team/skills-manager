package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// fakePoller is a mock GitHubPoller for testing.
type fakePoller struct {
	responses map[string]fakeResponse
	notFound  bool
}

type fakeResponse struct {
	commit string
	etag   string
	err    string
}

func (f *fakePoller) Poll(owner, repo, ref string) (commit, etag string, err error) {
	key := owner + "/" + repo
	resp, ok := f.responses[key]
	if !ok || f.notFound {
		return "", "", ErrGHNotFound
	}
	if resp.err != "" {
		return "", "", &MockError{msg: resp.err}
	}
	return resp.commit, resp.etag, nil
}

type MockError struct {
	msg string
}

func (e *MockError) Error() string {
	return e.msg
}

var ErrGHNotFound = &MockError{msg: "gh CLI not found"}

func TestCheckHappyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Set up state DB
	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	// Set up library with a GitHub-sourced skill
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "pdf")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write SKILL.md
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\ndescription: PDF tool\n---\nBody v1\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Write .skill-meta.yaml
	metaContent := `version: 1
origin:
  type: github
  url: https://github.com/user/pdf-skill
  commit: abc1234
`
	if err := os.WriteFile(filepath.Join(skillDir, ".skill-meta.yaml"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	// Create fake poller
	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "def5678",
				etag:   "w/\"new-etag\"",
			},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)

	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "pdf") || !strings.Contains(output, "updated") {
		t.Fatalf("stdout missing expected content:\n%s", output)
	}

	// Verify pending update was staged
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); err != nil {
		t.Fatalf("pending dir not created: %v", err)
	}

	if _, err := os.Stat(filepath.Join(pendingRoot, "from.md")); err != nil {
		t.Fatalf("from.md not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pendingRoot, "to.md")); err != nil {
		t.Fatalf("to.md not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pendingRoot, "meta.yaml")); err != nil {
		t.Fatalf("meta.yaml not created: %v", err)
	}

	// Verify updates table was inserted
	pending, err := stateDB.GetPendingUpdate("pdf")
	if err != nil {
		t.Fatalf("GetPendingUpdate: %v", err)
	}
	if pending == nil || pending.ToVersion != "def5678" {
		t.Fatalf("pending update not recorded properly: %+v", pending)
	}

	// Verify skill_polls was updated
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll == nil || poll.LastCommit != "def5678" || poll.ETag != "w/\"new-etag\"" {
		t.Fatalf("skill_polls not recorded: %+v", poll)
	}
}

func TestCheckLazy24h(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	// Set up state DB and pre-populate skill_polls
	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	// Populate skill_polls with a recent check
	if err := stateDB.UpsertSkillPoll("pdf", "abc1234", "w/\"old-etag\""); err != nil {
		t.Fatalf("UpsertSkillPoll: %v", err)
	}

	// Set up library
	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "pdf")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\n---\nBody\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	metaContent := `version: 1
origin:
  type: github
  url: https://github.com/user/pdf-skill
  commit: abc1234
`
	if err := os.WriteFile(filepath.Join(skillDir, ".skill-meta.yaml"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "def5678",
				etag:   "w/\"new-etag\"",
			},
		},
	}

	// First check: should skip due to recent last_checked_at
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	output := stdout.String()
	if !strings.Contains(output, "skipped") || !strings.Contains(output, "checked recently") {
		t.Fatalf("stdout should indicate skipped, got:\n%s", output)
	}

	// With --force, should poll anyway
	stdout.Reset()
	code = runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck --force returned %d, want 0", code)
	}

	output = stdout.String()
	if !strings.Contains(output, "updated") {
		t.Fatalf("with --force, should show update:\n%s", output)
	}
}

func TestCheckSkipsNonGitHub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "local-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: local\n---\nBody\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	metaContent := `version: 1
origin:
  type: local
`
	if err := os.WriteFile(filepath.Join(skillDir, ".skill-meta.yaml"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	poller := &fakePoller{notFound: true}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	output := stdout.String()
	if !strings.Contains(output, "skipped") || !strings.Contains(output, "not github-sourced") {
		t.Fatalf("should skip non-github skill, got:\n%s", output)
	}
}

func TestCheckNoChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "pdf")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\n---\nBody\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	metaContent := `version: 1
origin:
  type: github
  url: https://github.com/user/pdf-skill
  commit: abc1234
`
	if err := os.WriteFile(filepath.Join(skillDir, ".skill-meta.yaml"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	// Poller returns the same commit
	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "abc1234",
				etag:   "w/\"same-etag\"",
			},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	output := stdout.String()
	if !strings.Contains(output, "checked") || strings.Contains(output, "updated") {
		t.Fatalf("should show 'checked (no change)', got:\n%s", output)
	}

	// Verify no pending update was created
	pending, err := stateDB.GetPendingUpdate("pdf")
	if err != nil {
		t.Fatalf("GetPendingUpdate: %v", err)
	}
	if pending != nil {
		t.Fatalf("should not have created pending update when no change")
	}
}
