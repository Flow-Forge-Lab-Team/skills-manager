# Data Model

All formats. Source of truth for schemas across the CLI, state, and config.

## On-disk layout

```
~/.skills-manager/                  # global state (per machine)
├── library/                        # canonical store (a git repo)
│   ├── .git/
│   ├── catalog.yaml                # registry of all skills
│   ├── <skill-name>/
│   │   ├── SKILL.md                # canonical skill file
│   │   ├── .skill-meta.yaml        # origin, fingerprint, compat
│   │   ├── .variants.yaml          # if it has ported variants
│   │   ├── SKILL.<harness>.md      # variant files (optional)
│   │   ├── scripts/                # bundled scripts (optional)
│   │   ├── references/             # bundled refs (optional)
│   │   └── ...
│   └── ...
├── state.db                        # SQLite: usage, history, derived state
├── manifests/                      # per-project install manifests
│   ├── <project-slug>.json
│   └── ...
├── logs/                           # logs from scheduled checks
│   └── check-2026-05-22T09:00:00.log
├── summaries/                      # AI summary cache
│   └── <skill>-v1.4.2-to-v1.5.0.md
├── notifications/                  # output from scheduled tasks
├── backups/                        # backups of replaced/removed skills
└── config.yaml                     # user config

<project>/                          # per-project (committed to repo)
├── .skills/
│   ├── project.yaml                # project categories + tags
│   ├── installed.lock              # reproducible install
│   └── skills/                     # canonical content copies (gitignored)
└── <harness paths>/skills/         # populated by manager (typically gitignored)
```

## File schemas

### `catalog.yaml`

The library's index of every skill. Human-readable, edited rarely; the CLI keeps it in sync with the directory.

```yaml
version: 1
skills:
  - name: shadcn-ui
    categories: [Design, Engineering]
    tags: [shadcn, tailwind, react]
    compatibility:
      mode: portable
    requirements:
      model: {tool_use: optional}
      tools: []
      mcp_servers: []
      credentials: []
    summary: "Build UIs with shadcn/ui v4 components"
  - name: qa
    categories: [Engineering, Quality]
    tags: [gstack, slash-command]
    compatibility:
      mode: exclusive
      harness: claude
      reason: "Uses AskUserQuestion + gstack preamble"
    requirements:
      model: {tool_use: required}
      tools: [rg, git]
      mcp_servers: []
      credentials: []
    summary: "Systematically QA test a web application and fix bugs found"
  - name: linear-feature
    categories: [Engineering, Operations]
    tags: [linear, github, multi-agent]
    compatibility:
      mode: compatible
      harnesses: [claude, codex, grok]
    requirements:
      model: {tool_use: required}
      tools: [gh, git]
      mcp_servers: [linear]
      credentials: [github]
    summary: "Full orchestrator for completing Linear issues"
```

### `.skill-meta.yaml` (per-skill sidecar)

Lives next to each skill's `SKILL.md`. Tracks where the skill came from and its current state.

```yaml
version: 1
origin:
  type: marketplace                 # marketplace | github | url | local | authored | ai-authored
  source: anthropic-agent-skills
  path: skills/pdf
  url: https://github.com/anthropics/skills
  version: "1.4.2"
  commit: abc123def456              # for git sources
  installed_at: 2026-05-15T10:30:00Z
  installed_by: skills-manager 0.1.0

fingerprint:
  sha256: a7f3...                   # of SKILL.md at install time
  size: 7068

categorization:
  source: llm                       # llm | manual | imported | default
  categorized_at: 2026-05-15T10:30:05Z
  by_skill: skills-ingest           # which bundled skill produced this
  confidence: high

local_changes: false                # true if user edited SKILL.md after install
last_changed_at: ~

variants_index: .variants.yaml      # present if this skill has ported variants

requirements:
  model:
    tool_use: required              # required | optional | none
    min_context_tokens: ~           # optional, advisory
    notes: "Needs reliable tool-call planning for PR workflows"
  tools:
    - name: gh
      required: true
      check: "gh auth status"
    - name: rg
      required: true
      check: "rg --version"
  mcp_servers:
    - name: linear
      required: true
      config_hint: "Install the Linear connector/app before use"
  scripts:
    allow_auto_run: false           # manager never auto-runs skill scripts
    required_runtimes: [node]
  credentials:
    - name: github
      source: gh
      required: true
```

### `.variants.yaml` (optional, per-skill)

Only present for skills that have harness-specific variants beyond the canonical.

```yaml
version: 1
default: SKILL.md
overrides:
  cursor: SKILL.cursor.mdc
  antigravity: SKILL.antigravity.md
generated_from: SKILL.md
generated_by: skills-port
last_synced: 2026-05-18T14:22:00Z
```

### `project.yaml` (per-project, committed)

The user-facing config in each project. Authored at `init`, edited as needed.

