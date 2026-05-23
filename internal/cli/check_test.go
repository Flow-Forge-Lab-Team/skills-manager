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

// fakeFetcher is a mock GitHubFetcher for testing.
type fakeFetcher struct {
	files               map[string][]byte
	err                 error
	fetchedPathsForTest *[]string // optional: if set, tracks paths requested
}

func (f *fakeFetcher) FetchFile(owner, repo, ref, path string) ([]byte, error) {
	// Track the path for test verification
	if f.fetchedPathsForTest != nil {
		*f.fetchedPathsForTest = append(*f.fetchedPathsForTest, path)
	}
	if f.err != nil {
		return nil, f.err
	}
	key := owner + "/" + repo + ":" + ref + ":" + path
	if content, ok := f.files[key]; ok {
		return content, nil
	}
	return nil, &MockError{msg: "file not found"}
}

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

	// Create fake poller and fetcher
	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "def5678",
				etag:   "w/\"new-etag\"",
			},
		},
	}

	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": []byte("---\nname: pdf\ndescription: PDF tool\n---\nBody v2\n"),
		},
	}
	defer func() { fetcher = oldFetcher }()

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

	if _, err := os.Stat(filepath.Join(pendingRoot, "from-current", "SKILL.md")); err != nil {
		t.Fatalf("from-current/SKILL.md not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pendingRoot, "to-incoming", "SKILL.md")); err != nil {
		t.Fatalf("to-incoming/SKILL.md not created: %v", err)
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

	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": []byte("---\nname: pdf\n---\nBody updated\n"),
		},
	}
	defer func() { fetcher = oldFetcher }()

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

func TestCheckFetchFailureDoesNotStage(t *testing.T) {
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

	// Poller returns a new commit, but fetcher fails
	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "def5678",
				etag:   "w/\"new-etag\"",
			},
		},
	}

	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		err: &MockError{msg: "network error"},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	output := stdout.String()
	if !strings.Contains(output, "error") || !strings.Contains(output, "fetch failed") {
		t.Fatalf("stdout should indicate fetch error, got:\n%s", output)
	}

	// Verify .update-pending directory was NOT created
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); !os.IsNotExist(err) {
		t.Fatalf("should NOT create .update-pending on fetch error")
	}

	// Verify updates table was NOT inserted
	pending, err := stateDB.GetPendingUpdate("pdf")
	if err != nil {
		t.Fatalf("GetPendingUpdate: %v", err)
	}
	if pending != nil {
		t.Fatalf("should not have inserted update on fetch error, got: %+v", pending)
	}

	// Verify skill_polls WAS updated (last_checked_at advances)
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll == nil || poll.LastCommit != "def5678" {
		t.Fatalf("skill_polls should still be updated: %+v", poll)
	}
}

func TestCheckStagesSkillMeta(t *testing.T) {
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

	// Write SKILL.md
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\n---\nBody v1\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Write .skill-meta.yaml with old commit
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

	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": []byte("---\nname: pdf\n---\nBody v2\n"),
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Verify .skill-meta.yaml was staged in both snapshots
	fromMetaPath := filepath.Join(skillDir, ".update-pending", "from-current", ".skill-meta.yaml")
	if _, err := os.Stat(fromMetaPath); err != nil {
		t.Fatalf("from-current/.skill-meta.yaml not created: %v", err)
	}

	toMetaPath := filepath.Join(skillDir, ".update-pending", "to-incoming", ".skill-meta.yaml")
	if _, err := os.Stat(toMetaPath); err != nil {
		t.Fatalf("to-incoming/.skill-meta.yaml not created: %v", err)
	}

	// Verify to-incoming has origin.commit rewritten to new SHA using readSkillMeta
	// (not just grep, to catch YAML parsing issues)
	toMeta, err := readSkillMeta(toMetaPath)
	if err != nil {
		t.Fatalf("readSkillMeta to-incoming/.skill-meta.yaml: %v", err)
	}
	if toMeta.Origin.Commit != "def5678" {
		t.Fatalf("to-incoming origin.commit should be def5678, got: %s", toMeta.Origin.Commit)
	}

	// Verify from-current has original commit unchanged
	fromMeta, err := readSkillMeta(fromMetaPath)
	if err != nil {
		t.Fatalf("readSkillMeta from-current/.skill-meta.yaml: %v", err)
	}
	if fromMeta.Origin.Commit != "abc1234" {
		t.Fatalf("from-current origin.commit should be abc1234, got: %s", fromMeta.Origin.Commit)
	}
}

