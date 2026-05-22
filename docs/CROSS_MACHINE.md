# Cross-machine sync

How a single skill library stays consistent across multiple machines (laptop, desktop, server).

## The mechanism: git

The canonical library at `~/.skills-manager/library/` is a **git repository**. Every machine clones it from a remote. Sync is `git push` / `git pull`.

That's it. No cloud service, no custom protocol. Use the same primitives developers already use for code.

## Setup

### First machine

```
$ skills-manager init-library

Where should the canonical library live?

  [g] GitHub (recommended for personal use)
      Creates a private repo at github.com:<you>/skills-store
  [c] Custom git remote (Gitea, GitLab, self-hosted)
  [l] Local-only (no remote — not synced)

> g

Creating private GitHub repo skills-store...
  ✓ Created at github.com/greg/skills-store
  ✓ Initialized ~/.skills-manager/library/ as git repo
  ✓ Pushed initial commit (251 skills + catalog)
  ✓ Set as origin/main

Done. Other machines: clone this with `skills-manager join greg/skills-store`.
```

### Additional machines

```
$ skills-manager join greg/skills-store

Cloning github.com/greg/skills-store into ~/.skills-manager/library/...
  ✓ Pulled 251 skills
  ✓ Catalog synced
  ✓ Machine registered as greg-macmini

Library ready. Run `skills-manager install` in any project to use these skills.
```

The `join` command:
1. Clones the repo
2. Verifies the catalog
3. Adds this machine's identity to `library/.machines.yaml` (so cross-machine status views know about it)

## What's in the library repo

```
.git/
catalog.yaml                  # registry of all skills
.machines.yaml                # known machines + last-seen timestamps
.gitignore                    # ignores transient files (notifications, logs, cache)
<skill-name>/
  SKILL.md
  .skill-meta.yaml
  ...
```

What's NOT in the library repo (lives in `~/.skills-manager/` but outside `library/`):

- `state.db` — derived from the library; machine-local
- `manifests/` — per-machine project installs
- `logs/` — machine-local
- `notifications/` — machine-local
- `summaries/` — could optionally be in library, but defaults to local
- `config.yaml` — per-machine settings

This split is important: **the library is the shared truth; everything else is per-machine state.**

## Sync flow

### Pull (most common)

```
$ skills-manager sync-library

Fetching from origin...
  3 new commits since last sync

Changes:
  • pdf: v1.4.2 → v1.5.0 (accepted on greg-laptop 2h ago)
  • code-reviewer: new skill (ingested on greg-laptop 5h ago)
  • qa: pinned to v2.3.4 (declined update on greg-laptop yesterday)

Merging into local library...
  ✓ pdf updated
  ✓ code-reviewer added
  ✓ qa pinning honored

Propagating to local projects (3 projects using these skills):
  ✓ ~/dev/my-saas-app   (pdf refreshed)
  ✓ ~/dev/blog          (pdf refreshed)
  ✓ ~/dev/scratch       (no relevant changes)
```

### Push (after local changes)

```
$ skills-manager sync-library --push

Local changes:
  • Added skill: email-summarizer (ingested today)
  • Updated category for invoice-organizer

Pushing to origin/main...
  ✓ Pushed 2 commits
  ✓ Other machines will see these on their next sync
```

### Auto-sync on operations

Optionally, the manager can auto-pull/push on certain operations:

```yaml
# config.yaml
library_sync:
  auto_pull_before: [check, update, install]   # ensure fresh state
  auto_push_after: [add, accept-update, remove] # share changes
```

Default: manual. Auto-sync is opt-in because it can hide conflicts.

## Drift detection

The manager tracks `last_synced` per machine in `library/.machines.yaml`:

```yaml
machines:
  greg-laptop:
    last_synced: 2026-05-22T14:30:00Z
    last_commit: abc123def
  greg-macmini:
    last_synced: 2026-05-22T09:15:00Z
    last_commit: abc123def
  greg-cloud-dev:
    last_synced: 2026-05-19T11:00:00Z   # 3 days old
    last_commit: 987abc456
```

`skills-manager machines` shows this:

```
$ skills-manager machines

● greg-laptop      (this)        ✓ in sync         12m ago
● greg-macmini                   ⚠ 3 behind        2h ago
○ greg-cloud-dev                 ✗ stale           3d ago
```

## Conflict handling

When two machines change the same skill before syncing, you get a normal git merge conflict.

```
$ skills-manager sync-library

Fetching from origin...
⚠ Merge conflict on:
  library/pdf/SKILL.md
  
Cause:
  greg-laptop accepted pdf v1.5.0 update at 14:30
  greg-macmini accepted pdf v1.4.3-patch update at 14:25
  
Both touched the same file. You need to choose.

Options:
  [t] Take theirs (greg-laptop's v1.5.0)
  [m] Keep mine (greg-macmini's v1.4.3-patch)
  [d] Show 3-way diff
  [r] Resolve manually (opens $EDITOR)
```

The manager uses git's merge under the hood. Three-way merge is shown for SKILL.md files where conflicts are content-level (vs structural).

## What gets synced vs not