```yaml
version: 1
name: my-saas-app
created: 2026-05-15
last_synced: 2026-05-22T14:30:00Z

categories:
  - Engineering
  - Design
  - Quality
  - Operations

tags:
  - react
  - nextjs
  - shadcn
  - supabase
  - stripe
  - sentry

# Active harnesses (auto-detected; user can override)
harnesses:
  - claude
  - codex
  - grok

# Per-project overrides
skills:
  always_include:
    - linear-feature        # force-include even if auto-match would skip
    - acme-style-guide
  never_include:
    - autoplan              # never auto-install here even on match
  pinned_versions:
    pdf: "1.4.2"            # don't auto-update past this
```

### `installed.lock` (per-project, committed)

Reproducibility lockfile. Lists exactly which skills + versions are installed.

```yaml
version: 1
generated_at: 2026-05-22T14:30:00Z
generated_by: skills-manager 0.1.0
skills:
  - name: shadcn-ui
    version: "0.8.0"
    commit: ~
    fingerprint: a7f3...
    installed_at: 2026-05-15T10:30:00Z
    harnesses: [claude, codex, grok]   # which harness dirs got populated
  - name: qa
    version: "v2.3.4"
    commit: abc123
    fingerprint: b2e1...
    installed_at: 2026-05-15T10:30:00Z
    harnesses: [claude]                # exclusive to claude
  - name: linear-feature
    version: ~
    commit: def456
    fingerprint: c4f9...
    installed_at: 2026-05-15T10:30:00Z
    harnesses: [claude, codex, grok]
```

A teammate running `skills-manager install` against this lockfile gets the exact same skills.

### Per-project manifest (`~/.skills-manager/manifests/<slug>.json`)

Internal tracking of what the manager has populated. Used for clean uninstall.

```json
{
  "version": 1,
  "project_path": "/Users/greg/dev/my-saas-app",
  "project_slug": "my-saas-app",
  "installed_at": "2026-05-15T10:30:00Z",
  "managed_paths": [
    ".claude/skills/shadcn-ui",
    ".claude/skills/qa",
    ".claude/skills/linear-feature",
    ".codex/skills/shadcn-ui",
    ".codex/skills/linear-feature",
    ".grok/skills/shadcn-ui",
    ".grok/skills/linear-feature",
    ".skills/skills/shadcn-ui",
    ".skills/skills/qa",
    ".skills/skills/linear-feature",
    ".skills/project.yaml",
    ".skills/installed.lock"
  ],
  "preserved_paths": [
    ".claude/skills/my-custom-rule"
  ]
}
```

`managed_paths` is what the manager will remove on uninstall. `preserved_paths` is what we found there but didn't touch. Surgical reversal.

### `config.yaml` (global user config)

```yaml
version: 1

# Install mode
mode: copy                          # copy | symlink

# Safety
preserve_existing: true             # never overwrite files we didn't create

# Library sync
library_sync:
  enabled: true
  remote: github.com:greg/skills-store
  branch: main
  auto_pull: false                  # require manual sync

# Update checking
update_check:
  frequency: weekly                 # daily | weekly | manual
  on_command: true                  # nudge if stale when running other commands

# LLM (optional — only for auto-summary)
llm:
  provider: ~                       # null | anthropic | openai
  api_key: ~                        # prefer $ANTHROPIC_API_KEY / $OPENAI_API_KEY or keychain
  model: ~

# Filesystem watcher
watcher:
  enabled: false                    # opt-in
  paths:                            # auto-populated from active harnesses
    - ~/.claude/skills
    - ~/.codex/skills
    - ~/.grok/skills
    - ~/.hermes/skills
    - ~/.openclaw/skills
    - ~/.gemini/antigravity/skills
```

## SQLite schema

`state.db` is for derived/queryable state.

