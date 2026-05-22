# Compatibility

How skills declare which harnesses they work in, what they require at runtime,
and how the manager respects intent.

Compatibility has two layers:

1. **Harness support**: where the skill text can be installed and interpreted.
2. **Execution requirements**: what the skill assumes when it runs, including
   model/tool capability, local binaries, MCP servers, scripts, and credentials.

A skill can be harness-portable but not runtime-ready on a machine. The manager
must represent both facts.

## The three states

Every skill in the library is in one of three compatibility states:

| State | Declaration | What it means | Install behavior |
|---|---|---|---|
| **Portable** | No declaration (default) | Could work anywhere; compat checked at install | Install into every active harness; auto-detect any concerning patterns and warn |
| **Compatible** | `compatible: [harness, ...]` in frontmatter | Works in this specific list, possibly extendable later | Install only into listed harnesses; never extend without user action |
| **Exclusive** | `exclusive: <harness>` in frontmatter | Intentionally for one harness, by design | Install only into the declared harness; never appear in selection for others |

## Why three states (not two)

The naive design is just "portable" vs "incompatible." But that misses:

- Skills that **could** work elsewhere with porting, but aren't ported yet (Compatible covers this)
- Skills that are **deliberately** single-harness (gstack's plan-mode-aware skills, Cursor's glob-driven rules) — these aren't "incompatible," they're correct as they are

The three-state model makes user intent first-class. The manager respects it instead of inferring it. It is not the whole compatibility story; execution requirements below cover model and local runtime assumptions.

## Declaration syntax

In `SKILL.md` frontmatter:

### State 1: Portable (default — no declaration)

```yaml
---
name: pdf
description: PDF manipulation toolkit for extracting text and tables...
---
```

No `compatible:` or `exclusive:` field. The manager will install it everywhere and run compatibility detection.

### State 2: Compatible (explicit list)

```yaml
---
name: linear-feature
description: Full orchestrator for completing Linear issues
compatible: [claude, codex, grok]
---
```

Installs only in claude, codex, grok. The user can edit this list to expand or contract.

### State 3: Exclusive (single-harness by intent)

```yaml
---
name: qa
description: Systematically QA test a web application and fix bugs found
exclusive: claude
reason: "Uses AskUserQuestion and gstack preamble + Plan Mode references"
---
```

Installs only in claude. The `reason` field is human documentation; the manager doesn't parse it but surfaces it in `skills-manager show`.

## Sidecar storage

The skill's own `SKILL.md` is the source of truth for compatibility declarations. The manager also caches the parsed state in `.skill-meta.yaml` for fast queries:

```yaml
compatibility:
  declared:
    mode: exclusive            # portable | compatible | exclusive
    harness: claude            # only set for exclusive
    harnesses: ~               # only set for compatible
    reason: "Uses AskUserQuestion + gstack preamble"
  detected:
    claude:      {confidence: high}
    codex:       {confidence: low,  reasons: ["uses AskUserQuestion"]}
    grok:        {confidence: low,  reasons: ["uses AskUserQuestion"]}
    hermes:      {confidence: none, reasons: ["multiple Claude-only tools"]}
    antigravity: {confidence: medium, reasons: ["MCP UUID format"]}
  matrix_updated: 2026-05-22T14:30:00Z
```

The `declared` block always wins; `detected` is informational (shown in `skills-manager show`).

## Execution requirements

Harness support answers "where can this be installed?" Requirements answer
"will it work here?"

Requirements are stored in `.skill-meta.yaml` and may also be inferred from
skill content:

```yaml
requirements:
  model:
    tool_use: required              # required | optional | none
    min_context_tokens: 32000       # optional, advisory
    reasoning: medium               # none | low | medium | high, advisory
    notes: "Needs reliable tool-call planning across Linear and GitHub"
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
    allow_auto_run: false
    required_runtimes: [node]
  credentials:
    - name: github
      source: gh
      required: true
```

