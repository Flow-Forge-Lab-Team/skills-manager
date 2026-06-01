# Release checklist

Run this checklist before pushing a version tag.

## Required validation

```sh
go test ./...
./scripts/validate-discover-first.sh
./docs/_build.sh
git diff --check -- .
```

`scripts/validate-discover-first.sh` is the release gate for the
install-to-discover-to-dashboard workflow. It builds the CLI, creates an
isolated temporary HOME, seeds Codex, Claude Code, Cursor, Grok, generic
`.agents/skills`, and `AGENTS.md` fixtures, verifies discovery output and
snapshot persistence, checks dashboard API responses, runs a dry-run action
plan, applies one safe manager-owned install, and verifies README/tutorial
commands that claim exact first-run behavior.

Do not tag a release if this validation fails.

## Tagging

1. Confirm the working tree is clean.
2. Confirm generated HTML docs are committed after `./docs/_build.sh`.
3. Confirm the GitHub Actions `test` job passed on the release commit.
4. Push the version tag only after the checks above pass locally.
