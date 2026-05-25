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

// stdinIsTTY checks whether stdin is connected to a terminal.
func stdinIsTTY() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// Exit code conventions (see docs/CLI_REFERENCE.md).
const (
	ExitSuccess    = 0
	ExitNotable    = 1
	ExitUsageError = 2
	ExitOpError    = 3
	ExitPartial    = 4
	ExitNoPending  = 5
)

// globalFlags captures the flags every subcommand honors.
// They are extracted from the argv slice before the per-command parser runs.
type globalFlags struct {
	NonInteractive bool
	Quiet          bool
	JSON           bool
	Verbose        bool
	Help           bool
	Config         string
}

// extractGlobalFlags strips supported global flags from args and returns the
// remaining tokens. Flags may appear anywhere in args, even after a subcommand
// name. Unknown flags are left in place for the per-command parser.
func extractGlobalFlags(args []string) (globalFlags, []string, error) {
	var gf globalFlags
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--non-interactive":
			gf.NonInteractive = true
		case "--quiet":
			gf.Quiet = true
		case "--json":
			gf.JSON = true
		case "--verbose":
			gf.Verbose = true
		case "--help", "-h":
			gf.Help = true
		case "--config":
			if i+1 >= len(args) {
				return gf, nil, fmt.Errorf("--config requires a path")
			}
			gf.Config = args[i+1]
			i++
		default:
			rest = append(rest, a)
		}
	}
	return gf, rest, nil
}

// outWriter returns the appropriate writer for human-readable output. It
// returns io.Discard when either --quiet or --json is set, so JSON output
// stands alone on stdout and quiet runs stay silent. JSON output is always
// written to the real stdout regardless.
func (gf globalFlags) outWriter(stdout io.Writer) io.Writer {
	if gf.Quiet || gf.JSON {
		return io.Discard
	}
	return stdout
}

// writeJSON marshals v as indented JSON to stdout. Returns an error from
// marshal/write; callers convert to exit codes.
func writeJSON(stdout io.Writer, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, string(data)); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// logger writes structured single-line records to a log file under
// ~/.skills-manager/logs/. It is best-effort: a logging failure never aborts
// a command. A nil logger is safe to call methods on (everything is a no-op).
type logger struct {
	w       io.WriteCloser
	verbose bool
}

func openLogger(verbose bool) *logger {
	home, err := managerHome()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil
	}
	path := filepath.Join(dir, "skills-manager.log")
	rotateLogIfTooLarge(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return &logger{w: f, verbose: verbose}
}

const maxLogBytes = 10 * 1024 * 1024 // 10 MiB

func rotateLogIfTooLarge(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Size() < maxLogBytes {
		return
	}
	// Single-generation rotation: keep one .1 backup.
	_ = os.Rename(path, path+".1")
}

func (l *logger) close() {
	if l == nil || l.w == nil {
		return
	}
	_ = l.w.Close()
}

// log writes a single record. level is "info", "warn", "error", "debug".
// debug records are suppressed unless verbose was set.
func (l *logger) log(level, command string, fields map[string]string) {
	if l == nil || l.w == nil {
		return
	}
	if level == "debug" && !l.verbose {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s level=%s cmd=%s",
		time.Now().UTC().Format(time.RFC3339Nano), level, command)
	for k, v := range fields {
		fmt.Fprintf(&b, " %s=%q", k, v)
	}
	b.WriteByte('\n')
	_, _ = l.w.Write([]byte(b.String()))
}

