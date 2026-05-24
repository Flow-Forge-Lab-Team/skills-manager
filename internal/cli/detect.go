package cli

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Flow-Forge-Lab-Team/skills-manager/detectors"
	"gopkg.in/yaml.v3"
)

type compatibilityDetector struct {
	ID         string   `json:"id,omitempty" yaml:"id,omitempty"`
	Harness    string   `json:"harness,omitempty" yaml:"harness,omitempty"`
	Confidence string   `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Patterns   []string `json:"patterns,omitempty" yaml:"patterns,omitempty"`
}

type requirementDetector struct {
	ID          string              `json:"id,omitempty" yaml:"id,omitempty"`
	Requirement detectorRequirement `json:"requirement,omitempty" yaml:"requirement,omitempty"`
	Confidence  string              `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Patterns    []string            `json:"patterns,omitempty" yaml:"patterns,omitempty"`
}

type detectorRequirement struct {
	Kind     string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
}

type detectorSet struct {
	compatibilityDetectors []compatibilityDetector
	requirementDetectors   []requirementDetector
}

func loadDetectors() (detectorSet, error) {
	set := detectorSet{
		compatibilityDetectors: []compatibilityDetector{},
		requirementDetectors:   []requirementDetector{},
	}

	// Always load the built-in detectors from the embedded FS first.
	// This makes the compatibility + requirements inference work for
	// normal `go install` / binary usage, not just source checkouts.
	loadEmbeddedDetectors(&set)

	detectorsDir := os.Getenv("SKILLS_MANAGER_DETECTORS_DIR")
	if detectorsDir == "" {
		// Try multiple paths to find detectors
		candidates := []string{}

		// 1. Resolve relative to installed binary
		if exePath, err := os.Executable(); err == nil {
			if evalPath, err := filepath.EvalSymlinks(exePath); err == nil {
				exeDir := filepath.Dir(evalPath)
				// Try <exe-dir>/detectors
				candidates = append(candidates, filepath.Join(exeDir, "detectors"))
				// Try <exe-dir>/../detectors (when binary is in bin/ subdir)
				candidates = append(candidates, filepath.Join(exeDir, "..", "detectors"))
			}
		}

		// 2. User home directory
		if userHome, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(userHome, ".skills-manager", "detectors"))
		}

		// 3. CWD-based fallback (for tests without install)
		candidates = append(candidates, "detectors")
		candidates = append(candidates, filepath.Join("..", "..", "detectors"))

		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				detectorsDir = candidate
				break
			}
		}
		if detectorsDir == "" {
			// None found; create empty set
			return set, nil
		}
	}

	// Load compatibility detectors
	compatDir := filepath.Join(detectorsDir, "compatibility")
	if entries, err := os.ReadDir(compatDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
				path := filepath.Join(compatDir, entry.Name())
				if dets, err := readCompatibilityDetectors(path); err == nil {
					set.compatibilityDetectors = append(set.compatibilityDetectors, dets...)
				}
			}
		}
	}

	// Load requirement detectors
	reqDir := filepath.Join(detectorsDir, "requirements")
	if entries, err := os.ReadDir(reqDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
				path := filepath.Join(reqDir, entry.Name())
				if dets, err := readRequirementDetectors(path); err == nil {
					set.requirementDetectors = append(set.requirementDetectors, dets...)
				}
			}
		}
	}

	return set, nil
}

func readCompatibilityDetectors(path string) ([]compatibilityDetector, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCompatibilityDetectors(data)
}

func readRequirementDetectors(path string) ([]requirementDetector, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseRequirementDetectors(data)
}

func detectCompatibility(detectors detectorSet, skillBody string) map[string]detectionResult {
	results := make(map[string]detectionResult)
	detectedHarnesses := make(map[string]map[string]bool) // harness -> pattern matches

	for _, det := range detectors.compatibilityDetectors {
		for _, pattern := range det.Patterns {
			if matchPattern(pattern, skillBody) {
				if detectedHarnesses[det.Harness] == nil {
					detectedHarnesses[det.Harness] = make(map[string]bool)
				}
				detectedHarnesses[det.Harness][det.ID] = true

				current := results[det.Harness]
				current.Confidence = maxConfidence(current.Confidence, det.Confidence)
				results[det.Harness] = current
			}
		}
	}

	// Build reason strings
	for harness, patterns := range detectedHarnesses {
		var reasons []string
		for det := range patterns {
			reasons = append(reasons, det)
		}
		sort.Strings(reasons)
		existing := results[harness]
		existing.Reasons = reasons
		results[harness] = existing
	}

	return results
}

