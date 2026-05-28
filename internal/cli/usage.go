package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

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
	case "setup":
		return runUsageSetup(args, stdout, stderr, gf)
	default:
		fmt.Fprintf(stderr, "unknown usage subcommand: %s\n", sub)
		fmt.Fprintln(stderr, "Usage: skills-manager usage [matrix|receiver|hook|setup]")
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
	if len(args) > 0 {
		fmt.Fprintf(stderr, "unexpected argument: %s\n", args[0])
		return ExitUsageError
	}
	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}
	view, err := loadUsageMatrix(home)
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
		fmt.Fprintln(out, "No skill invocations recorded yet.")
		fmt.Fprintln(out, "Run `skills-manager usage setup` to start capturing usage.")
		return ExitSuccess
	}
	fmt.Fprintf(out, "Skill usage (%d invocations across %d skills, %d projects)\n\n",
		view.Total, len(view.Skills), len(view.Projects))
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
// view consumed by the CLI and the serve API.
func loadUsageMatrix(home string) (usageMatrixView, error) {
	db, err := state.Open(home)
	if err != nil {
		return usageMatrixView{}, err
	}
	defer db.Close()

	cells, err := db.UsageMatrix()
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
