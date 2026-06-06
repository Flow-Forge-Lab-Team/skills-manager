package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// stdinReader is the source for hook payloads. It is a package var so tests can
// substitute a fixture without spawning a process.
var stdinReader io.Reader = os.Stdin

// runUsage dispatches the `usage` command and its subcommands:
//
//	skills-manager usage              show the aggregated usage matrix
//	skills-manager usage receiver     run the OTLP/HTTP log receiver
//	skills-manager usage hook         record one invocation from a PreToolUse hook (stdin)
//	skills-manager usage record       record one invocation from any harness (flags or stdin JSON)
//	skills-manager usage setup        print OTEL + hook setup instructions
func runUsage(args []string, stdout, stderr io.Writer, gf globalFlags) int {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "", "matrix":
		return runUsageMatrix(args, stdout, stderr, gf)
	case "receiver":
		return runOTELReceiver(args, stdout, stderr, gf)
	case "hook":
		return runUsageHook(args, stdout, stderr, gf)
	case "record":
		return runUsageRecord(args, stdout, stderr, gf)
	case "setup":
		return runUsageSetup(args, stdout, stderr, gf)
	default:
		fmt.Fprintf(stderr, "unknown usage subcommand: %s\n", sub)
		fmt.Fprintln(stderr, "Usage: skills-manager usage [matrix|receiver|hook|record|setup]")
		return ExitUsageError
	}
}

// usageMatrixView is the JSON payload returned by `usage` and the serve API.
type usageMatrixView struct {
	Skills    []string          `json:"skills"`
	Projects  []string          `json:"projects"`
	Harnesses []string          `json:"harnesses"`
	Cells     []state.UsageCell `json:"cells"`
	Total     int               `json:"total"`
}

func runUsageMatrix(args []string, stdout, stderr io.Writer, gf globalFlags) int {
	since, err := parseUsageSinceFlag(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	view, err := loadUsageMatrix(home, since)
	if err != nil {
		fmt.Fprintf(stderr, "usage matrix: %v\n", err)
		return ExitOpError
	}

	if gf.JSON {
		if err := writeJSON(stdout, view); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitOpError
		}
		return ExitSuccess
	}

	out := gf.outWriter(stdout)
	if view.Total == 0 {
		if since != "" {
			fmt.Fprintf(out, "No skill invocations recorded in the last %s.\n", since)
		} else {
			fmt.Fprintln(out, "No skill invocations recorded yet.")
		}
		fmt.Fprintln(out, "Run `skills-manager usage setup` to start capturing usage.")
		return ExitSuccess
	}
	window := "all time"
	if since != "" {
		window = "last " + since
	}
	fmt.Fprintf(out, "Skill usage (%d invocations across %d skills, %d projects, %s)\n\n",
		view.Total, len(view.Skills), len(view.Projects), window)
	for _, c := range view.Cells {
		project := c.ProjectSlug
		if project == "" {
			project = "(unattributed)"
		}
		harness := c.Harness
		if harness == "" {
			harness = "(unknown)"
		}
		fmt.Fprintf(out, "  %-30s %-24s %-10s %d\n", c.SkillName, project, harness, c.Count)
	}
	return ExitSuccess
}

// loadUsageMatrix reads the invocations table and shapes it into the matrix
// view consumed by the CLI and the serve API. since is a window label such as
// 30d; empty means all time.
func loadUsageMatrix(home string, since string) (usageMatrixView, error) {
	db, err := state.Open(home)
	if err != nil {
		return usageMatrixView{}, err
	}
	defer db.Close()

	cutoff, err := usageSinceCutoff(since)
	if err != nil {
		return usageMatrixView{}, err
	}
	cells, err := db.UsageMatrixSince(cutoff)
	if err != nil {
		return usageMatrixView{}, err
	}

	view := usageMatrixView{Cells: cells}
	if view.Cells == nil {
		view.Cells = []state.UsageCell{}
	}
	skillSet := map[string]bool{}
	projectSet := map[string]bool{}
	harnessSet := map[string]bool{}
	for _, c := range cells {
		view.Total += c.Count
		if c.SkillName != "" {
			skillSet[c.SkillName] = true
		}
		if c.ProjectSlug != "" {
			projectSet[c.ProjectSlug] = true
		}
		if c.Harness != "" {
			harnessSet[c.Harness] = true
		}
	}
	view.Skills = sortedKeys(skillSet)
	view.Projects = sortedKeys(projectSet)
	view.Harnesses = sortedKeys(harnessSet)
	return view, nil
}

