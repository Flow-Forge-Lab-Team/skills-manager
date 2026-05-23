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
		absPath, err := filepath.Abs(source)
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

func fetchGitHub(url string) (ingestSource, error) {
	tmpDir, err := os.MkdirTemp("", "sm-github-*")
	if err != nil {
		return ingestSource{}, fmt.Errorf("create temp dir: %w", err)
	}

	cmd := exec.Command("git", "clone", "--depth=1", url, tmpDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		return ingestSource{}, fmt.Errorf("git clone: %w", err)
	}

	// Get the commit
	cmdRev := exec.Command("git", "-C", tmpDir, "rev-parse", "HEAD")
	outRev, err := cmdRev.Output()
	commit := strings.TrimSpace(string(outRev))

	return ingestSource{
		kind:   "github",
		raw:    url,
		url:    url,
		commit: commit,
		path:   tmpDir,
		label:  url + " @ " + commit[:12],
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
