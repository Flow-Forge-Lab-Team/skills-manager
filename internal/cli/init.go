package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type initOptions struct {
	projectPath string
	noDetect    bool
	force       bool
}

type projectDetection struct {
	Categories []string `json:"categories"`
	Tags       []string `json:"tags"`
	Harnesses  []string `json:"harnesses"`
}

func runInit(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	opts, err := parseInitOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	projectPath, err := absoluteProjectPath(opts.projectPath)
	if err != nil {
		fmt.Fprintf(stderr, "project path: %v\n", err)
		return ExitOpError
	}

	configPath := filepath.Join(projectPath, ".skills", "project.yaml")
	if gf.Config != "" {
		configPath = gf.Config
	}
	lockPath := filepath.Join(projectPath, ".skills", "installed.lock")

	if !opts.force {
		if _, err := os.Stat(configPath); err == nil {
			fmt.Fprintf(stderr, "%s already exists; use --force to overwrite\n", configPath)
			return ExitUsageError
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "stat project config: %v\n", err)
			return ExitOpError
		}
		if _, err := os.Stat(lockPath); err == nil {
			fmt.Fprintf(stderr, "%s already exists; use --force to overwrite\n", lockPath)
			return ExitUsageError
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "stat installed lock: %v\n", err)
			return ExitOpError
		}
	}

	detection := projectDetection{}
	if !opts.noDetect {
		detection = detectProjectDefaults(projectPath)
		if len(detection.Harnesses) == 0 {
			detection.Harnesses = detectActiveHarnesses(projectPath)
		}
	}

	interactive := !gf.NonInteractive && !gf.JSON && !gf.Quiet && stdinIsTTY()
	if interactive {
		reader := bufio.NewReader(os.Stdin)
		detection.Categories = promptList(reader, realStdout, "Categories", detection.Categories)
		detection.Tags = promptList(reader, realStdout, "Tags", detection.Tags)
		detection.Harnesses = promptList(reader, realStdout, "Harnesses", detection.Harnesses)
	}

	project := projectConfig{
		Name:       slug(filepath.Base(projectPath)),
		Categories: detection.Categories,
		Tags:       detection.Tags,
		Harnesses:  detection.Harnesses,
	}
	if project.Name == "" {
		project.Name = "project"
	}
	project.Categories = nonNilStringSlice(project.Categories)
	project.Tags = nonNilStringSlice(project.Tags)
	project.Harnesses = nonNilStringSlice(project.Harnesses)

	if err := writeProjectConfig(configPath, project, time.Now().UTC()); err != nil {
		fmt.Fprintf(stderr, "write project config: %v\n", err)
		return ExitOpError
	}

	if _, err := os.Stat(lockPath); errors.Is(err, os.ErrNotExist) || opts.force {
		lock := installLock{
			Version:     1,
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			GeneratedBy: "skills-manager " + Version,
			Skills:      []installLockEntry{},
		}
		if err := writeInstallLock(lockPath, lock); err != nil {
			fmt.Fprintf(stderr, "write installed lock: %v\n", err)
			return ExitOpError
		}
	} else if err != nil {
		fmt.Fprintf(stderr, "stat installed lock: %v\n", err)
		return ExitOpError
	}

	if err := ensureGitignoreEntry(filepath.Join(projectPath, ".gitignore"), ".skills/skills/"); err != nil {
		fmt.Fprintf(stderr, "write gitignore: %v\n", err)
		return ExitOpError
	}

	if gf.JSON {
		if err := writeJSON(realStdout, map[string]interface{}{
			"project":    projectPath,
			"config":     configPath,
			"lock":       lockPath,
			"categories": project.Categories,
			"tags":       project.Tags,
			"harnesses":  project.Harnesses,
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}

	stdout := gf.outWriter(realStdout)
	fmt.Fprintf(stdout, "Initialized %s\n", projectPath)
	fmt.Fprintf(stdout, "Categories: %s\n", strings.Join(project.Categories, ", "))
	fmt.Fprintf(stdout, "Tags: %s\n", strings.Join(project.Tags, ", "))
	fmt.Fprintf(stdout, "Harnesses: %s\n", strings.Join(project.Harnesses, ", "))
	return ExitSuccess
}

func ensureGitignoreEntry(path string, entry string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == entry {
				return nil
			}
		}
	}

	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString(entry)
	b.WriteByte('\n')
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func parseInitOptions(args []string) (initOptions, error) {
	opts := initOptions{projectPath: "."}
	pathSet := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-detect":
			opts.noDetect = true
		case "--force":
			opts.force = true
		default:
			if strings.HasPrefix(args[i], "--") {
				return opts, fmt.Errorf("unknown init argument: %s", args[i])
			}
			if pathSet {
				return opts, errors.New("init accepts at most one path")
			}
			opts.projectPath = args[i]
			pathSet = true
		}
	}
	return opts, nil
}

