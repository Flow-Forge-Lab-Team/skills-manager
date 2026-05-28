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
	tree                map[string][]TreeEntry // keyed by "owner/repo:ref:subPath"
	err                 error
	treeErr             error
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

func (f *fakeFetcher) FetchTree(owner, repo, ref, subPath string) ([]TreeEntry, error) {
	if f.treeErr != nil {
		return nil, f.treeErr
	}
	key := owner + "/" + repo + ":" + ref + ":" + subPath
	if tree, ok := f.tree[key]; ok {
		return tree, nil
	}
	// Return empty tree if not found
	return []TreeEntry{}, nil
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

	newSKILLContent := []byte("---\nname: pdf\ndescription: PDF tool\n---\nBody v2\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody updated\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
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

func TestCheckSkipsPinnedSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	stateDB, err := state.Open(home)
	if err != nil {
		t.Fatalf("Open state: %v", err)
	}
	defer stateDB.Close()

	skillDir := filepath.Join(home, "library", "pdf")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\ndescription: PDF tool\n---\nBody v1\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	// GitHub-sourced but pinned: check must not stage an update past the pin.
	metaContent := `version: 1
pinned: abc1234
origin:
  type: github
  url: https://github.com/user/pdf-skill
  commit: abc1234
`
	if err := os.WriteFile(filepath.Join(skillDir, ".skill-meta.yaml"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	poller := &fakePoller{responses: map[string]fakeResponse{
		"user/pdf-skill": {commit: "def5678", etag: "w/\"new-etag\""},
	}}

	var stdout, stderr bytes.Buffer
	code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "pinned") {
		t.Fatalf("expected pinned skip in output:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(skillDir, ".update-pending")); !os.IsNotExist(err) {
		t.Fatalf("pinned skill must not stage a pending update, err=%v", err)
	}
	if pending, _ := stateDB.GetPendingUpdate("pdf"); pending != nil {
		t.Fatalf("pinned skill must not record a pending update: %+v", pending)
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody updated\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		err: &MockError{msg: "network error"},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
		},
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody updated\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		err: &MockError{msg: "fetch error"},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
		},
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

	retrySKILLContent := []byte("---\nname: pdf\n---\nBody updated\n")
	oldFetcher2 := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": retrySKILLContent,
		},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(retrySKILLContent), Type: "blob"},
			},
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
	oldFetcher := fetcher
	fetchedPaths := []string{}
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			// Should fetch from skills/pdf/SKILL.md, not SKILL.md
			"user/repo:def5678:skills/pdf/SKILL.md": newSKILLContent,
		},
		fetchedPathsForTest: &fetchedPaths,
		tree: map[string][]TreeEntry{
			"user/repo:def5678:skills/pdf": {
				{Path: "skills/pdf/SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
		},
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
	oldFetcher := fetcher
	fetchedPaths := []string{}
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		fetchedPathsForTest: &fetchedPaths,
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
		},
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

	newSKILLContent := []byte("---\nname: advanced\ndescription: Advanced skill\n---\nBody v1 updated\n")
	oldSKILLContent := []byte("---\nname: advanced\ndescription: Advanced skill\n---\nBody v1\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/advanced-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			// Old upstream tree (at installed commit abc1234): sibling files present.
			"user/advanced-skill:abc1234:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(oldSKILLContent), Type: "blob"},
				{Path: "references/example.md", SHA: gitBlobSHA([]byte("# Example\nThis is a reference file\n")), Type: "blob"},
				{Path: "scripts/foo.sh", SHA: gitBlobSHA([]byte("#!/bin/bash\necho hello\n")), Type: "blob"},
			},
			// New upstream tree (at new commit def5678): sibling files unchanged.
			"user/advanced-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
				{Path: "references/example.md", SHA: gitBlobSHA([]byte("# Example\nThis is a reference file\n")), Type: "blob"},
				{Path: "scripts/foo.sh", SHA: gitBlobSHA([]byte("#!/bin/bash\necho hello\n")), Type: "blob"},
			},
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

