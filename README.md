# skills-manager

A local-first CLI for keeping AI coding-tool skills in one library, previewing
which ones fit a project, and installing manager-owned copies into the tool
directories you choose.

> Markdown under [`docs/`](docs/) is canonical. Committed HTML is generated with
> [`docs/_build.sh`](docs/_build.sh) for local/repository browsing; no public
> docs site is published yet. New here? Start with the discover-first
> [5-minute tutorial](docs/TUTORIAL.md).

## Install

If you have Go 1.26 or newer, install from the public Go module:

```sh
go install github.com/Flow-Forge-Lab-Team/skills-manager/cmd/skills-manager@v0.1.0
```

This puts the binary in `$(go env GOPATH)/bin`. Add that directory to `PATH` if
needed, then run:

```sh
skills-manager --help
```

On a system without Go, use one of the release-backed install paths below.
Release assets are checksum-verified by the shell installer and npm wrapper. The
checksum protects artifact integrity after download; it is not a signed
authenticity guarantee unless you separately verify the signed checksum artifact
from the release.

```sh
curl -fsSL https://raw.githubusercontent.com/Flow-Forge-Lab-Team/skills-manager/main/install.sh | sh

# Inspect first, then run. The installer verifies the release tarball against
# skills-manager_checksums.txt before moving the binary into place.
curl -fsSLO https://raw.githubusercontent.com/Flow-Forge-Lab-Team/skills-manager/main/install.sh
sh install.sh

npm install -g @flowforgelab/skills-manager

brew install Flow-Forge-Lab-Team/tap/skills-manager
```

## First run: guided setup

The guided path is the local dashboard. Start it, then open the URL it prints:

```sh
skills-manager serve
```

On first run — before the manager owns any skills — the dashboard does not drop
you on an empty screen. It routes you into a five-step **setup** wizard: choose a
**scope** (global skills, your saved project roots, or both), run a read-only
**discover**,
**review** the deterministic recommendations, preview each change as a
**dry-run**, and **apply** only the actions you confirm. Discover and dry-run
never write to your tool directories, so nothing changes until that final apply,
and every applied action records an audit entry. See
[`docs/SETUP_WIZARD.md`](docs/SETUP_WIZARD.md) for the full setup contract and
vocabulary.

**Discovered vs. managed.** *Discovered* skills are everything the wizard finds
already on disk; most start **unmanaged** — the manager did not create them and
never modifies or removes them without an explicit dry-run plan and your
confirmation. A skill becomes **managed** once you ingest or install it through
the manager, which records a manifest so uninstall removes only manager-owned
files.

### Or drive it from the CLI

The wizard runs commands you can also invoke directly. `discover` is the
read-only inventory pass: it separates exact inventory facts from deterministic
recommendations and persists local state under `~/.skills-manager` for dashboard
review.

```sh
skills-manager discover --global
```

To include project-local skills, approve explicit roots:

```sh
skills-manager discover --projects ~/dev --save-project-roots
skills-manager discover --saved-project-roots
```

Discovery is read-only for tool and project directories. It reports detected
tools, global and project-local skills, content hashes, drift groups, duplicate
content, and missing tool coverage before any install or cleanup action. It
persists manager-local inventory snapshots in `~/.skills-manager/state.db` so
later commands can compare snapshots. Human output separates exact review facts from
recommendations, and JSON includes the same assessment under `report`. Saved
project roots are manager-local consent state and can be listed or removed
later. Discovery never performs a full-home scan: it only reads known global
tool roots when `--global` is set and explicit project roots when `--projects`
or `--saved-project-roots` is set.

AI assessment is opt-in. Inventory, hashes, drift groups, coverage gaps, and
deterministic recommendations run without a provider. Use `assess --handoff` for
a local prompt or `assess --auto` only when you want configured provider
credentials used for advisory output.

## Real CLI Demo

This transcript was generated from the current CLI with a temporary manager home
and the committed [`examples/hello-skill`](examples/hello-skill) and
[`examples/python-skill`](examples/python-skill) fixtures. The transcript is
checked by `TestReadmeDemoTranscriptMatchesGolden`; update
[`docs/demo_transcript.txt`](docs/demo_transcript.txt) and rerun `go test ./...`
when changing these commands or output.

```text
$ skills-manager add ./examples/hello-skill --yes
Ingested hello-skill to $SKILLS_MANAGER_HOME/library/hello-skill

$ skills-manager add ./examples/python-skill --yes
Ingested python-skill to $SKILLS_MANAGER_HOME/library/python-skill

$ skills-manager init ./demo-project --non-interactive
Initialized ./demo-project
Categories: Engineering, Design
Tags: nextjs, nodejs, react
Harnesses: claude, codex, grok, antigravity, gemini, hermes, openclaw

$ skills-manager match --project ./demo-project --explain
hello-skill (score: 1) - category overlap: 1
  harnesses: antigravity, claude, codex, gemini, grok, hermes, openclaw
python-skill (rejected) - no category or tag overlap

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

## Security model

`skills-manager` installs executable agent instructions into local AI-tool
directories, so review remains part of the trust boundary. Copy mode is the
default: installs create manager-owned copies, preserve unmanaged local edits,
record manifests, and uninstall only removes files that the manager owns.

Updates and scans are review-first unless you choose an explicit automated path.
AI assessment is opt-in, uses configured provider credentials only after
`--auto` or writes a local handoff prompt after `--handoff`, and stores cached
advisory results under the manager home. Secret-bearing files such as `.env`,
credential files, and private keys are skipped by default; obvious secret values
in skill and instruction excerpts are redacted before provider calls or handoff
prompts.
Checksum verification in `install.sh` and the npm wrapper confirms that the
downloaded archive matches the published release checksum. It does not prove who
published that checksum unless you independently verify the release's signature
artifact. See [`docs/SECURITY_MODEL.md`](docs/SECURITY_MODEL.md) for the full
install, update, copy-mode, uninstall, and release-integrity boundaries.

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
5. [`docs/DISCOVERY.md`](docs/DISCOVERY.md) — discover-first inventory
6. [`docs/CLI_REFERENCE.md`](docs/CLI_REFERENCE.md) — command surface
7. [`docs/TAXONOMY.md`](docs/TAXONOMY.md) — categories + tags
8. [`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md) — compatibility + execution requirements
9. [`docs/INGEST_FLOW.md`](docs/INGEST_FLOW.md) — adding skills
10. [`docs/UPDATE_FLOW.md`](docs/UPDATE_FLOW.md) — tracking changes
11. [`docs/BUNDLED_SKILLS.md`](docs/BUNDLED_SKILLS.md) — manager skills
12. [`docs/SCHEDULING.md`](docs/SCHEDULING.md) — scheduling design
13. [`docs/CROSS_MACHINE.md`](docs/CROSS_MACHINE.md) — git sync
14. [`docs/SECURITY_MODEL.md`](docs/SECURITY_MODEL.md) — install and release trust boundaries

## Naming

- GitHub org/repo and Go module: `Flow-Forge-Lab-Team/skills-manager`
- npm package name: `@flowforgelab/skills-manager`
- Homebrew tap: `Flow-Forge-Lab-Team/tap`
- Legal/license display name: `Flow Forge Lab`

These names differ because GitHub, npm, Homebrew, Go modules, and legal notices
have different naming conventions; remaining references use those forms
intentionally.

## Mockup

A clickable UI mockup of a possible dashboard is at [`mockup.html`](mockup.html).
It is design-only; the current local UI is `skills-manager serve`.

## License

[MIT](LICENSE) © Flow Forge Lab.
