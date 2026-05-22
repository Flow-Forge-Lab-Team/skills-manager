# Taxonomy

The two-dimensional system for organizing skills.

## Design principles

1. **Small enough to fit on one screen.** 10 categories + tags as needed. Not 71.
2. **Each tag obvious from its name.** No "is this `kind:` or `fn:`?" questions.
3. **Matches ecosystem consensus.** Aligns with [AGNT.gg](https://agnt.gg/articles/100-best-ai-agent-skills), [SkillsMP](https://skillsmp.com/), and similar marketplaces.
4. **Handles non-coding skills.** Business, Productivity, Writing as first-class.
5. **Two layers, not three.** Categories (fixed list) + flat tags (open).

## Dimension 1: Categories

A skill has **one or more** categories. Pick from this fixed list of 10.

| Category | What goes here |
|---|---|
| **Engineering** | Building software — frontend, backend, mobile, APIs, integrations, libraries |
| **Quality** | Testing, code review, debugging, QA, performance auditing, verification |
| **Operations** | Deploying, monitoring, infrastructure, CI/CD, incident response |
| **Data** | Data engineering, analytics, ML, databases, ETL, BI |
| **Design** | UI/UX, visual design, brand systems, diagramming, prototyping |
| **Documents** | File-format work — PDFs, spreadsheets, presentations, Word docs |
| **Writing** | Technical writing, copywriting, communications, content creation |
| **Business** | Strategy, product mgmt, marketing, sales, finance, compliance |
| **Productivity** | Personal/team automation, organizing, scheduling, workflows |
| **Agent-tooling** | Meta: building skills, agents, MCP servers, plugins, hooks, session config |

### Multi-category guidance

Most skills land in 1 or 2 categories. A skill landing in 3+ is usually over-tagged — pick the most central two.

Examples:
- `shadcn-ui` → Design, Engineering (it's both an aesthetic system and an implementation library)
- `qa` → Engineering, Quality (it tests Engineering work; it IS Quality work)
- `pdf` → Documents (just one — it's pure file-format work)
- `linear-feature` → Engineering, Operations (implements features; lands them via PR)

### Naming convention

- **Capitalized** (`Engineering`, not `engineering`)
- **Singular** (`Document`, not `Documents` — wait, we use `Documents` plural. OK fine, plural is the convention)
- **No abbreviations** (`Agent-tooling`, not `Agent`)

## Dimension 2: Tags

Open, flat, optional. Add as needed for precision. No namespaces.

### Tag conventions

- **lowercase-with-dashes** (`slash-command`, `multi-agent`, `session-mode`)
- **No prefixes** (we used `fn:`, `framework:`, etc. in design exploration; production is flat)
- **Tag what's specific.** A skill IS a `react` skill; it doesn't need `framework:react`.

### Standard tag clusters

These aren't enforced — just common patterns:

| Cluster | Examples |
|---|---|
| Stack | `react`, `nextjs`, `vue`, `svelte`, `tailwind`, `python`, `go`, `rust`, `typescript` |
| Framework | `shadcn`, `bmad`, `superpowers`, `gstack`, `prisma`, `laravel` |
| Integration | `linear`, `sentry`, `jira`, `supabase`, `stripe`, `github`, `slack`, `obsidian` |
| Method | `methodology`, `tdd`, `multi-agent`, `mcp`, `orchestration` |
| Scope | `personal`, `team`, `client` |
| Trigger | `slash-command` (only when distinctive — most aren't) |
| Mode | `session-mode`, `tool-wrapper` |

### When to make a new tag

A tag earns its place when:

1. It applies to **≥3 skills**, AND
2. It would meaningfully **filter** or **scope** — answers a real question the user would ask
3. It doesn't **subsume** an existing tag (no `framework-react` + `react` — pick one)

If you only have 1–2 skills with a property, it's not a tag yet. The library will tell you when it needs to become one.

### Anti-patterns

- `coding`, `engineering`, `dev` — Engineering is a category, not a tag
- `important`, `useful`, `cool` — value judgments aren't tags
- `claude`, `codex` — harness specificity is the compatibility model, not a tag
- `pr-2026-05-22` — temporal markers aren't tags
- `wip`, `experimental` — lifecycle state belongs in the skill's own description

## Project-side taxonomy

A project has the same shape — categories + tags. Two questions at init time:

```
What kinds of work does this project involve?
  [✓] Engineering   [✓] Design   [✓] Quality   [✓] Operations
  [ ] Data          [ ] Documents [ ] Writing   [ ] Business
  [ ] Productivity  [ ] Agent-tooling

What's the stack/tools?
  [ react ] [ nextjs ] [ shadcn ] [ supabase ] [ sentry ] [+ add ]
```

Categories scope **the universe**. Tags add **specificity**.

## Matching algorithm

A skill is a candidate for a project if and only if:

```
match(skill, project) iff
  (skill.categories ∩ project.categories) ≠ ∅
  AND respects(skill.compatibility, project.harnesses)
```

Once it is a candidate, **score by tag overlap**:

```
score(skill, project) =
    1 × |skill.categories ∩ project.categories|
  + 2 × |skill.tags ∩ project.tags|
```

Tags weighted 2× because they're more discriminating than categories. Sort
descending by score, then show the proposed list before install. v0.1 must not
silently auto-install newly matched skills; automatic application can come later
only after the ranking is trusted.

The algorithm is intentionally simple, but it needs negative signals and
explanations to avoid over-installing broad category matches:

- Stack mismatch: a skill tagged `python` should not outrank a `nodejs` project
  just because both are `Engineering`.
- Missing required dependency: a skill requiring `gh` or a Linear MCP server
  should warn or be held back when unavailable.
- Explicit project exclusions (`never_include`) always win.
- Low-confidence categorization lowers the score until the user confirms it.

Complexity should still live in the data and explainable rules, not opaque
ranking.

## Filesystem auto-detection

At `skills-manager init`, the CLI reads project files to propose categories + tags:

| Signal | Implies categories | Implies tags |
|---|---|---|
| `package.json` exists | Engineering | `nodejs` |
| `next.config.*` exists | Engineering, Design | `nextjs`, `react` |
| `components.json` exists | Design | `shadcn` |
| `tailwind.config.*` exists | Design | `tailwind` |
| `pyproject.toml` / `setup.py` | Engineering | `python` |
| `go.mod` | Engineering | `go` |
| `Cargo.toml` | Engineering | `rust` |
| `Gemfile` | Engineering | `ruby` |
| `composer.json` | Engineering | `php` |
| `playwright.config.*` | Quality | `playwright` |
| `vitest.config.*` / `jest.config.*` | Quality | `vitest` / `jest` |
| `Dockerfile`, `.github/workflows/` | Operations | — |
| `prisma/schema.prisma` | Data, Engineering | `prisma` |
| `@sentry/*` in dependencies | Operations | `sentry` |
| `@supabase/*` in dependencies | Data, Engineering | `supabase` |
| `stripe` in dependencies | Engineering | `stripe` |
| `.mcp.json` / mcp config | Agent-tooling | `mcp` |

The auto-detector is opinionated but always asks the user to confirm. Detectors live in the manager's repo and are PR-friendly so new stacks can be added.

## Categorization-at-import

When a new skill is added via `skills-manager add`, the CLI invokes the bundled `skills-ingest` skill (or the user's LLM API key, if configured) to suggest categories and tags from the skill's content. The user reviews and confirms.

The proposed categorization comes with **confidence levels**:

- **High**: name + description + body all align (e.g., a skill named `react-component-author` with React mentioned 12 times)
- **Medium**: name and body hint at it but description is vague
- **Low**: only weak signals — user input is essentially required

Low-confidence categorizations are flagged for the user; high-confidence ones can be auto-accepted (if `--auto` or `auto_ingest: true` is set).

## Evolution

The taxonomy is **versioned** (current: v1). When the ecosystem shifts or a category becomes too big:

- Splitting a category requires a migration
- Adding tags requires nothing (they're open)
- Removing/merging tags happens lazily via `set --remove-tag` per skill

If a category itself proves wrong (e.g., "Productivity" is too broad), v2 of the taxonomy could rename or split it. Migrations are tracked in the manager's CHANGELOG.

## Trust this taxonomy

It was derived from:
- Cross-referencing 4 major skill registries' category systems
- Tagging 251 real skills against the proposed system
- Validating that 93% of skills land in ≤2 categories
- Stress-testing against intentionally weird skills (algorithmic art, financial models, BMAD workflows)

It works. Resist the urge to add more categories until the data demands it.
