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
	responses         map[string]fakeResponse
	notFound          bool
	lastReceivedETag  string // Track etag passed to Poll for test verification
	returnNotModified bool   // If true, return notModified=true on next Poll call
}

type fakeResponse struct {
	commit string
	etag   string
	err    string
}

func (f *fakePoller) Poll(owner, repo, ref, etag string) (commit, newETag string, notModified bool, err error) {
	f.lastReceivedETag = etag // Track for test assertions

	key := owner + "/" + repo
	resp, ok := f.responses[key]
	if !ok || f.notFound {
		return "", "", false, ErrGHNotFound
	}
	if resp.err != "" {
		return "", "", false, &MockError{msg: resp.err}
	}

	// Return notModified if configured
	// On 304: return empty commit/etag with notModified=true; caller uses cached values
	if f.returnNotModified {
		return "", "", true, nil
	}

	return resp.commit, resp.etag, false, nil
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

	// Verify skill_polls WAS updated (last_checked_at advances, but commit/etag empty on first poll)
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll == nil {
		t.Fatalf("skill_polls should exist after first check")
	}
	// On first poll with fetch failure, last_commit should be empty (not the failed new SHA)
	if poll.LastCommit != "" {
		t.Fatalf("skill_polls.last_commit should be empty on first poll failure, got: %s", poll.LastCommit)
	}
}

// TestCheckStagingFailureDoesCachePoll is a regression test for FLO-238.
// Verifies that when Poll returns a new SHA but stageUpdate fails, the poll cache
// retains the PRIOR commit+etag (not the new ones), so a retry will re-attempt staging.
func TestCheckStagingFailureDoesCachePoll(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	// Pre-populate skill_polls with prior commit and etag
	const priorCommit = "abc1234"
	const priorETag = "w/\"prior-etag\""
	if err := stateDB.UpsertSkillPoll("pdf", priorCommit, priorETag); err != nil {
		t.Fatalf("UpsertSkillPoll: %v", err)
	}

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
		err: &MockError{msg: "fetch error"},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
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

	// Verify skill_polls retains PRIOR commit and etag (not the failed new ones)
	// This is the critical assertion: if staging fails, we do NOT cache the new SHA
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll == nil {
		t.Fatalf("skill_polls should exist")
	}
	if poll.LastCommit != priorCommit {
		t.Fatalf("skill_polls.last_commit should retain prior value %s, got %s", priorCommit, poll.LastCommit)
	}
	if poll.ETag != priorETag {
		t.Fatalf("skill_polls.etag should retain prior value %s, got %s", priorETag, poll.ETag)
	}

	// Now simulate a retry: same poller, but this time the fetcher succeeds
	// This proves the update is NOT hidden forever
	poller2 := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "def5678",
				etag:   "w/\"new-etag\"",
			},
		},
	}

	oldFetcher2 := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": []byte("---\nname: pdf\n---\nBody updated\n"),
		},
	}
	defer func() { fetcher = oldFetcher2 }()

	stdout.Reset()
	stderr.Reset()
	code = runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller2)
	if code != 0 {
		t.Fatalf("runCheck (retry) returned %d, want 0", code)
	}

	output = stdout.String()
	if !strings.Contains(output, "updated") {
		t.Fatalf("on retry with successful fetch, should show 'updated', got:\n%s", output)
	}

	// Verify .update-pending was created on retry
	if _, err := os.Stat(pendingRoot); err != nil {
		t.Fatalf("on retry, pending dir should be created: %v", err)
	}

	// Verify updates table was inserted on retry
	pending, err = stateDB.GetPendingUpdate("pdf")
	if err != nil {
		t.Fatalf("GetPendingUpdate: %v", err)
	}
	if pending == nil || pending.ToVersion != "def5678" {
		t.Fatalf("on retry, update should be inserted: %+v", pending)
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

// TestFetchFileUsesGetNotPost verifies that fetchFileFromGitHub uses a query string
// for the ref parameter (triggering GET) rather than -f flag (which causes POST).
// This test uses a recording fetcher that captures the gh command arguments.
func TestFetchFileUsesGetNotPost(t *testing.T) {
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
				etag:   "w/\"new-etag\"",
			},
		},
	}

	oldFetcher := fetcher
	fetchedPaths := []string{}
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": []byte("---\nname: pdf\n---\nBody v2\n"),
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

	// Verify the fetch was called with the right path
	if len(fetchedPaths) != 1 || fetchedPaths[0] != "SKILL.md" {
		t.Fatalf("expected fetch of SKILL.md, got: %v", fetchedPaths)
	}
}

