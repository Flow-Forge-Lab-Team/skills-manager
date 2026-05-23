package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type safetyFlag struct {
	Name     string
	File     string
	Line     int
	Detail   string
	Blocking bool
}

type safetyReport struct {
	Skill         string
	Flags         []safetyFlag
	SummaryStatus string
}

type pendingUpdatePaths struct {
	Skill string
	Root  string
	From  string
	To    string
}

type snapshotFile struct {
	Content string
	Mode    os.FileMode
}

func runUpdate(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	stdout := gf.outWriter(realStdout)
	if len(args) == 0 {
		fmt.Fprintln(stdout, "Usage: skills-manager update --safety <skill>")
		fmt.Fprintln(stdout, "       skills-manager update --accept-all-safe")
		return ExitSuccess
	}

	if args[0] == "--safety" {
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(stderr, "usage: skills-manager update --safety <skill>")
			return ExitUsageError
		}
		report, pending, code := analyzePendingUpdate(args[1], stderr)
		if code != 0 {
			return code
		}
		if report.SummaryStatus == "tainted" {
			if err := setPendingMetaValue(filepath.Join(pending.Root, "meta.yaml"), "summary_status", "tainted"); err != nil {
				fmt.Fprintf(stderr, "write pending metadata: %v\n", err)
				return ExitOpError
			}
		}
		printSafetyReport(report, stdout)
		if gf.JSON {
			if err := writeJSON(realStdout, safetyReportJSON(report)); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
		}
		return ExitSuccess
	}

	if args[0] == "--accept-all-safe" {
		return runAcceptAllSafe(realStdout, stdout, stderr, gf)
	}

	fmt.Fprintf(stderr, "unknown update option: %s\n", args[0])
	return ExitUsageError
}

type safetyFlagJSON struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Detail   string `json:"detail"`
	Blocking bool   `json:"blocking"`
}

type safetyReportJSONShape struct {
	Skill         string           `json:"skill"`
	Flags         []safetyFlagJSON `json:"flags"`
	SummaryStatus string           `json:"summary_status,omitempty"`
}

func safetyReportJSON(report safetyReport) safetyReportJSONShape {
	out := safetyReportJSONShape{Skill: report.Skill, SummaryStatus: report.SummaryStatus, Flags: []safetyFlagJSON{}}
	for _, f := range report.Flags {
		out.Flags = append(out.Flags, safetyFlagJSON{Name: f.Name, File: f.File, Line: f.Line, Detail: f.Detail, Blocking: f.Blocking})
	}
	return out
}

type acceptAllSafeJSON struct {
	Accepted []string `json:"accepted"`
	Blocked  []string `json:"blocked,omitempty"`
}

func analyzePendingUpdate(skill string, stderr io.Writer) (safetyReport, pendingUpdatePaths, int) {
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return safetyReport{}, pendingUpdatePaths{}, 3
	}
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return safetyReport{}, pendingUpdatePaths{}, 3
	}
	pending, err := findPendingUpdate(skill, filepath.Join(libraryPath, skill, ".update-pending"))
	if err != nil {
		fmt.Fprintf(stderr, "pending update for %s: %v\n", skill, err)
		return safetyReport{}, pendingUpdatePaths{}, 3
	}
	report, err := computeSafetyReport(skill, pending.From, pending.To)
	if err != nil {
		fmt.Fprintf(stderr, "compute safety flags: %v\n", err)
		return safetyReport{}, pendingUpdatePaths{}, 3
	}
	return report, pending, 0
}

