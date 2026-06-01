package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type planOptions struct {
	inventory      string
	recommendation string
	all            bool
}

type actionPlanOutput struct {
	SchemaVersion int          `json:"schema_version"`
	InventoryPath string       `json:"inventory_path"`
	GeneratedAt   string       `json:"generated_at"`
	Plans         []actionPlan `json:"plans"`
}

type actionPlan struct {
	RecommendationID      string          `json:"recommendation_id"`
	Kind                  string          `json:"kind"`
	Title                 string          `json:"title"`
	Reason                string          `json:"reason"`
	Confidence            string          `json:"confidence"`
	SkillName             string          `json:"skill_name,omitempty"`
	TargetToolID          string          `json:"target_tool_id,omitempty"`
	TargetProjectID       string          `json:"target_project_id,omitempty"`
	SourceInstallationIDs []string        `json:"source_installation_ids,omitempty"`
	Status                string          `json:"status"`
	Blockers              []string        `json:"blockers,omitempty"`
	Files                 actionPlanFiles `json:"files"`
}

type actionPlanFiles struct {
	Create   []actionPlanFile `json:"create"`
	Update   []actionPlanFile `json:"update"`
	Preserve []actionPlanFile `json:"preserve"`
	Skip     []actionPlanFile `json:"skip"`
	Remove   []actionPlanFile `json:"remove"`
}

type actionPlanFile struct {
	Path      string `json:"path"`
	Source    string `json:"source,omitempty"`
	Ownership string `json:"ownership"`
	Reason    string `json:"reason"`
}

func runPlan(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	stdout := gf.outWriter(realStdout)
	opts, err := parsePlanOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	inventory, err := readPlanInventory(opts.inventory)
	if err != nil {
		fmt.Fprintf(stderr, "read inventory: %v\n", err)
		return ExitOpError
	}
	plans, err := buildActionPlans(inventory, opts)
	if err != nil {
		fmt.Fprintf(stderr, "build plan: %v\n", err)
		return ExitUsageError
	}
	out := actionPlanOutput{
		SchemaVersion: 1,
		InventoryPath: opts.inventory,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Plans:         plans,
	}
	if gf.JSON {
		if err := writeJSON(realStdout, out); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitOpError
		}
		return ExitSuccess
	}
	printActionPlans(stdout, out)
	return ExitSuccess
}

func parsePlanOptions(args []string) (planOptions, error) {
	opts := planOptions{all: true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--inventory":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return opts, fmt.Errorf("usage: --inventory requires a file")
			}
			opts.inventory = args[i+1]
			i++
		case "--recommendation":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return opts, fmt.Errorf("usage: --recommendation requires an id")
			}
			opts.recommendation = args[i+1]
			opts.all = false
			i++
		case "--all":
			opts.all = true
			opts.recommendation = ""
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unexpected plan argument: %s", arg)
			}
			if opts.recommendation != "" {
				return opts, fmt.Errorf("unexpected plan argument: %s", arg)
			}
			opts.recommendation = arg
			opts.all = false
		}
	}
	if opts.inventory == "" {
		return opts, fmt.Errorf("usage: skills-manager plan --inventory <discover.json> [--recommendation <id>]")
	}
	return opts, nil
}

func readPlanInventory(path string) (discoverOutput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return discoverOutput{}, err
	}
	var out discoverOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return discoverOutput{}, fmt.Errorf("parse inventory: %w", err)
	}
	return out, nil
}

func buildActionPlans(inventory discoverOutput, opts planOptions) ([]actionPlan, error) {
	installsByID := map[string]discoverInstallation{}
	for _, inst := range inventory.Installations {
		installsByID[inst.InstallationID] = inst
	}
	projectsByID := map[string]discoverProject{}
	for _, project := range inventory.Projects {
		projectsByID[project.ProjectID] = project
	}
	plans := make([]actionPlan, 0)
	for _, rec := range inventory.Report.Recommendations {
		if !opts.all && rec.RecommendationID != opts.recommendation {
			continue
		}
		plan := buildActionPlan(inventory, installsByID, projectsByID, rec)
		plans = append(plans, plan)
	}
	if !opts.all && len(plans) == 0 {
		return nil, fmt.Errorf("recommendation not found: %s", opts.recommendation)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].RecommendationID < plans[j].RecommendationID })
	return plans, nil
}