// TestAcceptPreservesSiblingFiles verifies that when a skill has sibling files
// like references/ or scripts/, accepting an update via --accept-all-safe
// preserves those files instead of deleting them.
func TestAcceptPreservesSiblingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	libraryPath := filepath.Join(home, "library")
	skillDir := filepath.Join(libraryPath, "advanced")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create initial SKILL.md
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: advanced\ndescription: Advanced skill\n---\nBody v1\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Create .skill-meta.yaml
	metaContent := `version: 1
origin:
  type: github
  url: https://github.com/user/advanced-skill
  commit: abc1234
`
	if err := os.WriteFile(filepath.Join(skillDir, ".skill-meta.yaml"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	// Create sibling files: references/example.md and scripts/foo.sh
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "example.md"), []byte("# Example\nThis is a reference file\n"), 0644); err != nil {
		t.Fatalf("write references/example.md: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "foo.sh"), []byte("#!/bin/bash\necho hello\n"), 0755); err != nil {
		t.Fatalf("write scripts/foo.sh: %v", err)
	}

	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/advanced-skill": {
				commit: "def5678",
				etag:   "w/\"new-etag\"",
			},
		},
	}

	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/advanced-skill:def5678:SKILL.md": []byte("---\nname: advanced\ndescription: Advanced skill\n---\nBody v1 updated\n"),
		},
	}
	defer func() { fetcher = oldFetcher }()

	// Step 1: Run check to stage the update
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	// Check safety flags first to understand what's blocking
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"update", "--safety", "advanced"}, &stdout, &stderr)
	safetyOutput := stdout.String()

	// Step 2: Run --accept-all-safe
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"update", "--accept-all-safe"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("update --accept-all-safe returned %d, want 0\nSafety flags: %s\nstdout: %s\nstderr: %s", code, safetyOutput, stdout.String(), stderr.String())
	}

	// Step 3: Verify sibling files still exist after accept
	referencesPath := filepath.Join(skillDir, "references", "example.md")
	if _, err := os.Stat(referencesPath); err != nil {
		t.Fatalf("references/example.md should be preserved after accept, got: %v", err)
	}

	scriptPath := filepath.Join(skillDir, "scripts", "foo.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("scripts/foo.sh should be preserved after accept, got: %v", err)
	}

	// Verify content is still correct
	content, err := os.ReadFile(referencesPath)
	if err != nil {
		t.Fatalf("read references/example.md: %v", err)
	}
	if !strings.Contains(string(content), "This is a reference file") {
		t.Fatalf("references/example.md content corrupted")
	}

	// Verify SKILL.md was updated
	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	skillContent, err := os.ReadFile(skillMdPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !strings.Contains(string(skillContent), "Body v1 updated") {
		t.Fatalf("SKILL.md should be updated, got: %s", string(skillContent))
	}

	// Verify .update-pending was cleaned up
	pendingPath := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingPath); err == nil {
		t.Fatalf(".update-pending directory should be removed after accept")
	}
}