func TestCheckToIncomingMetaHasNewCommit(t *testing.T) {
	// Verify that when check stages a pending update, the to-incoming/.skill-meta.yaml
	// has the new commit SHA in origin.commit, so that applyPendingUpdate can advance it.
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

	// Initial SKILL.md and meta at commit abc1234
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\n---\nBody v1\n"), 0644); err != nil {
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
				etag:   "w/\"etag-1\"",
			},
		},
	}

	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": []byte("---\nname: pdf\n---\nBody v2\n"),
		},
	}
	defer func() { fetcher = oldFetcher }()

	// Check: stage update from abc1234 → def5678
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("check returned %d, want 0", code)
	}

	// Verify to-incoming/.skill-meta.yaml has the NEW commit (def5678)
	// so that when accept runs applyPendingUpdate, it reads this and advances origin.commit
	toMetaPath := filepath.Join(skillDir, ".update-pending", "to-incoming", ".skill-meta.yaml")
	toMeta, err := readSkillMeta(toMetaPath)
	if err != nil {
		t.Fatalf("readSkillMeta to-incoming: %v", err)
	}
	if toMeta.Origin.Commit != "def5678" {
		t.Fatalf("to-incoming origin.commit should be def5678, got: %s", toMeta.Origin.Commit)
	}
}

// TestAcceptAdvancesOriginCommit is a regression test for nested origin.commit rewrite.
// Verifies that after staging an update, accepting it (applyPendingUpdate) advances
// the live .skill-meta.yaml's origin.commit to the new SHA.
func TestAcceptAdvancesOriginCommit(t *testing.T) {
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

	// Initial state: skill at old commit
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\n---\nBody v1\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	metaContent := `version: 1
origin:
  type: github
  url: https://github.com/user/pdf-skill
  commit: abc1234
`
	liveMetaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	if err := os.WriteFile(liveMetaPath, []byte(metaContent), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "def5678",
				etag:   "w/\"etag-1\"",
			},
		},
	}

	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": []byte("---\nname: pdf\n---\nBody v2\n"),
		},
	}
	defer func() { fetcher = oldFetcher }()

	// Step 1: Run check to stage the update
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("check returned %d, want 0", code)
	}

	// Step 2: Apply the pending update (simulates --accept-all-safe)
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	pending := pendingUpdatePaths{
		Skill: "pdf",
		Root:  pendingRoot,
		From:  filepath.Join(pendingRoot, "from-current"),
		To:    filepath.Join(pendingRoot, "to-incoming"),
	}
	if err := applyPendingUpdate(pending); err != nil {
		t.Fatalf("applyPendingUpdate: %v", err)
	}

	// Step 3: Verify live .skill-meta.yaml now has the NEW commit (def5678)
	liveMeta, err := readSkillMeta(liveMetaPath)
	if err != nil {
		t.Fatalf("readSkillMeta live: %v", err)
	}
	if liveMeta.Origin.Commit != "def5678" {
		t.Fatalf("live origin.commit should be def5678 after accept, got: %s", liveMeta.Origin.Commit)
	}

	// Verify .update-pending was cleaned up
	pendingPath := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingPath); err == nil {
		t.Fatalf(".update-pending directory should be removed after accept")
	}
}