// hookPayload is the subset of the Claude Code PreToolUse hook JSON we read.
type hookPayload struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	Cwd           string          `json:"cwd"`
	ToolUseID     string          `json:"tool_use_id"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

// runUsageHook reads a PreToolUse hook payload from stdin and records a skill
// invocation when the hooked tool is the Skill tool. It is best-effort and
// always exits 0 so it can never block the tool call it observes.
func runUsageHook(args []string, stdout, stderr io.Writer, gf globalFlags) int {
	if len(args) > 0 && args[0] == "--print-config" {
		fmt.Fprint(stdout, hookConfigSnippet())
		return ExitSuccess
	}

	data, err := io.ReadAll(io.LimitReader(stdinReader, 1<<20))
	if err != nil {
		fmt.Fprintf(stderr, "usage hook: read stdin: %v\n", err)
		return ExitSuccess
	}
	var payload hookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		fmt.Fprintf(stderr, "usage hook: parse payload: %v\n", err)
		return ExitSuccess
	}
	if !strings.EqualFold(payload.ToolName, "Skill") {
		return ExitSuccess // not a skill activation; nothing to record
	}
	skill := jsonField(string(payload.ToolInput), "skill_name", "skill", "name")
	if skill == "" {
		return ExitSuccess
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "usage hook: manager home: %v\n", err)
		return ExitSuccess
	}
	db, err := state.Open(home)
	if err != nil {
		fmt.Fprintf(stderr, "usage hook: open state: %v\n", err)
		return ExitSuccess
	}
	defer db.Close()

	project := ""
	if payload.Cwd != "" {
		project = projectSlug(payload.Cwd)
	}
	if err := db.RecordInvocation(state.Invocation{
		SkillName:   skill,
		ProjectSlug: project,
		Harness:     "claude",
		Trigger:     "", // the hook can't observe the trigger; OTEL skill_activated enriches it
		Source:      "hook",
		ToolUseID:   payload.ToolUseID,
	}); err != nil {
		fmt.Fprintf(stderr, "usage hook: record: %v\n", err)
	}
	return ExitSuccess
}

func runUsageSetup(args []string, stdout, stderr io.Writer, gf globalFlags) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "unexpected argument: %s\n", args[0])
		return ExitUsageError
	}
	out := stdout
	fmt.Fprintln(out, "Skill usage tracking can be fed two ways.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "1. OTEL receiver (cross-harness, no per-project attribution):")
	fmt.Fprintln(out, indentLines(otelSetupInstructions("127.0.0.1:4318"), "   "))
	fmt.Fprintln(out, "2. PreToolUse hook (Claude Code only, attributes the project from cwd):")
	fmt.Fprintln(out, "   Add this to your Claude Code settings.json, then invocations are")
	fmt.Fprintln(out, "   recorded automatically on every Skill tool call:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, indentLines(hookConfigSnippet(), "   "))
	return ExitSuccess
}

func hookConfigSnippet() string {
	return `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Skill",
        "hooks": [
          { "type": "command", "command": "skills-manager usage hook" }
        ]
      }
    ]
  }
}
`
}

func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

type recordPayload struct {
	SkillName   string `json:"skill"`
	Skill       string `json:"skill_name"`
	Harness     string `json:"harness"`
	Cwd         string `json:"cwd"`
	ProjectSlug string `json:"project_slug"`
	Trigger     string `json:"trigger"`
	Source      string `json:"source"`
	ToolUseID   string `json:"tool_use_id"`
}

// runUsageRecord records one invocation from any harness. Flags take precedence
// over stdin JSON. Best-effort: always exits 0.
func runUsageRecord(args []string, stdout, stderr io.Writer, gf globalFlags) int {
	var harness, skill, cwd, projectSlugValue, trigger, source, toolUseID string
	var remaining []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--harness":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage record: --harness requires a value")
				return ExitSuccess
			}
			harness = args[i+1]
			i++
		case "--skill":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage record: --skill requires a value")
				return ExitSuccess
			}
			skill = args[i+1]
			i++
		case "--cwd":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage record: --cwd requires a value")
				return ExitSuccess
			}
			cwd = args[i+1]
			i++
		case "--trigger":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage record: --trigger requires a value")
				return ExitSuccess
			}
			trigger = args[i+1]
			i++
		case "--source":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage record: --source requires a value")
				return ExitSuccess
			}
			source = args[i+1]
			i++
		case "--tool-use-id":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "usage record: --tool-use-id requires a value")
				return ExitSuccess
			}
			toolUseID = args[i+1]
			i++
		default:
			remaining = args[i:]
			i = len(args)
		}
	}
	if len(remaining) > 0 {
		fmt.Fprintf(stderr, "usage record: unexpected argument: %s\n", remaining[0])
		return ExitSuccess
	}

	if skill == "" || harness == "" {
		data, err := io.ReadAll(io.LimitReader(stdinReader, 1<<20))
		if err != nil {
			fmt.Fprintf(stderr, "usage record: read stdin: %v\n", err)
			return ExitSuccess
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			var payload recordPayload
			if err := json.Unmarshal(data, &payload); err != nil {
				fmt.Fprintf(stderr, "usage record: parse payload: %v\n", err)
				return ExitSuccess
			}
			if skill == "" {
				skill = payload.SkillName
				if skill == "" {
					skill = payload.Skill
				}
			}
			if harness == "" {
				harness = payload.Harness
			}
			if cwd == "" {
				cwd = payload.Cwd
			}
			if trigger == "" {
				trigger = payload.Trigger
			}
			if source == "" {
				source = payload.Source
			}
			if toolUseID == "" {
				toolUseID = payload.ToolUseID
			}
			if projectSlugValue == "" {
				projectSlugValue = payload.ProjectSlug
			}
		}
	}

	if skill == "" || harness == "" {
		fmt.Fprintln(stderr, "usage record: --skill and --harness required (or stdin JSON with skill + harness)")
		return ExitSuccess
	}
	if source == "" {
		source = "record"
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "usage record: manager home: %v\n", err)
		return ExitSuccess
	}
	db, err := state.Open(home)
	if err != nil {
		fmt.Fprintf(stderr, "usage record: open state: %v\n", err)
		return ExitSuccess
	}
	defer db.Close()

	project := usageProjectSlug(cwd, projectSlugValue)
	if err := db.RecordInvocation(state.Invocation{
		SkillName:   skill,
		ProjectSlug: project,
		Harness:     harness,
		Trigger:     trigger,
		Source:      source,
		ToolUseID:   toolUseID,
	}); err != nil {
		fmt.Fprintf(stderr, "usage record: record: %v\n", err)
	}
	return ExitSuccess
}

func parseUsageSinceFlag(args []string) (string, error) {
	since := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--since":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--since requires a value (7d, 30d, 90d)")
			}
			since = args[i+1]
			i++
		default:
			return "", fmt.Errorf("unexpected argument: %s", args[i])
		}
	}
	if since != "" {
		if _, err := usageSinceCutoff(since); err != nil {
			return "", err
		}
	}
	return since, nil
}

func usageSinceCutoff(since string) (string, error) {
	if since == "" {
		return "", nil
	}
	days, err := parseUsageSinceDays(since)
	if err != nil {
		return "", err
	}
	return time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339), nil
}

func usageProjectSlug(cwd, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	return projectSlug(filepath.Clean(abs))
}

func parseUsageSinceDays(since string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(since)) {
	case "7d":
		return 7, nil
	case "30d":
		return 30, nil
	case "90d":
		return 90, nil
	default:
		return 0, fmt.Errorf("invalid --since value %q (use 7d, 30d, or 90d)", since)
	}
}
