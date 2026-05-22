# Roadmap

Phased delivery. Each phase ends with a usable product; nothing is "wait for v1
to be useful." The adversarial review changed the sequencing: prove the local
library, safety model, and reversible install path before adding long-running
automation, web surfaces, or cloud schedulers.

## v0.1 — Local library + reversible installs

**Goal:** A small working CLI that imports skills, installs them safely across
SKILL.md-native harnesses, detects upstream changes, and can prove exactly what
it owns. No UI. No watcher. No cloud scheduling. No LLM dependency.

**In scope:**

- CLI commands: `init`, `add`, `install`, `sync`, `check`, `update`, `list`, `show`, `status`, `uninstall`, `scan`, `doctor`, `config`
- Copy mode (no symlinks for v0.1)
- Manifest tracking (every file we create is recorded)
- Project config file (`.skills/project.yaml`) + lock file (`.skills/installed.lock`)
- Filesystem auto-detection for project init (Node, Python, Go, Rust, Ruby, PHP)
- Origin tracking with sidecars (`.skill-meta.yaml`)
- GitHub-based update detection (the largest source)
- 10-category + flat-tag taxonomy applied to existing library
- Compatibility model honored at install time
- Execution requirements declared and checked by `doctor` (system tools, MCP servers, scripts, credentials, model/tool assumptions)
- Update safety review: raw diff, changed description/frontmatter detection, suspicious instruction detection, no AI summary required
- Local state rebuild: `doctor` can rebuild `state.db` from library files and explain catalog/state drift

**Out of scope (deferred):**

- Local git-based library sync between machines
- Web UI
- Cursor / Copilot compilation
- AGENTS.md assembly
- Bundled skills (`skills-port`, etc.)
- Filesystem watcher daemon
- Local and cloud scheduling integration
- LLM-generated diff summaries
- Auto-install of newly-matched skills

**Acceptance criteria:**

- `skills-manager init` in a typical Next.js project sets up reasonable defaults in <30 seconds
- `skills-manager install` populates 7 harness paths correctly from one canonical library
- `skills-manager uninstall` cleanly removes everything we created, preserves what we didn't
- `skills-manager check` detects new commits on GitHub-sourced skills
- `skills-manager update --diff <skill>` shows raw diff plus safety flags before acceptance
- `skills-manager doctor` catches missing binaries/MCP servers/credentials for installed skills
- `skills-manager doctor --rebuild-state` can recover `state.db` from `catalog.yaml` + sidecars

**Time estimate:** 2–3 weeks of focused work.

---

## v0.2 — Local sync + optional LLM assistance

**Goal:** Add cross-machine sync and low-friction AI assistance without making
clipboard handoff the primary path.

**In scope:**

- Local git-based library sync between machines (`sync-library`, `join`, `machines`)
- Merge/conflict rules for generated metadata (`catalog.yaml`, `.machines.yaml`, `.skill-meta.yaml`)
- Optional local LLM provider path for summaries and ingest (`llm.provider`, `llm.api_key-env`, or environment variables)
- Bundled skill: `skills-diff-summary` (optional summary mechanism for updates)
- Bundled skill: `skills-ingest` (categorization + tagging at import time)
- Bundled skill: `skills-compat-check` (deeper compatibility + requirements analysis)
- Clipboard/file handoff remains available as a fallback, not the default recommended path
- Local OS scheduling integration (`setup-schedule --provider local`)
- Cost-honest opt-in for auto-summary via local API key configuration

**Out of scope:**

- Cloud scheduling (Claude routines / Codex automations) — pushed beyond v1
- Web UI — pushed to v0.3
- Cursor / Copilot compilation — pushed to v1.0
- Filesystem watcher daemon — deferred until scan-based ingest proves insufficient

**Acceptance criteria:**

- A second machine can join the library, pull updates, and install a project from a committed lockfile
- A teammate missing a locked skill gets a clear remediation path instead of a silent install failure
- Generated metadata conflicts are either auto-resolved deterministically or surfaced with a file-specific explanation
- A user can run `skills-manager summarize pdf --auto` with an explicit local provider and get a validated summary
- Clipboard handoff works as a fallback and is documented as higher-friction
- launchd / cron / systemd integration installs a working daily-check job

**Time estimate:** 3–4 weeks after v0.1.

---

## v0.3 — Triage UI + optional watcher

**Goal:** Add visual review surfaces only after the CLI state model is
trustworthy.

**In scope:**