func buildActionPlan(inventory discoverOutput, installsByID map[string]discoverInstallation, projectsByID map[string]discoverProject, rec discoverRecommendation) actionPlan {
	plan := actionPlan{
		RecommendationID:      rec.RecommendationID,
		Kind:                  rec.Kind,
		Title:                 rec.Title,
		Reason:                rec.Reason,
		Confidence:            rec.Confidence,
		SkillName:             rec.SkillName,
		TargetToolID:          rec.TargetToolID,
		TargetProjectID:       rec.TargetProjectID,
		SourceInstallationIDs: append([]string{}, rec.SourceInstallationIDs...),
		Status:                "ready",
		Files:                 emptyActionPlanFiles(),
	}
	sources, missing := planSources(installsByID, rec.SourceInstallationIDs)
	for _, id := range missing {
		plan.addBlocker("source installation not found: " + id)
	}
	switch rec.Kind {
	case "ingest":
		planIngest(&plan, sources)
	case "install_global":
		planInstallGlobal(&plan, inventory, installsByID, sources, rec)
	case "install_project":
		planInstallProject(&plan, projectsByID, installsByID, sources, rec)
	case "remove":
		planRemove(&plan, sources)
	case "ignore":
		planSkipRecommendation(&plan, sources, "recommendation is explicitly ignored")
	case "review_drift":
		planSkipRecommendation(&plan, sources, "drift review must be opened before filesystem changes")
	case "needs_port":
		planSkipRecommendation(&plan, sources, "source is incompatible with target harness")
		plan.addBlocker("port and validate before install")
	default:
		planSkipRecommendation(&plan, sources, "unsupported recommendation kind")
		plan.addBlocker("unsupported recommendation kind: " + rec.Kind)
	}
	plan.sortFiles()
	if len(plan.Blockers) > 0 {
		plan.Status = "blocked"
	}
	return plan
}

func planSources(installsByID map[string]discoverInstallation, ids []string) ([]discoverInstallation, []string) {
	var sources []discoverInstallation
	var missing []string
	for _, id := range ids {
		inst, ok := installsByID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		sources = append(sources, inst)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].InstallationID < sources[j].InstallationID })
	sort.Strings(missing)
	return sources, missing
}

func emptyActionPlanFiles() actionPlanFiles {
	return actionPlanFiles{
		Create:   []actionPlanFile{},
		Update:   []actionPlanFile{},
		Preserve: []actionPlanFile{},
		Skip:     []actionPlanFile{},
		Remove:   []actionPlanFile{},
	}
}

func planIngest(plan *actionPlan, sources []discoverInstallation) {
	if len(sources) == 0 {
		plan.addBlocker("ingest requires a source installation")
		return
	}
	for _, source := range sources {
		if source.Managed {
			plan.addSkip(source.SourcePath, source.SourcePath, source.Ownership, "source is already manager-owned")
			continue
		}
		if source.Format != "skill_md" {
			plan.addBlocker("ingest only supports skill_md sources: " + source.InstallationID)
			plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "source format is not ingestable")
			continue
		}
		if !isValidSkillName(source.SkillName) {
			plan.addBlocker("invalid skill name for ingest: " + source.SkillName)
			plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "skill name is not library-safe")
			continue
		}
		if source.Ownership != "" && source.Ownership != "unmanaged" {
			plan.addBlocker("uncertain source ownership: " + source.InstallationID)
			plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "source ownership is uncertain")
			continue
		}
		plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "ingest preserves original source")
		libraryTarget := filepath.Join(planManagerHome(), "library", source.SkillName)
		target := filepath.Join(libraryTarget, "SKILL.md")
		metaTarget := filepath.Join(libraryTarget, ".skill-meta.yaml")
		skillSource := planSkillMarkdownPath(source)
		blocked := existingLibraryTargetBlocksIngest(plan, libraryTarget, source.SourcePath)
		blocked = existingLibraryTargetBlocksIngest(plan, target, skillSource) || blocked
		blocked = existingLibraryTargetBlocksIngest(plan, metaTarget, source.SourcePath) || blocked
		if blocked {
			continue
		}
		plan.addCreate(target, skillSource, "manager", "library copy for ingested skill")
		plan.addCreate(metaTarget, source.SourcePath, "manager", "metadata for ingested skill")
	}
}

