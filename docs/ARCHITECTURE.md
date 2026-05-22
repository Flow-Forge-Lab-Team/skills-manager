# Architecture

## High-level shape

```
┌──────────────────────────────────────────────────────────────┐
│  skills-manager (CLI — canonical surface)                    │
│                                                               │
│  • init / install / sync / update / scan / status / port     │
│  • Reads & writes: state, library, manifests                 │
│  • Headless-capable (no TTY required)                        │
│  • Pure file ops + git + HTTP polling                        │
│  • No LLM calls unless explicitly configured                 │
└──────────────────────────────────────────────────────────────┘
                          ↑↓
            ┌─────────────┴─────────────┐
            │       Local state          │
            │  ~/.skills-manager/        │
            │   ├── library/             │
            │   ├── state.db             │
            │   ├── manifests/           │
            │   ├── logs/                │
            │   └── config.yaml          │
            └─────────────┬─────────────┘
                          ↑
            ┌─────────────┴─────────────┐
            │  skills-manager serve      │
            │  (web UI — deferred)       │
            │  Reads state, calls CLI    │
            └────────────────────────────┘
                          ↑
                  user opens browser

  ╭───────────────────────────────────────────────────────────╮
  │  Scheduler (one of):                                       │
  │   • launchd / cron / systemd  (local OS)                   │
  │   • Cloud schedulers          (deferred beyond v1)         │
  │                                                            │
  │  All run: `skills-manager check --non-interactive --json`  │
  ╰───────────────────────────────────────────────────────────╯

  ╭───────────────────────────────────────────────────────────╮
  │  Bundled manager skills (installed into the user's agent) │
  │                                                            │
  │   • skills-port      — cross-harness translation           │
  │   • skills-ingest    — categorize + tag new skills         │
  │   • skills-compat    — analyze compatibility + requirements│
  │   • skills-author    — guide for writing new skills        │
  │   • skills-diff-summary — AI summary of update diffs       │
  │                                                            │
  │  Invoked via configured provider or agent fallback.        │
  │  The CLI validates all generated output.                   │
  ╰───────────────────────────────────────────────────────────╯
```

## Core principle: CLI is canonical

Everything is a CLI command. The web UI is a viewer that reads CLI-written state and triggers CLI invocations. The scheduler runs CLI commands. Bundled skills produce output that the CLI consumes.

This means:

- The product is useful without a UI (CLI-only workflows are first-class)
- Scheduling is just "run a command on a timer"
- Headless servers, CI environments, and remote machines all work
- Cross-platform is easier (no Electron/Tauri/native UI complexity in v1)

## Component responsibilities

### CLI

- **State management**: SQLite + flat files in `~/.skills-manager/`
- **Library operations**: import, list, remove, set tags/categories/compatibility
- **Install operations**: copy or symlink into harness paths; manifest tracking
- **Match operations**: compute which skills belong in a project
- **Scan operations**: walk harness directories, detect unregistered skills
- **Update operations**: poll sources, store diffs, propagate updates
- **Sync operations**: git push/pull against library remote
- **Prompt generation**: produce prompts for bundled skills the user runs in their agent
- **Validation**: lock files, fingerprints, manifest integrity, execution requirements

### State layer

Local-only by default. Structure:

```
~/.skills-manager/
├── library/                  # canonical skill store (git-managed)
│   ├── <skill-name>/
│   │   ├── SKILL.md
│   │   ├── .skill-meta.yaml
│   │   ├── .variants.yaml    # if it has harness-specific variants
│   │   ├── scripts/          # optional, from upstream
│   │   └── ...
│   └── ...
├── state.db                  # SQLite: catalog, usage, manifests, history
├── manifests/                # per-project install manifests
│   ├── my-saas-app.json
│   └── ...
├── logs/                     # scheduled check logs, diagnostics
├── summaries/                # AI summary cache (when generated)
├── notifications/            # output from scheduled tasks
├── backups/                  # backups of replaced skills
└── config.yaml               # user config: mode, sync source, etc.
```

Details in `DATA_MODEL.md`.

### Web UI (deferred)

Pure read+invoke. Frontend is a static SPA bundled with the manager. Started via `skills-manager serve`.

- Reads state via HTTP/SSE from the CLI server process
- User actions translate to CLI invocations (POST endpoints that run commands)
- Never has its own state — always reflects what's on disk

The web UI is a deferred local triage surface. It should not own product
behavior or state, but it may become the best way to review update batches,
matrix views, and cross-machine drift once the CLI state model is trustworthy.
The mockup at `mockup.html` previews possible views; it is not a v0.1
commitment.

### Bundled manager skills

These are SKILL.md files that live in the manager's own repo and get installed into the user's harness like any other skill. They handle the LLM-heavy operations that would otherwise require the manager to call an API.

Architecture pattern:

1. CLI generates a prompt + context (e.g., the diff content for `skills-diff-summary`)
2. CLI sends it to the configured provider, or writes a handoff file for an agent fallback
3. User invokes the bundled skill through an explicitly configured local LLM provider, or uses a clipboard/file handoff fallback in an agent session
4. The agent's model produces the output
5. User saves output back; CLI validates and stores

