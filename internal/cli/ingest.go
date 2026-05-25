package cli

import (
	"encoding/json"
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
	handoff     bool   // --handoff: write prompt file for manual LLM/agent run of skills-ingest
	from        string // --from <file>: use JSON from handoff instead of local detectors
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

// ingestOutput mirrors the shape produced by the skills-ingest bundled skill (and its schema).
// Used by --from to ingest LLM-provided (or handoff) categorization, compatibility, requirements, confidence.
type ingestOutput struct {
	Categories    []string `json:"categories"`
	Tags          []string `json:"tags"`
	Compatibility struct {
		Mode      string   `json:"mode"`
		Harnesses []string `json:"harnesses"`
		Harness   string   `json:"harness"`
		Reason    string   `json:"reason"`
	} `json:"compatibility"`
	Requirements struct {
		Tools []struct {
			Name     string `json:"name"`
			Required bool   `json:"required"`
			Check    string `json:"check,omitempty"`
		} `json:"tools"`
		MCPServers []struct {
			Name       string `json:"name"`
			Required   bool   `json:"required"`
			ConfigHint string `json:"config_hint,omitempty"`
		} `json:"mcp_servers"`
		Credentials []struct {
			Name     string `json:"name"`
			Source   string `json:"source,omitempty"`
			Required bool   `json:"required"`
		} `json:"credentials,omitempty"`
		Scripts struct {
			AllowAutoRun     *bool    `json:"allow_auto_run,omitempty"`
			RequiredRuntimes []string `json:"required_runtimes,omitempty"`
		} `json:"scripts,omitempty"`
		Model struct {
			ToolUse          string `json:"tool_use"`
			MinContextTokens int    `json:"min_context_tokens,omitempty"`
			Reasoning        string `json:"reasoning,omitempty"`
			Notes            string `json:"notes,omitempty"`
		} `json:"model"`
	} `json:"requirements"`
	Confidence struct {
		Categories    string `json:"categories"`
		Tags          string `json:"tags"`
		Compatibility string `json:"compatibility"`
		Requirements  string `json:"requirements"`
	} `json:"confidence"`
	Notes []string `json:"notes"`
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

	// Validate skill name to prevent path traversal
	if !isValidSkillName(decl.name) {
		return ingestResult{
			Name:    decl.name,
			Skipped: true,
			Reason:  fmt.Sprintf("invalid skill name %q: must be 3-64 alphanumeric chars + dashes", decl.name),
		}
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

	// 6. Categorization: --handoff (early exit), --from (LLM JSON), or local detectors
	skillBody, _ := os.ReadFile(skillMdPath)
	skillBodyStr := string(skillBody)

	var cats, tags []string
	var confidence string
	var compatResults map[string]detectionResult
	var reqs requirements
	var fromOut *ingestOutput
	categorizationSource := "ingest"

	if opts.handoff {
		promptPath, err := writeIngestHandoffPrompt(decl.name, src.label, src.raw, skillBodyStr)
		if err != nil {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "failed to write handoff prompt: " + err.Error(),
			}
		}
		fmt.Fprintf(out, "Handoff prompt written to: %s\n\n", promptPath)
		fmt.Fprintln(out, "To continue:")
		fmt.Fprintln(out, "  1. Paste the entire contents of that file (or attach it) into your preferred LLM/agent session (Claude, Grok, Codex, etc.)")
		fmt.Fprintln(out, "  2. The model will act as the skills-ingest skill and emit the strict JSON categorization")
		fmt.Fprintln(out, "  3. Save the agent's JSON output to a file (e.g. /tmp/ingest-foo.json)")
		fmt.Fprintf(out, "  4. Re-run the ingest using: skills-manager add %s --from /path/to/output.json [--yes|--auto]\n\n", src.raw)
		return ingestResult{
			Name:        decl.name,
			Skipped:     false,
			LibraryPath: promptPath,
			Reason:      "handoff prompt written",
		}
	} else if opts.from != "" {
		var err error
		fromOut, err = loadIngestOutput(opts.from)
		if err != nil {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  err.Error(),
			}
		}
		cats = fromOut.Categories
		tags = fromOut.Tags
		confidence = fromOut.Confidence.Categories // primary signal for confirmation/auto flow
		// Map requirements for auto-missing checks + later meta
		reqs = requirements{Inferred: false}
		for _, t := range fromOut.Requirements.Tools {
			reqs.Tools = append(reqs.Tools, toolRequirement{Name: t.Name, Required: t.Required, Check: t.Check})
		}
		for _, m := range fromOut.Requirements.MCPServers {
			reqs.MCPServers = append(reqs.MCPServers, mcpRequirement{Name: m.Name, Required: m.Required, ConfigHint: m.ConfigHint})
		}
		for _, c := range fromOut.Requirements.Credentials {
			reqs.Credentials = append(reqs.Credentials, credentialRequirement{Name: c.Name, Source: c.Source, Required: c.Required})
		}
		reqs.Scripts.AllowAutoRun = fromOut.Requirements.Scripts.AllowAutoRun
		reqs.Scripts.RequiredRuntimes = fromOut.Requirements.Scripts.RequiredRuntimes
		if tu := fromOut.Requirements.Model.ToolUse; tu != "" {
			reqs.Model.ToolUse = tu
		}
		reqs.Model.MinContextTokens = fromOut.Requirements.Model.MinContextTokens
		reqs.Model.Reasoning = fromOut.Requirements.Model.Reasoning
		reqs.Model.Notes = fromOut.Requirements.Model.Notes
		categorizationSource = "ingest-handoff"
		// compatResults remains nil (handled in meta block using fromOut)
	} else if opts.auto && llmProviderConfigured(home) {
		prompt := buildIngestPrompt(decl.name, src.label, src.raw, skillBodyStr)
		output, err := runConfiguredLLMProvider(home, prompt)
		if err != nil {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  "provider ingest failed: " + err.Error(),
			}
		}
		fromOut, err = parseIngestOutput([]byte(output), "provider output")
		if err != nil {
			return ingestResult{
				Name:    decl.name,
				Skipped: true,
				Reason:  err.Error(),
			}
		}
		cats = fromOut.Categories
		tags = fromOut.Tags
		confidence = fromOut.Confidence.Categories
		reqs = requirements{Inferred: false}
		for _, t := range fromOut.Requirements.Tools {
			reqs.Tools = append(reqs.Tools, toolRequirement{Name: t.Name, Required: t.Required, Check: t.Check})
		}
		for _, m := range fromOut.Requirements.MCPServers {
			reqs.MCPServers = append(reqs.MCPServers, mcpRequirement{Name: m.Name, Required: m.Required, ConfigHint: m.ConfigHint})
		}
		for _, c := range fromOut.Requirements.Credentials {
			reqs.Credentials = append(reqs.Credentials, credentialRequirement{Name: c.Name, Source: c.Source, Required: c.Required})
		}
		reqs.Scripts.AllowAutoRun = fromOut.Requirements.Scripts.AllowAutoRun
		reqs.Scripts.RequiredRuntimes = fromOut.Requirements.Scripts.RequiredRuntimes
		if tu := fromOut.Requirements.Model.ToolUse; tu != "" {
			reqs.Model.ToolUse = tu
		}
		reqs.Model.MinContextTokens = fromOut.Requirements.Model.MinContextTokens
		reqs.Model.Reasoning = fromOut.Requirements.Model.Reasoning
		reqs.Model.Notes = fromOut.Requirements.Model.Notes
		categorizationSource = "skills-ingest-provider"
	} else {
		detectors, _ := loadDetectors()
		compatResults = detectCompatibility(detectors, skillBodyStr)
		reqs = inferRequirements(detectors, skillBodyStr)
		cats, tags, _ = suggestCategories(decl.name, decl.description)
		confidence = computeConfidence(decl, compatResults, cats)
	}

	// 7. Populate result (used for JSON output / human summary)
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
		missingCredentials := missingRequiredCredentials(reqs)
		missingRuntimes := missingRequiredScriptRuntimes(reqs)
		allMissing := append(append(append(append(missing, missingMCP...), missingModel...), missingCredentials...), missingRuntimes...)

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
			if len(missingCredentials) > 0 {
				parts = append(parts, "credentials="+strings.Join(missingCredentials, ","))
			}
			if len(missingRuntimes) > 0 {
				parts = append(parts, "runtimes="+strings.Join(missingRuntimes, ","))
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
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(targetDir)
		}
	}()

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
	meta.Categorization.Source = categorizationSource
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

	// Detected compatibility + Requirements: prefer --from (LLM via skills-ingest) values when provided,
	// otherwise fall back to local keyword detectors. Frontmatter Declared always takes precedence for mode.
	if fromOut != nil {
		llmReason := fromOut.Compatibility.Reason
		if llmReason == "" {
			llmReason = "provided via --from / skills-ingest handoff"
		}
		meta.Compatibility.Detected = map[string]detectionResult{
			"skills-ingest": {Confidence: fromOut.Confidence.Compatibility, Reasons: []string{llmReason}},
		}
		// Respect explicit frontmatter declarations (Declared != nil means the SKILL.md had compatible:/exclusive:).
		// If there was no frontmatter declaration, apply the LLM-suggested mode/harnesses so that
		// install and matching actually see the handoff-provided restrictions.
		if meta.Compatibility.Declared == nil {
			if m := fromOut.Compatibility.Mode; m != "" {
				meta.Compatibility.Mode = m
			}
			if h := fromOut.Compatibility.Harness; h != "" {
				meta.Compatibility.Harness = h
			}
			if len(fromOut.Compatibility.Harnesses) > 0 {
				meta.Compatibility.Harnesses = fromOut.Compatibility.Harnesses
			}
			// Ensure ExplicitPortable is cleared so rebuildCatalogFromLibrary
			// does not override the LLM-provided mode back to portable.
			meta.Compatibility.ExplicitPortable = false
		}
		meta.Requirements = reqs
	} else {
		meta.Compatibility.Detected = compatResults
		meta.Requirements = reqs
	}

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
	committed = true
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

		// Use Lstat to detect symlinks without following them
		info, err := os.Lstat(srcPath)
		if err != nil {
			return err
		}

		// Skip symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		if info.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath, info.Mode()); err != nil {
				return err
			}
		}
	}

	return nil
}