func inferRequirements(detectors detectorSet, skillBody string) requirements {
	seenTools := make(map[string]bool)
	seenMCP := make(map[string]bool)
	var tools []toolRequirement
	var mcpServers []mcpRequirement
	var model modelRequirement

	for _, det := range detectors.requirementDetectors {
		for _, pattern := range det.Patterns {
			if !matchPattern(pattern, skillBody) {
				continue
			}

			switch det.Requirement.Kind {
			case "tool":
				if !seenTools[det.Requirement.Name] {
					tools = append(tools, toolRequirement{
						Name:     det.Requirement.Name,
						Required: det.Requirement.Required,
					})
					seenTools[det.Requirement.Name] = true
				}
			case "mcp_server":
				if !seenMCP[det.Requirement.Name] {
					mcpServers = append(mcpServers, mcpRequirement{
						Name:     det.Requirement.Name,
						Required: det.Requirement.Required,
					})
					seenMCP[det.Requirement.Name] = true
				}
			case "model":
				// For model requirements, use the name as the capability key.
				// e.g., "tool_use" is the capability with status from Required.
				if det.Requirement.Required {
					model.ToolUse = "required"
				}
			}
		}
	}

	return requirements{
		Tools:      tools,
		MCPServers: mcpServers,
		Model:      model,
	}
}

func matchPattern(pattern, text string) bool {
	// Use regex for the sentinel pattern "mcp__[hex]__" (generic MCP UUID format).
	// All other patterns use substring matching, including literal patterns like "mcp__linear__".
	if pattern == "mcp__[hex]__" {
		// Build a simple regex for generic mcp UUID pattern
		re := regexp.MustCompile(`mcp__[a-f0-9]+__`)
		return re.MatchString(text)
	}

	// Support simple ^ anchor (start of text or start of line)
	if strings.HasPrefix(pattern, "^") {
		rest := pattern[1:]
		if strings.HasPrefix(text, rest) {
			return true
		}
		// Also allow after a newline (common in multi-line skill bodies)
		return strings.Contains(text, "\n"+rest)
	}

	// Simple substring match for all other patterns
	return strings.Contains(text, pattern)
}

// maxConfidence returns the stronger of two confidence levels ("high" > "medium" > "low").
func maxConfidence(a, b string) string {
	if a == "high" || b == "high" {
		return "high"
	}
	if a == "medium" || b == "medium" {
		return "medium"
	}
	if a == "low" || b == "low" {
		return "low"
	}
	return b
}

// applyAutoClassification returns a compatibility declaration based on detection results.
// Rule: if one harness has high-confidence patterns and no other harness has any signal,
// mark exclusive. Else if multiple harnesses with at-least-medium confidence, mark compatible.
// Else leave portable.
func applyAutoClassification(detected map[string]detectionResult) compatibilityDeclaration {
	if len(detected) == 0 {
		// No signals: portable (default)
		return compatibilityDeclaration{Mode: "portable"}
	}

	// Collect harnesses by confidence level
	highConfidence := []string{}
	mediumOrHigher := []string{}

	for harness, result := range detected {
		if result.Confidence == "high" {
			highConfidence = append(highConfidence, harness)
			mediumOrHigher = append(mediumOrHigher, harness)
		} else if result.Confidence == "medium" {
			mediumOrHigher = append(mediumOrHigher, harness)
		}
	}

	// If exactly one harness with high confidence and no other signals: exclusive
	if len(highConfidence) == 1 && len(detected) == 1 {
		return compatibilityDeclaration{
			Mode:    "exclusive",
			Harness: highConfidence[0],
		}
	}

	// If multiple harnesses with at-least-medium confidence: compatible
	if len(mediumOrHigher) > 1 {
		sort.Strings(mediumOrHigher)
		return compatibilityDeclaration{
			Mode:      "compatible",
			Harnesses: mediumOrHigher,
		}
	}

	// Default: portable
	return compatibilityDeclaration{Mode: "portable"}
}

func readSkillBody(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	text := string(content)
	// Skip frontmatter if present
	if strings.HasPrefix(text, "---") {
		endIdx := strings.Index(text[3:], "---")
		if endIdx != -1 {
			return text[endIdx+6:], nil
		}
	}
	return text, nil
}

func loadEmbeddedDetectors(set *detectorSet) {
	// compatibility (paths are relative to the embedded FS root)
	if entries, err := fs.ReadDir(detectors.FS, "compatibility"); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
				p := "compatibility/" + e.Name()
				if dets, err := readCompatibilityDetectorsFromEmbed(p); err == nil {
					set.compatibilityDetectors = append(set.compatibilityDetectors, dets...)
				}
			}
		}
	}
	// requirements
	if entries, err := fs.ReadDir(detectors.FS, "requirements"); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
				p := "requirements/" + e.Name()
				if dets, err := readRequirementDetectorsFromEmbed(p); err == nil {
					set.requirementDetectors = append(set.requirementDetectors, dets...)
				}
			}
		}
	}
}

func readCompatibilityDetectorsFromEmbed(path string) ([]compatibilityDetector, error) {
	data, err := detectors.FS.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCompatibilityDetectors(data)
}

func readRequirementDetectorsFromEmbed(path string) ([]requirementDetector, error) {
	data, err := detectors.FS.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseRequirementDetectors(data)
}

// parse* functions are the extracted parsing logic so both FS and embed paths can share it.
func parseCompatibilityDetectors(data []byte) ([]compatibilityDetector, error) {
	var doc struct {
		Detectors []compatibilityDetector `yaml:"detectors"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Detectors, nil
}

func parseRequirementDetectors(data []byte) ([]requirementDetector, error) {
	var doc struct {
		Detectors []requirementDetector `yaml:"detectors"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Detectors, nil
}
