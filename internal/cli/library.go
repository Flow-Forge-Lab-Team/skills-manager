package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type skillOrigin struct {
	Type        string `json:"type,omitempty" yaml:"type,omitempty"`
	Source      string `json:"source,omitempty" yaml:"source,omitempty"`
	Path        string `json:"path,omitempty" yaml:"path,omitempty"`
	URL         string `json:"url,omitempty" yaml:"url,omitempty"`
	Version     string `json:"version,omitempty" yaml:"version,omitempty"`
	Commit      string `json:"commit,omitempty" yaml:"commit,omitempty"`
	InstalledAt string `json:"installed_at,omitempty" yaml:"installed_at,omitempty"`
	InstalledBy string `json:"installed_by,omitempty" yaml:"installed_by,omitempty"`
}

type skillFingerprint struct {
	SHA256 string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty" yaml:"size,omitempty"`
}

type skillCategorization struct {
	Source        string `json:"source,omitempty" yaml:"source,omitempty"`
	CategorizedAt string `json:"categorized_at,omitempty" yaml:"categorized_at,omitempty"`
	ByTool        string `json:"by_tool,omitempty" yaml:"by_tool,omitempty"`
	Confidence    string `json:"confidence,omitempty" yaml:"confidence,omitempty"`
}

type skillMeta struct {
	Version        int                 `yaml:"version"`
	Origin         skillOrigin         `yaml:"origin,omitempty"`
	Fingerprint    skillFingerprint    `yaml:"fingerprint,omitempty"`
	Categorization skillCategorization `yaml:"categorization,omitempty"`
	Categories     []string            `yaml:"categories,omitempty"`
	Tags           []string            `yaml:"tags,omitempty"`
	Compatibility  compatibility       `yaml:"compatibility,omitempty"`
	Requirements   requirements        `yaml:"requirements,omitempty"`
	Summary        string              `yaml:"summary,omitempty"`
	LocalChanges   bool                `yaml:"local_changes,omitempty"`
	LastChangedAt  string              `yaml:"last_changed_at,omitempty"`
	// Pinned, when set, freezes the skill at this version: the check/poll path
	// will not stage upstream updates past it until the pin is removed.
	Pinned string `yaml:"pinned,omitempty"`
}

func ensureLibrary(home string) (string, error) {
	libraryPath := filepath.Join(home, "library")
	if err := os.MkdirAll(libraryPath, 0755); err != nil {
		return "", fmt.Errorf("create library directory: %w", err)
	}
	return libraryPath, nil
}

func parseSkillFrontmatter(path string) (name, description string, err error) {
	decl, _, _ := parseSkillFrontmatterFull(path)
	return decl.name, decl.description, nil
}

type skillFrontmatterDeclaration struct {
	name        string
	description string
	compatible  []string
	exclusive   string
	reason      string
}