func runAcceptAllSafe(realStdout io.Writer, stdout io.Writer, stderr io.Writer, gf globalFlags) int {
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	libraryPath, err := ensureLibrary(home)
	if err != nil {
		fmt.Fprintf(stderr, "ensure library: %v\n", err)
		return ExitOpError
	}
	entries, err := os.ReadDir(libraryPath)
	if err != nil {
		fmt.Fprintf(stderr, "read library: %v\n", err)
		return ExitOpError
	}
	var blocked []string
	var safe []pendingUpdatePaths
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pendingRoot := filepath.Join(libraryPath, entry.Name(), ".update-pending")
		if _, err := os.Stat(pendingRoot); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			fmt.Fprintf(stderr, "inspect pending update for %s: %v\n", entry.Name(), err)
			return ExitOpError
		}
		pending, err := findPendingUpdate(entry.Name(), pendingRoot)
		if err != nil {
			fmt.Fprintf(stderr, "pending update for %s: %v\n", entry.Name(), err)
			return ExitPartial
		}
		report, err := computeSafetyReport(entry.Name(), pending.From, pending.To)
		if err != nil {
			fmt.Fprintf(stderr, "compute safety flags for %s: %v\n", entry.Name(), err)
			return ExitOpError
		}
		if report.SummaryStatus == "tainted" {
			if err := setPendingMetaValue(filepath.Join(pending.Root, "meta.yaml"), "summary_status", "tainted"); err != nil {
				fmt.Fprintf(stderr, "write pending metadata for %s: %v\n", entry.Name(), err)
				return ExitOpError
			}
		}
		if hasBlockingFlags(report) {
			blocked = append(blocked, entry.Name())
		} else {
			safe = append(safe, pending)
		}
	}
	sort.Strings(blocked)
	sort.Slice(safe, func(i, j int) bool { return safe[i].Skill < safe[j].Skill })
	if len(blocked) > 0 {
		fmt.Fprintf(stdout, "Refusing --accept-all-safe: %d update(s) have blocking safety flags\n", len(blocked))
		for _, skill := range blocked {
			fmt.Fprintf(stdout, "- %s\n", skill)
		}
		if gf.JSON {
			if err := writeJSON(realStdout, acceptAllSafeJSON{Blocked: blocked, Accepted: []string{}}); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
		}
		return ExitPartial
	}
	accepted := make([]string, 0, len(safe))
	for _, pending := range safe {
		if err := applyPendingUpdate(pending); err != nil {
			fmt.Fprintf(stderr, "accept update for %s: %v\n", pending.Skill, err)
			return ExitOpError
		}
		fmt.Fprintf(stdout, "- %s: accepted\n", pending.Skill)
		accepted = append(accepted, pending.Skill)
	}
	if len(accepted) > 0 {
		cat, err := rebuildCatalogFromLibrary(libraryPath)
		if err != nil {
			fmt.Fprintf(stderr, "rebuild catalog: %v\n", err)
			return ExitOpError
		}
		if err := writeCatalog(filepath.Join(libraryPath, "catalog.yaml"), cat); err != nil {
			fmt.Fprintf(stderr, "write catalog: %v\n", err)
			return ExitOpError
		}
	}
	fmt.Fprintf(stdout, "All pending updates accepted (%d).\n", len(safe))
	if gf.JSON {
		if err := writeJSON(realStdout, acceptAllSafeJSON{Accepted: accepted}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
	}
	return ExitSuccess
}

func applyPendingUpdate(pending pendingUpdatePaths) error {
	skillDir := filepath.Dir(pending.Root)
	toInfo, err := os.Stat(pending.To)
	if err != nil {
		return err
	}
	if !toInfo.IsDir() {
		if err := copyFile(pending.To, filepath.Join(skillDir, "SKILL.md"), toInfo.Mode()); err != nil {
			return err
		}
		if err := refreshSkillMeta(skillDir, nil); err != nil {
			return err
		}
		return os.RemoveAll(pending.Root)
	}
	if !pathExists(filepath.Join(pending.To, "SKILL.md")) {
		return fmt.Errorf("incoming snapshot missing SKILL.md")
	}
	tmp, err := os.MkdirTemp("", "skills-manager-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyDir(pending.To, tmp); err != nil {
		return err
	}
	var incomingMeta *skillMeta
	incomingMetaPath := filepath.Join(tmp, ".skill-meta.yaml")
	if pathExists(incomingMetaPath) {
		if m, err := readSkillMeta(incomingMetaPath); err == nil {
			incomingMeta = &m
		}
		if err := os.Remove(incomingMetaPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".update-pending" || entry.Name() == ".skill-meta.yaml" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(skillDir, entry.Name())); err != nil {
			return err
		}
	}
	if err := copyDir(tmp, skillDir); err != nil {
		return err
	}
	if err := refreshSkillMeta(skillDir, incomingMeta); err != nil {
		return err
	}
	return os.RemoveAll(pending.Root)
}

