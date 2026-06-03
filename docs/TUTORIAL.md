# 5-minute tutorial: guided setup, then the CLI

This tutorial starts with the intended first-run path: install the CLI, then let
the **guided setup wizard** in `skills-manager serve` walk you through it — a
read-only **discover** of the skills you already have, a **review** of
deterministic recommendations, a **dry-run** preview of every change, and an
**apply** step that only touches disk after you confirm. The same steps are
available as CLI commands, shown afterward. No LLM provider is required.

## 1. Install

The verified install path is the public Go module:

```sh
go install github.com/Flow-Forge-Lab-Team/skills-manager/cmd/skills-manager@v0.1.0
skills-manager --version
```

Expected output for the v0.1.0 release:

```text
skills-manager 0.1.0
```

This requires Go 1.26 or newer and installs the binary into
`$(go env GOPATH)/bin`. Add that directory to `PATH` if your shell cannot find
`skills-manager`.

## 2. Guided setup in your browser (recommended)

Start the local dashboard and open the URL it prints:

```sh
skills-manager serve
```

On a fresh machine the dashboard does not drop you on an empty screen — it routes
you into the five-step **setup** wizard:

1. **Scope** — choose what to inspect: global skills, the current project, or
   both. Inspection is local-only, and the wizard discloses which paths it may
   read.
2. **Discover** — run the read-only **discover** pass. It builds your inventory
   (detected tools, global and project skills, drift, duplicate content, coverage
   gaps) and writes only manager-local state under `~/.skills-manager`.
3. **Review** — read the deterministic **recommendations**, grouped by kind
   (ingest, install, review drift, ignore). Each explains *why*, and whether the
   skill is **unmanaged** or already **managed**.
4. **Apply** — preview each selected action as a **dry-run** (the exact files to
   create, update, preserve, skip, or remove), then **apply** only what you
   explicitly confirm. Each applied action records an audit entry.
5. **Done** — see what was applied, ignored, or failed, and land on the
   dashboard.

The order is deliberately **safe and read-first**: discover and dry-run never
touch your tool directories, so nothing changes until the confirmed apply.

**Discovered vs. managed.** Discovered skills are everything found on disk; most
are **unmanaged** — the manager did not create them and will not change them
without a dry-run and your confirmation. A skill becomes **managed** once you
ingest or install it through the manager, which manifest-tracks it so uninstall
removes only manager-owned files. See [`SETUP_WIZARD.md`](SETUP_WIZARD.md) for
the full contract and vocabulary.

## 3. Run discovery from the CLI

Prefer the terminal, or automating? The wizard's discover step is also a plain
command. Discovery is read-only for tool directories; it writes only
manager-local state under `~/.skills-manager`, including `state.db`, audit logs,
and inventory snapshots.

```sh
skills-manager discover --global
```

A small fixture with one Claude Code skill and an empty OpenClaw root currently
prints this shape:

```text
Discover assessment
Facts: 2 tools present, 5 tools missing, 1 global skills, 0 project-local skills, 0 projects
Review facts: 0 drift/overlap, 0 duplicate-content
Coverage gaps: 6

Exact review facts: none

Coverage gaps:
  - Global skill coverage gap: review - review is visible in claude but absent from openclaw.
  - Missing tool coverage: Antigravity - Antigravity was not detected in this scan scope.
  - Missing tool coverage: Codex - Codex was not detected in this scan scope.
  - Missing tool coverage: Gemini CLI - Gemini CLI was not detected in this scan scope.
  - Missing tool coverage: Grok - Grok was not detected in this scan scope.
  - Missing tool coverage: Hermes - Hermes was not detected in this scan scope.

Recommendations:
  - Ingest unmanaged skill: review (confidence: medium) - claude/global is unmanaged inventory; ingest would first create a dry-run plan and preserve the source path.
  - Install globally: review -> openclaw (confidence: medium) - review is visible in claude and absent from openclaw; openclaw has requested/on-demand loading cost, so a global install can be planned safely.
```

The exact tool counts depend on which supported tools have directories on your
machine. The important split is stable: facts and coverage gaps are exact
inventory signals; recommendations are deterministic proposals that still
require a dry-run plan.

## 4. Discover project-local skills with consent

Project discovery reads only the roots you pass or roots you previously saved. It
prunes generated directories such as `.git`, `node_modules`, `.next`, `dist`, `build`,
`.venv`, `.cache`, and Codex scratch workspaces by default.

```sh
skills-manager discover --projects ~/dev --save-project-roots
skills-manager discover --saved-project-roots
skills-manager discover --list-project-roots
skills-manager discover --remove-project-root ~/dev
```

Use `--saved-project-roots` only when you want to reuse that saved consent. The
manager never upgrades a missing scope into a full-home scan.

## 5. Review the dashboard

Once setup is complete, `skills-manager serve` opens the steady-state dashboard
instead of the wizard. The Discover view summarizes:

- Inventory
- Drift
- Global vs Project
- Tool Coverage
- Recommendations
- Actions

The Actions tab previews source path, destination path, target tool, ownership,
and hash impact before any write. Apply buttons require an already computed
dry-run plan plus explicit confirmation, and successful or failed actions write
an audit entry.

## 6. Optional AI assessment

AI advisory output is opt-in. Inventory, drift groups, hashes, coverage gaps,
and deterministic recommendations do not require a provider.

Use one of these only when you want advisory help:

```sh
skills-manager assess review --project /path/to/project --target codex --handoff
skills-manager assess review --project /path/to/project --target codex --auto
```

`--handoff` writes a local prompt for manual review. `--auto` uses the configured
provider credentials. Secret-looking values are redacted before handoff or
provider calls, and cached advisory output stays under the manager home.

## 7. Legacy zero-state demo

If you are starting with no existing skills and want to see install mechanics,
ingest the committed examples and install into a throwaway project:

```sh
skills-manager add ./examples/hello-skill --yes
skills-manager add ./examples/python-skill --yes
mkdir -p /tmp/sm-demo/.codex
printf '{"dependencies":{"next":"15.0.0","react":"19.0.0"}}\n' > /tmp/sm-demo/package.json
printf '{}\n' > /tmp/sm-demo/.codex/config.json
skills-manager init /tmp/sm-demo --non-interactive
skills-manager match --project /tmp/sm-demo --explain
skills-manager install --project /tmp/sm-demo
```

Everything installed by the manager is reversible:

```sh
skills-manager uninstall --project /tmp/sm-demo --confirm
```

## Where to go next

- [Setup wizard](SETUP_WIZARD.md) — the first-run setup contract and vocabulary
- [Discovery](DISCOVERY.md) — inventory schema, consent scopes, and recommendations
- [CLI reference](CLI_REFERENCE.md) — every command and flag
- [Security model](SECURITY_MODEL.md) — install, scan, and AI assessment boundaries
- [Cross-machine](CROSS_MACHINE.md) — sync library and inventory snapshots across machines
