# Bundled Skills

The manager ships skills that handle its own LLM-heavy operations. These skills
can run through an explicitly configured local LLM provider, or through a
manual agent handoff fallback.

## Why bundled skills exist

Two architectural pressures point to this design:

1. **The manager should not depend on one model provider.** Users choose the provider and credentials; the manager validates output.
2. **Clipboard handoff should not be the daily path.** It remains useful as a fallback, but configured local providers are the lower-friction route.

The pattern: the manager generates a prompt + context, runs the bundled skill
through a configured provider or writes a handoff file, then validates and
stores the result.

The bundled skills ARE the manager's intelligence layer. They live in the manager's git repo as plain markdown files. Community can improve them via PR without rebuilding code.

## The five bundled skills (v0.2)

| Skill | When invoked | What it produces |
|---|---|---|
| `skills-ingest` | New skill enters the library | Categories, tags, compatibility, requirements |
| `skills-compat-check` | User asks to verify compatibility | Harness + requirement assessment |
| `skills-diff-summary` | Update available, user wants AI summary | Plain-English diff explanation + hostile-instruction notes |
| `skills-port` (v1.0) | User asks to port a skill | Cross-harness rewritten variant |
| `skills-author` (v1.0) | User wants to write a new skill | Step-by-step authoring guide |

Each has its own SKILL.md in `~/.skills-manager/bundled-skills/<name>/`.

## How they're installed

Bundled skills are manager internals first. They are embedded in the manager
binary and invoked directly by the CLI when a local provider is configured.

They are installed into user harnesses only if the user enables agent handoff
support:

```
~/.skills-manager/bundled-skills/skills-ingest/
                                    └── SKILL.md

→ symlink/copy to →

~/.claude/skills/skills-ingest/SKILL.md
~/.codex/skills/skills-ingest/SKILL.md
~/.grok/skills/skills-ingest/SKILL.md
(etc., for whichever harnesses are active)
```

When installed into a harness, they must be namespaced and hidden from normal
activation as much as the harness allows:

- name prefix: `skills-manager-*`
- category/tag: `manager-internal`
- descriptions that activate only for explicit skills-manager maintenance tasks
- filtered from normal `skills-manager list` output

The goal is to avoid polluting the user's ordinary agent context or accidentally
triggering manager helper skills during normal coding.

The bundled skills are tagged with `bundled-skill` and `manager-internal` so
they don't pollute the user's library views.

## Invocation patterns

### Pattern 1: User-initiated (via slash command)