// loadIngestOutput reads a JSON file produced by skills-ingest (via handoff or provider),
// performs basic structural validation against the expected shape and value rules,
// and returns the parsed values for use instead of local detectors.
func loadIngestOutput(path string) (*ingestOutput, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read --from file: %w", err)
	}
	return parseIngestOutput(b, "--from")
}

func parseIngestOutput(b []byte, label string) (*ingestOutput, error) {
	var out ingestOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse JSON from %s: %w", label, err)
	}

	// Basic validation (no external schema lib; mirrors key constraints from schemas/ingest-output.json)
	if len(out.Categories) == 0 {
		return nil, fmt.Errorf("categories: at least 1 required")
	}
	if len(out.Categories) > 3 {
		return nil, fmt.Errorf("categories: at most 3 allowed")
	}
	validCat := map[string]bool{
		"Engineering": true, "Quality": true, "Operations": true, "Data": true,
		"Design": true, "Documents": true, "Writing": true, "Business": true,
		"Productivity": true, "Agent-tooling": true,
	}
	for _, c := range out.Categories {
		if !validCat[c] {
			return nil, fmt.Errorf("invalid category %q (must be one of the 10 official categories)", c)
		}
	}

	validMode := map[string]bool{"portable": true, "compatible": true, "exclusive": true}
	if !validMode[out.Compatibility.Mode] {
		return nil, fmt.Errorf("compatibility.mode must be portable|compatible|exclusive (got %q)", out.Compatibility.Mode)
	}

	validConf := map[string]bool{"high": true, "medium": true, "low": true}
	confs := map[string]string{
		"categories":    out.Confidence.Categories,
		"tags":          out.Confidence.Tags,
		"compatibility": out.Confidence.Compatibility,
		"requirements":  out.Confidence.Requirements,
	}
	for k, v := range confs {
		if !validConf[v] {
			return nil, fmt.Errorf("confidence.%s must be high|medium|low (got %q)", k, v)
		}
	}

	validToolUse := map[string]bool{"required": true, "optional": true, "none": true}
	if tu := out.Requirements.Model.ToolUse; tu != "" && !validToolUse[tu] {
		return nil, fmt.Errorf("requirements.model.tool_use must be required|optional|none (got %q)", tu)
	}

	return &out, nil
}

