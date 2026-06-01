# Discover-first inventory

This document defines the first-run product contract for the discover-first
workflow tracked by FLO-381. It is the source of truth for the follow-up
implementation work in global discovery, project discovery, inventory
persistence, recommendations, dashboard views, and release validation.

## First-run flow

The first successful experience should answer "what skills do I already have,
where are they installed, what differs, and what should I review next?" before
it asks the user to install or modify anything.

1. Install the binary.
2. Run a read-only inventory command:

   ```bash
   skills-manager discover --global
   ```

3. Review the summary: detected tools, global skill roots, skill counts,
   unmanaged skills, same-name drift, duplicate content, and coverage gaps.
4. Optionally approve project roots:

   ```bash
   skills-manager discover --projects ~/dev --save-project-roots
   ```

5. Reuse or revoke saved project roots later:

   ```bash
   skills-manager discover --saved-project-roots
   skills-manager discover --list-project-roots
   skills-manager discover --remove-project-root ~/dev
   ```

6. Open the local dashboard:

   ```bash
   skills-manager serve
   ```

7. Review deterministic recommendations and dry-run action plans before any
   write operation.

`discover` is read-only for tool and project directories. It may write only to
`~/.skills-manager` for local state, logs, saved approved roots, and inventory
snapshots.

## Consent boundaries

Discovery has three consent scopes.

| Scope | Example command | What can be read | Default behavior |
|---|---|---|---|
| Known global paths | `skills-manager discover --global` | Supported tool skill roots under the user's home directory | Allowed only after the user chooses `--global` or confirms an interactive prompt |
| Approved project roots | `skills-manager discover --projects ~/dev,~/work --save-project-roots` or `skills-manager discover --saved-project-roots` | Git repositories under the approved roots, with generated directories pruned | Allowed only for roots passed on the command line or saved earlier by the user |
| Broader scans | `skills-manager discover --projects ~/ --include-codex-workspaces` | Larger user-selected roots | Must be explicit and should show a warning before scanning |

The command must not run a full-home or arbitrary filesystem scan just because
no flags were provided. In non-interactive mode, missing scan scope is a usage
error.

Project discovery prunes noisy or generated areas by default:

- `.git`
- `node_modules`
- `.next`
- `dist`
- `build`
- `vendor`
- `.venv`
- `.cache`
- `.turbo`
- Codex scratch/workspaces under `Documents/Codex/.../work`

Those paths can be included only through an explicit flag designed for that
purpose.

Saved approved roots are manager-local consent state. They are stored under
`~/.skills-manager`, can be inspected with `--list-project-roots`, and can be
revoked with `--remove-project-root <root>`. A saved root is not scanned unless
the user explicitly passes `--saved-project-roots`. If a saved root no longer
exists, it is skipped and reported as a stale saved root instead of blocking the
rest of the scan.

## Inventory entities

The inventory model is fact-first. Recommendations and AI assessment are
separate layers that read inventory facts; they do not replace them.

### Tool

A supported tool or instruction harness that can own skill or rule locations.

Fields:

- `tool_id`: stable id such as `claude`, `codex`, `grok`, `cursor`,
  `gemini`, `antigravity`, `hermes`, `openclaw`, `agents`, or `agents_md`.
- `display_name`: human-readable name.
- `detected`: whether any known root or signal exists.
- `global_roots`: known global directories checked for this tool.
- `project_patterns`: project-local paths or files checked for this tool.
- `status`: `present`, `missing`, `unsupported`, or `unknown`.

Missing tools are coverage gaps, not command errors.

### Project

A git repository or approved directory that may contain project-local skills or
rules.

Fields:

- `project_id`: stable local id derived from normalized path and repository
  identity when available.
- `root_path`: local path. Human output may display it; exported artifacts
  should support path-prefix redaction.
- `repo_remote`: optional remote URL, redacted or omitted in export modes where
  repository identity is sensitive.
- `detected_tools`: tool ids with project-local findings.
- `last_scanned_at`: timestamp for the latest inventory pass.

### Skill installation

A concrete skill or rule file discovered on disk.

Fields:

- `installation_id`: stable id derived from path, scope, and tool.
- `skill_name`: directory name, frontmatter name, rule filename, or AGENTS.md
  label depending on source.
