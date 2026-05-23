package cli

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Flow-Forge-Lab-Team/skills-manager/detectors"
)

type compatibilityDetector struct {
	ID         string   `json:"id,omitempty"`
	Harness    string   `json:"harness,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	Patterns   []string `json:"patterns,omitempty"`
}

type requirementDetector struct {
	ID          string              `json:"id,omitempty"`
	Requirement detectorRequirement `json:"requirement,omitempty"`
	Confidence  string              `json:"confidence,omitempty"`
	Patterns    []string            `json:"patterns,omitempty"`
}

type detectorRequirement struct {
	Kind     string `json:"kind,omitempty"`
	Name     string `json:"name,omitempty"`
	Required bool   `json:"required,omitempty"`
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
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	return parseCompatibilityDetectors(lines)
}

func readRequirementDetectors(path string) ([]requirementDetector, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	return parseRequirementDetectors(lines)
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

// --- Embed support for built-in detectors (fixes Codex P2: detectors not available in installed binary) ---

// readEmbeddedYAML is the embed equivalent of readLines.
func readEmbeddedYAML(path string) ([]string, error) {
	data, err := fs.ReadFile(detectors.FS, path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.Split(text, "\n"), nil
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
	lines, err := readEmbeddedYAML(path)
	if err != nil {
		return nil, err
	}
	return parseCompatibilityDetectors(lines)
}

func readRequirementDetectorsFromEmbed(path string) ([]requirementDetector, error) {
	lines, err := readEmbeddedYAML(path)
	if err != nil {
		return nil, err
	}
	return parseRequirementDetectors(lines)
}

// parse* functions are the extracted parsing logic so both FS and embed paths can share it.
func parseCompatibilityDetectors(lines []string) ([]compatibilityDetector, error) {
	var detectors []compatibilityDetector

	for i := 0; i < len(lines); i++ {
		raw := stripComment(lines[i])
		if strings.TrimSpace(raw) == "" {
			continue
		}

		key, _, ok := splitYAMLKey(raw)
		if !ok {
			continue
		}

		if key == "detectors" {
			for i = i + 1; i < len(lines); i++ {
				raw = stripComment(lines[i])
				trimmed := strings.TrimSpace(raw)
				if trimmed == "" {
					continue
				}
				if strings.HasPrefix(trimmed, "- id:") {
					det := compatibilityDetector{}
					det.ID = unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- id:")))
					for i = i + 1; i < len(lines); i++ {
						raw = stripComment(lines[i])
						trimmed = strings.TrimSpace(raw)
						if trimmed == "" {
							continue
						}
						if strings.HasPrefix(trimmed, "- ") {
							i--
							break
						}
						key, val, ok := splitYAMLKey(raw)
						if !ok {
							if !strings.HasPrefix(raw, "  ") {
								i--
								break
							}
							continue
						}
						switch key {
						case "harness":
							det.Harness = unquote(val)
						case "confidence":
							det.Confidence = unquote(val)
						case "patterns":
							items, next := readYAMLStringList(lines, i, val)
							i = next
							det.Patterns = items
						}
					}
					detectors = append(detectors, det)
				}
			}
			break
		}
	}
	return detectors, nil
}

func parseRequirementDetectors(lines []string) ([]requirementDetector, error) {
	var detectors []requirementDetector

	for i := 0; i < len(lines); i++ {
		raw := stripComment(lines[i])
		if strings.TrimSpace(raw) == "" {
			continue
		}

		key, _, ok := splitYAMLKey(raw)
		if !ok {
			continue
		}

		if key == "detectors" {
			for i = i + 1; i < len(lines); i++ {
				raw = stripComment(lines[i])
				trimmed := strings.TrimSpace(raw)
				if trimmed == "" {
					continue
				}
				if strings.HasPrefix(trimmed, "- id:") {
					det := requirementDetector{}
					det.ID = unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- id:")))
					var section2 string
					for i = i + 1; i < len(lines); i++ {
						raw = stripComment(lines[i])
						trimmed = strings.TrimSpace(raw)
						if trimmed == "" {
							continue
						}
						if strings.HasPrefix(trimmed, "- ") {
							i--
							break
						}
						key, val, ok := splitYAMLKey(raw)
						if !ok {
							if !strings.HasPrefix(raw, "  ") {
								i--
								break
							}
							continue
						}
						switch key {
						case "requirement":
							section2 = "requirement"
						case "confidence":
							det.Confidence = unquote(val)
						case "patterns":
							items, next := readYAMLStringList(lines, i, val)
							i = next
							det.Patterns = items
						case "kind":
							if section2 == "requirement" {
								det.Requirement.Kind = unquote(val)
							}
						case "name":
							if section2 == "requirement" {
								det.Requirement.Name = unquote(val)
							}
						case "required":
							if section2 == "requirement" {
								det.Requirement.Required = val == "true"
							}
						}
					}
					detectors = append(detectors, det)
				}
			}
			break
		}
	}
	return detectors, nil
}
