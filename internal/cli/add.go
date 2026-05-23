package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func expandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}

	if path == "~" {
		return home, nil
	}

	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}

	// ~user/... form is not supported in v0.1
	return "", fmt.Errorf("~user expansion not supported")
}

func runAdd(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: skills-manager add <source> [--auto] [--yes] [--name <override>]")
		return ExitUsageError
	}

	var source string
	var opts ingestOptions
	var nameOverride string

	// Parse arguments: first non-flag is the source
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--auto":
			opts.auto = true
		case "--yes":
			opts.yes = true
		case "--name":
			if i+1 < len(args) {
				nameOverride = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "--name requires a value")
				return ExitUsageError
			}
		default:
			if strings.HasPrefix(arg, "--name=") {
				nameOverride = strings.TrimPrefix(arg, "--name=")
			} else if source == "" && !strings.HasPrefix(arg, "--") {
				source = arg
			}
		}
	}

	if source == "" {
		fmt.Fprintln(stderr, "usage: skills-manager add <source> [--auto] [--yes] [--name <override>]")
		return ExitUsageError
	}

	// Classifying the source
	kind, err := classifyAddArgument(source)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitUsageError
	}

	opts.interactive = !gf.NonInteractive && !gf.JSON && !gf.Quiet
	// If stdin is not a TTY, require explicit consent (--yes or --auto)
	if !stdinIsTTY() && !opts.auto && !opts.yes {
		opts.interactive = false
	}
	opts.name = nameOverride
	humanOut := gf.outWriter(stdout)

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	// Prepare the source based on kind
	var src ingestSource
	src.raw = source
	src.kind = kind

	switch kind {
	case "github":
		src, err = fetchGitHub(source)
		if err != nil {
			fmt.Fprintf(stderr, "github fetch: %v\n", err)
			return ExitOpError
		}
	case "local":
		expandedPath, err := expandTilde(source)
		if err != nil {
			fmt.Fprintf(stderr, "expand path: %v\n", err)
			return ExitUsageError
		}
		absPath, err := filepath.Abs(expandedPath)
		if err != nil {
			fmt.Fprintf(stderr, "resolve path: %v\n", err)
			return ExitUsageError
		}
		src.path = absPath
		src.label = absPath
	case "marketplace":
		src, err = findMarketplaceSkill(source)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitOpError
		}
	default:
		fmt.Fprintf(stderr, "unknown kind: %v\n", kind)
		return ExitUsageError
	}

	// Clean up temp directory for GitHub sources after ingest completes
	defer func() {
		if src.kind == "github" && src.path != "" {
			os.RemoveAll(src.path)
		}
	}()

	result := ingestFromSource(src, opts, home, humanOut)

	if gf.JSON {
		writeJSON(stdout, result)
	} else if !result.Skipped {
		fmt.Fprintf(humanOut, "Ingested %s to %s\n", result.Name, result.LibraryPath)
	} else {
		fmt.Fprintf(humanOut, "Skipped %s: %s\n", result.Name, result.Reason)
	}

	if result.Skipped {
		return ExitNotable
	}

	return ExitSuccess
}

func classifyAddArgument(arg string) (string, error) {
	if strings.HasPrefix(arg, "http://github.com/") || strings.HasPrefix(arg, "https://github.com/") || strings.HasPrefix(arg, "github.com/") {
		return "github", nil
	}

	if strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, "/") {
		return "local", nil
	}

	if strings.HasPrefix(arg, "~/") {
		return "local", nil
	}

	// Marketplace: contains exactly one "/" and no ":" and no "."
	slashCount := strings.Count(arg, "/")
	if slashCount == 1 && !strings.Contains(arg, ":") && !strings.Contains(arg, ".") {
		return "marketplace", nil
	}

	return "", fmt.Errorf("cannot classify source: %q", arg)
}