- `tool_id`: owning tool or harness.
- `scope`: `global` or `project`.
- `project_id`: present only for project-local findings.
- `source_path`: file or directory that contains the instruction content.
- `content_path`: exact file or directory hashed for content identity.
- `content_sha256`: exact content identity for the instruction file or skill
  directory.
- `content_size_bytes`: size of hashed content.
- `modified_at`: filesystem modification time when available.
- `managed`: whether the manager has manifest evidence that it created this
  installation.
- `ownership`: `manager`, `unmanaged`, or `unknown`.
- `format`: `skill_md`, `cursor_rule`, `github_instruction`, `agents_md`, or
  `unknown`.
- `present`: false when a previously persisted installation is no longer found.

### Content hash

`content_sha256` is exact identity only. It proves two discovered files have the
same bytes; it does not prove two different files are semantically equivalent.

Near-duplicate and similarity review must use an explicit diff or similarity
step after inventory, and the UI must label those findings as review signals,
not exact facts.

### Drift group

A group used for review.

Fields:

- `group_id`: stable local id.
- `group_type`: `same_name_different_hash`, `same_hash_different_name`, or
  `global_project_overlap`.
- `skill_name`: present for same-name groups.
- `content_sha256`: present for same-content groups.
- `installation_ids`: discovered installations in the group.
- `status`: `new`, `accepted`, `ignored`, or `resolved`.
- `review_reason`: optional user-supplied reason.

Same-name/different-hash is drift. Different-name/same-hash is duplicate
content. Global/project overlap is a coverage or override signal depending on
tool and project context.

### Recommendation

A deterministic proposal generated from inventory facts.

Fields:

- `recommendation_id`
- `kind`: `install_global`, `install_project`, `ingest`, `remove`,
  `ignore`, `review_drift`, or `needs_port`
- `confidence`: `high`, `medium`, or `low`
- `reason`: human-readable explanation tied to inventory facts
- `source_installation_ids`
- `target_tool_id`
- `target_project_id`
- `requires_plan`: always true for write-capable recommendations

Recommendations do not write files. They feed dry-run action plans.

## Reuse and replacement map

Current implementation pieces are useful, but they map unevenly to the
discover-first model.

| Current surface | Reuse | Replace or extend |
|---|---|---|
| `scan` | Reuse fingerprinting, global skill directory walking, ignore list, and ingest preflight concepts | `scan` remains ingest-oriented; `discover` should become the assessment-first command with stable inventory schema and explicit consent scopes |
| `status` | Reuse library, update, detected, watcher, and usage summaries | Add inventory snapshot status: last scan, detected tools, global/project counts, drift groups, and coverage gaps |
| `serve` | Reuse local HTTP server, static app, auth token, and CLI-run endpoint | Add inventory, drift, coverage, recommendations, action-plan, and review-state APIs |
| `detected` table | Reuse as legacy scan/ingest staging data | Add versioned inventory snapshot tables rather than overloading `detected` |
| `skills`, `projects`, `installs` tables | Reuse canonical library and manager-owned install state | Link discovered unmanaged installations to tools/projects and manager manifests |
| Manifest tracking | Reuse for ownership checks and rollback boundaries | Add global install ownership and action-plan audit records |

## Dashboard contract

The dashboard should open on an assessment-first view once inventory exists.

Required sections:

- Inventory
- Drift
- Global vs Project
- Tool Coverage
- Recommendations
- Actions

All write-capable controls must require a dry-run plan preview. The dashboard
must label deterministic facts separately from optional AI advisory output.

## Verification sample

For FLO-381, review this design against a real local sample by running a
read-only scan with an isolated manager home:

```bash
tmp_home="$(mktemp -d)"
SKILLS_MANAGER_HOME="$tmp_home" skills-manager scan --json \
  --paths="$HOME/.claude/skills,$HOME/.codex/skills,$HOME/.grok/skills"
rm -rf "$tmp_home"
```

The command may create state under the temporary manager home. It must not
write into the scanned global skill directories.

This sample intentionally uses the existing `scan` command only as current-state
evidence. It is not the final `discover` interface.