The user types `/skills-port qa --to codex,grok` in their agent. The skill activates, reads its instructions, asks the user (or finds in the agent's context) which skill to port and to which harnesses, produces the output.

### Pattern 2: Manager-initiated (via configured provider)

The CLI generates a prompt + context and calls the configured provider:

```
$ skills-manager summarize pdf --auto

✓ Prepared skills-diff-summary invocation.
✓ Calling configured provider.
✓ Output validated against schema.
✓ Saved summary to ~/.skills-manager/summaries/pdf-v1.4.2-to-v1.5.0.md
```

### Pattern 3: Manager-initiated (handoff fallback)

If the user has no provider configured, the CLI writes a prompt file:

```
$ skills-manager summarize pdf --handoff

✓ Wrote prompt to /tmp/skills-manager/pdf-summary-prompt.md
  Run it in your agent, then import with:
  skills-manager summarize pdf --from <agent-output.md>
```

## Bundled skill structure

Each bundled skill follows the standard SKILL.md format with a few conventions:

```yaml
---
name: skills-ingest
description: Use when a new skill needs to be categorized and tagged for the
  skills-manager library. Takes a SKILL.md path and produces structured JSON
  with category, tags, compatibility, and requirement suggestions.
compatible: [claude, codex, grok, antigravity, gemini, hermes, openclaw]
output_format: json-strict        # manager validates against schema
output_schema: schemas/ingest-output.json
---

# skills-ingest

You are categorizing a developer skill for the skills-manager library.

## Inputs

The user (or skills-manager) will provide a SKILL.md file. You analyze it
and produce a structured categorization.

## Your output

Return ONLY a JSON object matching this schema:

{
  "categories": ["Engineering", "Quality"],    // 1-3 from the fixed list
  "tags": ["python", "testing"],               // open list, see guidelines
  "compatibility": {
    "mode": "portable" | "compatible" | "exclusive",
    "harnesses": ["claude", "codex"],          // only for compatible
    "harness": "claude",                       // only for exclusive
    "reason": "..."                            // optional for exclusive
  },
  "requirements": {
    "model": {"tool_use": "required" | "optional" | "none"},
    "tools": [{"name": "gh", "required": true}],
    "mcp_servers": [{"name": "linear", "required": true}],
    "credentials": [{"name": "github", "source": "gh", "required": true}]
  },
  "confidence": {
    "categories": "high" | "medium" | "low",
    "tags": "high" | "medium" | "low",
    "compatibility": "high" | "medium" | "low",
    "requirements": "high" | "medium" | "low"
  },
  "notes": ["any observations the user should see"]
}

## Categories — the fixed list

[lists the 10 categories from TAXONOMY.md with descriptions]

## Tags — guidelines

[lists tag conventions from TAXONOMY.md]

## Compatibility and requirements — pattern matching

[lists patterns from COMPATIBILITY.md]

## Output requirements

- JSON only, no commentary
- Valid against the schema (the manager will reject and retry if invalid)
- Confidence honest — `low` is fine; better than guessing high
```

## Each bundled skill in detail

### `skills-ingest`

Input: a SKILL.md path
Output: categorization JSON (above schema)
When invoked: any new skill enters the library; updates that change description significantly

### `skills-compat-check`

Input: a SKILL.md path + list of target harnesses
Output: per-harness compatibility and execution-requirement assessment with confidence + reasons

```json
{
  "skill": "qa",
  "assessments": {
    "claude":      {"compatible": true,  "confidence": "high",   "notes": ["native — uses Plan Mode"]},
    "codex":       {"compatible": false, "confidence": "high",   "notes": ["AskUserQuestion is Claude-only"]},
    "grok":        {"compatible": false, "confidence": "medium", "notes": ["AskUserQuestion + slash-command format"]},
    "hermes":      {"compatible": false, "confidence": "high",   "notes": ["multiple Claude-only tools"]}
  },
  "recommendation": "exclusive: claude",
  "requirements": {
    "tools": [{"name": "rg", "required": true}],
    "mcp_servers": [],
    "model": {"tool_use": "required"}
  }
}
```

### `skills-diff-summary`

Input: two versions of a SKILL.md (before/after diff)
Output: structured summary

```markdown
# <skill-name> <from> → <to>

## What changed
- bullet
- bullet

## Impact assessment
- Breaking changes: none | yes (details)
- Description changed: yes | no
- Compatibility changed: yes | no
- Requirements changed: yes | no
- Safety flags: [flag, ...]
- Hostile review instructions: none | yes (quote short excerpt)
- Body additions: ~N lines
- Body removals: ~N lines

## Recommended action
[Accept | Review carefully | Reject] - [1-2 sentence reason]
```

The manager parses the section headers to populate badges. The prose is whatever
the model produced. A summary cannot clear deterministic safety flags.

### `skills-port` (v1.0)

Input: source skill + list of target harnesses
Output: rewritten SKILL.md content for each target

The detailed prompt is in [the porter design from earlier](INGEST_FLOW.md#porter). It includes:

- Translation patterns (`AskUserQuestion` → generic phrasing, etc.)
- What to preserve (name, description, intent)
- Capability matrix for context
- Output format requirements

The output is validated by the manager — banned patterns, frontmatter integrity, description unchanged.

### `skills-author` (v1.0)

Input: user's description of what they want a skill to do
Output: a complete SKILL.md draft

Guides the user through:

- Naming
- Description writing (the activation control)
- Body structure
- Compatibility declaration
- Testing approach

This is the "help me write a new skill" UX, but living in the user's agent rather than as a manager subcommand.

## Where bundled skills live in the repo

```
skills-manager/                       # this repo
├── docs/                             # design docs
├── src/                              # CLI source
└── bundled-skills/                   # the bundled skills (markdown)
    ├── skills-ingest/
    │   ├── SKILL.md
    │   └── schemas/
    │       └── ingest-output.json
    ├── skills-compat-check/
    │   └── SKILL.md
    ├── skills-diff-summary/
    │   └── SKILL.md
    ├── skills-port/                  # v1.0
    │   └── SKILL.md
    └── skills-author/                # v1.0
        └── SKILL.md
```

They ship inside the manager's binary (embedded). Updates to the manager update
the bundled skills. If the user enabled handoff installation and has edited a
bundled skill in a harness path, manager updates must treat that as a local
override and avoid overwriting it without confirmation.

## Output validation

The CLI validates bundled-skill output before saving:

```
$ skills-manager port-apply qa --from /tmp/qa.ported.md

Validating output from skills-port...
  ✓ Frontmatter valid YAML
  ✓ name unchanged (qa)
  ✓ description unchanged (preserves activation)
  ✓ No banned patterns detected for target harness (codex)
  ⚠ compatibility-notes added: hermes (sequential fallback)
  
Save as new variant? [Y/n]
```

If validation fails (model renamed the skill, broke frontmatter, introduced banned patterns), the CLI rejects the input and shows the diff so the user can re-run.

## Community improvement model

Each bundled skill is a markdown file. Improving them is straightforward:

1. Fork the manager repo
2. Edit the SKILL.md in `bundled-skills/<name>/`
3. Open a PR
4. Maintainers merge if it improves output on test cases

A `bundled-skills/test-cases/` directory holds expected inputs/outputs for regression testing. PRs that change a bundled skill must keep tests passing or update them.

## Custom bundled skills (advanced)

Users with strong preferences can override the shipped bundled skills:

```yaml
# ~/.skills-manager/config.yaml
bundled_skills:
  override_path: ~/dev/my-custom-bundled-skills/
```

The manager will use the user's versions instead of the shipped ones. Useful for orgs that want custom categorization rules or stricter porting policies.

## What bundled skills DON'T do

- Don't execute arbitrary code
- Don't have access to network beyond the configured provider or user's agent
- Don't see user's other skill content (each invocation is scoped)
- Don't store state between invocations (stateless)
- Don't bypass the manager's validation layer
- Don't hide raw diffs, safety flags, or validation failures

## Future bundled skills (no commitment)

Ideas worth tracking:

- `skills-cleanup` — analyze a project's installed skills and suggest removal of unused ones
- `skills-merge` — combine two overlapping skills into one
- `skills-fork` — create a personal variant of a community skill
- `skills-quality` — score a skill for clarity, completeness, activation precision
