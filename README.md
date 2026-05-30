# skills-manager

A local-first CLI for keeping AI coding-tool skills in one library, previewing
which ones fit a project, and installing manager-owned copies into the tool
directories you choose.

> Markdown under [`docs/`](docs/) is canonical. Committed HTML is generated with
> [`docs/_build.sh`](docs/_build.sh) for local/repository browsing; no public
> docs site is published yet. New here? Start with the
> [5-minute tutorial](docs/TUTORIAL.md).

## Install

The verified path today is to install from the public Go module:

```sh
go install github.com/Flow-Forge-Lab-Team/skills-manager/cmd/skills-manager@latest
```

This requires Go 1.26 or newer and puts the binary in `$(go env GOPATH)/bin`.
Add that directory to `PATH` if needed, then run:

```sh
skills-manager --help
```

Pre-release channels are intentionally not the happy path yet:

```sh
# Planned after signed release assets exist.
curl -fsSL https://raw.githubusercontent.com/Flow-Forge-Lab-Team/skills-manager/main/install.sh | sh

# Inspect first, then run. The installer verifies the release tarball against
# skills-manager_checksums.txt before moving the binary into place.
curl -fsSLO https://raw.githubusercontent.com/Flow-Forge-Lab-Team/skills-manager/main/install.sh
sh install.sh

# Planned after @flowforgelab/skills-manager is published.
npm install -g @flowforgelab/skills-manager

# Planned after Flow-Forge-Lab-Team/homebrew-tap exists.
brew install Flow-Forge-Lab-Team/tap/skills-manager
```

Then run the [5-minute tutorial](docs/TUTORIAL.md), or `skills-manager --help`.

## Real CLI Demo

This transcript was generated from the current CLI with a temporary manager home
and the committed [`examples/hello-skill`](examples/hello-skill) fixture:

```text
$ skills-manager add ./examples/hello-skill --yes
Ingested hello-skill to $SKILLS_MANAGER_HOME/library/hello-skill

$ skills-manager init ./demo-project --non-interactive
Initialized ./demo-project
Categories: Engineering, Design
Tags: nextjs, nodejs, react
Harnesses: claude, codex, grok, antigravity, gemini, hermes, openclaw

$ skills-manager match --project ./demo-project --explain
hello-skill (score: 1) — category overlap: 1
  harnesses: antigravity, claude, codex, gemini, grok, hermes, openclaw

$ skills-manager install --project ./demo-project
Installing skills:
- hello-skill: category match; harnesses: antigravity, claude, codex, gemini, grok, hermes, openclaw
  copied .agents/skills/hello-skill
  copied .claude/skills/hello-skill
  copied .codex/skills/hello-skill
  copied .grok/skills/hello-skill
  copied skills/hello-skill
```

## What it does

- **One library, selected tool targets.** Maintain a canonical store of skills
  and install copies into supported SKILL.md-style tool directories.
- **Project-aware matching.** Tag a project with categories and stack tags; preview relevant skills before installing them.
- **Trust-first defaults.** Copy mode is the default, installs are manifest
  tracked, uninstall removes only manager-owned files, dependency gates run
  before install, and update review shows diffs before accepting changes.
- **Update tracking with safety review.** Detect upstream changes, inspect diffs,
  and optionally generate guarded summaries before accepting.
- **Execution-aware compatibility.** Skills declare harness support, model/tool assumptions, local binaries, MCP servers, scripts, and credentials.
- **Cross-machine via git.** Sync your library between laptop, desktop, server.

## Why

Skills now live in `~/.claude/skills/`, `~/.codex/skills/`, `~/.grok/skills/`, `~/.hermes/skills/`, `~/.openclaw/skills/`, `~/.gemini/antigravity/skills/`, `.agents/skills/`, marketplaces, GitHub repos, and ad-hoc files. They get duplicated, drift, and go stale. Nothing tracks what you have where, what's been updated, or what would actually be useful in the project you just opened.

This tool fixes that incrementally. v0.1 focuses on a reliable local library,
install manifests, update detection, and reversible installs. Automation, richer
UI, and cross-harness ports come only after the local workflow is trusted.

## Design docs

Read in this order:

1. [`docs/VISION.md`](docs/VISION.md) — the why
2. [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — the how
3. [`docs/ROADMAP.md`](docs/ROADMAP.md) — what ships when
4. [`docs/DATA_MODEL.md`](docs/DATA_MODEL.md) — schemas
5. [`docs/CLI_REFERENCE.md`](docs/CLI_REFERENCE.md) — command surface
6. [`docs/TAXONOMY.md`](docs/TAXONOMY.md) — categories + tags
7. [`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md) — compatibility + execution requirements
8. [`docs/INGEST_FLOW.md`](docs/INGEST_FLOW.md) — adding skills
9. [`docs/UPDATE_FLOW.md`](docs/UPDATE_FLOW.md) — tracking changes
10. [`docs/BUNDLED_SKILLS.md`](docs/BUNDLED_SKILLS.md) — manager skills
11. [`docs/SCHEDULING.md`](docs/SCHEDULING.md) — scheduling design
12. [`docs/CROSS_MACHINE.md`](docs/CROSS_MACHINE.md) — git sync

## Naming

- GitHub org/repo and Go module: `Flow-Forge-Lab-Team/skills-manager`
- npm package name, when published: `@flowforgelab/skills-manager`
- Homebrew tap, when created: `Flow-Forge-Lab-Team/tap`
- Legal/license display name: `Flow Forge Lab`

These names differ because GitHub, npm, Homebrew, Go modules, and legal notices
have different naming conventions; remaining references use those forms
intentionally.

## Mockup

A clickable UI mockup of a possible dashboard is at [`mockup.html`](mockup.html).
It is design-only; the current local UI is `skills-manager serve`.

## License

[MIT](LICENSE) © Flow Forge Lab.
