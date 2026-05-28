# 5-minute tutorial: from zero to a working setup

This walks you from a fresh install to skills installed in a real project, in
about five minutes. It uses **copy mode** (the default) and needs no LLM
provider or network beyond the install step.

## 1. Install (30s)

```sh
curl -fsSL https://raw.githubusercontent.com/Flow-Forge-Lab-Team/skills-manager/main/install.sh | sh
# or: npm install -g @flowforgelab/skills-manager
# or: brew install Flow-Forge-Lab-Team/tap/skills-manager
skills-manager --version
```

## 2. See what you already have (1 min)

If you already use Claude Code, Codex, etc., you have skills scattered in
`~/.claude/skills/`, `~/.codex/skills/`, and friends. Discover them:

```sh
skills-manager scan
```

Bring the ones you want into your canonical library:

```sh
skills-manager scan --ingest        # interactive review
# or, for a single known-good source:
skills-manager add <path-or-repo>
```

Check the library and overall state:

```sh
skills-manager list
skills-manager status
```

## 3. Tag a project and preview matches (1 min)

Initialize a project so the manager knows its stack:

```sh
cd ~/code/my-app
skills-manager init                 # detects categories/tags/harnesses
skills-manager match --explain      # preview which skills apply, and why
```

## 4. Install into the project (1 min)

```sh
skills-manager install
```

This copies matching skills into the right per-harness directories
(`.claude/skills/`, `.codex/skills/`, …), records an `installed.lock` and a
manifest, regenerates `AGENTS.md` for always-on skills, and — if your project
lists `cursor`/`copilot` — compiles `.cursor/rules/*.mdc` and
`.github/instructions/*.instructions.md`.

Everything is reversible:

```sh
skills-manager uninstall            # removes only what the manager created
```

## 5. Keep it healthy (1 min)

```sh
skills-manager check                # poll GitHub-sourced skills for updates
skills-manager update               # review pending updates (raw diff + safety flags)
skills-manager doctor               # verify manifests, fingerprints, requirements
skills-manager serve                # open the local triage web UI
```

## Where to go next

- [CLI reference](CLI_REFERENCE.md) — every command and flag
- [Architecture](ARCHITECTURE.md) — how install/compile/assemble fit together
- [Update flow](UPDATE_FLOW.md) — safety review of upstream changes
- [Cross-machine](CROSS_MACHINE.md) — sync your library across machines
- The [`examples/sample-project`](https://github.com/Flow-Forge-Lab-Team/skills-manager/tree/main/examples/sample-project)
  reference application shows a typical project setup end to end.
