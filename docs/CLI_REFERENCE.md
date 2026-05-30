# CLI Reference

Every command the v1 CLI exposes. The CLI is the canonical surface; everything else (web UI, scheduler, bundled skills) calls these commands.

## Conventions

All commands support:

- `--non-interactive` — never prompt; bail with exit 2 if input would be needed
- `--quiet` — suppress progress output (still writes to log file)
- `--json` — emit structured JSON instead of human text
- `--verbose` — extra detail in logs
- `--config <path>` — override config file location
- `--help` — show command help

Exit codes:

- `0` — success, no action needed
- `1` — success with notable result (e.g., updates found)
- `2` — usage error (bad flags, missing input in non-interactive mode)
- `3` — operation error (network, filesystem, permissions)
- `4` — partial success (some skills installed, some failed)

## Lifecycle commands

### `skills-manager init [path]`

Set up a new project. Detects stack, proposes categories + tags, writes `.skills/project.yaml`.

```
skills-manager init                # current directory
skills-manager init ./my-app       # specific path
skills-manager init --no-detect    # skip filesystem auto-detection
skills-manager init --quiet        # don't prompt; use detection only
```

Writes:
- `.skills/project.yaml`
- Empty `.skills/installed.lock`
- Adds `.skills/skills/` to `.gitignore`

Exits 2 if `.skills/project.yaml` already exists (use `--force` to overwrite).

### `skills-manager install [--project <path>]`

Compute matches, populate harness paths. Idempotent.

```
skills-manager install
skills-manager install --project ~/dev/my-app
skills-manager install --dry-run         # show what would happen, don't write
skills-manager install --only <skill>    # install just one
skills-manager install --mode symlink    # override copy default
```

Writes per the manifest (`~/.skills-manager/manifests/<slug>.json`). Refuses to overwrite files the manager didn't create unless `--force-overwrite` (with backup).

### `skills-manager sync [--project <path>]`

Re-run install for current project config. Catches up after the canonical library was updated.

```
skills-manager sync
skills-manager sync --all-projects       # sync every registered project
```

Effectively: `install` over the existing setup, refreshing copies.

### `skills-manager uninstall --project <path>`

Reverse what we did to a project. Reads the manifest, removes only managed paths.

```
skills-manager uninstall --project ~/dev/my-app
skills-manager uninstall --all           # uninstall from every project
skills-manager purge                     # uninstall everything + remove ~/.skills-manager
```

Always shows what will be removed and what will be preserved before acting (unless `--non-interactive` + `--confirm`).

## Library commands

### `skills-manager add <source>`

Bring a skill into the library.

```
skills-manager add github.com/user/some-skill
skills-manager add https://example.com/skill.zip
skills-manager add ./local-skill-directory/
skills-manager add anthropic-skills/pdf      # marketplace shortcut
```

Generates origin metadata, fingerprints, runs categorization (auto-suggests tags). Prompts for compatibility intent if not declared.

### `skills-manager scan`

Walk known skill directories on this machine, report unregistered skills.

```
skills-manager scan
skills-manager scan --auto-ingest        # ingest detected skills without prompting (for daemons)
skills-manager scan --json
```

Output:
```json
{
  "found": 47,
  "registered": 43,
  "unregistered": [
    {"name": "email-summarizer", "path": "~/.claude/skills/email-summarizer", "source_guess": "ai-authored"},
    ...
  ]
}
```

### `skills-manager list [--filter ...]`

List skills in the library.

```
skills-manager list
skills-manager list --category Engineering
skills-manager list --tag shadcn
skills-manager list --compatibility exclusive
skills-manager list --unused              # in library, not installed anywhere
```

### `skills-manager show <skill>`

Detailed info about one skill.

```
skills-manager show qa
skills-manager show qa --json
```

Shows: origin, version, fingerprint, compatibility, categories, tags, install locations, usage stats.

### `skills-manager set <skill> --<field> <value>`

Edit a skill's metadata.

```
skills-manager set qa --add-tag testing
skills-manager set qa --remove-tag old-tag
skills-manager set qa --category Quality
skills-manager set qa --compatibility exclusive --harness claude
skills-manager set qa --compatibility portable
```