// TestCheckDetectsMultiFileUpstreamChange tests that multi-file upstream changes
// are rejected and reported as errors, without staging or advancing last_commit.
func TestCheckDetectsMultiFileUpstreamChange(t *testing.T) {
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

	// Write initial SKILL.md
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

	// Create a sibling file locally
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "foo.md"), []byte("Local reference\n"), 0644); err != nil {
		t.Fatalf("write references/foo.md: %v", err)
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
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				// Upstream has both SKILL.md and a different references/foo.md
				{Path: "SKILL.md", SHA: gitBlobSHA([]byte("---\nname: pdf\n---\nBody v2\n")), Type: "blob"},
				{Path: "references/foo.md", SHA: gitBlobSHA([]byte("Different reference\n")), Type: "blob"},
			},
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	output := stdout.String()
	if !strings.Contains(output, "error") || !strings.Contains(output, "multi-file upstream") {
		t.Fatalf("stdout should indicate multi-file error, got:\n%s", output)
	}

	// Verify .update-pending was NOT created
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); !os.IsNotExist(err) {
		t.Fatalf("should NOT create .update-pending on multi-file change")
	}

	// Verify updates table was NOT inserted
	pending, err := stateDB.GetPendingUpdate("pdf")
	if err != nil {
		t.Fatalf("GetPendingUpdate: %v", err)
	}
	if pending != nil {
		t.Fatalf("should not insert update on multi-file change")
	}

	// Verify skill_polls.last_commit was NOT advanced
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll != nil && poll.LastCommit != "" {
		t.Fatalf("skill_polls.last_commit should not be advanced on multi-file change, got: %s", poll.LastCommit)
	}
}

// TestCheckAcceptsSingleFileChange tests that if only SKILL.md differs,
// the update proceeds normally (no multi-file error).
func TestCheckAcceptsSingleFileChange(t *testing.T) {
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
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
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				// Only SKILL.md in upstream
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "updated") {
		t.Fatalf("stdout should show 'updated', got:\n%s", output)
	}

	// Verify .update-pending WAS created
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); err != nil {
		t.Fatalf("pending dir should be created: %v", err)
	}

	// Verify updates table WAS inserted
	pending, err := stateDB.GetPendingUpdate("pdf")
	if err != nil {
		t.Fatalf("GetPendingUpdate: %v", err)
	}
	if pending == nil || pending.ToVersion != "def5678" {
		t.Fatalf("update should be inserted, got: %+v", pending)
	}

	// Verify skill_polls WAS updated
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll == nil || poll.LastCommit != "def5678" {
		t.Fatalf("skill_polls.last_commit should be advanced to def5678, got: %+v", poll)
	}
}

// TestCheckWhitelistsLocalFiles tests that local-only files (.skill-meta.yaml, .update-pending, etc.)
// don't count as divergence when they exist in live but not upstream.
func TestCheckWhitelistsLocalFiles(t *testing.T) {
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

	// Create local-only manager-generated/whitelisted dotfiles that shouldn't count as divergence.
	// .gitignore is explicitly whitelisted — its presence in live but not upstream is not a deletion.
	if err := os.WriteFile(filepath.Join(skillDir, ".gitignore"), []byte("*.swp\n"), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
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
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				// Upstream only has SKILL.md, no .gitignore or .skill-meta.yaml
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "updated") {
		t.Fatalf("stdout should show 'updated' (local files whitelisted), got:\n%s", output)
	}

	// Verify staging proceeded
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); err != nil {
		t.Fatalf("pending dir should be created: %v", err)
	}
}