func planInstallGlobal(plan *actionPlan, inventory discoverOutput, installsByID map[string]discoverInstallation, sources []discoverInstallation, rec discoverRecommendation) {
	if len(sources) == 0 {
		plan.addBlocker("global install requires a source installation")
		return
	}
	if !validatePlanTargetSkillName(plan, rec.SkillName, sources) {
		return
	}
	if rec.TargetToolID == "" {
		plan.addBlocker("global install requires a target tool")
	}
	root, ok := globalPlanRoot(inventory.Tools, rec.TargetToolID)
	if !ok {
		plan.addBlocker("missing global target path for tool: " + rec.TargetToolID)
	}
	source := sources[0]
	if ok, reason := planSourceCompatibleWithTool(source, rec.TargetToolID); !ok {
		plan.addBlocker(reason + ": " + rec.TargetToolID)
		plan.addSkip(source.SourcePath, source.SourcePath, ownershipLabel(source), "incompatible with target harness")
		return
	}
	if source.Format != "skill_md" {
		plan.addBlocker("global install only supports skill_md sources")
		plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "source format is not installable")
		return
	}
	if !ok || rec.TargetToolID == "" {
		for _, source := range sources {
			plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "source preserved until target path is known")
		}
		return
	}
	target := filepath.Join(root, rec.SkillName)
	plan.addTargetCreateOrUpdate(target, source.SourcePath, installsByID, rec.SkillName, rec.TargetToolID, "", "global install target")
}

func planInstallProject(plan *actionPlan, projectsByID map[string]discoverProject, installsByID map[string]discoverInstallation, sources []discoverInstallation, rec discoverRecommendation) {
	if !validatePlanTargetSkillName(plan, rec.SkillName, sources) {
		return
	}
	project, ok := projectsByID[rec.TargetProjectID]
	if !ok {
		plan.addBlocker("target project not found: " + rec.TargetProjectID)
		for _, source := range sources {
			plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "source preserved until target project is known")
		}
		return
	}
	if len(sources) == 0 {
		plan.addBlocker("project install requires a source installation")
		return
	}
	targetTools := []string{}
	if rec.TargetToolID != "" {
		targetTools = append(targetTools, rec.TargetToolID)
	} else {
		seen := map[string]bool{}
		for _, source := range sources {
			if !seen[source.ToolID] {
				seen[source.ToolID] = true
				targetTools = append(targetTools, source.ToolID)
			}
		}
		sort.Strings(targetTools)
	}
	for _, toolID := range targetTools {
		base, baseOK := harnessProjectPaths[toolID]
		if !baseOK {
			plan.addBlocker("missing project target path for tool: " + toolID)
			continue
		}
		source := bestPlanSourceForTool(sources, toolID)
		if source.InstallationID == "" {
			plan.addBlocker("no source installation for project target: " + toolID)
			continue
		}
		if ok, reason := planSourceCompatibleWithTool(source, toolID); !ok {
			plan.addBlocker(reason + ": " + toolID)
			plan.addSkip(source.SourcePath, source.SourcePath, ownershipLabel(source), "incompatible with target harness")
			continue
		}
		if !ok {
			continue
		}
		target := filepath.Join(project.RootPath, base, rec.SkillName)
		if samePlanPath(source.SourcePath, target) {
			plan.addSkip(target, source.SourcePath, ownershipLabel(source), "source already present in project target")
			continue
		}
		plan.addTargetCreateOrUpdate(target, source.SourcePath, installsByID, rec.SkillName, toolID, rec.TargetProjectID, "project install target")
	}
}