`skills-manager doctor` validates requirements on the current machine. Missing
required dependencies block auto-install by default and are shown in dry-run
output. Missing optional dependencies produce warnings.

The manager does not try to certify subjective model quality. Model fields are
coarse capability hints, not a promise that every model behind a harness will
follow the skill well.

## Auto-detection of incompatibility patterns

When a skill is added without a declaration, the manager scans the body for known harness-specific patterns and either:

- Auto-classifies as exclusive (high confidence)
- Asks the user (medium confidence)
- Leaves portable + adds notes (low confidence)

### Pattern library

| Pattern | Indicates harness | Confidence |
|---|---|---|
| `the Skill tool`, `Agent tool with subagent_type` | Claude | high |
| `AskUserQuestion`, `ExitPlanMode`, `Plan Mode` | Claude | high |
| `mcp__[hex-uuid]__*` (machine-local MCP) | Claude/Codex | medium (warn, not block) |
| `$ARGUMENTS` (slash command syntax) | Claude/Codex | low (varies) |
| Bash via `!` prefix | Claude | high |
| `AGENTS.md`, `agents.md` references | Codex/Cursor/Copilot | low (most generic) |
| `.cursor/rules/`, `globs:` frontmatter | Cursor | high |
| `~/.hermes/` references, `/memory` directives | Hermes | high |
| `~/.openclaw/` references | OpenClaw | high |
| Claude Code hook configs (PreToolUse, PostToolUse) | Claude | high |
| Cursor `globs:` | Cursor | high |

Pattern matchers live in the manager repo (`detectors/compatibility/*.yaml`) and are PR-friendly.

Requirement detectors live alongside them (`detectors/requirements/*.yaml`).
Examples:

| Pattern | Requirement inferred | Confidence |
|---|---|---|
| `gh pr`, `gh api`, `GitHub CLI` | tool: `gh` | high |
| `rg `, `ripgrep` | tool: `rg` | medium |
| `ffmpeg` | tool: `ffmpeg` | high |
| `mcp__linear__`, `Linear connector` | MCP server: `linear` | high |
| `python-docx`, `openpyxl` | runtime/library note | medium |
| "use the browser tool" | model/tool_use: required | medium |

### Auto-classification rule

```
if patterns_for(harness_X).confidence == high AND no patterns for other harnesses:
    suggest exclusive: harness_X
elif multiple harness-specific patterns detected:
    suggest compatible: [<list of harnesses with at-least-medium signal>]
elif weak patterns only:
    leave portable, log notes
```

The user can always override.

## Behavior matrix

| Skill state | Active project harnesses | What happens at install |
|---|---|---|
| `portable` | claude, codex, grok | Install in all 3; run compat detection in background |
| `portable` (with detected Claude-only patterns) | claude, codex | Install in claude; warn for codex with option to port |
| `compatible: [claude, codex]` | claude, codex, grok | Install in claude + codex; grok skipped silently |
| `compatible: [claude, codex]` | claude only | Install in claude |
| `exclusive: claude` | claude, codex, grok | Install in claude only; codex/grok don't even see this skill in selection |
| `exclusive: claude` | codex, grok (no claude) | Skill doesn't apply; not surfaced in matches |

## Project-side filtering

The matcher (see `TAXONOMY.md`) respects compatibility:

```python
def matches(skill, project):
    if not (skill.categories & project.categories):
        return False

    if skill.compatibility.mode == "exclusive":
        if skill.compatibility.harness not in project.harnesses:
            return False
    elif skill.compatibility.mode == "compatible":
        if not (set(skill.compatibility.harnesses) & project.harnesses):
            return False
    # portable: passes through

    if not requirements_available(skill.requirements, project.machine):
        return "warning"  # surfaced in dry-run; blocks only required missing deps

    return True
```

A project with only `codex` won't surface `exclusive: claude` skills at all. The user doesn't see them. No "incompatibility warning noise."

