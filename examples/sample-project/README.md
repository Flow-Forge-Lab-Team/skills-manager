# Sample project — skills-manager reference application

A minimal project that demonstrates a typical `skills-manager` setup: a project
profile (`.skills/project.yaml`) that declares the stack and harnesses, so
`match`/`install` select the right skills.

It's a Next.js + TypeScript style app (categories/tags chosen accordingly), and
opts into both copy-mode harnesses (claude, codex) and the compile-only `cursor`
harness so you can see `.cursor/rules/*.mdc` generated on install.

## Try it

From a checkout of skills-manager (with some skills in your library):

```sh
cd examples/sample-project

# Preview which library skills match this project, and why:
skills-manager match --project . --explain

# Install matching skills (copies into .claude/skills, .codex/skills, and
# compiles .cursor/rules for the cursor harness):
skills-manager install --project .

# See it reversed cleanly:
skills-manager uninstall --project . --confirm
```

## What's here

```
.skills/project.yaml   # the project profile (stack tags + harnesses)
src/                    # placeholder app source so detectors have something to see
```

`.skills/installed.lock`, generated `AGENTS.md`, `.cursor/rules/`, and the
per-harness `*/skills/` copies are created by `install` and removed by
`uninstall`; they are intentionally not committed here.
