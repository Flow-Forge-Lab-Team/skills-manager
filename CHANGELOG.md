# Changelog

All notable changes to skills-manager are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- **Triage UI** (`skills-manager serve`): Overview, Library, Updates, Matrix,
  Skill Detail, Project Detail, Cross-machine, Settings, and a Discover stub —
  a localhost web UI + REST API over the local library.
- **Usage tracking** from Claude Code OTEL events and a PreToolUse hook.
- **Filesystem watcher** (`skills-manager watch`): optional, dependency-free
  polling daemon that surfaces new/changed skills as review notifications.
- **AGENTS.md assembler** (`skills-manager assemble`): aggregate always-on
  skills into a project-root AGENTS.md, preserving user content.
- **Harness compilers** (`skills-manager compile cursor|copilot`): translate
  skills into Cursor `.mdc` and Copilot `.instructions.md` formats.
- **Variants system**: per-harness ported skill files (`.variants.yaml`) chosen
  at install, with drift detection via `doctor` and `skills-manager variants`.
- **skills-port** bundled skill + `skills-manager port`: cross-harness rewriter
  with validation, saved as variants.
- **skills-author** bundled skill + `skills-manager new --guided`: guided
  creation of well-formed skills (validated before ingest).
- **Local OS scheduling** (`setup-schedule` / `unschedule`).
- Release engineering: cross-platform binaries, install scripts (`curl | sh`,
  npm, Homebrew tap), docs site, signed releases, and this changelog.

### Notes
- Markdown design docs under `docs/` are canonical; the published docs site is
  generated from them with MkDocs.

[Unreleased]: https://github.com/Flow-Forge-Lab-Team/skills-manager/commits/main