Requirement warnings are different from harness compatibility. A `compatible:
[claude, codex]` skill can still warn that `gh` is missing or the Linear MCP
server is not configured on this machine.

## Changing compatibility intent

Users can change a skill's compatibility via CLI:

```
skills-manager set qa --compatibility exclusive --harness claude
skills-manager set qa --compatibility compatible --harnesses claude,codex
skills-manager set qa --compatibility portable
```

This rewrites the skill's `SKILL.md` frontmatter AND updates `.skill-meta.yaml`. The change is committed to the library on next sync.

Side effects:

- Going **more restrictive** (portable → exclusive): triggers uninstall from harnesses that no longer apply (with confirmation)
- Going **more permissive** (exclusive → portable): triggers compatibility detection, may install in new harnesses on next `install`/`sync`

## Variants — porting without losing the original

When a skill is portable-with-caveats, the manager can store **variants**: a ported version per target harness, kept alongside the canonical.

Layout:

```
~/.skills-manager/library/code-reviewer/
├── SKILL.md                    # canonical (original Hermes version)
├── SKILL.claude.md             # ported for claude
├── SKILL.antigravity.md        # ported for antigravity
├── .skill-meta.yaml
└── .variants.yaml
```

`.variants.yaml`:

```yaml
version: 1
default: SKILL.md
overrides:
  claude: SKILL.claude.md
  antigravity: SKILL.antigravity.md
canonical_fingerprint: a7f3...
last_ported: 2026-05-22T14:30:00Z
ported_by: skills-port
```

At install time, the manager picks the right file:

- claude harness → `SKILL.claude.md`
- antigravity harness → `SKILL.antigravity.md`
- everyone else → `SKILL.md` (canonical)

## Generating variants

The bundled `skills-port` skill (see `BUNDLED_SKILLS.md`) generates variants. The user invokes it via their agent:

```
$ skills-manager port code-reviewer --to claude,antigravity

# Manager generates prompt + context.
# Preferred: configured local LLM provider runs skills-port.
# Fallback: user pastes into Claude/Codex/whatever and runs the bundled skill.
# The model rewrites the skill for each target harness.
# Manager saves output as SKILL.claude.md, SKILL.antigravity.md.
```

## Drift between canonical and variants

When the canonical SKILL.md updates upstream, its variants are now potentially stale. The manager detects this via fingerprint comparison and flags it:

```
$ skills-manager doctor

⚠ code-reviewer: canonical SKILL.md changed (fingerprint a7f3 → b2e1)
   Variants may be stale:
     SKILL.claude.md      (last ported 2026-05-22)
     SKILL.antigravity.md (last ported 2026-05-22)
   
   To refresh: skills-manager port code-reviewer --to claude,antigravity
```

Stale variants still install (better than nothing) but show as drift in the dashboard.

## Things the compatibility system does NOT do

- Does not execute skill content to check compatibility (pure static analysis)
- Does not call any LLM for detection unless the user explicitly invokes `skills-compat-check` through a configured provider or handoff fallback
- Does not block installation of declared-compatible skills (respects user's declaration even if patterns suggest otherwise)
- Does not auto-port without user invocation (porting requires their agent to run)
- Does not guarantee that a specific model will follow a skill well; it records coarse capability requirements only
- Does not pretend to handle every edge case — it's heuristic, surfaces uncertainty, and respects user override

## Reference card

Add this to your skill's frontmatter:

```yaml
# Works anywhere — the default
# (no declaration)

# Works in specific harnesses
compatible: [claude, codex, grok]

# Built for one harness on purpose
exclusive: claude
reason: "Uses Plan Mode + AskUserQuestion"

# Runtime assumptions
requirements:
  model:
    tool_use: required
  tools:
    - name: gh
      required: true
  mcp_servers:
    - name: linear
      required: true
```

That's the core user-facing surface. The manager can infer parts of it, but
users and skill authors can override it when the heuristics are wrong.