func planRemove(plan *actionPlan, sources []discoverInstallation) {
	if len(sources) == 0 {
		plan.addBlocker("remove requires at least one source installation")
		return
	}
	removed := 0
	preservedCanonical := false
	for _, source := range sources {
		if !source.Managed {
			preservedCanonical = true
			break
		}
	}
	for _, source := range sources {
		if !source.Managed {
			plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "unmanaged source is preserved")
			continue
		}
		if !preservedCanonical {
			plan.addPreserve(source.SourcePath, source.SourcePath, "manager", "canonical manager-owned copy is preserved")
			preservedCanonical = true
			continue
		}
		plan.addRemove(source.SourcePath, source.SourcePath, "manager", "manager-owned duplicate can be removed")
		removed++
	}
	if removed == 0 {
		plan.addBlocker("no manager-owned install selected for removal")
	}
}

func planSkipRecommendation(plan *actionPlan, sources []discoverInstallation, reason string) {
	if len(sources) == 0 {
		plan.addSkip("", "", "unknown", reason)
		return
	}
	for _, source := range sources {
		plan.addSkip(source.SourcePath, source.SourcePath, ownershipLabel(source), reason)
	}
}

func globalPlanRoot(tools []discoverTool, toolID string) (string, bool) {
	for _, tool := range tools {
		if tool.ToolID == toolID && len(tool.GlobalRoots) > 0 && strings.TrimSpace(tool.GlobalRoots[0]) != "" {
			return tool.GlobalRoots[0], true
		}
	}
	return "", false
}

func existingLibraryTargetBlocksIngest(plan *actionPlan, target, source string) bool {
	if _, err := os.Stat(target); err == nil {
		plan.addPreserve(target, source, "unknown", "existing library target is not present in inventory")
		plan.addBlocker("library target exists but ownership is unknown: " + target)
		return true
	} else if !errors.Is(err, os.ErrNotExist) {
		plan.addPreserve(target, source, "unknown", "library target existence could not be verified")
		plan.addBlocker("library target ownership is uncertain: " + target)
		return true
	}
	return false
}

func validatePlanTargetSkillName(plan *actionPlan, skillName string, sources []discoverInstallation) bool {
	if isValidSkillName(skillName) {
		return true
	}
	plan.addBlocker("invalid skill name for target path: " + skillName)
	for _, source := range sources {
		plan.addPreserve(source.SourcePath, source.SourcePath, ownershipLabel(source), "skill name is not target-path-safe")
	}
	return false
}

func planSkillMarkdownPath(source discoverInstallation) string {
	contentPath := strings.TrimSpace(source.ContentPath)
	if contentPath == "" {
		contentPath = source.SourcePath
	}
	if filepath.Base(contentPath) == "SKILL.md" {
		return contentPath
	}
	return filepath.Join(contentPath, "SKILL.md")
}

func bestPlanSourceForTool(sources []discoverInstallation, toolID string) discoverInstallation {
	for _, source := range sources {
		if source.ToolID == toolID {
			return source
		}
	}
	for _, source := range sources {
		if ok, _ := planSourceCompatibleWithTool(source, toolID); ok {
			return source
		}
	}
	return discoverInstallation{}
}

func planSourceCompatibleWithTool(inst discoverInstallation, targetTool string) (bool, string) {
	if targetTool == "" {
		return false, "missing target tool"
	}
	if inst.ToolID == targetTool {
		return true, ""
	}
	if inst.ExclusiveToolID != "" {
		if inst.ExclusiveToolID == targetTool {
			return true, ""
		}
		return false, "source compatibility does not include target tool"
	}
	if len(inst.CompatibleToolIDs) == 0 {
		return true, ""
	}
	if stringSliceContains(inst.CompatibleToolIDs, targetTool) {
		return true, ""
	}
	return false, "source compatibility does not include target tool"
}

func (plan *actionPlan) addTargetCreateOrUpdate(target, source string, installsByID map[string]discoverInstallation, skillName, toolID, projectID, reason string) {
	for _, inst := range installsByID {
		if inst.SkillName == skillName && inst.ToolID == toolID && inst.ProjectID == projectID && samePlanPath(inst.SourcePath, target) {
			if inst.Managed {
				plan.addUpdate(target, source, "manager", "manager-owned install")
			} else {
				plan.addPreserve(target, source, ownershipLabel(inst), "unmanaged target is preserved")
				plan.addBlocker("target exists but is not manager-owned: " + target)
			}
			return
		}
	}
	if _, err := os.Stat(target); err == nil {
		plan.addPreserve(target, source, "unknown", "existing target is not present in inventory")
		plan.addBlocker("target exists but ownership is unknown: " + target)
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		plan.addPreserve(target, source, "unknown", "target existence could not be verified")
		plan.addBlocker("target ownership is uncertain: " + target)
		return
	}
	parent := filepath.Dir(target)
	if info, err := os.Stat(parent); err != nil {
		plan.addPreserve(target, source, "unknown", "target root is not present")
		plan.addBlocker("target root is missing: " + parent)
		return
	} else if !info.IsDir() {
		plan.addPreserve(target, source, "unknown", "target root is not a directory")
		plan.addBlocker("target root is not a directory: " + parent)
		return
	}
	plan.addCreate(target, source, "manager", reason)
}