// helpText returns the help string for the top-level CLI or a specific
// subcommand. Unknown subcommands fall back to the top-level help.
func helpText(cmd string) string {
	switch cmd {
	case "install":
		return `skills-manager install [flags]

Install matching skills into a project. Idempotent.

Flags:
  --project <path>               project to install into (default: current dir)
  --only <skill>                 install just one named skill
  --dry-run                      preview without writing
  --allow-missing-requirements   install even if required tools are missing
  --skip-missing-locked          skip lock entries missing from the library

` + globalFlagHelp()
	case "sync":
		return `skills-manager sync [flags]

Re-run install for current project config. Refreshes copies after the library
was updated. Same flags as install.

` + globalFlagHelp()
	case "init-library":
		return `skills-manager init-library [--remote <git-url> | --local-only]

Initialize the canonical skill library as a git repository.

` + globalFlagHelp()
	case "join":
		return `skills-manager join <remote>

Clone an existing skill library into the manager home and register this machine.

` + globalFlagHelp()
	case "sync-library":
		return `skills-manager sync-library [--pull | --push | --status]

Synchronize the canonical skill library with its git remote. Pull is the default.

` + globalFlagHelp()
	case "machines":
		return `skills-manager machines

List machines recorded in the shared library metadata.

` + globalFlagHelp()
	case "uninstall":
		return `skills-manager uninstall --project <path> --confirm

Reverse what was installed. Reads the manifest and removes only managed paths
whose contents still match what the manager installed.

Flags:
  --project <path>   project to uninstall from (required)
  --confirm          required to actually remove files

` + globalFlagHelp()
	case "list":
		return `skills-manager list [flags]

List skills in the canonical library.

Flags:
  --category <name>   filter by category
  --tag <name>        filter by tag
  --rebuild           rebuild catalog.yaml from library before listing

` + globalFlagHelp()
	case "match":
		return `skills-manager match [flags]

Preview ranked skills matching the project's categories+tags (read-only; no install).

Flags:
  --project <path>   project to preview for (default: current dir)
  --explain          show reasons, overlaps, negative signals, missing requirements per candidate
  --suggest          hide skills already present in .skills/installed.lock

` + globalFlagHelp()
	case "show":
		return `skills-manager show <skill> [flags]

Show details for a single skill, including requirements and install locations.

` + globalFlagHelp()
	case "check":
		return `skills-manager check [flags]

Poll GitHub for new commits on skills with github-sourced origins.
Stages pending updates under library/<skill>/.update-pending/ when new commits are found.

Flags:
  --skill <name>   check only this skill (default: check all)
  --force          bypass 24-hour lazy-skip and poll anyway

` + globalFlagHelp()
	case "update":
		return `skills-manager update [flags]
       skills-manager update --safety <skill>
       skills-manager update --diff <skill>
       skills-manager update --accept-all-safe

Review and accept pending library updates staged under
library/<skill>/.update-pending/.

Forms:
  (no args)           list all pending updates
  --diff <skill>      print unified diff between from.md and to.md
  --safety <skill>    print a safety report for one pending update
  --accept-all-safe   apply every pending update that has no blocking flags;
                      refuses (exit 4) if any update is blocked

` + globalFlagHelp()
	case "summarize":
		return `skills-manager summarize <skill> (--auto | --handoff | --from <file>)

Generate or import an advisory AI summary for a pending update. Raw diffs and
deterministic safety flags remain the source of truth.

Forms:
  --auto          run the configured LLM provider
  --handoff       write a prompt file for an agent fallback
  --from <file>   validate and cache saved agent/provider output

` + globalFlagHelp()
	case "config":
		return `skills-manager config <get|set|show> [args]

Configure local, opt-in manager behavior.

Forms:
  config set llm.provider anthropic|openai
  config set llm.api_key-env <ENV_VAR_NAME>
  config set llm.model <model-id>
  config get <key>
  config show [llm.usage]

` + globalFlagHelp()
	case "compat-check":
		return `skills-manager compat-check <skill> [--to <h1,h2,...>] (--auto | --handoff | --from <file>)

Deeper (LLM) compatibility + execution requirements analysis beyond static detectors.

Forms:
  --auto          run the configured LLM provider
  --handoff       write a prompt file for an agent fallback
  --from <file>   validate and use saved agent/provider output
  --to <list>     comma-separated target harnesses (default: common set)

` + globalFlagHelp()
	case "seed-catalog":
		return `skills-manager seed-catalog --from <remap-results.json> [--dry-run]

Apply deterministic seed categories/tags, compatibility, and inferred
requirements to library sidecars, then rebuild catalog.yaml.

Forms:
  --from <file>   JSON array of {name, locs, categories, tags}
  --dry-run       validate and report without writing sidecars/catalog

` + globalFlagHelp()
	case "set":
		return `skills-manager set <skill> --compatibility <mode> [flags]

Update a skill's compatibility declaration. Rewrites SKILL.md frontmatter and
.skill-meta.yaml, then refreshes catalog.yaml.

Flags:
  --compatibility <mode>     portable | compatible | exclusive  (required)
  --harness <name>           target harness (required for exclusive)
  --harnesses <a,b,c>        target harnesses (required for compatible)
  --reason "<text>"          optional human note (exclusive mode)

` + globalFlagHelp()
	case "status":
		return `skills-manager status

Show library counts, pending updates, unregistered skills, stale scheduled check state.

` + globalFlagHelp()
	case "doctor":
		return `skills-manager doctor [--rebuild-state] [--rebuild-catalog]

Check manifests, fingerprints, catalog/state drift, required tools/MCP/credentials/runtimes.
Rebuild derived state or catalog if requested. Non-zero exit on problems.

` + globalFlagHelp()
	case "add":
		return `skills-manager add <source> [flags]

Bring a skill into the library from GitHub, a local path, or the marketplace cache.

Flags:
  --auto                  auto-ingest only high-confidence skills
  --yes                   accept all suggestions without prompting
  --name <name>           override the skill name

` + globalFlagHelp()
	case "scan":
		return `skills-manager scan [flags]

Discover and report skills in harness skill directories.

Flags:
  --paths <dirs>          comma-separated list of directories to scan (overrides defaults)
  --ingest                interactively ingest unregistered skills
  --auto-ingest           auto-ingest high-confidence unregistered skills

` + globalFlagHelp()
	case "new":
		return `skills-manager new <name>

Create a new skill and open it in $EDITOR. Name must be 3-64 alphanumeric chars + dashes.

` + globalFlagHelp()
	case "init":
		return `skills-manager init [path]

Set up a project by detecting stack signals, proposing categories/tags/harnesses,
and writing .skills/project.yaml plus an empty .skills/installed.lock.

Flags:
  --no-detect             skip filesystem auto-detection
  --force                 overwrite an existing project.yaml and installed.lock

` + globalFlagHelp()
	}
	return `skills-manager: AI skill library manager

Usage: skills-manager <command> [flags]

Commands:
  init        set up project config from filesystem detection
  add         bring a skill into the library
  scan        discover skills in harness directories
  new         create a new skill
  check       poll GitHub for new commits on skills
  install     install matching skills into a project
  sync        re-run install to pick up library updates
  init-library initialize the canonical library git repo
  join        clone an existing library onto this machine
  sync-library synchronize the library git repo
  machines    list machines registered in the library
  uninstall   remove managed skills from a project
  list        list skills in the canonical library
  match       preview ranked skills matching project categories+tags (with --explain --suggest)
  show        show details for a single skill
  update      review and accept pending library updates
  summarize   generate or import advisory update summaries
  config      configure opt-in provider settings and usage accounting
  seed-catalog apply seed taxonomy metadata to the library
  status      show library counts, pending, unregistered, scheduled state
  doctor      diagnose problems; --rebuild-state --rebuild-catalog
  set         update a skill's compatibility declaration
  help        show help for a command

` + globalFlagHelp()
}

func globalFlagHelp() string {
	return `Global flags:
  --non-interactive   never prompt; exit 2 if input would be needed
  --quiet             suppress human-readable output (JSON still emitted with --json)
  --json              emit structured JSON instead of human text
  --verbose           include debug records in the log file
  --config <path>     override config file location
  --help, -h          show help for a command
  --version           show version`
}