// refreshSkillMeta rewrites the local .skill-meta.yaml with a refreshed
// fingerprint. When an incoming snapshot meta is provided, its content fields
// (summary, categories, tags, compatibility, requirements) overwrite the
// local copy so that safe updates don't silently drop non-blocking metadata
// changes. Install-time fields (origin, categorization, local-change tracking)
// are preserved from the local meta.
func refreshSkillMeta(skillDir string, incoming *skillMeta) error {
	metaPath := filepath.Join(skillDir, ".skill-meta.yaml")
	meta, _ := readSkillMeta(metaPath)
	if incoming != nil {
		meta.Summary = incoming.Summary
		meta.Categories = incoming.Categories
		meta.Tags = incoming.Tags
		meta.Compatibility = incoming.Compatibility
		meta.Requirements = incoming.Requirements
		if incoming.Version != 0 {
			meta.Version = incoming.Version
		}
		// Advance upstream identity so the next update check doesn't replay
		// this revision. Preserve local install-time fields.
		if incoming.Origin.Version != "" {
			meta.Origin.Version = incoming.Origin.Version
		}
		if incoming.Origin.Commit != "" {
			meta.Origin.Commit = incoming.Origin.Commit
		}
	}
	if meta.Version == 0 {
		meta.Version = 1
	}
	sha, size, err := fingerprintSkillMd(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return err
	}
	meta.Fingerprint = skillFingerprint{SHA256: sha, Size: size}
	return writeSkillMeta(metaPath, meta)
}

func printSafetyReport(report safetyReport, stdout io.Writer) {
	fmt.Fprintf(stdout, "Safety review for %s\n", report.Skill)
	if len(report.Flags) == 0 {
		fmt.Fprintln(stdout, "No safety flags.")
		return
	}
	fmt.Fprintf(stdout, "Safety flags (%d):\n", len(report.Flags))
	for _, flag := range report.Flags {
		loc := flag.File
		if flag.Line > 0 {
			loc = fmt.Sprintf("%s:%d", flag.File, flag.Line)
		}
		blocking := "warn"
		if flag.Blocking {
			blocking = "block"
		}
		fmt.Fprintf(stdout, "- %s [%s] %s — %s\n", flag.Name, blocking, loc, flag.Detail)
	}
	if report.SummaryStatus != "" {
		fmt.Fprintf(stdout, "summary_status=%s\n", report.SummaryStatus)
	}
}

func hasBlockingFlags(report safetyReport) bool {
	for _, flag := range report.Flags {
		if flag.Blocking {
			return true
		}
	}
	return false
}

func findPendingUpdate(skill string, root string) (pendingUpdatePaths, error) {
	stat, err := os.Stat(root)
	if err != nil {
		return pendingUpdatePaths{}, err
	}
	if !stat.IsDir() {
		return pendingUpdatePaths{}, fmt.Errorf("%s is not a directory", root)
	}

	pending := pendingUpdatePaths{Skill: skill, Root: root}
	for _, name := range []string{"from", "from-current"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			pending.From = path
			break
		}
	}
	for _, name := range []string{"to", "to-incoming"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			pending.To = path
			break
		}
	}

	if pending.From == "" || pending.To == "" {
		entries, err := os.ReadDir(root)
		if err != nil {
			return pendingUpdatePaths{}, err
		}
		for _, entry := range entries {
			name := entry.Name()
			path := filepath.Join(root, name)
			if pending.From == "" && strings.HasPrefix(name, "from-") {
				pending.From = path
			}
			if pending.To == "" && strings.HasPrefix(name, "to-") {
				pending.To = path
			}
		}
	}

	if pending.From == "" || pending.To == "" {
		return pendingUpdatePaths{}, fmt.Errorf("expected from/to snapshots under %s", root)
	}
	return pending, nil
}