// writeIngestHandoffPrompt creates a self-contained markdown prompt file the user can
// paste/attach to any LLM session. It includes instructions for running skills-ingest
// plus the exact SKILL.md content of the *target* skill being added.
func writeIngestHandoffPrompt(name, label, rawSource, skillContent string) (string, error) {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, name)
	if safe == "" {
		safe = "skill"
	}
	prompt := buildIngestPrompt(name, label, rawSource, skillContent)
	return writeHandoffPrompt("ingest-"+safe+"-prompt.md", prompt)
}

func buildIngestPrompt(name, label, rawSource, skillContent string) string {
	// Static instructions distilled from the skills-ingest SKILL.md + schema + taxonomy rules.
	// This makes the prompt runnable by a configured provider or an external agent.
	instructions := `You are an expert librarian for the skills-manager project. Your job is to analyze a new skill (provided as a SKILL.md file) and produce a high-quality, honest structured categorization.

## Output Requirements
Return ONLY a single valid JSON object. No markdown, no commentary, no code fences.

The JSON must match exactly:
{
  "categories": ["Engineering", "Quality"],   // 1-3 from the official list below
  "tags": ["python", "testing", "review"],
  "compatibility": {
    "mode": "portable" | "compatible" | "exclusive",
    "harnesses": ["claude", "codex"],
    "harness": "claude",
    "reason": "short explanation"
  },
  "requirements": {
    "model": {"tool_use": "required"|"optional"|"none", "min_context_tokens": 32000, "reasoning": "medium"|"high", "notes": "..."},
    "tools": [{"name": "gh", "required": true, "check": "gh auth status"}],
    "mcp_servers": [{"name": "linear", "required": true, "config_hint": "..."}],
    "credentials": [{"name": "github", "source": "gh", "required": true}],
    "scripts": {"allow_auto_run": false, "required_runtimes": ["node"]}
  },
  "confidence": {
    "categories": "high"|"medium"|"low",
    "tags": "high"|"medium"|"low",
    "compatibility": "high"|"medium"|"low",
    "requirements": "high"|"medium"|"low"
  },
  "notes": ["any warnings or observations"]
}

## Strict Rules
Categories (use ONLY these 10, 1-3 max): Engineering, Quality, Operations, Data, Design, Documents, Writing, Business, Productivity, Agent-tooling.
Prefer the most central ones.

Tags: lowercase-with-dashes. Specific (stack/framework/integration/method). Avoid generic "ai","code","tool".

Compatibility (choose one):
- portable: no strong harness signals
- compatible: works well in listed harnesses
- exclusive: designed for one harness (e.g. heavy AskUserQuestion / Plan Mode / AGENTS.md / globs: usage)

Requirements: be conservative; only required:true when the skill clearly cannot work without it. Common tool names: gh, rg, node, python, docker, etc.

Confidence: be honest; low is acceptable and preferred to overconfidence. Reflect low confidence in notes.

Analyze the SKILL.md that follows these instructions. Output the JSON now.`
	if bundled := readBundledSkillMarkdown("skills-ingest"); bundled != "" {
		instructions = bundled
	}

	return fmt.Sprintf(`# skills-manager ingest handoff for %s

**Source:** %s
**Label:** %s
**Generated:** %s

---

**Bundled skills-ingest SKILL.md:**

%s

---

**Target skill SKILL.md to categorize:**

%s

---

Now output ONLY the JSON described above for the target skill.
`, name, rawSource, label, time.Now().UTC().Format(time.RFC3339), instructions, skillContent)
}
