# skills-manager

A local-first CLI for managing AI agent skills across the harnesses you actually use — Claude Code, Codex CLI, Grok Build, Antigravity, Gemini CLI, Hermes, OpenClaw, and more.

> Markdown under [`docs/`](docs/) is canonical; the published docs site is
> generated from it with MkDocs. New here? Start with the
> [5-minute tutorial](docs/TUTORIAL.md).

## Install

```sh
# curl | sh (downloads the latest release binary)
curl -fsSL https://raw.githubusercontent.com/Flow-Forge-Lab-Team/skills-manager/main/install.sh | sh

# npm
npm install -g @flowforgelab/skills-manager

# Homebrew
brew install Flow-Forge-Lab-Team/tap/skills-manager

# from source
go install github.com/Flow-Forge-Lab-Team/skills-manager/cmd/skills-manager@latest
```

Then run the [5-minute tutorial](docs/TUTORIAL.md), or `skills-manager --help`.

## What it does

- **One library, many harnesses.** Maintain a canonical store of skills and have them appear in the right place for each agent CLI you use.
- **Project-aware matching.** Tag a project with categories and stack tags; preview relevant skills before installing them.
- **Update tracking with safety review.** Detect upstream changes, inspect diffs, and optionally generate guarded summaries before accepting.
- **Execution-aware compatibility.** Skills declare harness support, model/tool assumptions, local binaries, MCP servers, scripts, and credentials.
- **Cross-machine via git.** Sync your library between laptop, desktop, server.
- **Reversible.** Copy mode by default. Uninstall removes only files the manager created.

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

## Mockup

A clickable UI mockup of a possible dashboard is at [`mockup.html`](mockup.html). It is a reference for later triage surfaces, not a v0.1 commitment.

## License

[MIT](LICENSE) © Flow Forge Lab.