func computeSafetyReport(skill, fromPath, toPath string) (safetyReport, error) {
	fromFiles, err := snapshotFiles(fromPath)
	if err != nil {
		return safetyReport{}, err
	}
	toFiles, err := snapshotFiles(toPath)
	if err != nil {
		return safetyReport{}, err
	}

	report := safetyReport{Skill: skill}
	addFlag := func(name, file string, line int, detail string, blocking bool) {
		report.Flags = append(report.Flags, safetyFlag{Name: name, File: file, Line: line, Detail: detail, Blocking: blocking})
	}
	if _, ok := toFiles["SKILL.md"]; !ok {
		addFlag("missing-skill-file", "SKILL.md", 0, "incoming snapshot does not contain SKILL.md", true)
	}

	if from, ok := fromFiles["SKILL.md"]; ok {
		if to, ok := toFiles["SKILL.md"]; ok {
			_, fromDesc := parseFrontmatterText(from.Content)
			_, toDesc := parseFrontmatterText(to.Content)
			if fromDesc != toDesc {
				addFlag("description-changed", "SKILL.md", frontmatterLine(to.Content, "description"), "frontmatter description changed", true)
			}
		}
	}

	fromMeta := parseMetaText(fromFiles[".skill-meta.yaml"].Content)
	toMeta := parseMetaText(toFiles[".skill-meta.yaml"].Content)
	if !stringSlicesEqual(fromMeta["compatibility"], toMeta["compatibility"]) {
		addFlag("compatibility-changed", ".skill-meta.yaml", 0, "compatibility section changed", true)
	}
	if !stringSlicesEqual(fromMeta["requirements"], toMeta["requirements"]) {
		addFlag("requirements-changed", ".skill-meta.yaml", 0, "requirements section changed", true)
	}

	allRel := map[string]bool{}
	for rel := range fromFiles {
		allRel[rel] = true
	}
	for rel := range toFiles {
		allRel[rel] = true
	}
	var rels []string
	for rel := range allRel {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	addedLines := 0
	removedLines := 0
	for _, rel := range rels {
		from, fromOK := fromFiles[rel]
		to, toOK := toFiles[rel]
		if !fromOK && toOK {
			if strings.HasPrefix(filepath.ToSlash(rel), "scripts/") {
				addFlag("script-added", rel, 0, "new script file added", true)
			}
			if to.Mode&0o111 != 0 {
				addFlag("script-added", rel, 0, "executable file added", true)
			}
			addedLines += lineCount(to.Content)
			for lineNo, line := range strings.Split(to.Content, "\n") {
				checkAddedLine(addFlag, rel, lineNo+1, line)
			}
			continue
		}
		if fromOK && !toOK {
			removedLines += lineCount(from.Content)
			continue
		}
		if !fromOK || !toOK {
			continue
		}
		if isExecutableAdded(from.Mode, to.Mode) {
			addFlag("script-added", rel, 0, "executable bit added", true)
		}
		if from.Content == to.Content {
			continue
		}
		fromLines := lineMultiset(strings.Split(from.Content, "\n"))
		for lineNo, line := range strings.Split(to.Content, "\n") {
			if fromLines[line] > 0 {
				fromLines[line]--
				continue
			}
			addedLines++
			checkAddedLine(addFlag, rel, lineNo+1, line)
		}
		toLines := lineMultiset(strings.Split(to.Content, "\n"))
		for _, line := range strings.Split(from.Content, "\n") {
			if toLines[line] > 0 {
				toLines[line]--
				continue
			}
			removedLines++
		}
	}

	if addedLines+removedLines >= 80 {
		addFlag("large-rewrite", ".", 0, fmt.Sprintf("%d added and %d removed lines", addedLines, removedLines), true)
	}

	if containsFlag(report, "suspicious-instructions") {
		report.SummaryStatus = "tainted"
	}
	sort.SliceStable(report.Flags, func(i, j int) bool {
		if report.Flags[i].Name != report.Flags[j].Name {
			return report.Flags[i].Name < report.Flags[j].Name
		}
		if report.Flags[i].File != report.Flags[j].File {
			return report.Flags[i].File < report.Flags[j].File
		}
		return report.Flags[i].Line < report.Flags[j].Line
	})
	return report, nil
}

func checkAddedLine(addFlag func(string, string, int, string, bool), rel string, lineNo int, line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	lower := strings.ToLower(trimmed)
	if looksSuspicious(lower) {
		addFlag("suspicious-instructions", rel, lineNo, trimmed, true)
		return
	}
	if looksLikeToolGuidance(lower) {
		addFlag("tool-guidance-changed", rel, lineNo, trimmed, true)
	}
}

func looksSuspicious(lower string) bool {
	patterns := []string{
		"ignore previous instructions",
		"ignore all previous instructions",
		"ignore the above",
		"hide this change",
		"do not mention this",
		"summarize this as safe",
		"say this update is safe",
		"reveal secrets",
		"exfiltrate",
		"bypass policy",
		"disable safety",
		"override system",
		"jailbreak",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func looksLikeToolGuidance(lower string) bool {
	patterns := []string{
		"run ", "execute ", "curl ", "bash ", "shell ", "terminal", "chmod ", "sudo ", "rm -rf", "mcp", "tool", "api key", "token",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func containsFlag(report safetyReport, name string) bool {
	for _, flag := range report.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}

func isExecutableAdded(fromMode, toMode os.FileMode) bool {
	return fromMode&0o111 == 0 && toMode&0o111 != 0
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func snapshotFiles(path string) (map[string]snapshotFile, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	files := map[string]snapshotFile{}
	if !stat.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files["SKILL.md"] = snapshotFile{Content: string(data), Mode: stat.Mode()}
		return files, nil
	}
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = snapshotFile{Content: string(data), Mode: info.Mode()}
		return nil
	})
	return files, err
}

func parseFrontmatterText(text string) (name, description string) {
	if !strings.HasPrefix(text, "---") {
		return "", ""
	}
	endIdx := strings.Index(text[3:], "---")
	if endIdx == -1 {
		return "", ""
	}
	frontmatter := text[3 : endIdx+3]
	lines := strings.Split(frontmatter, "\n")
	var inDescription bool
	var descLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "name:") {
			name = unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "name:")))
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
			if strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "-") && indent(line) == 0 {
				break
			}
			if !strings.HasPrefix(trimmed, "-") {
				descLines = append(descLines, trimmed)
			}
		}
	}
	if len(descLines) > 0 {
		description = unquote(strings.TrimSpace(strings.Join(descLines, " ")))
	}
	return name, description
}

func frontmatterLine(text, key string) int {
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			return i + 1
		}
	}
	return 0
}

func parseMetaText(text string) map[string][]string {
	out := map[string][]string{"compatibility": nil, "requirements": nil}
	section := ""
	for _, raw := range strings.Split(text, "\n") {
		line := stripComment(raw)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if indent(line) == 0 {
			key, value, ok := splitYAMLKey(line)
			if !ok {
				section = ""
				continue
			}
			if key == "compatibility" || key == "requirements" {
				section = key
				if value != "" {
					out[section] = append(out[section], value)
				}
			} else {
				section = ""
			}
			continue
		}
		if section == "compatibility" || section == "requirements" {
			out[section] = append(out[section], trimmed)
		}
	}
	return out
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Split(strings.TrimRight(text, "\n"), "\n"))
}

func lineMultiset(lines []string) map[string]int {
	out := map[string]int{}
	for _, line := range lines {
		out[line]++
	}
	return out
}

func setPendingMetaValue(path, key, value string) error {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return err
	}
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			lines[i] = fmt.Sprintf("%s: %s", key, value)
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, fmt.Sprintf("%s: %s", key, value))
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