// TestCheckRejectsUpstreamDeletion verifies that upstream deletions of non-SKILL files are detected.
// Regression test for: upstream deletes a file (e.g., references/foo.md) but live still has it →
// reject with multi-file error, no staging.
func TestCheckRejectsUpstreamDeletion(t *testing.T) {
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

	// Write initial SKILL.md
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

	// Create a references/foo.md file locally — the exact case codex flagged.
	// references/ is NOT whitelisted: it's real upstream content for many skills
	// (e.g. Anthropic's pdf skill), so upstream deleting it must be detected.
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "foo.md"), []byte("reference content"), 0644); err != nil {
		t.Fatalf("write references/foo.md: %v", err)
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
		tree: map[string][]TreeEntry{
			// Old upstream tree (at installed commit abc1234): references/foo.md was present.
			"user/pdf-skill:abc1234:": {
				{Path: "SKILL.md", SHA: gitBlobSHA([]byte("---\nname: pdf\n---\nBody v1\n")), Type: "blob"},
				{Path: "references/foo.md", SHA: gitBlobSHA([]byte("reference content")), Type: "blob"},
			},
			// New upstream tree (at new commit def5678): references/foo.md deleted upstream.
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA([]byte("---\nname: pdf\n---\nBody v2\n")), Type: "blob"},
			},
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	output := stdout.String()
	if !strings.Contains(output, "error") || !strings.Contains(output, "multi-file") || !strings.Contains(output, "upstream") {
		t.Fatalf("stdout should indicate multi-file error due to upstream deletion, got:\n%s", output)
	}

	// Verify .update-pending was NOT created
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); !os.IsNotExist(err) {
		t.Fatalf("should NOT create .update-pending when upstream deletes files")
	}

	// Verify origin.commit was not advanced
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read updated meta: %v", err)
	}
	if strings.Contains(string(data), "def5678") {
		t.Fatalf("origin.commit should NOT advance on multi-file deletion, but it did")
	}
}

// TestCheckSkipsNoOpUpdate verifies that when upstream commit advances but SKILL.md blob
// SHA is identical to live, the check skips staging (no .update-pending, no updates row)
// but still advances skill_polls to the new commit and etag.
func TestCheckSkipsNoOpUpdate(t *testing.T) {
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

	// Live SKILL.md with specific content
	liveContent := []byte("---\nname: pdf\ndescription: PDF tool\n---\nBody unchanged\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), liveContent, 0644); err != nil {
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

	// Poller returns a new commit SHA
	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {
				commit: "def5678",
				etag:   "w/\"new-etag\"",
			},
		},
	}

	// Upstream tree has SKILL.md with the same blob SHA as live — commit advanced but content unchanged
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		// FetchFile should NOT be called (no staging)
		tree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(liveContent), Type: "blob"},
			},
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	output := stdout.String()
	// Should report "no change" (not "updated")
	if !strings.Contains(output, "no change") {
		t.Fatalf("stdout should contain 'no change', got:\n%s", output)
	}
	if strings.Contains(output, "updated") {
		t.Fatalf("stdout should NOT contain 'updated' for no-op, got:\n%s", output)
	}

	// .update-pending must NOT be created
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); !os.IsNotExist(err) {
		t.Fatalf("should NOT create .update-pending when SKILL.md is unchanged")
	}

	// updates table must NOT have a row
	pending, err := stateDB.GetPendingUpdate("pdf")
	if err != nil {
		t.Fatalf("GetPendingUpdate: %v", err)
	}
	if pending != nil {
		t.Fatalf("should not insert update when SKILL.md is unchanged, got: %+v", pending)
	}

	// skill_polls MUST be advanced to the new commit and etag
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll == nil {
		t.Fatalf("skill_polls should exist after check")
	}
	if poll.LastCommit != "def5678" {
		t.Fatalf("skill_polls.last_commit should be advanced to def5678, got: %s", poll.LastCommit)
	}
	if poll.ETag != "w/\"new-etag\"" {
		t.Fatalf("skill_polls.etag should be updated to new-etag, got: %s", poll.ETag)
	}
}

