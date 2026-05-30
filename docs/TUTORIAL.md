# 5-minute tutorial: from zero to a working setup

This walks you from a fresh install to skills installed in a real project, in
about five minutes. It uses **copy mode** (the default) and needs no LLM
provider or network beyond the install step. The path below starts from an empty
manager home and uses the committed `examples/hello-skill` fixture, so it works
even if you have never used an AI coding-tool skill before.

## 1. Install (30s)

The verified install path today is the public Go module:

```sh
go install github.com/Flow-Forge-Lab-Team/skills-manager/cmd/skills-manager@latest
skills-manager --version
```

Expected output:

```text
skills-manager 0.1.0-dev
```

The `curl | sh`, npm, and Homebrew commands are planned release channels. They
are not the recommended public happy path until release assets, the npm package,
and the Homebrew tap are live.

## 2. Add your first skill (1 min)

From a checkout of this repository, ingest the sample skill:

```sh
skills-manager add ./examples/hello-skill --yes
```

Expected output:

```text
Ingested hello-skill to ~/.skills-manager/library/hello-skill
```

Check the library:

```sh
skills-manager list
```

Expected output:

```text
hello-skill  Use when you want a small sample build skill for v
```

## 3. Create a zero-state project and preview matches (1 min)

Create a small project with one stack signal and one active target directory:

```sh
mkdir -p /tmp/sm-demo/.codex
printf '{"dependencies":{"next":"15.0.0","react":"19.0.0"}}\n' > /tmp/sm-demo/package.json
printf '{}\n' > /tmp/sm-demo/.codex/config.json
skills-manager init /tmp/sm-demo --non-interactive
```

Expected output:

```text
Initialized /tmp/sm-demo
Categories: Engineering, Design
Tags: nextjs, nodejs, react
Harnesses: claude, codex, grok, antigravity, gemini, hermes, openclaw
```

The project now has:

```text
/tmp/sm-demo/.skills/project.yaml
/tmp/sm-demo/.skills/installed.lock
```

Preview why the sample skill matches:

```sh
skills-manager match --project /tmp/sm-demo --explain
```

Expected output:

```text
hello-skill (score: 1) — category overlap: 1
  harnesses: antigravity, claude, codex, gemini, grok, hermes, openclaw
```

## 4. Install into the project (1 min)

```sh
skills-manager install --project /tmp/sm-demo
```

Expected output:

```text
Installing skills:
- hello-skill: category match; harnesses: antigravity, claude, codex, gemini, grok, hermes, openclaw
  copied .agents/skills/hello-skill
  copied .claude/skills/hello-skill
  copied .codex/skills/hello-skill
  copied .grok/skills/hello-skill
  copied skills/hello-skill
```

This copies matching skills into the active target directory and records the
manager-owned files. Inspect the result:

```sh
find /tmp/sm-demo -maxdepth 4 -type f | sort
```

Expected files include:

```text
/tmp/sm-demo/.agents/skills/hello-skill/.skill-meta.yaml
/tmp/sm-demo/.agents/skills/hello-skill/SKILL.md
/tmp/sm-demo/.claude/skills/hello-skill/.skill-meta.yaml
/tmp/sm-demo/.claude/skills/hello-skill/SKILL.md
/tmp/sm-demo/.codex/config.json
/tmp/sm-demo/.codex/skills/hello-skill/.skill-meta.yaml
/tmp/sm-demo/.codex/skills/hello-skill/SKILL.md
/tmp/sm-demo/.grok/skills/hello-skill/.skill-meta.yaml
/tmp/sm-demo/.grok/skills/hello-skill/SKILL.md
/tmp/sm-demo/.skills/installed.lock
/tmp/sm-demo/.skills/project.yaml
/tmp/sm-demo/skills/hello-skill/.skill-meta.yaml
/tmp/sm-demo/skills/hello-skill/SKILL.md
/tmp/sm-demo/package.json
```

The lock file records the desired skill set:

```text
version: 1
generated_by: skills-manager 0.1.0-dev
skills:
  - name: hello-skill
    harnesses:
      - antigravity
      - claude
      - codex
      - gemini
      - grok
      - hermes
      - openclaw
```

Everything is reversible:

```sh
skills-manager uninstall --project /tmp/sm-demo --confirm
```

## 5. Keep it healthy (1 min)

```sh
skills-manager check                # poll GitHub-sourced skills for updates
skills-manager update               # review pending updates (raw diff + safety flags)
skills-manager doctor               # verify manifests, fingerprints, requirements
skills-manager serve                # open the local triage web UI
```

## Existing-user scan path

If you already use Claude Code, Codex, or another supported SKILL.md-style tool,
you may have skills scattered in `~/.claude/skills/`, `~/.codex/skills/`, and
other tool directories. Discover them after the zero-state flow:

```sh
skills-manager scan
skills-manager scan --ingest        # interactive review
skills-manager scan --auto-ingest   # non-interactive high-confidence ingest
```

## Where to go next

- [CLI reference](CLI_REFERENCE.md) — every command and flag
- [Architecture](ARCHITECTURE.md) — how install/compile/assemble fit together
- [Update flow](UPDATE_FLOW.md) — safety review of upstream changes
- [Cross-machine](CROSS_MACHINE.md) — sync your library across machines
- The [`examples/sample-project`](https://github.com/Flow-Forge-Lab-Team/skills-manager/tree/main/examples/sample-project)
  reference application shows a typical project setup end to end.