// TestUnsafeGitHubURL verifies that parseGitHubURL rejects URLs with injection attempts.
func TestUnsafeGitHubURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Safe cases
		{"basic", "https://github.com/owner/repo", false},
		{"hyphens", "https://github.com/my-owner/my-repo", false},
		{"underscores", "https://github.com/my_owner/my_repo", false},
		{"dots", "https://github.com/owner.name/repo.name", false},
		{"numbers", "https://github.com/owner123/repo456", false},

		// Unsafe cases (injection attempts)
		{"semicolon injection", "https://github.com/foo;rm -rf/bar", true},
		{"pipe injection", "https://github.com/foo|cat/bar", true},
		{"backtick injection", "https://github.com/foo`whoami`/bar", true},
		{"dollar injection", "https://github.com/foo$(whoami)/bar", true},
		{"space injection", "https://github.com/foo bar/baz", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := parseGitHubURL(tt.url)
			if tt.wantErr && err == nil {
				t.Errorf("parseGitHubURL(%q) should error, got owner=%q repo=%q", tt.url, owner, repo)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("parseGitHubURL(%q) should not error, got: %v", tt.url, err)
			}
		})
	}
}

// TestIsSafeGitHubSegment tests the segment validation helper.
func TestIsSafeGitHubSegment(t *testing.T) {
	tests := []struct {
		segment string
		safe    bool
	}{
		{"owner", true},
		{"repo-name", true},
		{"my_repo", true},
		{"repo.name", true},
		{"test123", true},
		{"", false},             // empty
		{"foo;bar", false},      // semicolon
		{"foo|bar", false},      // pipe
		{"foo$(whoami)", false}, // command substitution
		{"foo`cmd`", false},     // backticks
		{"foo bar", false},      // space
		{"foo/bar", false},      // slash
	}

	for _, tt := range tests {
		if got := isSafeGitHubSegment(tt.segment); got != tt.safe {
			t.Errorf("isSafeGitHubSegment(%q) = %v, want %v", tt.segment, got, tt.safe)
		}
	}
}

func TestCheckFetchesFromOriginPath(t *testing.T) {
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

	// Write SKILL.md
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\n---\nBody v1\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Write .skill-meta.yaml with origin.path set to skills/pdf
	metaContent := `version: 1
origin:
  type: github
  url: https://github.com/user/repo
  path: skills/pdf
  commit: abc1234
`
	if err := os.WriteFile(filepath.Join(skillDir, ".skill-meta.yaml"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	// Create fake poller and fetcher
	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/repo": {
				commit: "def5678",
				etag:   "w/\"new-etag\"",
			},
		},
	}

	oldFetcher := fetcher
	fetchedPaths := []string{}
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			// Should fetch from skills/pdf/SKILL.md, not SKILL.md
			"user/repo:def5678:skills/pdf/SKILL.md": []byte("---\nname: pdf\n---\nBody v2\n"),
		},
		fetchedPathsForTest: &fetchedPaths,
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)

	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Verify that the correct path was requested
	if len(fetchedPaths) == 0 {
		t.Fatal("no fetch was made")
	}
	if fetchedPaths[0] != "skills/pdf/SKILL.md" {
		t.Errorf("fetcher received path %q, want skills/pdf/SKILL.md", fetchedPaths[0])
	}
}

func TestCheckRejectsUnsafeOriginPath(t *testing.T) {
	tests := []struct {
		path string
		safe bool
	}{
		{"skills/pdf", true},
		{"my-skill", true},
		{"skills/my_skill", true},
		{"", true},                  // empty is allowed
		{"../../etc/passwd", false}, // directory traversal
		{"/etc/passwd", false},      // absolute path
		{"foo;rm -rf", false},       // shell metacharacter
		{"foo|bar", false},          // pipe
		{"foo bar", false},          // space
	}

	for _, tt := range tests {
		if got := isSafeGitHubPath(tt.path); got != tt.safe {
			t.Errorf("isSafeGitHubPath(%q) = %v, want %v", tt.path, got, tt.safe)
		}
	}
}
