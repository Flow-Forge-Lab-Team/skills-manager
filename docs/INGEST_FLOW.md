# Ingest Flow

How skills enter the library. Five sources, three triggers, one validation pipeline.

## The five sources

| Source | Trigger | Examples |
|---|---|---|
| **Marketplace** | `skills-manager add <marketplace>/<skill>` | Claude Code marketplace, agentskills.io |
| **GitHub** | `skills-manager add <github-url>` | Any public repo with SKILL.md at the root |
| **Local path** | `skills-manager add ./path/to/skill` | A skill folder you have on disk |
| **Hand-authored** | `skills-manager new <name>` | You wrote it from scratch in the manager |
| **AI-authored** | Watcher detects skill in `~/.<harness>/skills/` | Hermes auto-improve, Claude wrote it inline |

Each source produces the same end state — a skill in `~/.skills-manager/library/` with origin metadata.

## The ingest triggers

1. **Explicit** — user runs `skills-manager add <source>`
2. **Detected** — `skills-manager scan` finds a SKILL.md the manager hasn't seen
3. **Suggested** — during another operation (e.g., user installs from a project that references unregistered skills)
4. **Watched** — optional v0.3 watcher finds a new SKILL.md after scan-based ingest has proven insufficient

## Ingest pipeline

Every source goes through the same pipeline:

```
┌─────────────────────────────────────────────────────────────┐
│  1. Fetch & validate                                         │
│     • Pull the skill content (clone / copy / download)       │
│     • Verify SKILL.md exists and parses                      │
│     • Compute SHA-256 fingerprint                            │
├─────────────────────────────────────────────────────────────┤
│  2. Origin metadata                                          │
│     • Record source type, URL, version/commit, install time │
│     • Generate .skill-meta.yaml                              │
├─────────────────────────────────────────────────────────────┤
│  3. Categorization                                           │
│     • Pattern-based: derive from name + description          │
│     • LLM-based (optional): invoke skills-ingest via provider│
│     • Confidence levels: high / medium / low                 │
├─────────────────────────────────────────────────────────────┤
│  4. Compatibility analysis                                   │
│     • Scan body for harness-specific patterns                │
│     • Suggest portable / compatible / exclusive              │
│     • Prompt user for intent if uncertain                    │
├─────────────────────────────────────────────────────────────┤
│  5. User confirmation                                        │
│     • Show proposed categories, tags, compatibility          │
│     • User accepts / edits / rejects                         │
├─────────────────────────────────────────────────────────────┤
│  6. Commit                                                   │
│     • Copy to library                                        │
│     • Update catalog.yaml                                    │
│     • Update state.db                                        │
│     • Offer auto-install in matching projects                │
└─────────────────────────────────────────────────────────────┘
```

Steps 3, 4, 5 can be skipped with `--auto` only when confidence is high and
required dependency checks are available. v1 does not schedule auto-ingest by
default; capture should not outrun review.

## Source-specific behavior

### Marketplace ingest

```
$ skills-manager add anthropic-skills/pdf

Fetching from Claude Code marketplace...
  ✓ Located at ~/.claude/plugins/marketplaces/anthropic-agent-skills/skills/pdf/
  ✓ Read SKILL.md (7068 bytes)
  ✓ Fingerprint: a7f3...

Origin recorded:
  source: marketplace
  marketplace: anthropic-agent-skills
  path: skills/pdf
  version: 1.4.2
  commit: abc123  (from marketplace git repo)

Suggested categories: [Documents]
Suggested tags: [pypdf]
Suggested compatibility: portable

Confirm? [Y/n]
```

Marketplace versioning comes from the marketplace's git tags or version file. Updates are detected by re-polling.

### GitHub ingest

```
$ skills-manager add github.com/user/some-skill

Cloning github.com/user/some-skill...
  ✓ SKILL.md found at root
  ✓ Commit: def456789
  ✓ Fingerprint: b2e1...

Origin recorded:
  source: github
  url: https://github.com/user/some-skill
  commit: def456789
  installed_at: 2026-05-22T14:30:00Z

[suggested categorization + compatibility flow as above]
```

For subdirectory skills, supports `github.com/org/repo/path/to/skill` syntax.

### Local path

```
$ skills-manager add ./my-skill/

Importing from /Users/greg/dev/my-skill/...
  ✓ SKILL.md valid
  ✓ Fingerprint: c4f9...

Origin recorded:
  source: local
  path: /Users/greg/dev/my-skill/
  imported_at: 2026-05-22T14:30:00Z
  (no upstream tracking — local-only)

[continues...]
```

No update tracking — local sources have no upstream by definition.

### Hand-authored

```
$ skills-manager new my-custom-skill

Opens $EDITOR with the SKILL.md template:
---
name: my-custom-skill
description: TODO
---

# TODO

Saves to ~/.skills-manager/library/my-custom-skill/SKILL.md
Origin: authored locally
```

The manager generates a template scaffolded with the user's name/email if available.

### AI-authored (detected skill case)

```
(scan or watcher finds a new skill)

━━━ New skill detected ━━━
Path: ~/.hermes/skills/code-reviewer/SKILL.md
Created: 12 minutes ago
Likely: Hermes auto-improve session (no .git, recent timestamp)

Auto-ingest? [Y]es / [N]o / [S]kip-this-time / [I]gnore-forever
```

If the user enabled `auto_ingest: true` in config, the prompt is skipped only
for high-confidence cases. The accepted skill still appears in the activity feed
for review.

## Filesystem watcher

Optional background daemon, shipped in v0.3 (`skills-manager watch`). v0.1/v0.2
rely on `skills-manager scan` so ingest is reviewable and does not require a
resident process; the watcher is opt-in and, by default, only writes review
notifications rather than changing the catalog.