func detectProjectDefaults(projectPath string) projectDetection {
	d := projectDetection{
		Categories: []string{},
		Tags:       []string{},
		Harnesses:  []string{},
	}
	categories := map[string]bool{}
	tags := map[string]bool{}

	add := func(cats []string, ts []string) {
		for _, c := range cats {
			categories[c] = true
		}
		for _, t := range ts {
			tags[t] = true
		}
	}
	exists := func(parts ...string) bool {
		_, err := os.Stat(filepath.Join(append([]string{projectPath}, parts...)...))
		return err == nil
	}
	glob := func(pattern string) bool {
		matches, _ := filepath.Glob(filepath.Join(projectPath, pattern))
		return len(matches) > 0
	}

	if exists("package.json") {
		add([]string{"Engineering"}, []string{"nodejs"})
		if deps := readPackageDependencies(filepath.Join(projectPath, "package.json")); len(deps) > 0 {
			if hasPackage(deps, "next") {
				add([]string{"Engineering", "Design"}, []string{"nextjs", "react"})
			}
			if hasPackage(deps, "react") {
				add([]string{"Engineering", "Design"}, []string{"react"})
			}
			if hasPackagePrefix(deps, "@sentry/") {
				add([]string{"Operations"}, []string{"sentry"})
			}
			if hasPackagePrefix(deps, "@supabase/") {
				add([]string{"Data", "Engineering"}, []string{"supabase"})
			}
			if hasPackage(deps, "stripe") {
				add([]string{"Engineering"}, []string{"stripe"})
			}
			if hasPackage(deps, "tailwindcss") {
				add([]string{"Design"}, []string{"tailwind"})
			}
			if hasPackage(deps, "prisma") || hasPackage(deps, "@prisma/client") {
				add([]string{"Data", "Engineering"}, []string{"prisma"})
			}
			if hasPackage(deps, "playwright") || hasPackage(deps, "@playwright/test") {
				add([]string{"Quality"}, []string{"playwright"})
			}
			if hasPackage(deps, "vitest") {
				add([]string{"Quality"}, []string{"vitest"})
			}
			if hasPackage(deps, "jest") {
				add([]string{"Quality"}, []string{"jest"})
			}
		}
	}
	if glob("next.config.*") {
		add([]string{"Engineering", "Design"}, []string{"nextjs", "react"})
	}
	if exists("components.json") {
		add([]string{"Design"}, []string{"shadcn"})
	}
	if glob("tailwind.config.*") {
		add([]string{"Design"}, []string{"tailwind"})
	}
	if exists("pyproject.toml") || exists("setup.py") {
		add([]string{"Engineering"}, []string{"python"})
	}
	if exists("go.mod") {
		add([]string{"Engineering"}, []string{"go"})
	}
	if exists("Cargo.toml") {
		add([]string{"Engineering"}, []string{"rust"})
	}
	if exists("Gemfile") {
		add([]string{"Engineering"}, []string{"ruby"})
	}
	if exists("composer.json") {
		add([]string{"Engineering"}, []string{"php"})
	}
	if glob("playwright.config.*") {
		add([]string{"Quality"}, []string{"playwright"})
	}
	if glob("vitest.config.*") {
		add([]string{"Quality"}, []string{"vitest"})
	}
	if glob("jest.config.*") {
		add([]string{"Quality"}, []string{"jest"})
	}
	if exists("Dockerfile") || exists(".github", "workflows") || glob("*.tf") || exists("terraform") {
		add([]string{"Operations"}, nil)
	}
	if exists("prisma", "schema.prisma") {
		add([]string{"Data", "Engineering"}, []string{"prisma"})
	}
	if exists(".mcp.json") {
		add([]string{"Agent-tooling"}, []string{"mcp"})
	}

	d.Categories = orderedDetectedCategories(categories)
	d.Tags = sortedKeys(tags)
	return d
}