This keeps the manager LLM-agnostic while avoiding clipboard handoff as the
daily path. API-key usage is explicit, local, and opt-in. Clipboard/file handoff
exists for users who refuse to configure a provider.

Details in `BUNDLED_SKILLS.md`.

### Scheduler integration

The CLI is invoked by external schedulers. Three modes supported:

- **Local OS** (`launchd`, `cron`, `systemd`, Windows Task Scheduler) — free, runs only when machine is on
- **Cloud-scheduled** (Claude Code routines, Codex automations) — deferred beyond v1; provider churn, billing, credentials, and result writeback are not worth the early complexity
- **Manual** — user runs `skills-manager update` ad-hoc

The CLI doesn't run its own daemon. The local OS handles the timer in v1; cloud
schedulers are beyond v1.

Details in `SCHEDULING.md`.

## Harness integration model

### Install mechanisms

| Mechanism | When used | Trade-off |
|---|---|---|
| **Copy** (default) | All v1 harnesses (all are SKILL.md-native) | Safest, each location has independent file, drift possible |
| **Symlink** (opt-in) | When user enables `mode: symlink` | One source of truth, auto-propagate, less Windows-friendly |
| **Compile** (deferred to v1.0) | Cursor, Copilot | Different formats require translation |
| **Assemble** (deferred) | AGENTS.md, CLAUDE.md aggregation | Multi-file → single-file |

### v1 supported harnesses

All SKILL.md-native:

| Harness | Global path | Project path |
|---|---|---|
| Claude Code | `~/.claude/skills/` | `.claude/skills/` |
| Codex CLI | `~/.codex/skills/` | `.codex/skills/` |
| Grok Build | `~/.grok/skills/` | `.grok/skills/` |
| Antigravity | `~/.gemini/antigravity/skills/` | `.agents/skills/` |
| Gemini CLI | `~/.gemini/skills/` | `.agents/skills/` |
| Hermes | `~/.hermes/skills/` | `skills/` |
| OpenClaw | `~/.openclaw/skills/` | `skills/` |

`.agents/skills/` is shared between Antigravity and Gemini CLI (one symlink/copy serves both). `skills/` is shared between Hermes and OpenClaw. So 7 harnesses map to 5 distinct project paths.

### Deferred (v1.0+)

- Cursor (`.cursor/rules/*.mdc` — different format, needs compiler)
- Copilot (`.github/instructions/*.instructions.md` — different format)
- AGENTS.md (aggregation — needs assembler)

## Auth and credentials

By design, the manager itself stores zero credentials:

- **GitHub access**: uses the user's existing `gh` CLI auth or `GIT_TOKEN` env var
- **LLM access for bundled skills**: optional provider credentials are explicitly configured by the user, preferably through environment variables or OS keychain integration
- **Agent handoff fallback**: no API key needed by the manager, but higher friction and never the recommended unattended path
- **Claude/Codex OAuth**: never used directly (per [Anthropic's Feb 2026 ToS](https://lalatenduswain.medium.com/claude-code-on-claude-max-plan-understanding-oauth-token-vs-api-key-authentication-in-2026-96a6213d2cde)) — the manager only invokes the user's harness CLI which uses its own auth

## What the manager never does

Hard rules:

- Never modifies skills the user authored locally (no upstream → no auto-update)
- Never silently overwrites a file at a target path; backs up + prompts
- Never makes LLM API calls without explicit user consent + visible credential
- Never uses harness OAuth tokens directly
- Never deletes anything the manifest didn't record creating
- Never bypasses the compatibility model (an `exclusive: claude` skill is never installed elsewhere)
- Never treats an AI summary as sufficient proof that an update is safe; raw diff and deterministic safety flags remain visible

## v1 implementation tech

Resolved during v0.1 kickoff:

- **Language**: Go. The manager needs a globally installable single binary, heavy filesystem work, GitHub/Git integration, SQLite, YAML, and a small local HTTP server later. Go keeps that implementation straightforward while avoiding Rust's extra onboarding cost for the first CLI slice. Python remains useful for experiments, but not for the shipped binary.
- **State**: SQLite via system libraries; YAML for human-facing config
- **HTTP**: built-in stdlib (no heavy framework)
- **Watcher**: `fsnotify` if the optional watcher ships in v0.3
- **Web UI** (when added): static SPA in `dist/`, served by the CLI's `serve` mode

The implementation language matters less than the design. The architecture should hold up regardless.

## Open questions

These should be resolved before implementation kicks off:

1. **State storage format**: pure SQLite, or SQLite + YAML hybrid? (Lean: hybrid — YAML for human-edited config, SQLite for derived state)
2. **Library git sync** model: does the library itself live inside a git repo the user owns, or are pieces tracked separately? (Lean: whole library is one git repo)
3. **Manifest granularity**: one manifest file per project, or one shared manifest with project sections? (Lean: per-project for parallelism)
4. **Bundled skill versioning**: do bundled skills travel with the manager binary, or are they pulled from a separate repo? (Lean: travel with the binary, but loadable from a custom path for community variants)
5. **LLM credential storage**: environment variables only, OS keychain, or encrypted local config? (Lean: environment variables first, keychain later)
6. **Generated metadata merge policy**: which files are source-authored vs derived, and which conflicts can be resolved deterministically?

See `ROADMAP.md` for sequencing of when these get resolved.
