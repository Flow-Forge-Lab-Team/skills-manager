package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type scanResult struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Status        string `json:"status"`
	GuessedOrigin string `json:"guessed_origin"`
}

type scanMissingDependencies struct {
	Tools       []string `json:"tools,omitempty"`
	MCPServers  []string `json:"mcp_servers,omitempty"`
	Model       []string `json:"model,omitempty"`
	Credentials []string `json:"credentials,omitempty"`
	Runtimes    []string `json:"runtimes,omitempty"`
}

type scanAutoIngestCandidate struct {
	Result      scanResult
	SkillName   string
	Confidence  string
	Missing     scanMissingDependencies
	Precomputed *ingestPrecomputed
}

func runScan(args []string, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	var ingest, autoIngest bool
	var pathsOverride string
	var ignoredCount int

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

	searchPaths, err := scanSearchPaths(pathsOverride)
	if err != nil {
		fmt.Fprintf(stderr, "get home: %v\n", err)
		return ExitOpError
	}

	managerHomeDir, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	results, ignoredCount, err := collectScanResults(managerHomeDir, searchPaths)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitOpError
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
		interactive := ingest && !autoIngest && !gf.NonInteractive && !gf.JSON && !gf.Quiet
		// If stdin is not a TTY, require explicit consent
		if !stdinIsTTY() && interactive {
			// Return error: --ingest requires interactive stdin
			fmt.Fprintf(stderr, "error: scan --ingest requires interactive stdin; use --auto-ingest for non-interactive mode\n")
			return ExitUsageError
		}
		opts := ingestOptions{
			auto:        autoIngest,
			yes:         autoIngest,
			interactive: interactive,
		}
		blockedAutoIngest := map[string]scanAutoIngestCandidate{}
		if autoIngest {
			preflight := buildScanAutoIngestPreflight(results, opts, home)
			printScanAutoIngestPreflight(humanOut, preflight, len(results)+ignoredCount, ignoredCount)
			for _, candidate := range preflight {
				blockedAutoIngest[candidate.Result.Path] = candidate
			}
		}

		for _, r := range results {
			if r.Status != "unregistered" {
				fmt.Fprintf(humanOut, "Already known: %s (%s) - %s\n", r.Name, r.Path, r.Status)
				continue
			}

			candidate, hasPreflight := blockedAutoIngest[r.Path]
			if hasPreflight && candidate.Missing.any() {
				fmt.Fprintf(humanOut, "Blocked %s (%s): missing required dependencies (%s)\n", candidate.displayName(), r.Path, candidate.Missing.inline())
				continue
			}

			// Prepare source
			src := ingestSource{
				kind:  "local",
				raw:   r.Path,
				path:  r.Path,
				label: r.Path,
			}

			if interactive {
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
					fmt.Fprintf(humanOut, "Skipped %s (%s)\n", r.Name, r.Path)
					continue
				}
				if response == "s" {
					// Skip forever
					_ = appendScanIgnore(home, r.Path)
					fmt.Fprintf(humanOut, "Skipped forever: %s (%s)\n", r.Name, r.Path)
					continue
				}
				fmt.Fprintf(humanOut, "Accepted %s (%s)\n", r.Name, r.Path)
			} else if autoIngest {
				fmt.Fprintf(humanOut, "Evaluating %s (%s)\n", r.Name, r.Path)
			} else {
				fmt.Fprintf(humanOut, "Skipping %s (%s): ingest requires interactive confirmation; use --auto-ingest for high-confidence cases\n", r.Name, r.Path)
				continue
			}

			ingestOpts := opts
			if hasPreflight {
				ingestOpts.precomputed = candidate.Precomputed
			}
			result := ingestFromSource(src, ingestOpts, home, humanOut)
			if result.Skipped {
				fmt.Fprintf(humanOut, "%s %s (%s): %s\n", scanIngestOutcome(result), result.Name, r.Path, result.Reason)
			} else {
				fmt.Fprintf(humanOut, "Ingested %s (%s)\n", result.Name, r.Path)
			}
		}
	}

	return ExitSuccess
}