- `skills-manager serve` — local web server
- All views from the mockup: Overview, Library, Skill Detail, Project, Updates, Matrix, Cross-machine
- Cross-machine status view (drift detection)
- Usage tracking from Claude Code OTEL events (`claude_code.skill_activated`)
- Optional filesystem watcher daemon (`skills-manager watch`) for users who find manual scan insufficient
- Watcher notifications and false-positive controls

**Out of scope:**

- Cursor / Copilot — pushed to v1.0
- Authentication beyond localhost (Tailscale users can punch through manually)
- Mobile / responsive UI polish
- Cloud scheduling

**Acceptance criteria:**

- The mockup's screens are functional, not just visual
- The Matrix view renders for a 250-skill / 7-project library in under 1 second
- The Updates view shows AI summaries inline (generated either by the bundled skill flow or auto-summary if configured)
- The UI can review a 10-update batch faster than the CLI without hiding raw diffs or safety flags
- Watcher mode ignores manager-owned writes and produces no duplicate ingest prompts in normal install/update flows

**Time estimate:** 4–5 weeks after v0.2.

---

## v1.0 — Cursor + Copilot + polish

**Goal:** Cover the remaining major harnesses that need format compilation.

**In scope:**

- Cursor `.mdc` compiler (SKILL.md → Cursor rule format with `globs` / `alwaysApply`)
- Copilot `.instructions.md` compiler
- AGENTS.md assembler (concatenate relevant skills into project-root AGENTS.md)
- Bundled skill: `skills-port` (cross-harness rewriter)
- Bundled skill: `skills-author` (skill creation guide)
- Variants system (per-harness ported variants of a canonical skill)
- Public release: documentation, install scripts, marketplace listings

**Acceptance criteria:**

- A skill installed via `skills-manager` works correctly in Cursor with the right globs
- A skill installed via `skills-manager` works correctly in projects using AGENTS.md
- Public install with `npm install -g skills-manager` (or equivalent) works on macOS, Linux, Windows
- A user new to the tool can go from zero to working setup in 5 minutes following the docs
- v1 still works without cloud scheduling, a hosted backend, or a web UI session left running

**Time estimate:** 4–6 weeks after v0.3.

---

## Beyond v1.0 (no commitments)

Ideas worth tracking but not designing in detail yet:

- **Skill author dashboard** — for skill maintainers to see install counts, version adoption
- **Community ports registry** — a shared store of `skills-port` results (so popular ports don't need to be re-generated by each user)
- **Multi-team library** — orgs with private skill libraries shared across teammates
- **Skill quality scoring** — heuristics for skill quality (length, clarity, examples)
- **MCP server packaging** — bundle skills + MCP servers as a unit
- **Mobile companion** — read-only dashboard from your phone
- **Skill exchange / marketplace** — but only if a community emerges that wants it
- **Cloud scheduling** — Claude routines / Codex automations, only if local scheduling is not enough and provider APIs are stable

## Time totals

Realistic indicative numbers (solo developer, focused):

- v0.1: 2–3 weeks
- v0.2: 3–4 weeks
- v0.3: 4–5 weeks
- v1.0: 4–6 weeks

**Total to v1.0: 13–18 weeks** (~3–4 months of focused work).

These are estimates, not commitments. Real estimates depend on language choice, testing rigor, and how often the design needs to be revised based on what we learn.

## What's deferred and why

Things deliberately not in any current phase:

- **Marketplace publishing** — we integrate with marketplaces but don't publish to them. Possibly never.
- **Cloud backend** — local-first is a core principle.
- **Cloud scheduling** — explicitly beyond v1. It adds provider churn, billing, credential, and result-writeback complexity before the local product has earned it.
- **Auth system** — localhost-only is fine for v1. Multi-user / team-shared comes later if needed.
- **Plugin system for the manager itself** — the manager isn't pluggable. Bundled skills cover most extension cases.
- **GUI desktop app** — web UI in `serve` mode covers this; native shells (Tauri/Electron) aren't worth the build complexity.

## Risk register

Top risks to delivery:

1. **Clipboard handoff becomes the product's daily UX.** Mitigation: make local provider automation the preferred LLM path; keep clipboard/file handoff only as fallback.
2. **AI summaries hide risky upstream changes.** Mitigation: raw diffs and deterministic safety flags are mandatory; summaries are advisory and validated.
3. **v0.1 sprawls before value is proven.** Mitigation: no sync, UI, watcher, cloud scheduling, or LLM features in v0.1.
4. **Compatibility is underspecified.** Mitigation: model execution requirements separately from harness support and validate them in `doctor`.
5. **No one cares about this category of tool.** Mitigation: ship the smallest local workflow, validate with real users (you), iterate.
6. **Harnesses diverge from SKILL.md standard.** Mitigation: variants system (v1.0) handles per-harness divergence.