It is implemented with interval **polling** (dependency-free) rather than an OS
file-event API, so it behaves identically across platforms with no external
dependencies. Run it in the foreground (`skills-manager watch`), as a detached
daemon (`watch --daemon` / `watch --stop`), and tune the cadence with
`--interval <secs>`. `--auto-ingest` is opt-in and only ingests high-confidence,
non-suspicious, unregistered skills.

### What it watches

By default, all known harness skill paths on the machine:

```
~/.claude/skills/
~/.codex/skills/
~/.grok/skills/
~/.hermes/skills/
~/.openclaw/skills/
~/.gemini/antigravity/skills/
```

Plus, on a per-project basis (when registered), the project's local skill paths:

```
<project>/.claude/skills/
<project>/.codex/skills/
<project>/.agents/skills/
<project>/skills/
```

### What triggers a candidate

A `SKILL.md` file appearing or being modified, where the file's containing
directory is NOT in the manager's manifest (i.e., we didn't put it there).

Specifically:

- Newly-appeared SKILL.md in a watched directory → ingest candidate
- Modified SKILL.md that the manager created → drift warning, not ingest
- Modified SKILL.md that we didn't create → "user edit" event, not ingest
- Non-`SKILL.md` memory/cache files → ignored
- Duplicate events for the same fingerprint → collapsed into one notification

### How notifications surface

The watcher writes to `~/.skills-manager/notifications/` and the manager's `status` shows them:

```
$ skills-manager status

Library:           251 skills
Pending updates:   4
Unregistered:      2 detected   ⚠ run `skills-manager scan`
Last sync:         2h ago
```

The web UI's Overview page shows them as activity feed entries with [Ingest] [Skip] [Dismiss] buttons.

## `skills-manager scan`

Non-daemon alternative to the watcher. Walks every known location once, reports.

```
$ skills-manager scan

Scanning 7 directories...

Found 47 skills total
  43 already in library
  4 unregistered:
    • email-summarizer       ~/.claude/skills/        ai-authored (likely)
    • code-reviewer          ~/.hermes/skills/        ai-authored (likely)
    • style-guide            ~/dev/acme/.claude/      hand-authored
    • debug-helper           ~/.openclaw/skills/      unknown source

Ingest now?  [a] all  [s] select  [n] none
```

Useful for first-time setup and for users who don't want a background process.

## How "AI-authored" vs "hand-authored" is guessed

Heuristics (none authoritative — these are just hints):

- **AI-authored signals**: very recent timestamp (created within the last hour during an active agent session); SKILL.md structure suspiciously well-formed; description-then-overview pattern of Claude-generated content
- **Hand-authored signals**: file in a git repo with history; timestamp older than 24h; manual editing patterns (typos, irregular structure)
- **Unknown**: skill predates the watcher; no other signals

The classification appears in `.skill-meta.yaml` as `origin.guess: ai-authored | hand-authored | unknown`. The user can correct it manually.

## Ingest UX detail

The interactive ingest flow:

```
━━━ Ingesting: code-reviewer ━━━

Source: filesystem watcher
Path:   ~/.hermes/skills/code-reviewer/
Body:   2,847 bytes
Fingerprint: a7f3...d8c2

Looking at content...

  Detected patterns:
    • Hermes /memory directive
    • References to ~/.hermes/ paths
    • Code review workflow (10 mentions of "review", "audit", "diff")

  Categorization:
    Categories: [Quality]                  ← confidence: high
    Tags: [hermes-authored, code-review]   ← confidence: high

  Compatibility:
    Suggested: exclusive: hermes           ← confidence: high

  Description:
    "Reviews staged changes for style, logic, and security issues..."
    (preserves as-is — changing description shifts activation)

━━━ How should we ingest this? ━━━

  [a] Accept all suggestions
  [c] Customize before ingest
  [r] Mark as PORTABLE instead (offer porting to other harnesses)
  [l] Mark as COMPATIBLE with multiple harnesses
  [s] Skip for now
  [i] Ignore forever (don't ask again for this path)

>
```

## Auto-ingest mode (advanced)

```
$ skills-manager scan --auto-ingest
```

Or, in v0.3, watcher with `auto_ingest: true`:

```yaml
# ~/.skills-manager/config.yaml
watcher:
  enabled: true
  auto_ingest: true
  auto_ingest_rules:
    high_confidence_only: true     # only auto-accept categorization at high confidence
    exclude_paths:                 # never auto-ingest from these
      - ~/dev/scratch/             # personal experiments
```

Skills accepted via auto-ingest still appear in the activity feed so the user
can review post-hoc. Low-confidence categorization, unknown compatibility,
missing required dependencies, and suspicious instructions disable auto-ingest.

## What happens after ingest

1. Skill is in library
2. Manager runs `skills-manager match --explain` against all registered projects
3. For each project where the new skill would match:
   - Offer install (interactive) OR
   - Auto-install if project config says `auto_apply_new_matches: true`
4. The skill becomes available in the user's library indefinitely

## Failure modes and what happens

| Failure | Behavior |
|---|---|
| `SKILL.md` malformed (no frontmatter) | Refuse ingest, report error |
| Skill name conflicts with existing library entry | Prompt: skip / replace / rename / merge |
| Same fingerprint as existing (re-ingest) | Skip silently (we already have it) |
| Network failure during marketplace fetch | Retry once, then save state and report |
| Categorization confidence too low | Mark categories as `[]`, ingest anyway, surface for user review |

Ingest is intentionally lenient — better to capture a skill with incomplete metadata than to lose it.

## What ingest never does

- Never executes scripts bundled with the skill
- Never modifies the skill's content during ingest
- Never auto-changes the description (which controls activation)
- Never auto-ingests from paths the user marked `ignore`
- Never silently fails — every error surfaces in `status` and logs