func scanSearchPaths(pathsOverride string) ([]string, error) {
	var searchPaths []string
	if pathsOverride != "" {
		for _, p := range strings.Split(pathsOverride, ",") {
			searchPaths = append(searchPaths, strings.TrimSpace(p))
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
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
	return searchPaths, nil
}

func collectScanResults(managerHomeDir string, searchPaths []string) ([]scanResult, int, error) {
	libraryPath, err := ensureLibrary(managerHomeDir)
	if err != nil {
		return nil, 0, fmt.Errorf("ensure library: %w", err)
	}

	// Build fingerprint index of library skills
	fingerprintIndex := buildFingerprintIndex(libraryPath)

	// Load scan-ignore list
	ignoreList, _ := loadScanIgnore(managerHomeDir)

	// Scan and collect results
	var results []scanResult
	var ignoredCount int
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

			if _, err := os.Stat(skillMdPath); err != nil {
				continue
			}

			// Check if skill is ignored
			if inSlice(ignoreList, skillPath) {
				ignoredCount++
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
	return results, ignoredCount, nil
}

func buildScanAutoIngestPreflight(results []scanResult, opts ingestOptions, home string) []scanAutoIngestCandidate {
	var candidates []scanAutoIngestCandidate
	for _, r := range results {
		if r.Status != "unregistered" {
			continue
		}
		src := ingestSource{
			kind:  "local",
			raw:   r.Path,
			path:  r.Path,
			label: r.Path,
		}
		candidates = append(candidates, analyzeScanAutoIngestCandidate(r, src, opts, home))
	}
	return candidates
}

func analyzeScanAutoIngestCandidate(result scanResult, src ingestSource, opts ingestOptions, home string) scanAutoIngestCandidate {
	candidate := scanAutoIngestCandidate{Result: result, SkillName: result.Name}
	skillMdPath := filepath.Join(src.path, "SKILL.md")
	decl, _, err := parseSkillFrontmatterFull(skillMdPath)
	if err == nil && decl.name != "" {
		candidate.SkillName = decl.name
	}
	if err != nil || decl.name == "" || !isValidSkillName(decl.name) {
		return candidate
	}

	fp, _, err := fingerprintSkillMd(skillMdPath)
	if err != nil {
		return candidate
	}
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		return candidate
	}
	entries, _ := os.ReadDir(libraryPath)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(libraryPath, entry.Name(), ".skill-meta.yaml")
		existingMeta, err := readSkillMeta(metaPath)
		if err == nil && existingMeta.Fingerprint.SHA256 == fp {
			return candidate
		}
	}
	targetDir := filepath.Join(libraryPath, decl.name)
	if _, err := os.Stat(targetDir); err == nil {
		existingMetaPath := filepath.Join(targetDir, ".skill-meta.yaml")
		existingMeta, _ := readSkillMeta(existingMetaPath)
		if existingMeta.Fingerprint.SHA256 != fp {
			return candidate
		}
	}

	skillBody, err := os.ReadFile(skillMdPath)
	if err != nil {
		return candidate
	}
	skillBodyStr := string(skillBody)

	var confidence string
	var reqs requirements
	var cats, tags []string
	var compatResults map[string]detectionResult
	var fromOut *ingestOutput
	categorizationSource := "ingest"
	if opts.auto && llmProviderConfigured(home) {
		prompt := buildIngestPrompt(decl.name, src.label, src.raw, skillBodyStr)
		output, err := runConfiguredLLMProvider(home, prompt)
		if err != nil {
			return candidate
		}
		fromOut, err = parseIngestOutput([]byte(output), "provider output")
		if err != nil {
			return candidate
		}
		cats = fromOut.Categories
		tags = fromOut.Tags
		confidence = fromOut.Confidence.Categories
		reqs = requirementsFromIngestOutput(fromOut)
		categorizationSource = "skills-ingest-provider"
	} else {
		detectors, _ := loadDetectors()
		compatResults = detectCompatibility(detectors, skillBodyStr)
		cats, tags, _ = suggestCategories(decl.name, decl.description)
		confidence = computeConfidence(decl, compatResults, cats)
		reqs = inferRequirements(detectors, skillBodyStr)
	}

	candidate.Confidence = confidence
	candidate.Missing = missingDependenciesForRequirements(reqs)
	candidate.Precomputed = &ingestPrecomputed{
		Categories:           cats,
		Tags:                 tags,
		Confidence:           confidence,
		CompatibilityResults: compatResults,
		Requirements:         reqs,
		FromOutput:           fromOut,
		CategorizationSource: categorizationSource,
	}
	return candidate
}

func printScanAutoIngestPreflight(out io.Writer, candidates []scanAutoIngestCandidate, discoveredCount int, ignoredCount int) {
	if out == io.Discard {
		return
	}

	eligibleCount := 0
	blockedCount := 0
	for _, candidate := range candidates {
		if candidate.Missing.any() {
			blockedCount++
			continue
		}
		if candidate.Confidence == "high" {
			eligibleCount++
		}
	}

	knownCount := discoveredCount - len(candidates)
	fmt.Fprintln(out, "Auto-ingest preflight:")
	fmt.Fprintf(out, "  Discovered candidates: %d\n", discoveredCount)
	fmt.Fprintf(out, "  Already in-library / duplicate / ignored: %d", knownCount)
	if ignoredCount > 0 {
		fmt.Fprintf(out, " (%d ignored)", ignoredCount)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Unregistered candidates: %d\n", len(candidates))
	fmt.Fprintf(out, "  Eligible for auto-ingest: %d\n", eligibleCount)
	fmt.Fprintf(out, "  Blocked by missing dependencies: %d\n", blockedCount)

	groups := groupScanMissingDependencies(candidates)
	if len(groups) > 0 {
		fmt.Fprintln(out, "Missing dependency groups:")
		for _, key := range sortedMapKeys(groups) {
			fmt.Fprintf(out, "  - %s: %s\n", key, strings.Join(groups[key], ", "))
		}
	}

	fmt.Fprintln(out, "Options:")
	fmt.Fprintln(out, "  - Continue now: --auto-ingest will ingest eligible candidates only and skip dependency-blocked candidates.")
	fmt.Fprintln(out, "  - Review first: rerun `skills-manager scan --ingest` to inspect candidates manually.")
	fmt.Fprintln(out, "  - Configure dependencies: install tools, enable MCP servers, provide credentials, or use a capable model, then rerun.")
	if len(candidates) > 0 && eligibleCount == 0 {
		fmt.Fprintln(out, "No eligible candidates remain after dependency preflight.")
	}
}

func missingDependenciesForRequirements(reqs requirements) scanMissingDependencies {
	return scanMissingDependencies{
		Tools:       missingRequiredTools(reqs),
		MCPServers:  missingRequiredMCPServers(reqs),
		Model:       missingModelCapabilities(reqs),
		Credentials: missingRequiredCredentials(reqs),
		Runtimes:    missingRequiredScriptRuntimes(reqs),
	}
}

func (m scanMissingDependencies) any() bool {
	return len(m.Tools)+len(m.MCPServers)+len(m.Model)+len(m.Credentials)+len(m.Runtimes) > 0
}

func (m scanMissingDependencies) inline() string {
	var parts []string
	if len(m.Tools) > 0 {
		parts = append(parts, "tools="+strings.Join(m.Tools, ","))
	}
	if len(m.MCPServers) > 0 {
		parts = append(parts, "mcp_servers="+strings.Join(m.MCPServers, ","))
	}
	if len(m.Model) > 0 {
		parts = append(parts, "model="+strings.Join(m.Model, ","))
	}
	if len(m.Credentials) > 0 {
		parts = append(parts, "credentials="+strings.Join(m.Credentials, ","))
	}
	if len(m.Runtimes) > 0 {
		parts = append(parts, "runtimes="+strings.Join(m.Runtimes, ","))
	}
	return strings.Join(parts, ", ")
}

func (c scanAutoIngestCandidate) displayName() string {
	if c.SkillName != "" {
		return c.SkillName
	}
	return c.Result.Name
}

func groupScanMissingDependencies(candidates []scanAutoIngestCandidate) map[string][]string {
	groups := map[string][]string{}
	for _, candidate := range candidates {
		if !candidate.Missing.any() {
			continue
		}
		label := fmt.Sprintf("%s (%s)", candidate.displayName(), candidate.Result.Path)
		addScanMissingDependencyGroups(groups, "tools", candidate.Missing.Tools, label)
		addScanMissingDependencyGroups(groups, "mcp_servers", candidate.Missing.MCPServers, label)
		addScanMissingDependencyGroups(groups, "model", candidate.Missing.Model, label)
		addScanMissingDependencyGroups(groups, "credentials", candidate.Missing.Credentials, label)
		addScanMissingDependencyGroups(groups, "runtimes", candidate.Missing.Runtimes, label)
	}
	return groups
}

func addScanMissingDependencyGroups(groups map[string][]string, kind string, names []string, label string) {
	for _, name := range names {
		key := kind + "=" + name
		groups[key] = append(groups[key], label)
	}
}

func sortedMapKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
		sort.Strings(m[key])
	}
	sort.Strings(keys)
	return keys
}

func scanIngestOutcome(result ingestResult) string {
	if strings.HasPrefix(result.Reason, "auto-ingest refused:") {
		return "Refused"
	}
	if result.Reason == "duplicate fingerprint" ||
		strings.HasPrefix(result.Reason, "declined") ||
		result.Reason == "edit requested" ||
		strings.HasPrefix(result.Reason, "ingest requires confirmation") {
		return "Skipped"
	}
	return "Failed"
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