### `skills-manager remove <skill>`

Remove a skill from the library entirely. Uninstalls from all projects first.

```
skills-manager remove old-skill
skills-manager remove old-skill --keep-installed    # leave already-installed copies alone
```

## Match and project commands

### `skills-manager match [--project <path>]`

Show what would install (or what currently matches the project config).

```
skills-manager match
skills-manager match --explain           # show why each skill matches or doesn't
skills-manager match --suggest           # only show not-yet-installed candidates
```

### `skills-manager projects`

List projects this manager has touched.

```
skills-manager projects
skills-manager projects --json
```

## Update commands

### `skills-manager check`

Poll all sources for new versions. Updates `state.db` with available updates. Does not generate AI summaries (those are deferred / on-demand).

```
skills-manager check
skills-manager check --source github      # only check GitHub-sourced skills
skills-manager check --since 7d           # only check skills last polled more than 7d ago
skills-manager check --non-interactive --json   # for cron / scheduled use
```

Output (JSON mode):
```json
{
  "checked_at": "2026-05-22T14:30:00Z",
  "skills_checked": 251,
  "updates_available": 4,
  "errors": [],
  "updates": [
    {"name": "pdf", "from": "1.4.2", "to": "1.5.0", "source": "marketplace"}
  ]
}
```

### `skills-manager update [<skill>]`

Show pending updates (interactive review). Or accept/reject specific ones.

```
skills-manager update                    # list pending; prompt for each
skills-manager update --accept pdf       # accept just this one
skills-manager update --accept-all-safe  # accept where compat hasn't changed
skills-manager update --reject pdf
skills-manager update --pin pdf 1.4.2
skills-manager update --diff pdf         # show full diff
skills-manager update --safety pdf       # show deterministic safety flags
skills-manager update --summary pdf      # show AI summary if available
```

### `skills-manager summarize <skill>`

Generate AI summary for a pending update. Three modes:

```
skills-manager summarize pdf --auto      # call configured LLM provider
skills-manager summarize pdf --handoff   # write prompt file for an agent fallback
skills-manager summarize pdf --from <file>   # apply the agent's saved output
```

Raw diff and safety flags remain visible even when a summary exists.
`--auto` requires `llm.provider`; direct API providers also require
`llm.api_key-env` and `llm.model`. CLI providers (`codex-cli`, `cursor-cli`)
use the user's existing local CLI authentication. The Cursor CLI provider runs
headlessly with `--trust` in ask mode.

## Status and inspection

### `skills-manager status`

What's the state of things?

```
skills-manager status
```

Output:
```
Library:          251 skills
Projects:         7
Pending updates:  4 (run `skills-manager update`)
Unregistered:     2 detected (run `skills-manager scan`)
Last sync:        2h ago (greg-laptop)
```

### `skills-manager doctor`

Check for problems.

```
skills-manager doctor
```

Validates:
- All manifest paths exist
- All installs match the library fingerprints
- No drift between catalog.yaml and SQLite
- Required system tools, MCP servers, credentials, script runtimes, and model/tool assumptions are available
- Watcher daemon running (if enabled)
- Sync remote reachable

## Cross-machine commands

### `skills-manager init-library [--remote <git-url> | --local-only]`

Initialize `~/.skills-manager/library/` as the canonical git-backed skill
library. With `--remote`, the command sets `origin` and pushes the initial
`main` branch. With `--local-only`, it creates the same local repository without
a remote.

```
skills-manager init-library --local-only
skills-manager init-library --remote git@github.com:you/skills-store.git
```

### `skills-manager join <remote>`

Clone an existing skill library into `~/.skills-manager/library/` on a new
machine and register that machine in `.machines.yaml`.

```
skills-manager join git@github.com:you/skills-store.git
```

### `skills-manager sync-library [--push | --pull]`

Sync the library to/from the configured git remote.

```
skills-manager sync-library              # pull (default)
skills-manager sync-library --push       # push local library changes
skills-manager sync-library --status     # show diff vs remote
```

### `skills-manager machines`

List machines recorded in the shared library's `.machines.yaml`.

```
skills-manager machines
```

## Bundled skill helpers

