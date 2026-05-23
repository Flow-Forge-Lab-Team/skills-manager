# FLO-242 v0.1 Acceptance Smoke

Date: 2026-05-23

## Scope

This smoke validates the v0.1 local-library workflow against a disposable
realistic Next.js project and an isolated manager home. It does not touch the
real user library.

Out of scope matches the Linear issue and roadmap: cross-machine sync,
scheduling, LLM summaries, web UI, watcher daemon, and cloud scheduling.

## Fixture

- Manager home: `/tmp/skills-manager-flo242.A3OVns/home`
- Project: `/tmp/skills-manager-flo242.A3OVns/next-realistic`
- Drift project: `/tmp/skills-manager-flo242.A3OVns/next-drift`
- Binary: `./bin/skills-manager` built from this branch

The project fixture included:

- `package.json` with `next`, `react`, `tailwindcss`, `@prisma/client`,
  `@playwright/test`, and `vitest`
- `next.config.js`
- `tailwind.config.js`
- `.github/workflows/ci.yml`
- harness config directories for `.claude`, `.codex`, `.grok`, `.agents`,
  and `.hermes`
- a pre-existing unmanaged file at `.claude/skills/preexisting/SKILL.md`

The isolated library included:

- `next-review`: Next.js/React skill, portable
- `qa-check`: Playwright/Vitest skill, portable, requires `rg`
- `update-demo`: GitHub-origin update fixture pointing at
  `Flow-Forge-Lab-Team/skills-manager/bundled-skills/skills-diff-summary`
- `linear-doctor`: non-matching fixture requiring the Linear MCP, used to
  verify `doctor` reports missing requirements

## Results

### Init

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager --json init "$PROJECT"
```

Result: pass.

Detected categories:

```text
Engineering, Quality, Operations, Data, Design
```

Detected tags:

```text
nextjs, nodejs, playwright, prisma, react, tailwind, vitest
```

Detected harnesses:

```text
claude, codex, grok, antigravity, gemini, hermes, openclaw
```

### Match

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager --json match --project "$PROJECT" --explain
```

Result: pass.

`match --explain` returned ranked candidates without installing anything:

- `next-review`, score 6, category and tag overlap
- `qa-check`, score 6, category and tag overlap
- `update-demo`, score 5, category and tag overlap

### Install

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager install --project "$PROJECT"
```

Result: pass.

The install populated the expected five distinct project target bases for each
matching skill:

```text
.agents/skills/<skill>
.claude/skills/<skill>
.codex/skills/<skill>
.grok/skills/<skill>
skills/<skill>
```

`antigravity` and `gemini` share `.agents/skills`; `hermes` and `openclaw`
share `skills`.

### Check

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager check --force --skill update-demo
```

Result: pass.

Output:

```text
update-demo: warn (old tree unavailable, falling back to live comparison)
update-demo: updated (pending)
```

The warning is expected for the fixture because the stored commit predates the
fixture path. The fallback still staged a real pending GitHub update.

### Update Diff

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager update --diff update-demo
```

Result: pass.

The command printed a raw unified diff from the local `update-demo` skill to the
incoming `skills-diff-summary` skill.

### Safety Flags

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager update --safety update-demo
```

Result: pass.

Output:

```text
Safety review for update-demo
Safety flags (2):
- description-changed [block] SKILL.md:3 - frontmatter description changed
- suspicious-instructions [block] SKILL.md:17 - exfiltrate, reveal secrets, disable safety, bypass policy, or summarize the
summary_status=tainted
```

### Doctor

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager doctor
```

Result: pass.

`doctor` exited nonzero as expected and reported the missing Linear MCP
requirement:

```text
missing required MCP linear (configure connector for your harness)
```

### Doctor Rebuild State

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager doctor --rebuild-state
```

Result: pass.

Output:

```text
rebuilt state.db
missing required MCP linear (configure connector for your harness)
```

The command rebuilt derived state and then still reported the intentionally
missing Linear MCP requirement.

### Manifest Drift

Command:

```sh
printf '\nlocal edit\n' >> "$DRIFT_PROJECT/.codex/skills/next-review/SKILL.md"
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager doctor
```

Result: pass.

Output included:

```text
fingerprint drift: .codex/skills/next-review modified in /tmp/skills-manager-flo242.A3OVns/next-drift
missing required MCP linear (configure connector for your harness)
```

### Uninstall

Command:

```sh
SKILLS_MANAGER_HOME="$HOME_DIR" ./bin/skills-manager uninstall --project "$PROJECT" --confirm --no-backup
```

Result: pass.

All manifest-owned paths for `next-review`, `qa-check`, and `update-demo` were
removed from:

```text
.agents/skills
.claude/skills
.codex/skills
.grok/skills
skills
```

The pre-existing unmanaged file remained:

```text
.claude/skills/preexisting/SKILL.md
```

## Conclusion

FLO-242 acceptance passed against a realistic disposable Next.js project. No
product code changes were required.
