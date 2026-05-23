package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type skillOrigin struct {
	Type        string `json:"type,omitempty"`
	Source      string `json:"source,omitempty"`
	Path        string `json:"path,omitempty"`
	URL         string `json:"url,omitempty"`
	Version     string `json:"version,omitempty"`
	Commit      string `json:"commit,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	InstalledBy string `json:"installed_by,omitempty"`
}

type skillFingerprint struct {
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

type skillCategorization struct {
	Source        string `json:"source,omitempty"`
	CategorizedAt string `json:"categorized_at,omitempty"`
	ByTool        string `json:"by_tool,omitempty"`
	Confidence    string `json:"confidence,omitempty"`
}

type skillMeta struct {
	Version        int
	Origin         skillOrigin
	Fingerprint    skillFingerprint
	Categorization skillCategorization
	Categories     []string
	Tags           []string
	Compatibility  compatibility
	Requirements   requirements
	Summary        string
	LocalChanges   bool
	LastChangedAt  string
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
	lines := strings.Split(frontmatter, "\n")

	decl := skillFrontmatterDeclaration{}
	var inDescription bool
	var descLines []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "name:") {
			decl.name = unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "name:")))
			inDescription = false
			continue
		}

		if strings.HasPrefix(trimmed, "description:") {
			inDescription = true
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
			if rest != "" {
				descLines = append(descLines, rest)
			}
			continue
		}

		if inDescription {
			if strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
				inDescription = false
			} else {
				if !strings.HasPrefix(trimmed, "-") {
					descLines = append(descLines, trimmed)
				}
				continue
			}
		}

		if strings.HasPrefix(trimmed, "compatible:") {
			inDescription = false
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "compatible:"))
			if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
				decl.compatible = parseInlineList(rest)
			} else {
				items, next := readYAMLStringList(lines, i, rest)
				i = next
				decl.compatible = items
			}
			continue
		}

		if strings.HasPrefix(trimmed, "exclusive:") {
			inDescription = false
			decl.exclusive = unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "exclusive:")))
			continue
		}

		if strings.HasPrefix(trimmed, "reason:") {
			inDescription = false
			decl.reason = unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "reason:")))
			continue
		}
	}

	if len(descLines) > 0 {
		joined := strings.TrimSpace(strings.Join(descLines, " "))
		decl.description = unquote(joined)
		if len(decl.description) > 200 {
			decl.description = decl.description[:200]
		}
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
	lines, err := readLines(path)
	if err != nil {
		return skillMeta{Version: 1}, nil
	}

	meta := skillMeta{Version: 1}
	var section string
	var sectionIndent int

	for i := 0; i < len(lines); i++ {
		raw := stripComment(lines[i])
		if strings.TrimSpace(raw) == "" {
			continue
		}

		key, value, ok := splitYAMLKey(raw)
		if !ok {
			continue
		}

		currentIndent := indent(raw)

		switch key {
		case "version":
			if section == "" {
				if v, err := parseYAMLInt(value); err == nil {
					meta.Version = v
				}
			} else if section == "origin" {
				meta.Origin.Version = unquote(value)
			}
		case "categories", "tags":
			items, next := readYAMLStringList(lines, i, value)
			i = next
			if key == "categories" {
				meta.Categories = items
			} else {
				meta.Tags = items
			}
		case "summary":
			meta.Summary = unquote(value)
		case "local_changes":
			meta.LocalChanges = value == "true"
		case "last_changed_at":
			meta.LastChangedAt = unquote(value)
		case "origin":
			section = "origin"
			sectionIndent = currentIndent
		case "fingerprint":
			section = "fingerprint"
			sectionIndent = currentIndent
		case "categorization":
			section = "categorization"
			sectionIndent = currentIndent
		case "compatibility":
			section = "compatibility"
			sectionIndent = currentIndent
		case "requirements":
			section = "requirements"
			sectionIndent = currentIndent
		case "type":
			if section == "origin" {
				meta.Origin.Type = unquote(value)
			}
		case "source":
			if section == "origin" {
				meta.Origin.Source = unquote(value)
			} else if section == "categorization" {
				meta.Categorization.Source = unquote(value)
			}
		case "path":
			if section == "origin" {
				meta.Origin.Path = unquote(value)
			}
		case "url":
			if section == "origin" {
				meta.Origin.URL = unquote(value)
			}
		case "commit":
			if section == "origin" {
				meta.Origin.Commit = unquote(value)
			}
		case "installed_at":
			if section == "origin" {
				meta.Origin.InstalledAt = unquote(value)
			}
		case "categorized_at":
			if section == "categorization" {
				meta.Categorization.CategorizedAt = unquote(value)
			}
		case "installed_by":
			if section == "origin" {
				meta.Origin.InstalledBy = unquote(value)
			}
		case "sha256":
			if section == "fingerprint" {
				meta.Fingerprint.SHA256 = unquote(value)
			}
		case "size":
			if section == "fingerprint" {
				if sz, err := parseYAMLInt(value); err == nil {
					meta.Fingerprint.Size = int64(sz)
				}
			}
		case "by_tool":
			if section == "categorization" {
				meta.Categorization.ByTool = unquote(value)
			}
		case "confidence":
			if section == "categorization" {
				meta.Categorization.Confidence = unquote(value)
			}
		case "mode":
			if section == "compatibility" {
				meta.Compatibility.Mode = unquote(value)
			} else if section == "compatibility:declared" {
				if meta.Compatibility.Declared == nil {
					meta.Compatibility.Declared = &compatibilityDeclaration{}
				}
				meta.Compatibility.Declared.Mode = unquote(value)
			}
		case "harness":
			if section == "compatibility" {
				meta.Compatibility.Harness = unquote(value)
			} else if section == "compatibility:declared" {
				if meta.Compatibility.Declared == nil {
					meta.Compatibility.Declared = &compatibilityDeclaration{}
				}
				meta.Compatibility.Declared.Harness = unquote(value)
			}
		case "harnesses":
			if section == "compatibility" {
				items, next := readYAMLStringList(lines, i, value)
				i = next
				meta.Compatibility.Harnesses = items
			} else if section == "compatibility:declared" {
				if meta.Compatibility.Declared == nil {
					meta.Compatibility.Declared = &compatibilityDeclaration{}
				}
				items, next := readYAMLStringList(lines, i, value)
				i = next
				meta.Compatibility.Declared.Harnesses = items
			}
		case "reason":
			if section == "compatibility:declared" {
				if meta.Compatibility.Declared == nil {
					meta.Compatibility.Declared = &compatibilityDeclaration{}
				}
				meta.Compatibility.Declared.Reason = unquote(value)
			}
		case "declared":
			if section == "compatibility" {
				section = "compatibility:declared"
			}
		case "detected":
			if section == "compatibility" {
				section = "compatibility:detected"
				if meta.Compatibility.Detected == nil {
					meta.Compatibility.Detected = make(map[string]detectionResult)
				}
			}
		case "tools":
			if section == "requirements" || section == "requirements:model" {
				if section == "requirements:model" {
					section = "requirements" // Reset to parent section
				}
				if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
					meta.Requirements.Tools = toolRequirementsFromNames(parseInlineList(value))
				} else {
					items, next := readToolRequirements(lines, i)
					i = next
					meta.Requirements.Tools = items
				}
			}
		case "mcp_servers":
			if section == "requirements" || section == "requirements:model" {
				if section == "requirements:model" {
					section = "requirements" // Reset to parent section
				}
				if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
					meta.Requirements.MCPServers = mcpRequirementsFromNames(parseInlineList(value))
				} else {
					items, next := readMCPServerRequirements(lines, i)
					i = next
					meta.Requirements.MCPServers = items
				}
			}
		case "model":
			if section == "requirements" {
				section = "requirements:model"
				sectionIndent = currentIndent // Track the indent of "model:" itself
			}
		case "tool_use":
			if section == "requirements:model" {
				meta.Requirements.Model.ToolUse = unquote(value)
			}
		case "inferred":
			// "inferred" is at the requirements level, so if we're in a nested section, reset
			if strings.Contains(section, ":") && currentIndent == sectionIndent {
				section = strings.Split(section, ":")[0] // Get parent section
			}
			if section == "requirements" {
				meta.Requirements.Inferred = value == "true"
			}
		}
	}

	return meta, nil
}

func writeSkillMeta(path string, meta skillMeta) error {
	var buf strings.Builder

	fmt.Fprintf(&buf, "version: %d\n", meta.Version)

	if meta.Origin.Type != "" || meta.Origin.Source != "" || meta.Origin.Path != "" || meta.Origin.URL != "" {
		fmt.Fprint(&buf, "origin:\n")
		if meta.Origin.Type != "" {
			fmt.Fprintf(&buf, "  type: %s\n", meta.Origin.Type)
		}
		if meta.Origin.Source != "" {
			fmt.Fprintf(&buf, "  source: %s\n", meta.Origin.Source)
		}
		if meta.Origin.Path != "" {
			fmt.Fprintf(&buf, "  path: %s\n", meta.Origin.Path)
		}
		if meta.Origin.URL != "" {
			fmt.Fprintf(&buf, "  url: %s\n", meta.Origin.URL)
		}
		if meta.Origin.Version != "" {
			fmt.Fprintf(&buf, "  version: %q\n", meta.Origin.Version)
		}
		if meta.Origin.Commit != "" {
			fmt.Fprintf(&buf, "  commit: %s\n", meta.Origin.Commit)
		}
		if meta.Origin.InstalledAt != "" {
			fmt.Fprintf(&buf, "  installed_at: %s\n", meta.Origin.InstalledAt)
		}
		if meta.Origin.InstalledBy != "" {
			fmt.Fprintf(&buf, "  installed_by: %s\n", meta.Origin.InstalledBy)
		}
	}

	if meta.Fingerprint.SHA256 != "" || meta.Fingerprint.Size > 0 {
		fmt.Fprint(&buf, "fingerprint:\n")
		if meta.Fingerprint.SHA256 != "" {
			fmt.Fprintf(&buf, "  sha256: %s\n", meta.Fingerprint.SHA256)
		}
		if meta.Fingerprint.Size > 0 {
			fmt.Fprintf(&buf, "  size: %d\n", meta.Fingerprint.Size)
		}
	}

	if meta.Categorization.Source != "" || meta.Categorization.CategorizedAt != "" {
		fmt.Fprint(&buf, "categorization:\n")
		if meta.Categorization.Source != "" {
			fmt.Fprintf(&buf, "  source: %s\n", meta.Categorization.Source)
		}
		if meta.Categorization.CategorizedAt != "" {
			fmt.Fprintf(&buf, "  categorized_at: %s\n", meta.Categorization.CategorizedAt)
		}
		if meta.Categorization.ByTool != "" {
			fmt.Fprintf(&buf, "  by_tool: %s\n", meta.Categorization.ByTool)
		}
		if meta.Categorization.Confidence != "" {
			fmt.Fprintf(&buf, "  confidence: %s\n", meta.Categorization.Confidence)
		}
	}

	if len(meta.Categories) > 0 {
		fmt.Fprint(&buf, "categories:\n")
		for _, cat := range meta.Categories {
			fmt.Fprintf(&buf, "  - %q\n", cat)
		}
	}

	if len(meta.Tags) > 0 {
		fmt.Fprint(&buf, "tags:\n")
		for _, tag := range meta.Tags {
			fmt.Fprintf(&buf, "  - %q\n", tag)
		}
	}

	if meta.Compatibility.Mode != "" || meta.Compatibility.Declared != nil || len(meta.Compatibility.Detected) > 0 {
		fmt.Fprint(&buf, "compatibility:\n")
		fmt.Fprintf(&buf, "  mode: %s\n", meta.Compatibility.Mode)
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

		if len(meta.Compatibility.Detected) > 0 {
			fmt.Fprint(&buf, "  detected:\n")
			for harness, result := range meta.Compatibility.Detected {
				fmt.Fprintf(&buf, "    %s:\n", harness)
				fmt.Fprintf(&buf, "      confidence: %s\n", result.Confidence)
				if len(result.Reasons) > 0 {
					fmt.Fprint(&buf, "      reasons:\n")
					for _, reason := range result.Reasons {
						fmt.Fprintf(&buf, "        - %q\n", reason)
					}
				}
			}
		}
	}

	// Write requirements block if ANY requirement field is non-empty or Inferred is true
	hasRequirements := len(meta.Requirements.Tools) > 0 ||
		len(meta.Requirements.MCPServers) > 0 ||
		meta.Requirements.Model.ToolUse != "" ||
		meta.Requirements.Inferred
	if hasRequirements {
		fmt.Fprint(&buf, "requirements:\n")
		if len(meta.Requirements.Tools) > 0 {
			allRequired := true
			for _, tool := range meta.Requirements.Tools {
				if !tool.Required {
					allRequired = false
					break
				}
			}
			if allRequired {
				fmt.Fprint(&buf, "  tools: [")
				for i, tool := range meta.Requirements.Tools {
					if i > 0 {
						fmt.Fprint(&buf, ", ")
					}
					fmt.Fprintf(&buf, "%q", tool.Name)
				}
				fmt.Fprint(&buf, "]\n")
			} else {
				fmt.Fprint(&buf, "  tools:\n")
				for _, tool := range meta.Requirements.Tools {
					fmt.Fprintf(&buf, "    - name: %q\n", tool.Name)
					fmt.Fprintf(&buf, "      required: %t\n", tool.Required)
				}
			}
		}
		if len(meta.Requirements.MCPServers) > 0 {
			allRequired := true
			for _, server := range meta.Requirements.MCPServers {
				if !server.Required {
					allRequired = false
					break
				}
			}
			if allRequired {
				fmt.Fprint(&buf, "  mcp_servers: [")
				for i, server := range meta.Requirements.MCPServers {
					if i > 0 {
						fmt.Fprint(&buf, ", ")
					}
					fmt.Fprintf(&buf, "%q", server.Name)
				}
				fmt.Fprint(&buf, "]\n")
			} else {
				fmt.Fprint(&buf, "  mcp_servers:\n")
				for _, server := range meta.Requirements.MCPServers {
					fmt.Fprintf(&buf, "    - name: %q\n", server.Name)
					fmt.Fprintf(&buf, "      required: %t\n", server.Required)
				}
			}
		}
		if meta.Requirements.Model.ToolUse != "" {
			fmt.Fprint(&buf, "  model:\n")
			fmt.Fprintf(&buf, "    tool_use: %q\n", meta.Requirements.Model.ToolUse)
		}
		if meta.Requirements.Inferred {
			fmt.Fprintf(&buf, "  inferred: true\n")
		}
	}

	if meta.Summary != "" {
		fmt.Fprintf(&buf, "summary: %q\n", meta.Summary)
	}

	if meta.LocalChanges {
		fmt.Fprint(&buf, "local_changes: true\n")
	}

	if meta.LastChangedAt != "" {
		fmt.Fprintf(&buf, "last_changed_at: %s\n", meta.LastChangedAt)
	}

	return os.WriteFile(path, []byte(buf.String()), 0644)
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
		if _, err := os.Stat(metaPath); err == nil {
			meta, _ = readSkillMeta(metaPath)
		}

		// Read skill body once for detection and inference
		skillBody, _ := readSkillBody(skillMdPath)

		// Check for explicit declaration: either in SKILL.md frontmatter OR in .skill-meta.yaml
		hasExplicitDecl := compDecl.Mode != "" || (meta.Compatibility.Declared != nil && meta.Compatibility.Declared.Mode != "")
		effectiveDecl := compDecl
		if compDecl.Mode == "" && meta.Compatibility.Declared != nil && meta.Compatibility.Declared.Mode != "" {
			effectiveDecl = *meta.Compatibility.Declared
		}

		if hasExplicitDecl {
			// Use the effective declaration
			meta.Compatibility.Declared = &effectiveDecl
			meta.Compatibility.Mode = effectiveDecl.Mode
			meta.Compatibility.Harness = effectiveDecl.Harness
			meta.Compatibility.Harnesses = effectiveDecl.Harnesses
			// If portable, ensure harness fields are cleared
			if effectiveDecl.Mode == "portable" {
				meta.Compatibility.Harness = ""
				meta.Compatibility.Harnesses = nil
			}
		} else {
			// No explicit declaration: run detection and apply auto-classification
			detected := detectCompatibility(detectors, skillBody)
			if len(detected) > 0 {
				meta.Compatibility.Detected = detected
				// Apply auto-classification rule to set effective mode/harness/harnesses
				autoClass := applyAutoClassification(detected)
				meta.Compatibility.Mode = autoClass.Mode
				meta.Compatibility.Harness = autoClass.Harness
				meta.Compatibility.Harnesses = autoClass.Harnesses
			} else {
				// No detection signals: default to portable, clear any stale harness data
				meta.Compatibility.Mode = "portable"
				meta.Compatibility.Harness = ""
				meta.Compatibility.Harnesses = nil
			}
		}

		// Infer requirements only if marked as inferred or all requirement fields are empty.
		// If Inferred=false and any field is populated, preserve the explicit requirements.
		hasExplicitRequirements := len(meta.Requirements.Tools) > 0 ||
			len(meta.Requirements.MCPServers) > 0 ||
			meta.Requirements.Model.ToolUse != ""
		if meta.Requirements.Inferred || !hasExplicitRequirements {
			inferred := inferRequirements(detectors, skillBody)
			meta.Requirements = inferred
			meta.Requirements.Inferred = true
		}

		if meta.Compatibility.Mode == "" {
			meta.Compatibility.Mode = "portable"
		}

		// Persist the updated meta back to .skill-meta.yaml
		_ = writeSkillMeta(metaPath, meta)

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
	var buf strings.Builder

	fmt.Fprint(&buf, "version: 1\n")
	fmt.Fprint(&buf, "skills:\n")

	for _, skill := range cat.Skills {
		fmt.Fprintf(&buf, "  - name: %q\n", skill.Name)

		if skill.Summary != "" {
			fmt.Fprintf(&buf, "    summary: %q\n", skill.Summary)
		}

		if len(skill.Categories) > 0 {
			fmt.Fprint(&buf, "    categories: [")
			for i, cat := range skill.Categories {
				if i > 0 {
					fmt.Fprint(&buf, ", ")
				}
				fmt.Fprintf(&buf, "%q", cat)
			}
			fmt.Fprint(&buf, "]\n")
		} else {
			fmt.Fprint(&buf, "    categories: []\n")
		}

		if len(skill.Tags) > 0 {
			fmt.Fprint(&buf, "    tags: [")
			for i, tag := range skill.Tags {
				if i > 0 {
					fmt.Fprint(&buf, ", ")
				}
				fmt.Fprintf(&buf, "%q", tag)
			}
			fmt.Fprint(&buf, "]\n")
		} else {
			fmt.Fprint(&buf, "    tags: []\n")
		}

		fmt.Fprint(&buf, "    compatibility:\n")
		fmt.Fprintf(&buf, "      mode: %s\n", skill.Compatibility.Mode)
		if skill.Compatibility.Harness != "" {
			fmt.Fprintf(&buf, "      harness: %s\n", skill.Compatibility.Harness)
		}
		if len(skill.Compatibility.Harnesses) > 0 {
			fmt.Fprint(&buf, "      harnesses: [")
			for i, h := range skill.Compatibility.Harnesses {
				if i > 0 {
					fmt.Fprint(&buf, ", ")
				}
				fmt.Fprintf(&buf, "%q", h)
			}
			fmt.Fprint(&buf, "]\n")
		}

		if len(skill.Requirements.Tools) > 0 || len(skill.Requirements.MCPServers) > 0 || skill.Requirements.Model.ToolUse != "" {
			fmt.Fprint(&buf, "    requirements:\n")

			if len(skill.Requirements.Tools) > 0 {
				allRequired := true
				for _, tool := range skill.Requirements.Tools {
					if !tool.Required {
						allRequired = false
						break
					}
				}
				if allRequired {
					fmt.Fprint(&buf, "      tools: [")
					for i, tool := range skill.Requirements.Tools {
						if i > 0 {
							fmt.Fprint(&buf, ", ")
						}
						fmt.Fprintf(&buf, "%q", tool.Name)
					}
					fmt.Fprint(&buf, "]\n")
				} else {
					fmt.Fprint(&buf, "      tools:\n")
					for _, tool := range skill.Requirements.Tools {
						fmt.Fprintf(&buf, "        - name: %q\n", tool.Name)
						fmt.Fprintf(&buf, "          required: %t\n", tool.Required)
					}
				}
			}

			if len(skill.Requirements.MCPServers) > 0 {
				allRequired := true
				for _, server := range skill.Requirements.MCPServers {
					if !server.Required {
						allRequired = false
						break
					}
				}
				if allRequired {
					fmt.Fprint(&buf, "      mcp_servers: [")
					for i, server := range skill.Requirements.MCPServers {
						if i > 0 {
							fmt.Fprint(&buf, ", ")
						}
						fmt.Fprintf(&buf, "%q", server.Name)
					}
					fmt.Fprint(&buf, "]\n")
				} else {
					fmt.Fprint(&buf, "      mcp_servers:\n")
					for _, server := range skill.Requirements.MCPServers {
						fmt.Fprintf(&buf, "        - name: %q\n", server.Name)
						fmt.Fprintf(&buf, "          required: %t\n", server.Required)
					}
				}
			}

			if skill.Requirements.Model.ToolUse != "" {
				fmt.Fprint(&buf, "      model:\n")
				fmt.Fprintf(&buf, "        tool_use: %q\n", skill.Requirements.Model.ToolUse)
			}
		}
	}

	return os.WriteFile(path, []byte(buf.String()), 0644)
}

func parseYAMLInt(value string) (int, error) {
	value = strings.TrimSpace(value)
	var result int
	_, err := fmt.Sscanf(value, "%d", &result)
	return result, err
}