### `skills-manager port <skill> --to <harness>[,<harness>...]`

Generate the prompt to port a skill (uses the bundled `skills-port` skill).

```
skills-manager port qa --to codex,grok
skills-manager port qa --to codex,grok --auto           # use configured LLM provider
skills-manager port qa --to codex,grok --apply <file>   # save the result back
```

With `--auto`, the configured local provider runs the bundled skill. Without it,
the CLI writes a handoff prompt file for the user's agent; the user saves output
and runs `port --apply`.

### `skills-manager ingest <path> [--auto]`

Ingest a specific path. Generates the prompt to invoke `skills-ingest` for categorization.

```
skills-manager ingest ~/.hermes/skills/code-reviewer
skills-manager ingest ~/.hermes/skills/code-reviewer --auto    # if LLM API key configured
```

## Scheduling commands

### `skills-manager setup-schedule [--provider <name>]`

Interactive wizard to set up scheduled checks.

```
skills-manager setup-schedule
skills-manager setup-schedule --provider local       # cron / launchd / systemd
```

Cloud providers are beyond v1. The local provider is enough to validate the
scheduled check workflow.

### `skills-manager unschedule [--provider <name>]`

Remove a scheduled check.

## Watcher commands

### `skills-manager watch`

Start the filesystem watcher daemon (foreground).

```
skills-manager watch
skills-manager watch --daemon            # detach to background
skills-manager watch --stop              # stop a running daemon
```

Watches harness skill directories; on new SKILL.md, triggers ingest flow.

## Configuration

### `skills-manager config get <key>`

```
skills-manager config get mode
skills-manager config get llm.provider
skills-manager config get llm.api_key-env
skills-manager config get llm.model
```

### `skills-manager config set <key> <value>`

```
skills-manager config set mode symlink
skills-manager config set llm.provider anthropic
skills-manager config set llm.api_key-env ANTHROPIC_API_KEY
skills-manager config set llm.model claude-3-5-haiku-latest
skills-manager config set llm.provider codex-cli
skills-manager config set llm.provider cursor-cli
```

### `skills-manager config show`

Print full config (with sensitive values masked).

```
skills-manager config show
skills-manager config show llm.usage
```

## Web UI

### `skills-manager serve [--port <n>]`

Start the local web UI.

```
skills-manager serve                     # localhost:7777
skills-manager serve --port 8000
skills-manager serve --host 0.0.0.0      # for Tailscale / cross-device access
```

## Usage tracking

### `skills-manager usage [matrix]`

Show recorded skill usage aggregated by skill × project × harness × count.

```
skills-manager usage                     # human-readable matrix
skills-manager --json usage              # structured matrix for the UI
```

### `skills-manager usage receiver [--port <n>] [--host <addr>]`

Run a small OTLP/HTTP log receiver (default `127.0.0.1:4318`) that ingests
Claude Code telemetry at `/v1/logs` and records skill activations. Skill names
require Claude Code to run with `OTEL_LOG_TOOL_DETAILS=1`. OTEL events carry no
project, so the project dimension is filled by the hook below.

### `skills-manager usage hook [--print-config]`

Reads a PreToolUse hook payload on stdin and records one invocation when the
hooked tool is the `Skill` tool, attributing the project from the hook's `cwd`.
Best-effort: it always exits 0 so it can never block a tool call. Use
`--print-config` to print the `settings.json` hook snippet.

### `skills-manager usage setup`

Print step-by-step OTEL receiver and PreToolUse hook setup instructions.

See [USAGE_TRACKING.md](USAGE_TRACKING.md) for the full picture.

## Catch-all

### `skills-manager help [<command>]`

```
skills-manager help
skills-manager help install
```

### `skills-manager --version`

## v1 command checklist

Commands in v0.1: `init`, `add`, `install`, `sync`, `uninstall`, `scan`, `list`, `show`, `match`, `check`, `update`, `status`, `doctor`, `config`, `help`, `--version`

Added in v0.2: `summarize`, `ingest`, `setup-schedule`, `unschedule`, `sync-library`, `join`, `machines`, `set`, `remove`, `purge`

Added in v0.3: `serve`, `watch` (plus UI integration commands)

Added in v1.0: `port`
