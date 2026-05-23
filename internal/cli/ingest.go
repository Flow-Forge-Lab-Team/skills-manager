package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ingestSource struct {
	kind   string // "github" | "local" | "marketplace" | "authored"
	raw    string // original user input, for error messages
	url    string // github only
	commit string // github only, filled by fetch
	path   string // local-equivalent staging path after fetch
	label  string // display label (e.g. "github.com/foo/bar @ abc1234")
}

type ingestOptions struct {
	auto        bool   // skip confirmation when high-confidence
	yes         bool   // accept all suggestions without prompting
	name        string // optional override (used by `new`)
	interactive bool   // false when --json, --quiet, or not a TTY
}

type ingestResult struct {
	Name        string                 `json:"name"`
	Skipped     bool                   `json:"skipped"`
	Reason      string                 `json:"reason,omitempty"`
	LibraryPath string                 `json:"library_path,omitempty"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	Categories  []string               `json:"categories,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Confidence  string                 `json:"confidence,omitempty"`
	Origin      map[string]interface{} `json:"origin,omitempty"`
}

func ingestFromSource(src ingestSource, opts ingestOptions, home string, out io.Writer) ingestResult {
	result := ingestResult{Origin: map[string]interface{}{}}

	// 1. Check SKILL.md exists
	skillMdPath := filepath.Join(src.path, "SKILL.md")
	if _, err := os.Stat(skillMdPath); err != nil {
		return ingestResult{
			Name:    opts.name,
			Skipped: true,
			Reason:  "no SKILL.md found at " + src.path,
		}
	}

	// 2. Parse frontmatter
	decl, compatDecl, err := parseSkillFrontmatterFull(skillMdPath)
	if err != nil || decl.name == "" {
		if opts.name == "" {
			return ingestResult{
				Name:    opts.name,
				Skipped: true,
				Reason:  "frontmatter missing or no name found",
			}
		}
		decl.name = opts.name
	}

	if opts.name != "" {
		decl.name = opts.name
	}

	result.Name = decl.name

	// 3. Compute fingerprint
	fp, size, err := fingerprintSkillMd(skillMdPath)
	if err != nil {
		return ingestResult{
			Name:    decl.name,
			Skipped: true,
			Reason:  "failed to fingerprint: " + err.Error(),
		}
	}
	result.Fingerprint = fp

	// 4. Check duplicate by fingerprint
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		return ingestResult{
			Name:    decl.name,
			Skipped: true,
			Reason:  "failed to access library: " + err.Error(),
		}
	}

	entries, _ := os.ReadDir(libraryPath)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(libraryPath, entry.Name(), ".skill-meta.yaml")
		existingMeta, err := readSkillMeta(metaPath)
		if err == nil && existingMeta.Fingerprint.SHA256 == fp {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "duplicate fingerprint",
			}
		}
	}

	// 5. Check name collision
	targetDir := filepath.Join(libraryPath, decl.name)
	if _, err := os.Stat(targetDir); err == nil {
		existingMetaPath := filepath.Join(targetDir, ".skill-meta.yaml")
		existingMeta, _ := readSkillMeta(existingMetaPath)
		if existingMeta.Fingerprint.SHA256 != fp {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "skill " + decl.name + " already in library with different content (use a different name or run `skills-manager update`)",
			}
		}
	}

	// 6. Run detectors
	skillBody, _ := os.ReadFile(skillMdPath)
	skillBodyStr := string(skillBody)

	detectors, _ := loadDetectors()
	compatResults := detectCompatibility(detectors, skillBodyStr)
	reqs := inferRequirements(detectors, skillBodyStr)
	cats, tags, _ := suggestCategories(decl.name, decl.description)

	// 7. Confidence calculation
	confidence := computeConfidence(decl, compatResults, cats)

	result.Categories = cats
	result.Tags = tags
	result.Confidence = confidence

	// 8. Confirmation flow
	if !opts.interactive && !opts.auto && !opts.yes {
		return ingestResult{
			Name:    decl.name,
			Skipped: true,
			Reason:  "ingest requires confirmation; rerun with --auto (high-confidence cases) or --yes (accept suggestions)",
		}
	}

	if opts.auto {
		if confidence != "high" {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "auto-ingest refused: confidence " + confidence + "; rerun without --auto",
			}
		}

		// Check for missing required dependencies
		missing := missingRequiredTools(reqs)
		missingMCP := missingRequiredMCPServers(reqs)
		missingModel := missingModelCapabilities(reqs)
		allMissing := append(append(missing, missingMCP...), missingModel...)

		if len(allMissing) > 0 {
			var parts []string
			if len(missing) > 0 {
				parts = append(parts, "tools="+strings.Join(missing, ","))
			}
			if len(missingMCP) > 0 {
				parts = append(parts, "mcp="+strings.Join(missingMCP, ","))
			}
			if len(missingModel) > 0 {
				parts = append(parts, "model="+strings.Join(missingModel, ","))
			}
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "auto-ingest refused: missing required dependencies (" + strings.Join(parts, ", ") + "); rerun without --auto",
			}
		}
	}

	if opts.interactive && !opts.auto && !opts.yes {
		fmt.Fprintf(out, "Ingest: %s\n", decl.name)
		fmt.Fprintf(out, "  Description: %s\n", decl.description)
		fmt.Fprintf(out, "  Categories: %v\n", cats)
		fmt.Fprintf(out, "  Confidence: %s\n", confidence)
		fmt.Fprint(out, "Accept? [Y/n/e] ")
		var response string
		_, err := fmt.Scanln(&response)
		// EOF or read error means stdin was closed/empty — treat as reject
		if err == io.EOF || err != nil {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "declined (no input)",
			}
		}
		response = strings.ToLower(strings.TrimSpace(response))
		if response == "e" {
			fmt.Fprintln(out, "edit not implemented in v0.1; rerun with --yes after hand-editing the source")
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "edit requested",
			}
		}
		if response == "n" {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "declined",
			}
		}
	}

	// 9. Commit
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return ingestResult{
			Name:    decl.name,
			Skipped: true,
			Reason:  "failed to create skill directory: " + err.Error(),
		}
	}

	// Copy SKILL.md and sibling files (not .git)
	if err := copySkillDirWithoutGit(src.path, targetDir); err != nil {
		return ingestResult{
			Name:    decl.name,
			Skipped: true,
			Reason:  "failed to copy skill files: " + err.Error(),
		}
	}

	// Prepare metadata
	meta := skillMeta{
		Version: 1,
		Fingerprint: skillFingerprint{
			SHA256: fp,
			Size:   size,
		},
		Categories: cats,
		Tags:       tags,
	}

	meta.Categorization.Confidence = confidence
	meta.Categorization.Source = "ingest"
	meta.Categorization.CategorizedAt = time.Now().UTC().Format(time.RFC3339)

	// Set origin
	meta.Origin.Type = src.kind
	meta.Origin.Source = src.kind
	if src.url != "" {
		meta.Origin.URL = src.url
	}
	if src.commit != "" {
		meta.Origin.Commit = src.commit
	}
	if src.path != "" && src.kind == "local" {
		meta.Origin.Path = src.path
	}
	meta.Origin.InstalledAt = time.Now().UTC().Format(time.RFC3339)

	// Compatibility from frontmatter
	if compatDecl.Harness != "" {
		meta.Compatibility.Mode = "exclusive"
		meta.Compatibility.Harness = compatDecl.Harness
		meta.Compatibility.Declared = &compatDecl
	} else if len(compatDecl.Harnesses) > 0 {
		meta.Compatibility.Mode = "compatible"
		meta.Compatibility.Harnesses = compatDecl.Harnesses
		meta.Compatibility.Declared = &compatDecl
	} else {
		meta.Compatibility.Mode = "portable"
		meta.Compatibility.ExplicitPortable = decl.exclusive == ""
	}

	// Detected compatibility
	meta.Compatibility.Detected = compatResults

	// Requirements
	meta.Requirements = reqs

	metaPath := filepath.Join(targetDir, ".skill-meta.yaml")
	if err := writeSkillMeta(metaPath, meta); err != nil {
		return ingestResult{
			Name:    decl.name,
			Skipped: true,
			Reason:  "failed to write metadata: " + err.Error(),
		}
	}

	// Rebuild catalog
	if _, err := rebuildCatalogFromLibrary(libraryPath); err != nil {
		return ingestResult{
			Name:    decl.name,
			Skipped: true,
			Reason:  "failed to rebuild catalog: " + err.Error(),
		}
	}

	result.LibraryPath = targetDir
	return result
}

func computeConfidence(decl skillFrontmatterDeclaration, compatResults map[string]detectionResult, cats []string) string {
	// high: exclusive declaration AND at least one category match
	// medium: at least one signal hit
	// low: no signals

	signals := 0

	// Check for exclusive declaration
	hasExclusive := decl.exclusive != ""
	if hasExclusive {
		signals++
	}

	// Check for compat signals
	hasCompatSignal := len(compatResults) > 0
	if hasCompatSignal {
		signals++
	}

	// Check for category match
	hasCategoryMatch := len(cats) > 0
	if hasCategoryMatch {
		signals++
	}

	if hasExclusive && hasCategoryMatch {
		return "high"
	}

	if hasCompatSignal && hasCategoryMatch {
		return "high"
	}

	if signals > 0 {
		return "medium"
	}

	return "low"
}

func copySkillDirWithoutGit(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		// Skip .git
		if entry.Name() == ".git" {
			continue
		}

		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			info, _ := os.Stat(srcPath)
			if err := copyFile(srcPath, dstPath, info.Mode()); err != nil {
				return err
			}
		}
	}

	return nil
}
