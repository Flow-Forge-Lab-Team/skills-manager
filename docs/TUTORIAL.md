# 5-minute tutorial: discover first, then act

This tutorial starts with the intended first-run path: install the CLI, run a
read-only inventory of skills you already have, review the dashboard assessment,
then choose whether to ingest or install anything. No LLM provider is required.

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

## 2. Discover global skills

Discovery is read-only for tool directories. It writes only manager-local state
under `~/.skills-manager`, including `state.db`, audit logs, and inventory
snapshots.

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

## 3. Discover project-local skills with consent

Project scans read only roots you pass or roots you previously saved. They prune
generated directories such as `.git`, `node_modules`, `.next`, `dist`, `build`,
`.venv`, `.cache`, and Codex scratch workspaces by default.

```sh
skills-manager discover --projects ~/dev --save-project-roots
skills-manager discover --saved-project-roots
skills-manager discover --list-project-roots
skills-manager discover --remove-project-root ~/dev
```

Use `--saved-project-roots` only when you want to reuse that saved consent. The
manager never upgrades a missing scope into a full-home scan.

## 4. Review the dashboard

```sh
skills-manager serve
```

Open the local URL printed by the command. The Discover view summarizes:

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

## 5. Optional AI assessment

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

## 6. Legacy zero-state demo

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

- [Discovery](DISCOVERY.md) — inventory schema, consent scopes, and recommendations
- [CLI reference](CLI_REFERENCE.md) — every command and flag
- [Security model](SECURITY_MODEL.md) — install, scan, and AI assessment boundaries
- [Cross-machine](CROSS_MACHINE.md) — sync library and inventory snapshots across machines
