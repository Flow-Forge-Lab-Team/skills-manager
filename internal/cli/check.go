package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// GitHubFetcher is an interface for fetching files from GitHub.
// It can be overridden in tests.
type GitHubFetcher interface {
	FetchFile(owner, repo, ref, path string) ([]byte, error)
}

// defaultFetcher implements GitHubFetcher using gh CLI.
type defaultFetcher struct{}

func (f *defaultFetcher) FetchFile(owner, repo, ref, path string) ([]byte, error) {
	return fetchFileFromGitHub(owner, repo, ref, path)
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

		// Poll GitHub
		newCommit, etag, err := poller.Poll(owner, repo, "HEAD")
		if err != nil {
			fmt.Fprintf(outWriter, "%s: error (poll failed)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  err.Error(),
			})
			// Still upsert to refresh last_checked_at on error
			_ = stateDB.UpsertSkillPoll(skillName, meta.Origin.Commit, cachedETag)
			continue
		}

		// Upsert skill_polls with latest etag and timestamp
		if err := stateDB.UpsertSkillPoll(skillName, newCommit, etag); err != nil {
			fmt.Fprintf(stderr, "upsert poll for %s: %v\n", skillName, err)
			return ExitOpError
		}

		// If no new commit (304 or same SHA), skip staging
		if newCommit == "" || newCommit == meta.Origin.Commit {
			fmt.Fprintf(outWriter, "%s: checked (no change)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "checked",
			})
			continue
		}

		// New commit detected: stage the update
		if err := stageUpdate(skillName, libraryPath, meta, newCommit); err != nil {
			fmt.Fprintf(outWriter, "%s: error (fetch failed)\n", skillName)
			results = append(results, checkResult{
				Skill:  skillName,
				Status: "error",
				Error:  err.Error(),
			})
			// Still upsert to refresh last_checked_at on fetch error
			_ = stateDB.UpsertSkillPoll(skillName, newCommit, etag)
			continue
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

// parseGitHubURL extracts owner and repo from a GitHub URL like https://github.com/owner/repo.
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

	return owner, repo, nil
}

// stageUpdate creates the .update-pending directory with snapshots.
// Currently stages SKILL.md and .skill-meta.yaml per snapshot directory.
// Multi-file scope (references/, scripts/, etc.) is deferred to a future PR.
func stageUpdate(skillName string, libraryPath string, meta skillMeta, newCommit string) error {
	skillDir := filepath.Join(libraryPath, skillName)
	pendingRoot := filepath.Join(skillDir, ".update-pending")

	// Read current SKILL.md as "from-current" snapshot
	currentPath := filepath.Join(skillDir, "SKILL.md")
	currentContent, err := os.ReadFile(currentPath)
	if err != nil {
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

	incomingContent, err := fetcher.FetchFile(owner, repo, newCommit, "SKILL.md")
	if err != nil {
		return fmt.Errorf("fetch incoming SKILL.md: %w", err)
	}

	// Now create the pending directory and write files
	if err := os.MkdirAll(pendingRoot, 0755); err != nil {
		return fmt.Errorf("create pending dir: %w", err)
	}

	// from-current: snapshot with current state (SKILL.md + live .skill-meta.yaml as-is)
	fromCurrentDir := filepath.Join(pendingRoot, "from-current")
	if err := os.MkdirAll(fromCurrentDir, 0755); err != nil {
		return fmt.Errorf("mkdir from-current: %w", err)
	}
	if err := os.WriteFile(filepath.Join(fromCurrentDir, "SKILL.md"), currentContent, 0644); err != nil {
		return fmt.Errorf("write from-current/SKILL.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(fromCurrentDir, ".skill-meta.yaml"), liveMetaContent, 0644); err != nil {
		return fmt.Errorf("write from-current/.skill-meta.yaml: %w", err)
	}

	// to-incoming: snapshot with incoming state; rewrite origin.commit to newCommit so accept advances it
	toIncomingDir := filepath.Join(pendingRoot, "to-incoming")
	if err := os.MkdirAll(toIncomingDir, 0755); err != nil {
		return fmt.Errorf("mkdir to-incoming: %w", err)
	}
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

// rewriteOriginCommit updates the origin.commit field in a .skill-meta.yaml file.
// Uses line-based rewriting to preserve formatting.
func rewriteOriginCommit(metaPath string, newCommit string) error {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	found := false
	for i, line := range lines {
		// Match "origin.commit:" at start of line (after any indent)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "origin.commit:") {
			// Preserve indentation
			indent := len(line) - len(trimmed)
			lines[i] = strings.Repeat(" ", indent) + "origin.commit: " + newCommit
			found = true
			break
		}
	}
	if !found {
		// If not found, append it (shouldn't happen for valid meta, but be safe)
		lines = append(lines, "origin.commit: "+newCommit)
	}
	return os.WriteFile(metaPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// fetchFileFromGitHub fetches a single file from a GitHub repo at a given ref.
func fetchFileFromGitHub(owner, repo, ref, path string) ([]byte, error) {
	// Use gh api repos/{owner}/{repo}/contents/{path}?ref={ref}
	// and extract the content field (base64 encoded)
	apiPath := "repos/" + owner + "/" + repo + "/contents/" + path + "?ref=" + ref

	var out strings.Builder
	var errOut strings.Builder

	// Call gh api with jq to extract content (which is base64-encoded)
	sh := exec.Command("sh", "-c", "gh api "+apiPath+" --jq '.content'")
	sh.Stdout = &out
	sh.Stderr = &errOut

	if err := sh.Run(); err != nil {
		return nil, fmt.Errorf("fetch %s from %s:%s: %v (stderr: %s)", path, owner+"/"+repo, ref, err, errOut.String())
	}

	// Decode base64 content
	content := strings.TrimSpace(out.String())
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("decode base64 content: %w", err)
	}

	return decoded, nil
}