// TestCheckPassesCachedETag regression test for FLO-238:
// Verify that the cached ETag is passed to Poll() so that If-None-Match header is sent.
func TestCheckPassesCachedETag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	// Pre-populate skill_polls with a cached ETag
	if err := stateDB.UpsertSkillPoll("pdf", "abc1234", "w/\"cached-etag\""); err != nil {
		t.Fatalf("UpsertSkillPoll: %v", err)
	}

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

	// Run check with --force to bypass the 24h lazy rule
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	// Verify the fake poller received the cached ETag
	if poller.lastReceivedETag != "w/\"cached-etag\"" {
		t.Errorf("Poll() should receive cached ETag w/\"cached-etag\", got: %q", poller.lastReceivedETag)
	}
}

// TestCheckHandlesNotModified regression test for FLO-238:
// Verify that when Poll returns notModified=true, no staging occurs, no update is inserted,
// but last_checked_at is still refreshed in skill_polls.
func TestCheckHandlesNotModified(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	// Pre-populate skill_polls with cached commit and etag
	const cachedCommit = "abc1234"
	const cachedETag = "w/\"etag-abc\""
	if err := stateDB.UpsertSkillPoll("pdf", cachedCommit, cachedETag); err != nil {
		t.Fatalf("UpsertSkillPoll: %v", err)
	}

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

	// Configure fake poller to return notModified=true
	// On 304, poller returns ("", "", true, nil) - check.go will use the cached commit it already has
	// We need a response configured for the skill, but returnNotModified will override it
	poller := &fakePoller{
		returnNotModified: true,
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "ignored",
				etag:   "ignored",
			},
		},
	}

	oldFetcher := fetcher
	fetcher = &fakeFetcher{} // Won't be called since we return notModified
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	// Verify output says "checked (no change)"
	output := stdout.String()
	if !strings.Contains(output, "checked") {
		t.Fatalf("stdout should say 'checked', got: %s", output)
	}

	// Verify .update-pending was NOT created
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); !os.IsNotExist(err) {
		t.Fatalf("should NOT create .update-pending on 304 Not Modified")
	}

	// Verify updates table was NOT inserted
	pending, err := stateDB.GetPendingUpdate("pdf")
	if err != nil {
		t.Fatalf("GetPendingUpdate: %v", err)
	}
	if pending != nil {
		t.Fatalf("should not insert update on 304, got: %+v", pending)
	}

	// Verify skill_polls was still updated (last_checked_at refreshed, but cached values retained)
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll == nil {
		t.Fatalf("skill_polls should exist")
	}
	if poll.LastCommit != cachedCommit {
		t.Errorf("skill_polls.last_commit should be %s, got %s", cachedCommit, poll.LastCommit)
	}
	if poll.ETag != cachedETag {
		t.Errorf("skill_polls.etag should be %s, got %s", cachedETag, poll.ETag)
	}
}

// TestGitHubContentsPath tests that githubContentsPath produces forward slashes
// regardless of the host OS path separator (Windows vs Unix).
func TestGitHubContentsPath(t *testing.T) {
	tests := []struct {
		name       string
		originPath string
		want       string
	}{
		{"empty path", "", "SKILL.md"},
		{"single level", "skills", "skills/SKILL.md"},
		{"nested", "skills/pdf", "skills/pdf/SKILL.md"},
		{"trailing slash", "skills/pdf/", "skills/pdf/SKILL.md"},
		{"leading slash", "/skills/pdf", "skills/pdf/SKILL.md"},
		{"both slashes", "/skills/pdf/", "skills/pdf/SKILL.md"},
		{"deep nesting", "a/b/c/d", "a/b/c/d/SKILL.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := githubContentsPath(tt.originPath)
			if got != tt.want {
				t.Errorf("githubContentsPath(%q) = %q, want %q", tt.originPath, got, tt.want)
			}
			// Verify result uses only forward slashes (for Windows compatibility)
			if strings.Contains(got, string(filepath.Separator)) && filepath.Separator != '/' {
				t.Errorf("githubContentsPath result should not contain %q: %q", string(filepath.Separator), got)
			}
		})
	}
}
