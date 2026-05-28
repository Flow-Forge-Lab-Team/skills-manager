package cli

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// TreeEntry represents a single entry in a GitHub tree.
type TreeEntry struct {
	Path string // Path relative to repo root
	SHA  string // Git blob/tree SHA
	Type string // "blob" or "tree"
}

// GitHubFetcher is an interface for fetching files and trees from GitHub.
// It can be overridden in tests.
type GitHubFetcher interface {
	FetchFile(owner, repo, ref, path string) ([]byte, error)
	FetchTree(owner, repo, ref, subPath string) ([]TreeEntry, error)
}

// defaultFetcher implements GitHubFetcher using gh CLI.
type defaultFetcher struct{}

func (f *defaultFetcher) FetchFile(owner, repo, ref, path string) ([]byte, error) {
	return fetchFileFromGitHub(owner, repo, ref, path)
}

func (f *defaultFetcher) FetchTree(owner, repo, ref, subPath string) ([]TreeEntry, error) {
	return fetchTreeFromGitHub(owner, repo, ref, subPath)
}

var fetcher GitHubFetcher = &defaultFetcher{}

type checkResult struct {
	Skill  string `json:"skill"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func runCheck(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	return runCheckWithPoller(args, stdout, stderr, gf, newGHPoller())
}

func runCheckWithPoller(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags, poller GitHubPoller) int {
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	stateDB, err := state.Open(home)
	if err != nil {
		fmt.Fprintf(stderr, "open state: %v\n", err)
		return ExitOpError
	}
	defer stateDB.Close()

	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return ExitOpError
	}

	// Parse flags
	var skillFilter string
	var forceCheck bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--skill":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage: skills-manager check [--skill <name>] [--force]")
				return ExitUsageError
			}
			skillFilter = args[i+1]
			i++
		case "--force":
			forceCheck = true
		}
	}

	// Read library and determine which skills to check
	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		fmt.Fprintf(stderr, "read library: %v\n", err)
		return ExitOpError
	}

	var results []checkResult
	outWriter := gf.outWriter(stdout)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillName := entry.Name()

		// Filter by --skill if provided
		if skillFilter != "" && skillName != skillFilter {
			continue
		}

		// Read skill metadata to check origin
		metaPath := filepath.Join(libraryPath, skillName, ".skill-meta.yaml")
		meta, err := readSkillMeta(metaPath)
		if err != nil {
			fmt.Fprintf(outWriter, "%s: error (metadata read)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  err.Error(),
			})
			continue
		}

		// Only poll GitHub-sourced skills
		if meta.Origin.Type != "github" {
			fmt.Fprintf(outWriter, "%s: skipped (not github-sourced)\n", skillName)
			continue
		}

		// Pinned skills are frozen at a version: do not stage updates past it.
		if meta.Pinned != "" {
			fmt.Fprintf(outWriter, "%s: skipped (pinned to %s)\n", skillName, meta.Pinned)
			results = append(results, checkResult{Skill: skillName, Status: "pinned"})
			continue
		}

		// Check if we should skip due to 24h lazy rule
		if !forceCheck {
			poll, err := stateDB.GetSkillPoll(skillName)
			if err != nil {
				fmt.Fprintf(stderr, "read poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			if poll != nil && poll.LastCheckedAt != "" {
				// Parse the RFC3339 timestamp and check if < 24h old
				lastCheck, err := time.Parse(time.RFC3339, poll.LastCheckedAt)
				if err == nil && time.Since(lastCheck) < 24*time.Hour {
					fmt.Fprintf(outWriter, "%s: skipped (checked recently)\n", skillName)
					continue
				}
			}
		}

		// Extract owner/repo from URL (e.g., https://github.com/owner/repo)
		owner, repo, err := parseGitHubURL(meta.Origin.URL)
		if err != nil {
			fmt.Fprintf(outWriter, "%s: error (parse url)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  err.Error(),
			})
			continue
		}

		// Get cached poll info (for ETag)
		var cachedETag string
		poll, err := stateDB.GetSkillPoll(skillName)
		if err != nil {
			fmt.Fprintf(stderr, "read poll cache for %s: %v\n", skillName, err)
			return ExitOpError
		}
		if poll != nil {
			cachedETag = poll.ETag
		}

		// Poll GitHub, passing cached ETag for If-None-Match header
		newCommit, etag, notModified, err := poller.Poll(owner, repo, "HEAD", cachedETag)
		if err != nil {
			fmt.Fprintf(outWriter, "%s: error (poll failed)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  err.Error(),
			})
			// Don't touch skill_polls on failure — leaving last_checked_at stale lets
			// the next `check` retry instead of suppressing it via the 24h lazy gate.
			continue
		}

		// Handle 304 Not Modified: keep cached commit and etag, just refresh last_checked_at
		if notModified {
			if err := stateDB.RefreshSkillPollCheckedAt(skillName); err != nil {
				fmt.Fprintf(stderr, "refresh poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			fmt.Fprintf(outWriter, "%s: checked (no change)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "checked",
			})
			continue
		}

		// If no new commit (same SHA), skip staging
		if newCommit == meta.Origin.Commit {
			// Upsert skill_polls with latest commit and etag
			if err := stateDB.UpsertSkillPoll(skillName, newCommit, etag); err != nil {
				fmt.Fprintf(stderr, "upsert poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			fmt.Fprintf(outWriter, "%s: checked (no change)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "checked",
			})
			continue
		}

		// New commit detected: before staging, check if only SKILL.md differs
		// (reject multi-file upstream changes in v0.1)
		treeOwner, treeRepo, treeErr := parseGitHubURL(meta.Origin.URL)
		if treeErr != nil {
			fmt.Fprintf(outWriter, "%s: error (parse url)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  treeErr.Error(),
			})
			if err := stateDB.RefreshSkillPollCheckedAt(skillName); err != nil {
				fmt.Fprintf(stderr, "refresh poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			continue
		}

		// Fetch the upstream tree to check for multi-file changes
		upstreamPath := meta.Origin.Path
		if !isSafeGitHubPath(upstreamPath) {
			fmt.Fprintf(outWriter, "%s: error (unsafe origin.path)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  "unsafe origin.path",
			})
			if err := stateDB.RefreshSkillPollCheckedAt(skillName); err != nil {
				fmt.Fprintf(stderr, "refresh poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			continue
		}

		upstreamTree, treeErr := fetcher.FetchTree(treeOwner, treeRepo, newCommit, upstreamPath)
		if treeErr != nil {
			fmt.Fprintf(outWriter, "%s: error (fetch tree failed)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  treeErr.Error(),
			})
			if err := stateDB.RefreshSkillPollCheckedAt(skillName); err != nil {
				fmt.Fprintf(stderr, "refresh poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			continue
		}

		// Fetch the OLD upstream tree (at the installed commit) so we can compare
		// old-vs-new upstream rather than new-upstream-vs-live. This prevents local
		// edits to companion files from being misidentified as upstream multi-file changes.
		// On error, fall back to live-comparison (e.g. old commit was force-pushed away).
		var multiFileChange bool
		var upstreamSkillSHA string
		oldTree, oldTreeErr := fetcher.FetchTree(treeOwner, treeRepo, meta.Origin.Commit, upstreamPath)
		if oldTreeErr != nil {
			fmt.Fprintf(outWriter, "%s: warn (old tree unavailable, falling back to live comparison)\n", skillName)
			multiFileChange, upstreamSkillSHA, err = detectMultiFileChangeFromLive(skillName, libraryPath, meta, upstreamTree, upstreamPath)
		} else {
			multiFileChange, upstreamSkillSHA, err = detectMultiFileChangeFromTrees(oldTree, upstreamTree, upstreamPath)
		}
		if err != nil {
			fmt.Fprintf(outWriter, "%s: error (tree comparison failed)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  err.Error(),
			})
			if err := stateDB.RefreshSkillPollCheckedAt(skillName); err != nil {
				fmt.Fprintf(stderr, "refresh poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			continue
		}

		if multiFileChange {
			fmt.Fprintf(outWriter, "%s: error (multi-file upstream changes not supported in v0.1; only SKILL.md updates supported)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  "multi-file upstream change",
			})
			// Only refresh last_checked_at; do NOT advance last_commit
			if err := stateDB.RefreshSkillPollCheckedAt(skillName); err != nil {
				fmt.Fprintf(stderr, "refresh poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			continue
		}

		// Check if SKILL.md content actually changed. If upstream commit advanced but
		// SKILL.md blob SHA is identical to live, skip staging — it's a no-op update.
		liveSkillContent, liveReadErr := os.ReadFile(filepath.Join(libraryPath, skillName, "SKILL.md"))
		if liveReadErr != nil {
			fmt.Fprintf(outWriter, "%s: error (read live SKILL.md)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  liveReadErr.Error(),
			})
			if err := stateDB.RefreshSkillPollCheckedAt(skillName); err != nil {
				fmt.Fprintf(stderr, "refresh poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			continue
		}
		liveSkillSHA := gitBlobSHA(liveSkillContent)
		if upstreamSkillSHA != "" && liveSkillSHA == upstreamSkillSHA {
			// Commit advanced upstream but SKILL.md is unchanged — advance poll cache, no staging.
			if err := stateDB.UpsertSkillPoll(skillName, newCommit, etag); err != nil {
				fmt.Fprintf(stderr, "upsert poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			fmt.Fprintf(outWriter, "%s: checked (no change)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "checked",
			})
			continue
		}

		// SKILL.md content differs: proceed with staging
		if err := stageUpdate(skillName, libraryPath, meta, newCommit); err != nil {
			fmt.Fprintf(outWriter, "%s: error (fetch failed)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  err.Error(),
			})
			// Refresh last_checked_at only; keep the prior commit+etag so retry will re-attempt staging
			if err := stateDB.RefreshSkillPollCheckedAt(skillName); err != nil {
				fmt.Fprintf(stderr, "refresh poll for %s: %v\n", skillName, err)
				return ExitOpError
			}
			continue
		}

		// Staging succeeded: now upsert skill_polls with latest commit and etag
		if err := stateDB.UpsertSkillPoll(skillName, newCommit, etag); err != nil {
			fmt.Fprintf(stderr, "upsert poll for %s: %v\n", skillName, err)
			return ExitOpError
		}

		// Record in updates table
		if err := stateDB.InsertUpdate(skillName, meta.Origin.Commit, newCommit, "github"); err != nil {
			fmt.Fprintf(stderr, "insert update for %s: %v\n", skillName, err)
			return ExitOpError
		}

		fmt.Fprintf(outWriter, "%s: updated (pending)\n", skillName)
		results = append(results, checkResult{
			Skill:  skillName,
			Status: "updated",
		})
	}

	if gf.JSON {
		if err := writeJSON(stdout, results); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	}

	return ExitSuccess
}

// isSafeGitHubSegment validates that a GitHub owner or repo segment contains only safe characters.
// GitHub allows [A-Za-z0-9._-], but we restrict further to [A-Za-z0-9._-] to be conservative.
func isSafeGitHubSegment(s string) bool {
	// Allow alphanumerics, dots, underscores, and hyphens only
	matched, _ := regexp.MatchString(`^[A-Za-z0-9._-]+$`, s)
	return matched && len(s) > 0
}

// isSafeGitHubPath validates that a GitHub path (potentially with / separators) is safe.
// Validates each segment to prevent injection attacks or directory traversal.
func isSafeGitHubPath(p string) bool {
	if len(p) == 0 {
		return true // Empty path is allowed (means no subpath)
	}
	// Reject paths starting with / (absolute paths)
	if strings.HasPrefix(p, "/") {
		return false
	}
	// Reject paths with .. segments (directory traversal)
	if strings.Contains(p, "..") {
		return false
	}
	// Trim trailing slashes for validation
	p = strings.TrimSuffix(p, "/")
	if len(p) == 0 {
		return true
	}
	// Validate each segment
	segments := strings.Split(p, "/")
	for _, seg := range segments {
		if !isSafeGitHubSegment(seg) {
			return false
		}
	}
	return true
}

// parseGitHubURL extracts owner and repo from a GitHub URL like https://github.com/owner/repo.
// Returns an error if the URL contains unsafe characters (defense against command injection).
func parseGitHubURL(url string) (owner, repo string, err error) {
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, "/")
	if strings.HasSuffix(url, ".git") {
		url = strings.TrimSuffix(url, ".git")
	}

	parts := strings.Split(url, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid github url: %s", url)
	}

	owner = parts[len(parts)-2]
	repo = parts[len(parts)-1]

	if owner == "" || repo == "" {
		return "", "", fmt.Errorf("invalid github url: %s", url)
	}

	// Validate segments for safety (prevent injection attacks)
	if !isSafeGitHubSegment(owner) {
		return "", "", fmt.Errorf("unsafe github owner: %s (contains invalid characters)", owner)
	}
	if !isSafeGitHubSegment(repo) {
		return "", "", fmt.Errorf("unsafe github repo: %s (contains invalid characters)", repo)
	}

	return owner, repo, nil
}

// isWhitelistedLocalPath returns true if a skill-relative path is a local-only
// artifact that the manager itself creates, so its presence in live (but not
// upstream) is not real divergence.
//
// Whitelist is intentionally narrow: only manager-generated paths. Real skill
// content directories like references/ and scripts/ are NOT whitelisted —
// upstream changes (additions, modifications, or deletions) there must be
// surfaced as multi-file changes per the v0.1 boundary.
//
// The whitelist is used by detectMultiFileChangeFromLive (the fallback path).
// detectMultiFileChangeFromTrees does not need it: local-only files do not
// appear in either upstream tree, so they are never seen.
func isWhitelistedLocalPath(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 0 {
		return false
	}
	first := parts[0]
	switch first {
	case ".skill-meta.yaml", ".update-pending", // manager-generated
		".git", ".DS_Store", ".gitignore": // never legitimate upstream skill content
		return true
	}
	return false
}

// buildSkillRelativeMap builds a path→SHA map from a FetchTree result, stripping the
// upstreamPath prefix so keys are skill-relative (e.g. "SKILL.md", "references/foo.md").
func buildSkillRelativeMap(tree []TreeEntry, upstreamPath string) map[string]string {
	m := make(map[string]string, len(tree))
	prefix := ""
	if upstreamPath != "" {
		prefix = strings.Trim(upstreamPath, "/") + "/"
	}
	for _, entry := range tree {
		rel := entry.Path
		if prefix != "" && strings.HasPrefix(rel, prefix) {
			rel = rel[len(prefix):]
		}
		m[rel] = entry.SHA
	}
	return m
}

// detectMultiFileChangeFromTrees compares oldTree (upstream at the installed commit)
// against newTree (upstream at the new commit). Returns true if anything other than
// SKILL.md changed upstream (added, removed, or modified). Local edits are irrelevant
// because neither tree contains local-only files.
//
// Returns (multiFileChange bool, upstreamSkillSHA string, err error).
// upstreamSkillSHA is the blob SHA of SKILL.md in newTree ("" if absent).
func detectMultiFileChangeFromTrees(oldTree, newTree []TreeEntry, upstreamPath string) (bool, string, error) {
	oldMap := buildSkillRelativeMap(oldTree, upstreamPath)
	newMap := buildSkillRelativeMap(newTree, upstreamPath)

	upstreamSkillSHA := newMap["SKILL.md"]

	// Walk every path that appears in either tree.
	seen := make(map[string]bool, len(oldMap)+len(newMap))
	for p := range oldMap {
		seen[p] = true
	}
	for p := range newMap {
		seen[p] = true
	}

	for p := range seen {
		if p == "SKILL.md" {
			// Expected to differ; skip.
			continue
		}
		oldSHA, inOld := oldMap[p]
		newSHA, inNew := newMap[p]
		switch {
		case inOld && inNew && oldSHA == newSHA:
			// Unchanged upstream — allowed.
		default:
			// Added, removed, or modified upstream → multi-file change.
			return true, upstreamSkillSHA, nil
		}
	}

	return false, upstreamSkillSHA, nil
}

// detectMultiFileChangeFromLive compares the new upstream tree against the live skill
// directory. This is the fallback used when the old upstream tree cannot be fetched
// (e.g. the old commit was force-pushed away). The whitelist prevents local-only
// manager-generated files from being misidentified as upstream deletions.
//
// Returns (multiFileChange bool, upstreamSkillSHA string, err error).
func detectMultiFileChangeFromLive(skillName string, libraryPath string, meta skillMeta, upstreamTree []TreeEntry, upstreamPath string) (bool, string, error) {
	skillDir := filepath.Join(libraryPath, skillName)
	upstreamMap := buildSkillRelativeMap(upstreamTree, upstreamPath)
	upstreamSkillSHA := upstreamMap["SKILL.md"]

	// Check each upstream file against live.
	for p, upstreamSHA := range upstreamMap {
		if p == "SKILL.md" {
			continue
		}
		liveFilePath := filepath.Join(skillDir, filepath.FromSlash(p))
		content, err := os.ReadFile(liveFilePath)
		if err != nil && !os.IsNotExist(err) {
			return false, upstreamSkillSHA, fmt.Errorf("read live file %s: %w", p, err)
		}
		if os.IsNotExist(err) {
			return true, upstreamSkillSHA, nil
		}
		if gitBlobSHA(content) != upstreamSHA {
			return true, upstreamSkillSHA, nil
		}
	}

	// Check for upstream deletions: live files absent from the new upstream tree.
	var deletionFound bool
	if err := filepath.WalkDir(skillDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".update-pending" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		normalizedPath := filepath.ToSlash(rel)
		if _, existsInUpstream := upstreamMap[normalizedPath]; !existsInUpstream {
			if !isWhitelistedLocalPath(rel) {
				deletionFound = true
				return filepath.SkipDir
			}
		}
		return nil
	}); err != nil {
		return false, upstreamSkillSHA, err
	}

	if deletionFound {
		return true, upstreamSkillSHA, nil
	}
	return false, upstreamSkillSHA, nil
}

// stageUpdate creates the .update-pending directory with snapshots.
// Currently stages SKILL.md and .skill-meta.yaml per snapshot directory.
// Multi-file scope (references/, scripts/, etc.) is deferred to a future PR.
func stageUpdate(skillName string, libraryPath string, meta skillMeta, newCommit string) error {
	skillDir := filepath.Join(libraryPath, skillName)
	pendingRoot := filepath.Join(skillDir, ".update-pending")

	// Verify current SKILL.md exists (we'll copy the entire live directory as from-current)
	currentPath := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(currentPath); err != nil {
		return fmt.Errorf("read current SKILL.md: %w", err)
	}

	// Read live .skill-meta.yaml (will be used in both snapshots)
	liveMetaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	liveMetaContent, err := os.ReadFile(liveMetaPath)
	if err != nil {
		return fmt.Errorf("read live .skill-meta.yaml: %w", err)
	}

	// Fetch incoming SKILL.md via fetcher (GitHub or mock in tests)
	// Do this BEFORE creating the pending directory, so we don't leave a partial state on failure
	owner, repo, err := parseGitHubURL(meta.Origin.URL)
	if err != nil {
		return fmt.Errorf("parse url for fetch: %w", err)
	}

	// Build fetch path: prepend origin.path if non-empty
	// Validate origin.path for safety
	if meta.Origin.Path != "" && !isSafeGitHubPath(meta.Origin.Path) {
		return fmt.Errorf("unsafe origin.path: %s (contains invalid characters or directory traversal)", meta.Origin.Path)
	}
	// Use githubContentsPath to ensure forward slashes for GitHub API
	fetchPath := githubContentsPath(meta.Origin.Path)

	incomingContent, err := fetcher.FetchFile(owner, repo, newCommit, fetchPath)
	if err != nil {
		return fmt.Errorf("fetch incoming SKILL.md from %s: %w", fetchPath, err)
	}

	// Now create the pending directory and write files.
	// First, wipe any stale snapshots from previous runs (old layout: from, to, etc).
	if err := os.RemoveAll(pendingRoot); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale pending dir: %w", err)
	}
	if err := os.MkdirAll(pendingRoot, 0755); err != nil {
		return fmt.Errorf("create pending dir: %w", err)
	}

	// from-current: snapshot the entire live skill directory so safety checks compare
	// a complete baseline (not just SKILL.md) against to-incoming. This ensures that
	// sibling files are detected as part of the base, not as "added" by the update.
	fromCurrentDir := filepath.Join(pendingRoot, "from-current")
	if err := os.RemoveAll(fromCurrentDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing from-current: %w", err)
	}
	if err := os.MkdirAll(fromCurrentDir, 0755); err != nil {
		return fmt.Errorf("mkdir from-current: %w", err)
	}

	// Copy entire live skill directory (except .update-pending) into from-current
	if err := filepath.WalkDir(skillDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		if rel == ".update-pending" {
			return filepath.SkipDir
		}
		if rel == "." {
			return nil
		}
		destPath := filepath.Join(fromCurrentDir, rel)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, destPath, info.Mode())
	}); err != nil {
		return fmt.Errorf("copy live skill to from-current: %w", err)
	}

	// to-incoming: snapshot the entire live skill directory so applyPendingUpdate
	// (which treats directory-form "to" as authoritative) won't wipe sibling files
	// like references/ or scripts/. Then overwrite SKILL.md and .skill-meta.yaml
	// with incoming content.
	toIncomingDir := filepath.Join(pendingRoot, "to-incoming")
	if err := os.RemoveAll(toIncomingDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing to-incoming: %w", err)
	}
	if err := os.MkdirAll(toIncomingDir, 0755); err != nil {
		return fmt.Errorf("mkdir to-incoming: %w", err)
	}

	// Copy entire live skill directory (except .update-pending) into to-incoming
	if err := filepath.WalkDir(skillDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		if rel == ".update-pending" {
			return filepath.SkipDir
		}
		if rel == "." {
			return nil
		}
		destPath := filepath.Join(toIncomingDir, rel)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, destPath, info.Mode())
	}); err != nil {
		return fmt.Errorf("copy live skill to to-incoming: %w", err)
	}

	// Overwrite SKILL.md with incoming content
	if err := os.WriteFile(filepath.Join(toIncomingDir, "SKILL.md"), incomingContent, 0644); err != nil {
		return fmt.Errorf("write to-incoming/SKILL.md: %w", err)
	}

	// Write to-incoming/.skill-meta.yaml with origin.commit rewritten to newCommit
	toIncomingMetaPath := filepath.Join(toIncomingDir, ".skill-meta.yaml")
	if err := os.WriteFile(toIncomingMetaPath, liveMetaContent, 0644); err != nil {
		return fmt.Errorf("write to-incoming/.skill-meta.yaml: %w", err)
	}
	if err := rewriteOriginCommit(toIncomingMetaPath, newCommit); err != nil {
		return fmt.Errorf("rewrite origin.commit in to-incoming/.skill-meta.yaml: %w", err)
	}

	// Write meta.yaml with version info
	metaContent := fmt.Sprintf(`version: 1
detected_at: %s
from_version: %s
to_version: %s
source: github
status: pending
`, time.Now().UTC().Format(time.RFC3339), meta.Origin.Commit, newCommit)

	if err := os.WriteFile(filepath.Join(pendingRoot, "meta.yaml"), []byte(metaContent), 0644); err != nil {
		return fmt.Errorf("write meta.yaml: %w", err)
	}

	return nil
}

// rewriteOriginCommit updates the origin.commit field in a nested .skill-meta.yaml file.
// Handles YAML structure like:
//
//	origin:
//	  type: github
//	  commit: old_sha
//
// Walks lines to find the "origin:" section, then locates the indented "commit:" key
// within that section and updates its value, preserving formatting.
func rewriteOriginCommit(metaPath string, newCommit string) error {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	originIdx := -1
	originIndent := -1
	commitIdx := -1

	// Find the "origin:" line at root level (indent 0)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		currentIndent := indent(line)

		// If we already found origin section and we're at a root-level key (or end of file),
		// we can stop searching
		if originIdx != -1 && currentIndent == 0 && !strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
			break
		}

		// Look for "origin:" at root level
		if originIdx == -1 && currentIndent == 0 {
			if strings.HasPrefix(trimmed, "origin:") {
				originIdx = i
				originIndent = currentIndent
				continue
			}
		}

		// If we found the origin section, look for "commit:" at indent > originIndent
		if originIdx != -1 && commitIdx == -1 && currentIndent > originIndent {
			if strings.HasPrefix(trimmed, "commit:") {
				commitIdx = i
				break
			}
		}

		// If we hit another root-level key after finding origin, stop
		if originIdx != -1 && currentIndent == originIndent && i > originIdx && strings.HasPrefix(trimmed, "origin:") == false {
			if strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
				break
			}
		}
	}

	// Update the commit line if found
	if commitIdx != -1 {
		line := lines[commitIdx]
		trimmed := strings.TrimSpace(line)
		lineIndent := len(line) - len(trimmed)
		lines[commitIdx] = strings.Repeat(" ", lineIndent) + "commit: " + newCommit
		return os.WriteFile(metaPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
	}

	// If commit not found but origin was, we still write the file but return an error
	// (shouldn't happen for valid meta created by this tool, but don't corrupt the file)
	if originIdx != -1 {
		return fmt.Errorf("origin section found but no commit field to update")
	}

	// Origin not found at all; this is an error state
	return fmt.Errorf("origin section not found in .skill-meta.yaml")
}

// githubContentsPath constructs a GitHub API contents path using forward slashes
// (required by GitHub API), regardless of the host OS path separator.
func githubContentsPath(originPath string) string {
	if originPath == "" {
		return "SKILL.md"
	}
	// Use path.Join (not filepath.Join) to ensure / separators for GitHub API
	trimmed := strings.Trim(originPath, "/")
	if trimmed == "" {
		return "SKILL.md"
	}
	return path.Join(trimmed, "SKILL.md")
}

// gitBlobSHA computes the git blob SHA for the given content using the standard formula:
// sha1("blob " + len(content) + "\0" + content)
func gitBlobSHA(content []byte) string {
	header := fmt.Sprintf("blob %d\x00", len(content))
	h := sha1.New()
	h.Write([]byte(header))
	h.Write(content)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// fetchFileFromGitHub fetches a single file from a GitHub repo at a given ref.
// Uses argv-style invocation (no shell) to prevent command injection attacks.
func fetchFileFromGitHub(owner, repo, ref, filePath string) ([]byte, error) {
	var out strings.Builder
	var errOut strings.Builder

	// Call gh api with argv-style arguments (no shell to prevent injection)
	// Use query string for ref to ensure GET (not POST with -f flag)
	cmd := exec.Command("gh", "api",
		"repos/"+owner+"/"+repo+"/contents/"+filePath+"?ref="+ref,
		"--jq", ".content",
	)
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("fetch %s from %s:%s: %v (stderr: %s)", filePath, owner+"/"+repo, ref, err, errOut.String())
	}

	// Decode base64 content
	content := strings.TrimSpace(out.String())
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("decode base64 content: %w", err)
	}

	return decoded, nil
}

// fetchTreeFromGitHub fetches the tree for a given ref and filters to entries under subPath.
// Returns blob and tree entries, filtering to those whose path starts with subPath/ (if non-empty).
func fetchTreeFromGitHub(owner, repo, ref, subPath string) ([]TreeEntry, error) {
	var out strings.Builder
	var errOut strings.Builder

	// Call gh api to fetch the recursive tree
	cmd := exec.Command("gh", "api",
		"repos/"+owner+"/"+repo+"/git/trees/"+ref+"?recursive=true",
		"--jq", ".tree[] | select(.type==\"blob\") | {path, sha}",
	)
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("fetch tree from %s:%s: %v (stderr: %s)", owner+"/"+repo, ref, err, errOut.String())
	}

	// Parse the output line-by-line (one JSON object per line)
	var entries []TreeEntry
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry TreeEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse tree entry: %w", err)
		}
		entry.Type = "blob" // We only request blobs via jq filter

		// Filter to entries under subPath if provided
		if subPath != "" {
			prefix := strings.Trim(subPath, "/") + "/"
			if !strings.HasPrefix(entry.Path, prefix) {
				continue
			}
		}

		entries = append(entries, entry)
	}

	return entries, nil
}