// TestCheckAllowsSkillMdUpdateWithLocalCompanionEdit is the regression case for the
// codex pass-12 finding: a user has hand-edited a companion file locally (references/foo.md)
// but upstream only changed SKILL.md. The old live-comparison algorithm would see the local
// edit as a SHA mismatch and reject the update as a multi-file change. The new tree-vs-tree
// algorithm correctly ignores local edits — only upstream changes matter.
func TestCheckAllowsSkillMdUpdateWithLocalCompanionEdit(t *testing.T) {
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

	// Live state: SKILL.md at old content, references/foo.md locally edited (different from upstream).
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: pdf\n---\nBody v1\n"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	// Locally edited companion file — differs from the upstream SHA.
	if err := os.WriteFile(filepath.Join(skillDir, "references", "foo.md"), []byte("locally edited content\n"), 0644); err != nil {
		t.Fatalf("write references/foo.md: %v", err)
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			// Old upstream tree: SKILL.md=A, references/foo.md=X (original upstream content).
			"user/pdf-skill:abc1234:": {
				{Path: "SKILL.md", SHA: gitBlobSHA([]byte("---\nname: pdf\n---\nBody v1\n")), Type: "blob"},
				{Path: "references/foo.md", SHA: gitBlobSHA([]byte("upstream original content\n")), Type: "blob"},
			},
			// New upstream tree: SKILL.md=B, references/foo.md=X (unchanged upstream).
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
				{Path: "references/foo.md", SHA: gitBlobSHA([]byte("upstream original content\n")), Type: "blob"},
			},
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	output := stdout.String()
	// Must NOT reject as multi-file: the companion edit is local, upstream only changed SKILL.md.
	if strings.Contains(output, "multi-file") {
		t.Fatalf("should NOT reject as multi-file when companion file is only locally edited, got:\n%s", output)
	}
	if !strings.Contains(output, "updated") {
		t.Fatalf("stdout should show 'updated', got:\n%s", output)
	}

	// Verify staging proceeded
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); err != nil {
		t.Fatalf("pending dir should be created: %v", err)
	}
}

// TestCheckRejectsRealUpstreamMultiFile verifies that when upstream genuinely modifies a
// companion file (different SHA in old vs new upstream tree), the check is rejected.
func TestCheckRejectsRealUpstreamMultiFile(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0755); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "foo.md"), []byte("original upstream content\n"), 0644); err != nil {
		t.Fatalf("write references/foo.md: %v", err)
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")
	oldFetcher := fetcher
	fetcher = &fakeFetcher{
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		tree: map[string][]TreeEntry{
			// Old upstream tree: SKILL.md=A, references/foo.md=X.
			"user/pdf-skill:abc1234:": {
				{Path: "SKILL.md", SHA: gitBlobSHA([]byte("---\nname: pdf\n---\nBody v1\n")), Type: "blob"},
				{Path: "references/foo.md", SHA: gitBlobSHA([]byte("original upstream content\n")), Type: "blob"},
			},
			// New upstream tree: SKILL.md=B, references/foo.md=Y (upstream modified it).
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
				{Path: "references/foo.md", SHA: gitBlobSHA([]byte("upstream modified content\n")), Type: "blob"},
			},
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0", code)
	}

	output := stdout.String()
	if !strings.Contains(output, "multi-file") {
		t.Fatalf("stdout should indicate multi-file error for real upstream change, got:\n%s", output)
	}

	// Verify .update-pending was NOT created
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); !os.IsNotExist(err) {
		t.Fatalf("should NOT create .update-pending on real upstream multi-file change")
	}
}