func normalizeGitHubURL(raw string) (cloneURL string, err error) {
	// If input starts with "github.com/" (no scheme), prepend "https://"
	if strings.HasPrefix(raw, "github.com/") {
		raw = "https://" + raw
	}

	// Parse the URL to extract the org/repo part
	urlStr := raw
	if !strings.Contains(urlStr, "://") {
		return "", fmt.Errorf("invalid github URL format: %q", raw)
	}

	// Extract path after github.com/
	var pathPart string
	if strings.HasPrefix(urlStr, "https://github.com/") {
		pathPart = strings.TrimPrefix(urlStr, "https://github.com/")
	} else if strings.HasPrefix(urlStr, "http://github.com/") {
		pathPart = strings.TrimPrefix(urlStr, "http://github.com/")
	} else {
		return "", fmt.Errorf("invalid github URL: %q (must be github.com)", raw)
	}

	// Strip trailing slashes before splitting
	pathPart = strings.TrimRight(pathPart, "/")

	// Count path segments: exactly 2 (org/repo) is valid
	segments := strings.Split(pathPart, "/")
	if len(segments) < 2 {
		return "", fmt.Errorf("github URL needs at least org/repo: %q", raw)
	}
	if len(segments) > 2 {
		// Subdirectory form like github.com/org/repo/sub/path
		return "", fmt.Errorf("github subpath syntax not supported in v0.1; clone github.com/%s/%s and use `skills-manager add <local-path>` for subdirs", segments[0], segments[1])
	}

	// Build canonical URL: https://github.com/org/repo (no trailing .git or /)
	org := segments[0]
	repo := strings.TrimSuffix(segments[1], ".git")

	return "https://github.com/" + org + "/" + repo, nil
}

func truncateCommitSHA(sha string) (string, error) {
	// Validate: must be at least 12 hex chars or exactly 40 hex chars
	if len(sha) < 12 {
		return "", fmt.Errorf("unexpected git rev-parse output: %q (too short)", sha)
	}

	// Check all characters are hex
	for _, ch := range sha {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return "", fmt.Errorf("unexpected git rev-parse output: %q (contains non-hex chars)", sha)
		}
	}

	// Truncate to 12 chars
	return sha[:12], nil
}

func fetchGitHub(url string) (ingestSource, error) {
	// Normalize the GitHub URL
	cloneURL, err := normalizeGitHubURL(url)
	if err != nil {
		return ingestSource{}, err
	}

	tmpDir, err := os.MkdirTemp("", "sm-github-*")
	if err != nil {
		return ingestSource{}, fmt.Errorf("create temp dir: %w", err)
	}

	cmd := exec.Command("git", "clone", "--depth=1", cloneURL, tmpDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		return ingestSource{}, fmt.Errorf("git clone: %w", err)
	}

	// Get the commit
	cmdRev := exec.Command("git", "-C", tmpDir, "rev-parse", "HEAD")
	outRev, err := cmdRev.Output()
	if err != nil {
		os.RemoveAll(tmpDir)
		return ingestSource{}, fmt.Errorf("could not resolve commit SHA from cloned repo: %w", err)
	}

	commit := strings.TrimSpace(string(outRev))

	// Validate and truncate commit SHA
	commitShort, err := truncateCommitSHA(commit)
	if err != nil {
		os.RemoveAll(tmpDir)
		return ingestSource{}, err
	}

	return ingestSource{
		kind:   "github",
		raw:    url,
		url:    cloneURL,
		commit: commit,
		path:   tmpDir,
		label:  cloneURL + " @ " + commitShort,
	}, nil
}

func findMarketplaceSkill(marketplace_skill string) (ingestSource, error) {
	parts := strings.Split(marketplace_skill, "/")
	if len(parts) != 2 {
		return ingestSource{}, fmt.Errorf("invalid marketplace skill: %q", marketplace_skill)
	}
	marketplace := parts[0]
	skill := parts[1]

	home, err := os.UserHomeDir()
	if err != nil {
		return ingestSource{}, fmt.Errorf("get home: %w", err)
	}

	candidates := []string{
		filepath.Join(home, ".claude", "plugins", "marketplaces", marketplace, "skills", skill),
		filepath.Join(home, ".claude", "plugins", "marketplaces", marketplace, skill),
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "SKILL.md")); err == nil {
			return ingestSource{
				kind:  "marketplace",
				raw:   marketplace_skill,
				path:  candidate,
				label: marketplace + "/" + skill,
			}, nil
		}
	}

	return ingestSource{}, fmt.Errorf("marketplace skill not found in local cache. Sync the marketplace first (e.g. `claude plugin install %s`)", marketplace)
}