func parseSkillFrontmatterFull(path string) (skillFrontmatterDeclaration, compatibilityDeclaration, error) {
	file, err := os.Open(path)
	if err != nil {
		return skillFrontmatterDeclaration{}, compatibilityDeclaration{}, err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return skillFrontmatterDeclaration{}, compatibilityDeclaration{}, err
	}

	text := string(content)
	if !strings.HasPrefix(text, "---") {
		return skillFrontmatterDeclaration{}, compatibilityDeclaration{}, nil
	}

	endIdx := strings.Index(text[3:], "---")
	if endIdx == -1 {
		return skillFrontmatterDeclaration{}, compatibilityDeclaration{}, nil
	}

	frontmatter := text[3 : endIdx+3]
	var parsed struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Compatible  []string `yaml:"compatible"`
		Exclusive   string   `yaml:"exclusive"`
		Reason      string   `yaml:"reason"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &parsed); err != nil {
		return skillFrontmatterDeclaration{}, compatibilityDeclaration{}, err
	}
	decl := skillFrontmatterDeclaration{
		name:        parsed.Name,
		description: strings.TrimSpace(strings.ReplaceAll(parsed.Description, "\n", " ")),
		compatible:  parsed.Compatible,
		exclusive:   parsed.Exclusive,
		reason:      parsed.Reason,
	}
	if len(decl.description) > 200 {
		decl.description = decl.description[:200]
	}

	// Convert to compatibilityDeclaration if there are declarations
	var compDecl compatibilityDeclaration
	if decl.exclusive != "" {
		compDecl.Mode = "exclusive"
		compDecl.Harness = decl.exclusive
		compDecl.Reason = decl.reason
	} else if len(decl.compatible) > 0 {
		compDecl.Mode = "compatible"
		compDecl.Harnesses = decl.compatible
	}

	return decl, compDecl, nil
}

func fingerprintSkillMd(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}

	hexStr := hex.EncodeToString(hash.Sum(nil))
	return hexStr, written, nil
}

func readSkillMeta(path string) (skillMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return skillMeta{Version: 1}, nil
	}
	meta := skillMeta{Version: 1}
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return skillMeta{Version: 1}, err
	}
	if meta.Version == 0 {
		meta.Version = 1
	}
	return meta, nil
}

func writeSkillMeta(path string, meta skillMeta) error {
	if meta.Version == 0 {
		meta.Version = 1
	}
	return writeYAMLFile(path, meta)
}

// updateCompatibilitySection reads the existing sidecar (if any), surgically replaces
// only the top-level "compatibility:" block with a fresh serialization from meta,
// and writes the result back. This preserves any unmodeled requirement fields
// (scripts, credentials, advanced model options, etc.) when we only need to update
// compatibility metadata.
func updateCompatibilitySection(path string, meta skillMeta) error {
	original, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist or unreadable — fall back to full write
		return writeSkillMeta(path, meta)
	}

	lines := strings.Split(string(original), "\n")

	// Find the top-level "compatibility:" block
	start := -1
	end := len(lines)
	indentLevel := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "compatibility:") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			start = i
			indentLevel = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		if start != -1 {
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if strings.TrimSpace(line) != "" && lineIndent <= indentLevel {
				end = i
				break
			}
		}
	}

	if start == -1 {
		// No existing compatibility block — just append a new one at the end
		var buf strings.Builder
		buf.WriteString(string(original))
		if len(original) > 0 && !strings.HasSuffix(string(original), "\n") {
			buf.WriteString("\n")
		}
		fmt.Fprint(&buf, "compatibility:\n")
		if meta.Compatibility.Mode != "" {
			fmt.Fprintf(&buf, "  mode: %s\n", meta.Compatibility.Mode)
		}
		if meta.Compatibility.Harness != "" {
			fmt.Fprintf(&buf, "  harness: %s\n", meta.Compatibility.Harness)
		}
		if len(meta.Compatibility.Harnesses) > 0 {
			fmt.Fprint(&buf, "  harnesses: [")
			for i, h := range meta.Compatibility.Harnesses {
				if i > 0 {
					fmt.Fprint(&buf, ", ")
				}
				fmt.Fprintf(&buf, "%q", h)
			}
			fmt.Fprint(&buf, "]\n")
		}
		if meta.Compatibility.ExplicitPortable {
			fmt.Fprint(&buf, "  explicit_portable: true\n")
		}
		if meta.Compatibility.Declared != nil {
			fmt.Fprint(&buf, "  declared:\n")
			if meta.Compatibility.Declared.Mode != "" {
				fmt.Fprintf(&buf, "    mode: %s\n", meta.Compatibility.Declared.Mode)
			}
			if meta.Compatibility.Declared.Harness != "" {
				fmt.Fprintf(&buf, "    harness: %s\n", meta.Compatibility.Declared.Harness)
			}
			if len(meta.Compatibility.Declared.Harnesses) > 0 {
				fmt.Fprint(&buf, "    harnesses: [")
				for i, h := range meta.Compatibility.Declared.Harnesses {
					if i > 0 {
						fmt.Fprint(&buf, ", ")
					}
					fmt.Fprintf(&buf, "%q", h)
				}
				fmt.Fprint(&buf, "]\n")
			}
			if meta.Compatibility.Declared.Reason != "" {
				fmt.Fprintf(&buf, "    reason: %q\n", meta.Compatibility.Declared.Reason)
			}
		}
		return os.WriteFile(path, []byte(buf.String()), 0644)
	}

	// Build the new compatibility block
	var newBlock strings.Builder
	fmt.Fprint(&newBlock, "compatibility:\n")
	if meta.Compatibility.Mode != "" {
		fmt.Fprintf(&newBlock, "  mode: %s\n", meta.Compatibility.Mode)
	}
	if meta.Compatibility.Harness != "" {
		fmt.Fprintf(&newBlock, "  harness: %s\n", meta.Compatibility.Harness)
	}
	if len(meta.Compatibility.Harnesses) > 0 {
		fmt.Fprint(&newBlock, "  harnesses: [")
		for i, h := range meta.Compatibility.Harnesses {
			if i > 0 {
				fmt.Fprint(&newBlock, ", ")
			}
			fmt.Fprintf(&newBlock, "%q", h)
		}
		fmt.Fprint(&newBlock, "]\n")
	}
	if meta.Compatibility.ExplicitPortable {
		fmt.Fprint(&newBlock, "  explicit_portable: true\n")
	}
	if meta.Compatibility.Declared != nil {
		fmt.Fprint(&newBlock, "  declared:\n")
		if meta.Compatibility.Declared.Mode != "" {
			fmt.Fprintf(&newBlock, "    mode: %s\n", meta.Compatibility.Declared.Mode)
		}
		if meta.Compatibility.Declared.Harness != "" {
			fmt.Fprintf(&newBlock, "    harness: %s\n", meta.Compatibility.Declared.Harness)
		}
		if len(meta.Compatibility.Declared.Harnesses) > 0 {
			fmt.Fprint(&newBlock, "    harnesses: [")
			for i, h := range meta.Compatibility.Declared.Harnesses {
				if i > 0 {
					fmt.Fprint(&newBlock, ", ")
				}
				fmt.Fprintf(&newBlock, "%q", h)
			}
			fmt.Fprint(&newBlock, "]\n")
		}
		if meta.Compatibility.Declared.Reason != "" {
			fmt.Fprintf(&newBlock, "    reason: %q\n", meta.Compatibility.Declared.Reason)
		}
	}
	if len(meta.Compatibility.Detected) > 0 {
		fmt.Fprint(&newBlock, "  detected:\n")
		for _, harness := range sortedDetectionHarnesses(meta.Compatibility.Detected) {
			result := meta.Compatibility.Detected[harness]
			fmt.Fprintf(&newBlock, "    %s:\n", harness)
			fmt.Fprintf(&newBlock, "      confidence: %s\n", result.Confidence)
			if len(result.Reasons) > 0 {
				fmt.Fprint(&newBlock, "      reasons:\n")
				for _, r := range result.Reasons {
					fmt.Fprintf(&newBlock, "        - %q\n", r)
				}
			}
		}
	}

	// Replace [start, end) with the new block
	var result strings.Builder
	result.WriteString(strings.Join(lines[:start], "\n"))
	if start > 0 {
		result.WriteString("\n")
	}
	result.WriteString(newBlock.String())
	if end < len(lines) {
		result.WriteString(strings.Join(lines[end:], "\n"))
	}

	return os.WriteFile(path, []byte(result.String()), 0644)
}

func sortedDetectionHarnesses(detected map[string]detectionResult) []string {
	harnesses := make([]string, 0, len(detected))
	for harness := range detected {
		harnesses = append(harnesses, harness)
	}
	sort.Strings(harnesses)
	return harnesses
}

func rebuildCatalogFromLibrary(libraryPath string) (catalog, error) {
	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		return catalog{}, err
	}

	detectors, _ := loadDetectors()

	var skills []catalogSkill

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		skillPath := filepath.Join(libraryPath, entry.Name())
		skillMdPath := filepath.Join(skillPath, "SKILL.md")

		if _, err := os.Stat(skillMdPath); err != nil {
			continue
		}

		// Parse the skill declaration from SKILL.md
		decl, compDecl, _ := parseSkillFrontmatterFull(skillMdPath)
		name := decl.name
		if name == "" {
			name = entry.Name()
		}

		var meta skillMeta
		metaPath := filepath.Join(skillPath, ".skill-meta.yaml")
		sidecarExisted := false
		if _, err := os.Stat(metaPath); err == nil {
			meta, _ = readSkillMeta(metaPath)
			sidecarExisted = true
		}

		// Read skill body once for detection and inference
		skillBody, _ := readSkillBody(skillMdPath)

		// Ensure Detected is populated from live detection for the purpose of the
		// "did anything change?" comparison. This makes the no-op rewrite protection
		// work even though we don't yet fully round-trip the detected map on read.
		if len(meta.Compatibility.Detected) == 0 {
			if d := detectCompatibility(detectors, skillBody); len(d) > 0 {
				meta.Compatibility.Detected = d
			}
		}

		// Refresh the sidecar's Declared block from frontmatter when present (source of truth).
		// Only mark for write if the modeled compatibility state actually changes.
		oldCompat := meta.Compatibility // snapshot before refresh

		if compDecl.Mode != "" {
			meta.Compatibility.Declared = &compDecl
			meta.Compatibility.ExplicitPortable = false
		} else {
			meta.Compatibility.Declared = nil
		}

		// Check for explicit declaration: either from frontmatter OR from explicit_portable flag
		hasFrontmatterDecl := compDecl.Mode != ""
		hasExplicitPortable := meta.Compatibility.ExplicitPortable

		if hasFrontmatterDecl {
			// Frontmatter declaration takes precedence
			effectiveDecl := compDecl
			meta.Compatibility.Mode = effectiveDecl.Mode
			meta.Compatibility.Harness = effectiveDecl.Harness
			meta.Compatibility.Harnesses = effectiveDecl.Harnesses
			// If portable, ensure harness fields are cleared
			if effectiveDecl.Mode == "portable" {
				meta.Compatibility.Harness = ""
				meta.Compatibility.Harnesses = nil
			}
		} else if hasExplicitPortable {
			// Explicit portable flag set
			meta.Compatibility.Mode = "portable"
			meta.Compatibility.Harness = ""
			meta.Compatibility.Harnesses = nil
		} else {
			// No explicit declaration: run detection and apply auto-classification
			detected := detectCompatibility(detectors, skillBody)
			if len(detected) > 0 {
				meta.Compatibility.Detected = detected
				// Apply auto-classification rule to set effective mode/harness/harnesses
				autoClass := applyAutoClassification(detected)
				if autoClass.Mode != "" && autoClass.Mode != "portable" {
					meta.Compatibility.Mode = autoClass.Mode
					meta.Compatibility.Harness = autoClass.Harness
					meta.Compatibility.Harnesses = autoClass.Harnesses
				} else if meta.Categorization.Source == "seed-remap" &&
					(meta.Compatibility.Mode == "exclusive" || meta.Compatibility.Mode == "compatible") {
					// Seed catalog classifications are curated metadata. Preserve them
					// when weak detector signals do not produce a stronger classification.
				} else {
					meta.Compatibility.Mode = autoClass.Mode
					meta.Compatibility.Harness = autoClass.Harness
					meta.Compatibility.Harnesses = autoClass.Harnesses
				}
				// Do not set needsWrite here — let the final DeepEqual decide
			} else if meta.Categorization.Source == "seed-remap" &&
				(meta.Compatibility.Mode == "exclusive" || meta.Compatibility.Mode == "compatible") {
				// Seed catalog classifications are curated metadata. Preserve them
				// when no stronger frontmatter or detector signal overrides them.
			} else if meta.Compatibility.Mode != "portable" || meta.Compatibility.Harness != "" || len(meta.Compatibility.Harnesses) > 0 {
				// No detection signals: default to portable
				meta.Compatibility.Mode = "portable"
				meta.Compatibility.Harness = ""
				meta.Compatibility.Harnesses = nil
				// Do not set needsWrite here — let the final DeepEqual decide
			}
		}

		// Track separately whether compatibility or requirements modeling actually changed.
		compatibilityChanged := !reflect.DeepEqual(oldCompat, meta.Compatibility)
		requirementsChanged := false

		// Infer requirements only if marked as inferred or all requirement fields are empty.
		// If Inferred=false and any field is populated, preserve the explicit requirements.
		hasExplicitRequirements := meta.Requirements.hasExplicitFields()
		// If the sidecar already existed and has no modeled requirement fields,
		// it may contain unmodeled explicit requirements (scripts, credentials, etc.).
		// In that case, do not overwrite with inference.
		if meta.Requirements.Inferred || (!hasExplicitRequirements && !sidecarExisted) {
			inferred := inferRequirements(detectors, skillBody)
			inferred.Inferred = true // normalize so DeepEqual only cares about modeled data
			oldRequirements := meta.Requirements
			if sidecarHasUnmodeledRequirements(metaPath) {
				mergeSeedRequirements(&meta.Requirements, inferred)
			} else {
				meta.Requirements = inferred
			}
			if !reflect.DeepEqual(oldRequirements, meta.Requirements) {
				requirementsChanged = true
			}
		}

		if meta.Compatibility.Mode == "" {
			meta.Compatibility.Mode = "portable"
		}

		// Write strategy:
		// - If requirements modeling changed → full modeled write is correct.
		// - If only compatibility changed → surgically update only the compatibility
		//   section in the raw file so we don't clobber unmodeled requirement fields.
		if requirementsChanged {
			if sidecarHasUnmodeledRequirements(metaPath) {
				_ = writeSeedSkillMeta(metaPath, meta)
			} else {
				_ = writeSkillMeta(metaPath, meta)
			}
		} else if compatibilityChanged {
			updateCompatibilitySection(metaPath, meta)
		}

		summary := meta.Summary
		if summary == "" {
			summary = decl.description
		}

		skill := catalogSkill{
			Name:          name,
			Categories:    meta.Categories,
			Tags:          meta.Tags,
			Compatibility: meta.Compatibility,
			Requirements:  meta.Requirements,
			Summary:       summary,
		}

		skills = append(skills, skill)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	return catalog{Skills: skills}, nil
}

func writeCatalog(path string, cat catalog) error {
	cat.Version = 1
	sort.SliceStable(cat.Skills, func(i, j int) bool {
		return cat.Skills[i].Name < cat.Skills[j].Name
	})
	return writeYAMLFile(path, cat)
}