func readPackageDependencies(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pkg struct {
		Dependencies         map[string]interface{} `json:"dependencies"`
		DevDependencies      map[string]interface{} `json:"devDependencies"`
		PeerDependencies     map[string]interface{} `json:"peerDependencies"`
		OptionalDependencies map[string]interface{} `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, deps := range []map[string]interface{}{pkg.Dependencies, pkg.DevDependencies, pkg.PeerDependencies, pkg.OptionalDependencies} {
		for name := range deps {
			out[name] = true
		}
	}
	return out
}

func hasPackage(deps map[string]bool, name string) bool {
	return deps[name]
}

func hasPackagePrefix(deps map[string]bool, prefix string) bool {
	for name := range deps {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func orderedDetectedCategories(values map[string]bool) []string {
	order := []string{"Engineering", "Quality", "Operations", "Data", "Design", "Documents", "Writing", "Business", "Productivity", "Agent-tooling"}
	out := []string{}
	for _, category := range order {
		if values[category] {
			out = append(out, category)
		}
	}
	return out
}

func detectActiveHarnesses(projectPath string) []string {
	configDirs := map[string][]string{
		"claude":      {".claude"},
		"codex":       {".codex"},
		"grok":        {".grok"},
		"antigravity": {".agents"},
		"gemini":      {".agents", ".gemini"},
		"hermes":      {".hermes"},
		"openclaw":    {".openclaw"},
	}
	binaries := map[string]string{
		"claude":      "claude",
		"codex":       "codex",
		"grok":        "grok",
		"antigravity": "antigravity",
		"gemini":      "gemini",
		"hermes":      "hermes",
		"openclaw":    "openclaw",
	}

	active := map[string]bool{}
	for harness, binary := range binaries {
		if _, err := exec.LookPath(binary); err == nil {
			active[harness] = true
		}
	}
	for harness, dirs := range configDirs {
		for _, dir := range dirs {
			if existsDir(filepath.Join(projectPath, dir)) || existsDir(filepath.Join(userHomeOrEmpty(), dir)) {
				active[harness] = true
			}
		}
	}
	return orderedHarnesses(active)
}

func userHomeOrEmpty() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func existsDir(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func orderedHarnesses(active map[string]bool) []string {
	order := []string{"claude", "codex", "grok", "antigravity", "gemini", "hermes", "openclaw"}
	out := []string{}
	for _, harness := range order {
		if active[harness] {
			out = append(out, harness)
		}
	}
	return out
}

func promptList(reader *bufio.Reader, stdout io.Writer, label string, defaults []string) []string {
	fmt.Fprintf(stdout, "%s [%s]: ", label, strings.Join(defaults, ", "))
	line, err := reader.ReadString('\n')
	if err != nil {
		return defaults
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaults
	}
	items := strings.Split(line, ",")
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		out = append(out, item)
		seen[item] = true
	}
	sort.Strings(out)
	return out
}

func writeProjectConfig(path string, project projectConfig, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf strings.Builder
	buf.WriteString("version: 1\n")
	buf.WriteString(fmt.Sprintf("name: %s\n", project.Name))
	buf.WriteString(fmt.Sprintf("created: %s\n", now.Format("2006-01-02")))
	buf.WriteString(fmt.Sprintf("last_synced: %s\n\n", now.Format(time.RFC3339)))
	writeStringList(&buf, "categories", project.Categories)
	writeStringList(&buf, "tags", project.Tags)
	buf.WriteString("# Active harnesses (auto-detected; user can override)\n")
	writeStringList(&buf, "harnesses", project.Harnesses)
	buf.WriteString("# Per-project overrides\n")
	buf.WriteString("skills:\n")
	buf.WriteString("  always_include: []\n")
	buf.WriteString("  never_include: []\n")
	buf.WriteString("  pinned_versions: {}\n")
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

func nonNilStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func writeStringList(buf *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		buf.WriteString(key + ": []\n\n")
		return
	}
	buf.WriteString(key + ":\n")
	for _, value := range values {
		buf.WriteString(fmt.Sprintf("  - %q\n", value))
	}
	buf.WriteString("\n")
}