| File | Synced via library repo | Reason |
|---|---|---|
| `SKILL.md` for each skill | ✓ | The whole point |
| `.skill-meta.yaml` | ✓ | Origin/version info should be consistent |
| `.variants.yaml` + variant files | ✓ | Ported variants are part of the skill |
| `catalog.yaml` | ✓ | Index of what's in the library |
| `.machines.yaml` | ✓ | Cross-machine view needs it |
| `state.db` | ✗ | Derived from library + machine-local usage |
| `manifests/<project>.json` | ✗ | Per-machine project installs |
| `logs/` | ✗ | Machine-local diagnostics |
| `summaries/<skill>.md` | optional | Could share, but cheap to regenerate |
| `notifications/` | ✗ | Machine-local |
| `config.yaml` | ✗ | Per-machine settings |

The principle: **anything derived or machine-specific stays local. The library is the shared content.**

## Project configs across machines

`<project>/.skills/project.yaml` and `<project>/.skills/installed.lock` are in the **project's** git repo, not the library repo. They travel with the project naturally.

The lockfile is a requested skill set, not proof that every teammate already has
those skills in their personal library. Install must fail clearly when the local
library is missing a locked skill.

So when you check out a project on a new machine:

```
$ cd ~/dev/my-saas-app    # cloned from github
$ skills-manager install

Reading .skills/project.yaml...
Reading .skills/installed.lock...

Resolving from library... (will pull library if not present)
  ✓ shadcn-ui v0.8.0 — in library
  ✓ qa v2.3.4 — in library
  ✓ verification-before-completion v3.0.0 — in library
  ...

Populating harness paths...
  ✓ Done. 18 skills installed across 3 harnesses.
```

If a locked skill is missing locally:

```
Resolving from library...
  ✗ acme-style-guide v1.2.0 — missing from this machine's library

Options:
  [p] Pull/sync library remote and retry
  [a] Add from origin recorded in lockfile
  [s] Skip this skill for now (writes local warning, does not edit lockfile)
```

The lockfile should include enough origin data for a missing skill to be
retrieved when possible. If the origin is private or unavailable, the failure is
explicit and does not partially install that skill.

The combination — project repo carries project config, library repo carries skill content — means everything is reproducible.

## What if a teammate doesn't use skills-manager?

The project's `.claude/skills/` (etc.) directories might be gitignored (they're populated by the manager). For teammates who don't run the manager:

**Option A: Commit harness dirs.** Set `gitignore_harness_dirs: false` in project config. Now `.claude/skills/` is committed. Teammates without the manager still get the skills, they just don't get update tracking.

**Option B: Run the manager.** Provide a one-liner: `npm install -g skills-manager && skills-manager install`.

**Option C: Hybrid.** Commit the harness dirs for non-manager users; the manager owns them when it's around. Manifest tracking still works (manager respects the existing files via `preserve_existing: true`).

## Library backups

The library is git, so:

- Every machine that clones it is a backup
- The remote (GitHub) is a backup
- Specific versions are recoverable via `git checkout <commit>`

The manager can also explicitly archive:

```
$ skills-manager backup-library --to ~/Dropbox/skills-backup-2026-05-22.tar.gz

Creating tarball of ~/.skills-manager/library...
  ✓ 251 skills, 14MB compressed
  ✓ Saved to ~/Dropbox/skills-backup-2026-05-22.tar.gz
```

## Multi-user libraries (out of scope for v1)

If multiple developers on a team want a shared library:

- They share a git remote
- They have read/write access per their git permissions
- Conflicts work the same way

But for v1 we assume single-user (you across your machines). Multi-user adds complications:

- Whose `.skill-meta.yaml` wins on conflict?
- How do you handle one user editing a skill another considers stable?
- Who owns the catalog?

We'll design that if/when there's demand. For now: one library = one person.

## Cloud-only scheduling and sync

Deferred beyond v1. If cloud scheduling returns later, result writeback should
use the same library git remote and must not require broad credentials or a
long-lived local webhook. Until then, cross-machine state changes happen from a
real machine running `sync-library`.

## The minimum viable cross-machine workflow

If a user has two machines and wants them in sync:

```
# On machine 1
$ skills-manager init-library --provider github
# (sets up repo, pushes initial library)

# On machine 2
$ skills-manager join greg/skills-store
# (clones library, registers machine)

# Daily on both machines
$ skills-manager sync-library
# (pull latest, push local changes)

# That's it.
```

Cross-machine sync requires nothing beyond the git knowledge they already have.

## Failure modes

| Case | Behavior |
|---|---|
| Remote unreachable | Sync fails clearly, machine continues working locally; queues changes for next push |
| Merge conflict | Surfaces in `sync-library`; user resolves source files manually; generated metadata should be regenerated when possible |
| `catalog.yaml` conflict | Rebuild from `SKILL.md` + sidecars instead of hand-merging when possible |
| `.machines.yaml` conflict | Merge by machine key, keep newest `last_synced` per key |
| Library out of sync with state.db | `skills-manager doctor` detects, offers to rebuild state.db from library |
| Library is corrupted | Machine clones fresh from origin (after backing up local state) |
| User has no git skills | The wizard handles 95% of cases; for the 5% it falls back to "ask the user" |
