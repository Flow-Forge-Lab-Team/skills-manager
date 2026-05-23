package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type scanResult struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Status        string `json:"status"`
	GuessedOrigin string `json:"guessed_origin"`
}

func runScan(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	var ingest, autoIngest bool
	var pathsOverride string

	// Parse flags
	for _, arg := range args {
		switch arg {
		case "--ingest":
			ingest = true
		case "--auto-ingest":
			autoIngest = true
		default:
			if strings.HasPrefix(arg, "--paths=") {
				pathsOverride = strings.TrimPrefix(arg, "--paths=")
			} else if strings.HasPrefix(arg, "--paths") {
				idx := findArg("--paths", args)
				if idx+1 < len(args) {
					pathsOverride = args[idx+1]
				}
			}
		}
	}

	humanOut := gf.outWriter(stdout)

	// Determine search paths
	var searchPaths []string
	if pathsOverride != "" {
		for _, p := range strings.Split(pathsOverride, ",") {
			searchPaths = append(searchPaths, strings.TrimSpace(p))
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "get home: %v\n", err)
			return ExitOpError
		}
		defaultPaths := []string{
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(home, ".codex", "skills"),
			filepath.Join(home, ".grok", "skills"),
			filepath.Join(home, ".hermes", "skills"),
			filepath.Join(home, ".openclaw", "skills"),
			filepath.Join(home, ".gemini", "antigravity", "skills"),
		}
		for _, p := range defaultPaths {
			if _, err := os.Stat(p); err == nil {
				searchPaths = append(searchPaths, p)
			}
		}
	}

	// Get manager home and library
	managerHomeDir, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	libraryPath, err := ensureLibrary(managerHomeDir)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return ExitOpError
	}

	// Build fingerprint index of library skills
	fingerprintIndex := buildFingerprintIndex(libraryPath)

	// Load scan-ignore list
	ignoreList, _ := loadScanIgnore(managerHomeDir)

	// Scan and collect results
	var results []scanResult
	for _, searchPath := range searchPaths {
		entries, err := os.ReadDir(searchPath)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillPath := filepath.Join(searchPath, entry.Name())
			skillMdPath := filepath.Join(skillPath, "SKILL.md")

			// Check if skill is ignored
			if inSlice(ignoreList, skillPath) {
				continue
			}

			if _, err := os.Stat(skillMdPath); err != nil {
				continue
			}

			// Read fingerprint
			fp, _, err := fingerprintSkillMd(skillMdPath)
			if err != nil {
				continue
			}

			// Check library: match by fingerprint first, then by name as fallback
			var status string
			_, hasFingerprintMatch := fingerprintIndex[fp]

			if hasFingerprintMatch {
				// Found by fingerprint — skill is in library
				status = "in library"
			} else {
				// No fingerprint match; check by directory name (fallback)
				libraryEntry := filepath.Join(libraryPath, entry.Name())
				libraryMeta, _ := readSkillMeta(filepath.Join(libraryEntry, ".skill-meta.yaml"))

				if libraryMeta.Fingerprint.SHA256 == "" {
					status = "unregistered"
				} else if libraryMeta.Fingerprint.SHA256 == fp {
					status = "in library"
				} else {
					// Name matches but fingerprint differs — drift
					status = "drift"
				}
			}

			guessedOrigin := guessOrigin(skillPath)

			results = append(results, scanResult{
				Name:          entry.Name(),
				Path:          skillPath,
				Status:        status,
				GuessedOrigin: guessedOrigin,
			})
		}
	}

	// Output results
	if gf.JSON {
		writeJSON(stdout, results)
	} else {
		for _, r := range results {
			fmt.Fprintf(humanOut, "%-30s %-50s %-20s %s\n", r.Name, r.Path, r.Status, r.GuessedOrigin)
		}
	}

	// Handle --ingest or --auto-ingest
	if ingest || autoIngest {
		home, _ := managerHome()
		interactive := ingest && !autoIngest && !gf.NonInteractive
		// If stdin is not a TTY, require explicit consent
		if !stdinIsTTY() && interactive {
			interactive = false
		}
		opts := ingestOptions{
			auto:        autoIngest,
			yes:         autoIngest,
			interactive: interactive,
		}

		for _, r := range results {
			if r.Status != "unregistered" {
				continue
			}

			// Prepare source
			src := ingestSource{
				kind:  "local",
				raw:   r.Path,
				path:  r.Path,
				label: r.Path,
			}

			if ingest && !autoIngest {
				fmt.Fprintf(humanOut, "\nIngest %s? [Y/n/s] ", r.Name)
				var response string
				_, err := fmt.Scanln(&response)
				// EOF or read error means stdin was closed/empty — treat as skip
				if err != nil {
					fmt.Fprintf(humanOut, "Skipped %s (no input)\n", r.Name)
					continue
				}
				response = strings.ToLower(strings.TrimSpace(response))
				if response == "n" {
					fmt.Fprintf(humanOut, "Skipped %s\n", r.Name)
					continue
				}
				if response == "s" {
					// Skip forever
					_ = appendScanIgnore(home, r.Path)
					fmt.Fprintf(humanOut, "Skipped forever: %s\n", r.Name)
					continue
				}
			}

			result := ingestFromSource(src, opts, home, humanOut)
			if !result.Skipped {
				fmt.Fprintf(humanOut, "Ingested %s\n", result.Name)
			}
		}
	}

	return ExitSuccess
}

func guessOrigin(skillPath string) string {
	if _, err := os.Stat(filepath.Join(skillPath, ".git")); err == nil {
		return "hand-authored"
	}

	skillMdPath := filepath.Join(skillPath, "SKILL.md")
	info, err := os.Stat(skillMdPath)
	if err != nil {
		return "unknown"
	}

	if time.Since(info.ModTime()) < 24*time.Hour {
		return "ai-authored (likely)"
	}

	return "unknown"
}

func loadScanIgnore(managerHome string) ([]string, error) {
	ignoreFile := filepath.Join(managerHome, "scan-ignore.txt")
	content, err := os.ReadFile(ignoreFile)
	if err != nil {
		return nil, err
	}

	var lines []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func appendScanIgnore(managerHome string, path string) error {
	ignoreFile := filepath.Join(managerHome, "scan-ignore.txt")
	f, err := os.OpenFile(ignoreFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(path + "\n")
	return err
}

func inSlice(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

func findArg(name string, args []string) int {
	for i, arg := range args {
		if arg == name {
			return i
		}
	}
	return -1
}

// buildFingerprintIndex creates a map from fingerprint -> library skill name
// Used for fast fingerprint-based matching in scan.
func buildFingerprintIndex(libraryPath string) map[string]string {
	index := make(map[string]string)

	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		return index
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metaPath := filepath.Join(libraryPath, entry.Name(), ".skill-meta.yaml")
		meta, err := readSkillMeta(metaPath)
		if err != nil || meta.Fingerprint.SHA256 == "" {
			continue
		}

		index[meta.Fingerprint.SHA256] = entry.Name()
	}

	return index
}