```sql
-- Catalog mirror of catalog.yaml (kept in sync)
CREATE TABLE skills (
  name TEXT PRIMARY KEY,
  summary TEXT,
  categories JSON,                  -- ["Engineering","Quality"]
  tags JSON,
  compatibility_mode TEXT,          -- portable | compatible | exclusive
  compatibility_data JSON,
  requirements JSON,
  origin JSON,
  fingerprint TEXT,
  added_at TEXT,
  updated_at TEXT
);

-- One row per project the manager has touched
CREATE TABLE projects (
  slug TEXT PRIMARY KEY,
  path TEXT UNIQUE,
  name TEXT,
  categories JSON,
  tags JSON,
  harnesses JSON,
  last_synced TEXT
);

-- Which skills are installed where (denormalized for query speed)
CREATE TABLE installs (
  skill_name TEXT,
  project_slug TEXT,
  version TEXT,
  harnesses JSON,                   -- ["claude","codex"]
  installed_at TEXT,
  PRIMARY KEY (skill_name, project_slug)
);

-- Pending updates discovered by polling
CREATE TABLE updates (
  skill_name TEXT PRIMARY KEY,
  from_version TEXT,
  to_version TEXT,
  source TEXT,
  detected_at TEXT,
  summary_status TEXT,              -- pending | generated | failed
  summary_path TEXT,                -- path under summaries/
  status TEXT                       -- pending | accepted | rejected | pinned
);

-- Usage telemetry (from Claude Code OTEL, hooks, watchers)
CREATE TABLE invocations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  skill_name TEXT,
  project_slug TEXT,
  harness TEXT,
  trigger TEXT,                     -- user-initiated | proactive | nested
  invoked_at TEXT,
  source TEXT                       -- otel | hook | watcher | manual
);
CREATE INDEX idx_invocations_skill_date
  ON invocations (skill_name, invoked_at);

-- Drift detection: skills detected on disk but not in library
CREATE TABLE detected (
  path TEXT PRIMARY KEY,
  skill_name TEXT,
  detected_at TEXT,
  source_guess TEXT,                -- ai-authored | hand-authored | unknown
  action TEXT                       -- pending | ingested | skipped | ignored
);

-- Requirement check results are machine-local and can be rebuilt.
CREATE TABLE requirement_checks (
  skill_name TEXT,
  requirement_type TEXT,            -- tool | mcp | credential | model | script-runtime
  requirement_name TEXT,
  status TEXT,                      -- ok | missing | unknown | skipped
  checked_at TEXT,
  detail TEXT,
  PRIMARY KEY (skill_name, requirement_type, requirement_name)
);

-- Migration / schema versioning
CREATE TABLE schema_version (
  version INTEGER PRIMARY KEY,
  applied_at TEXT
);
```

## Identifier conventions

- **Skill name**: lowercase-with-dashes, matches the directory name and the `name:` in frontmatter
- **Project slug**: lowercase-with-dashes derived from the project's directory name; collisions across machines handled by full path mapping
- **Harness names**: `claude` `codex` `grok` `antigravity` `gemini` `hermes` `openclaw` `cursor` `copilot`
- **Categories**: `Engineering` `Quality` `Operations` `Data` `Design` `Documents` `Writing` `Business` `Productivity` `Agent-tooling` (capitalized, no plural)
- **Tags**: lowercase, dash-separated (e.g., `slash-command`, `multi-agent`, `session-mode`)

## What's not in the data model

To keep scope:

- **User accounts / authentication** — local-only, no users in the model
- **Team / org structures** — not in v1
- **Skill ratings, reviews, stars** — out of scope (we're not a marketplace)
- **Marketplace metadata caching** — we hit marketplaces live; we don't mirror their catalogs

## State ownership and rebuild rules

The hybrid model is intentional, but ownership must be strict:

| Data | Owner | Rebuild behavior |
|---|---|---|
| `SKILL.md` | User/library source | Never derived; do not overwrite without update acceptance |
| `.skill-meta.yaml` | Library source | Synced; conflict requires explicit resolution |
| `.variants.yaml` + variant files | Library source | Synced; stale variants are flagged, not regenerated automatically |
| `catalog.yaml` | Derived index in the library repo | Rebuildable from `SKILL.md` + sidecars; conflicts should prefer deterministic regeneration |
| `state.db` | Machine-local cache | Always rebuildable from library + manifests + local scans |
| `manifests/*.json` | Machine-local install ownership | Not shared; each machine owns only files it created in its own checkout |
| `.skills/installed.lock` | Project repo | Shared requested skill set; install fails clearly if the local library lacks a locked skill |

`skills-manager doctor --rebuild-state` drops and recreates derived SQLite rows
from the library and local manifests. `skills-manager doctor --rebuild-catalog`
regenerates `catalog.yaml` from sidecars. Generated files should be regenerated,
not hand-merged, whenever possible.

## Migrations and versioning

- Every YAML/JSON file has a `version:` field at the top
- The CLI checks file versions and migrates on read if needed (writes always at current version)
- SQLite schema version stored in `schema_version` table; migrations run automatically on first command of a new manager version
- Migration code is part of the CLI binary; no manual migration steps required

## Trust boundaries

- **The library is trusted.** Files in `~/.skills-manager/library/` are content the user has accepted.
- **External sources are not.** GitHub repos, marketplaces, etc. are scanned for known patterns but never executed.
- **Bundled scripts** in a skill's `scripts/` directory are user-runnable but **never auto-run** by the manager. Skills that bundle scripts are documented as such, and the user invokes them through their agent.
- **The manager never executes arbitrary code** from skill content. It only reads, copies, and symlinks.
- **Skill text is still executable instruction once installed.** Update review must treat changed prompts, frontmatter, activation descriptions, tool guidance, and embedded instructions as security-sensitive, even if no local code is executed by the manager.
- **AI summaries are untrusted interpretations.** Raw diffs and deterministic safety flags are the review source of truth; summaries are advisory and may be wrong or prompt-injected.