func (plan *actionPlan) addCreate(path, source, ownership, reason string) {
	plan.Files.Create = append(plan.Files.Create, actionPlanFile{Path: path, Source: source, Ownership: ownership, Reason: reason})
}

func (plan *actionPlan) addUpdate(path, source, ownership, reason string) {
	plan.Files.Update = append(plan.Files.Update, actionPlanFile{Path: path, Source: source, Ownership: ownership, Reason: reason})
}

func (plan *actionPlan) addPreserve(path, source, ownership, reason string) {
	plan.Files.Preserve = append(plan.Files.Preserve, actionPlanFile{Path: path, Source: source, Ownership: ownership, Reason: reason})
}

func (plan *actionPlan) addSkip(path, source, ownership, reason string) {
	plan.Files.Skip = append(plan.Files.Skip, actionPlanFile{Path: path, Source: source, Ownership: ownership, Reason: reason})
}

func (plan *actionPlan) addRemove(path, source, ownership, reason string) {
	plan.Files.Remove = append(plan.Files.Remove, actionPlanFile{Path: path, Source: source, Ownership: ownership, Reason: reason})
}

func (plan *actionPlan) addBlocker(reason string) {
	if reason == "" || stringSliceContains(plan.Blockers, reason) {
		return
	}
	plan.Blockers = append(plan.Blockers, reason)
	sort.Strings(plan.Blockers)
}

func (plan *actionPlan) sortFiles() {
	sortActionPlanFiles(plan.Files.Create)
	sortActionPlanFiles(plan.Files.Update)
	sortActionPlanFiles(plan.Files.Preserve)
	sortActionPlanFiles(plan.Files.Skip)
	sortActionPlanFiles(plan.Files.Remove)
	sort.Strings(plan.SourceInstallationIDs)
}

func sortActionPlanFiles(files []actionPlanFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].Path != files[j].Path {
			return files[i].Path < files[j].Path
		}
		return files[i].Source < files[j].Source
	})
}

func ownershipLabel(inst discoverInstallation) string {
	if inst.Ownership != "" {
		return inst.Ownership
	}
	if inst.Managed {
		return "manager"
	}
	return "unmanaged"
}

func samePlanPath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return absA == absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func planManagerHome() string {
	home, err := managerHome()
	if err != nil {
		return ""
	}
	return home
}

func printActionPlans(stdout io.Writer, out actionPlanOutput) {
	fmt.Fprintf(stdout, "Action plans (%d):\n", len(out.Plans))
	for _, plan := range out.Plans {
		fmt.Fprintf(stdout, "- %s %s [%s]\n", plan.RecommendationID, plan.Title, plan.Status)
		for _, blocker := range plan.Blockers {
			fmt.Fprintf(stdout, "  blocker: %s\n", blocker)
		}
		printActionPlanFiles(stdout, "create", plan.Files.Create)
		printActionPlanFiles(stdout, "update", plan.Files.Update)
		printActionPlanFiles(stdout, "preserve", plan.Files.Preserve)
		printActionPlanFiles(stdout, "skip", plan.Files.Skip)
		printActionPlanFiles(stdout, "remove", plan.Files.Remove)
	}
}

func printActionPlanFiles(stdout io.Writer, label string, files []actionPlanFile) {
	for _, file := range files {
		if file.Path == "" {
			fmt.Fprintf(stdout, "  %s: %s\n", label, file.Reason)
			continue
		}
		fmt.Fprintf(stdout, "  %s: %s (%s)\n", label, file.Path, file.Reason)
	}
}