// TestCheckFallsBackToLiveWhenOldTreeMissing verifies that when the old upstream tree
// cannot be fetched (e.g. old commit was force-pushed away), the check falls back to
// live-comparison rather than crashing or silently skipping detection.
func TestCheckFallsBackToLiveWhenOldTreeMissing(t *testing.T) {
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

	newSKILLContent := []byte("---\nname: pdf\n---\nBody v2\n")

	// Use a fetcher that errors on old-tree fetch but succeeds for the new tree and file.
	// We implement this with a custom fakeFetcher that tracks which ref is requested.
	oldFetcher := fetcher
	fetcher = &treeErrorOnOldCommitFetcher{
		oldCommit: "abc1234",
		files: map[string][]byte{
			"user/pdf-skill:def5678:SKILL.md": newSKILLContent,
		},
		newTree: map[string][]TreeEntry{
			"user/pdf-skill:def5678:": {
				{Path: "SKILL.md", SHA: gitBlobSHA(newSKILLContent), Type: "blob"},
			},
		},
	}
	defer func() { fetcher = oldFetcher }()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCheckWithPoller([]string{"--force"}, &stdout, &stderr, globalFlags{}, poller)
	if code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}

	output := stdout.String()
	// Should warn about fallback
	if !strings.Contains(output, "warn") || !strings.Contains(output, "old tree unavailable") {
		t.Fatalf("stdout should contain fallback warning, got:\n%s", output)
	}
	// Detection still ran (live-comparison fallback); only SKILL.md differs, so update proceeds.
	if !strings.Contains(output, "updated") {
		t.Fatalf("stdout should show 'updated' after fallback succeeds, got:\n%s", output)
	}

	// Verify staging proceeded despite fallback
	pendingRoot := filepath.Join(skillDir, ".update-pending")
	if _, err := os.Stat(pendingRoot); err != nil {
		t.Fatalf("pending dir should be created: %v", err)
	}
}

// treeErrorOnOldCommitFetcher is a helper that returns an error when FetchTree is called
// with the old commit ref, simulating a force-push that removed the old commit.
type treeErrorOnOldCommitFetcher struct {
	oldCommit string
	files     map[string][]byte
	newTree   map[string][]TreeEntry
}

func (f *treeErrorOnOldCommitFetcher) FetchFile(owner, repo, ref, path string) ([]byte, error) {
	key := owner + "/" + repo + ":" + ref + ":" + path
	if content, ok := f.files[key]; ok {
		return content, nil
	}
	return nil, &MockError{msg: "file not found"}
}

func (f *treeErrorOnOldCommitFetcher) FetchTree(owner, repo, ref, subPath string) ([]TreeEntry, error) {
	if ref == f.oldCommit {
		return nil, &MockError{msg: "old commit not found (force-pushed away)"}
	}
	key := owner + "/" + repo + ":" + ref + ":" + subPath
	if tree, ok := f.newTree[key]; ok {
		return tree, nil
	}
	return []TreeEntry{}, nil
}

// TestCheckPollFailureDoesNotSuppressRetry regression test for codex pass-13:
// when Poll returns an error (gh missing, auth expired, network down, etc.),
// skill_polls.last_checked_at must NOT be updated, so the next `check` will
// retry instead of getting suppressed by the 24h lazy-skip gate.
func TestCheckPollFailureDoesNotSuppressRetry(t *testing.T) {
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

	// First check: poll fails (network down, gh missing, etc.)
	poller := &fakePoller{
		responses: map[string]fakeResponse{
			"user/pdf-skill": {err: "network unreachable"},
		},
	}

	var stdout, stderr bytes.Buffer
	if code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller); code != 0 {
		t.Fatalf("runCheck returned %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "error") {
		t.Fatalf("expected error in output, got: %s", stdout.String())
	}

	// Verify skill_polls row was NOT created — leaving last_checked_at unset
	// means the next check won't be suppressed by the 24h lazy gate.
	poll, err := stateDB.GetSkillPoll("pdf")
	if err != nil {
		t.Fatalf("GetSkillPoll: %v", err)
	}
	if poll != nil && poll.LastCheckedAt != "" {
		t.Fatalf("skill_polls.last_checked_at must remain unset after poll failure, got %q", poll.LastCheckedAt)
	}

	// Second check: a normal rerun (no --force). Must retry, not skip.
	stdout.Reset()
	stderr.Reset()
	if code := runCheckWithPoller(nil, &stdout, &stderr, globalFlags{}, poller); code != 0 {
		t.Fatalf("second runCheck returned %d, want 0", code)
	}
	if strings.Contains(stdout.String(), "skipped") {
		t.Fatalf("second check must retry, not skip via 24h gate; got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "error") {
		t.Fatalf("second check should re-attempt and re-report the error, got: %s", stdout.String())
	}
}
