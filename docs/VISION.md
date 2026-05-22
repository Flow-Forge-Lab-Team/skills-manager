# Vision

## The problem

In 2026, AI coding agents finally converged on a shared skill format ([SKILL.md, the agentskills.io open standard](https://agentskills.io/)). Claude Code, Codex CLI, Grok Build, Antigravity, Gemini CLI, Hermes, OpenClaw, and others now all read the same `SKILL.md` files — different expected locations, but the same content.

This solved the *format* problem. It did not solve the *management* problem.

Today, a typical developer has:

- Skills in `~/.claude/skills/`, `~/.codex/skills/`, `~/.grok/skills/`, `~/.hermes/skills/`, `~/.openclaw/skills/`, `~/.gemini/antigravity/skills/`
- Project-level copies in `.claude/skills/`, `.codex/skills/`, `.agents/skills/`, bare `skills/`
- Plugins via marketplaces (`~/.claude/plugins/marketplaces/`) auto-pulling skills
- Skills authored inline by agents during sessions
- Skills written by hand and dropped in
- Skills from random GitHub repos

There is no tool that:

- Tracks what you have, where, and where it came from
- Tells you when upstream skills have updated (and what changed)
- Auto-suggests which skills belong in a project based on what the project is
- Keeps your library consistent across machines
- Handles the case where a skill is *intentionally* harness-specific vs accidentally non-portable

Existing skills-manager projects (xingkongliang, iamzhihuix, umutbozdag, ECC) solve the distribution layer reasonably well, but none address update tracking, project-aware matching, usage analytics, or compatibility-aware install.

## The solution

A local-first CLI (with optional web UI) that:

1. Maintains **one canonical library** of skills, with origin metadata for every skill
2. **Installs skills into the right places** for each harness you use, via copy or symlink
3. **Auto-detects new skills** appearing in any harness's skill directory (handles the case where Hermes auto-creates a skill or Claude writes one for you mid-session)
4. **Matches skills to projects** based on categories + tags (with filesystem auto-detection of the stack)
5. **Tracks upstream sources** and shows raw diffs, safety flags, and optional AI summaries when updates are available
6. **Respects compatibility and execution intent** — harness support plus required model/tool capabilities, local binaries, MCP servers, scripts, and credentials
7. **Syncs across machines** via git, with drift detection
8. **Reversibly** — copy mode default, manifest tracking, clean uninstall

## Who it's for

**Primary audience: power users with multiple harnesses.** People running Claude Code + Codex + Grok or any 2+ of the SKILL.md-compatible agents who currently maintain skills in duplicate. They feel the pain directly.

**Secondary audience: teams.** A project's `.skills/project.yaml` is committed to the repo, so cloning the repo + running `skills-manager install` reproduces the original author's skill setup.

**Tertiary audience: skill authors.** The manager gives them version tracking, usage analytics, and a clear path to publish updates.

Not for:

- Users on a single harness who never feel the cross-tool pain
- Users who don't author or customize skills (a marketplace UI would serve them better)

## Differentiation vs existing tools

| Capability | Existing tools | skills-manager |
|---|---|---|
| Distribution to multiple harnesses | Yes (Tauri desktops) | Yes (CLI + symlink/copy) |
| Origin tracking + update detection | No | **Yes** |
| Update review | No | **Yes** (raw diff + safety flags; AI summary optional) |
| Project-aware matching | No | **Yes** (categories + tags + filesystem detection) |
| Usage analytics | No | **Yes** (OTEL integration where available) |
| Compatibility + requirements model | No | **Yes** |
| Auto-detection of new skills | No | **Yes** (scan first; watcher optional later) |
| Bundled "manager skills" (port, ingest, summary) | No | **Yes** |
| Cross-machine git sync | Some via SQLite | **Yes** (git-native library) |
| CLI-first | No (desktop-first) | **Yes** |
| Web UI | Yes (Tauri) | Deferred local triage surface (`serve` command) |

## Non-goals

To keep scope honest:

- **Not a skill marketplace.** We integrate with marketplaces; we don't host one.
- **Not a skill authoring IDE.** We provide a `skills-author` bundled skill that guides authoring in whatever agent the user prefers; we don't build a custom editor.
- **Not a model provider.** AI summaries run via the user's explicitly-configured local provider credentials, or via a higher-friction agent handoff fallback. The manager never uses harness OAuth tokens directly.
- **Not a replacement for harness CLIs.** It augments them; it doesn't wrap them.
- **No subscription, no cloud backend.** Local-first. The only v1 cloud touch points are marketplace polls (HTTP) and user-configured git remotes.
- **No closed-marketplace integrations in v1.** Public APIs and public registries first; closed marketplaces require partnerships.

## Guiding principles

1. **The CLI is the canonical surface.** Every capability is a command. The web UI is a viewer.
2. **State is local and inspectable.** SQLite + flat files. No black-box cloud state.
3. **Reversible by default.** Copy mode, manifests, clean uninstall, no permanent surgery.
4. **Honest about costs and trust.** Token-spending features are opt-in with cost surfaced; model-generated summaries are advisory, never the only review artifact.
5. **LLM-agnostic.** Run the user's choice; never lock to one provider.
6. **Execution requirements are first-class.** Don't assume that harness compatibility means runtime readiness; record and check required tools, MCP servers, scripts, credentials, and model/tool capabilities.
7. **Open and inspectable.** Bundled skills are markdown files in the repo; community can improve them via PR.

## What success looks like

A user with 200+ skills across 5+ harnesses and 10+ projects can:

- See at a glance which skills are used where, with usage frequency
- Get notified when a skill they use has an update, with raw diff, safety flags, and an optional AI summary in under a minute of attention
- Open a new project, run `skills-manager init`, preview the likely skills, and install the selected set in 30 seconds
- Walk away from the tool any time and find their existing skill setup intact
- Share a project with a teammate who runs the same command and gets the same setup
- Move between laptop and desktop with `git pull` and have an identical library

If the user opens the manager weekly and finds it useful, the product is working. If they have to babysit it, it's failed.
