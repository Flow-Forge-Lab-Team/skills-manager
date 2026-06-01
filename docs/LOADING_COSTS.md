# Skill loading cost model

This document is the recommendation-engine source of truth for deciding whether
an inventory finding should be suggested as a global install, project-local
install, rule, or explicit review item. It captures current research for
FLO-386; recommendation code should treat unknown entries as low-confidence
until a validation test closes the gap.

## Classification vocabulary

| Classification | Meaning for recommendations |
|---|---|
| `always-loaded` | Full instruction text is normally added to model context without a specific task trigger. Keep global content short and generic; prefer project scope for stack-specific rules. |
| `requested/on-demand` | The tool sees a compact catalog or metadata first, then loads full instructions only when the user or model invokes the skill. Global installs are acceptable when the skill is broadly useful and low risk. |
| `project-scoped` | The tool limits the instruction or skill to a repository, workspace, project, folder, or agent. Prefer this for team workflow, repo conventions, deployment steps, and high-token reference material. |
| `global-scoped` | The tool applies a skill or rule across all or many projects for one user or machine. Reserve for personal preferences, general workflows, or explicit utilities. |
| `unknown` | Behavior is not documented or not verified. Do not make automatic global recommendations without a validation note. |

## Tool behavior matrix

| Tool or harness | Evidence-backed behavior | Loading classification | Recommendation guidance | Unknowns and validation tests |
|---|---|---|---|---|
| Claude Code skills | Claude Code supports personal `~/.claude/skills/<name>/SKILL.md` and project `.claude/skills/<name>/SKILL.md`. Skill descriptions are available for automatic matching; full `SKILL.md` content enters context when invoked and persists for the session. Sources: [Claude skills docs](https://docs.claude.com/en/docs/claude-code/skills). | `requested/on-demand`, `project-scoped`, `global-scoped` | Recommend global Claude skills for reusable personal workflows with concise descriptions. Recommend project skills for repo build/test/deploy procedures, tool permissions, or anything that should be reviewed before trusting a repo. Avoid stuffing large reference docs into `SKILL.md`; put them in referenced files. | Validate same-name precedence and compaction behavior with a small pair of global/project skills before relying on automated de-dupe advice. |
| Codex skills | Codex starts with each skill name, description, and file path, then loads full `SKILL.md` only when selected. The initial skills list has a context budget and may shorten descriptions or omit skills when the set is large. Codex reads repo skills from `.agents/skills` up the path to repo root, user skills from `$HOME/.agents/skills`, admin skills from `/etc/codex/skills`, and bundled system skills. Sources: [Codex skills docs](https://developers.openai.com/codex/skills). | `requested/on-demand`, `project-scoped`, `global-scoped` | Prefer `.agents/skills` for repo workflows that should travel with the codebase. Prefer `$HOME/.agents/skills` for compact personal utilities. Do not assume a global Codex skill is free: a large global skill set consumes the initial catalog budget and can hide lower-priority skills. | Existing manager inventory also scans legacy `~/.codex/skills` and `.codex/skills`. Keep those as discovered facts, but recommendation code should prefer the documented `.agents/skills` locations until a compatibility migration decision is made. |
| Codex `AGENTS.md` | Codex concatenates global `~/.codex/AGENTS.md` plus project `AGENTS.md` files from repo root down to the current directory, with later/deeper files overriding earlier guidance. Project docs are capped by `project_doc_max_bytes`, 32 KiB by default. Sources: [Codex AGENTS.md guide](https://developers.openai.com/codex/guides/agents-md). | `always-loaded`, `project-scoped`, `global-scoped` | Keep global `AGENTS.md` short: personal operating preferences only. Put stack-specific rules, test commands, and repo safety rules in project or nested `AGENTS.md`. Recommend assembly only when generated content remains concise and reviewable. | Add a local smoke test that asks Codex to summarize instructions from global, root, and nested `AGENTS.md` files and records byte-limit behavior. |
| Cursor rules | Cursor rules are prompt-level persistent context. User Rules are global and always applied. Project Rules live in `.cursor/rules` and support `Always`, `Auto Attached`, `Agent Requested`, and `Manual` application modes. Sources: [Cursor rules docs](https://docs.cursor.com/en/context). | `always-loaded`, `requested/on-demand`, `project-scoped`, `global-scoped` | Avoid recommending broad global Cursor rules except for short personal preferences. Prefer project `.cursor/rules/*.mdc` with `Agent Requested`, `Auto Attached`, or `Manual` unless a rule is truly always relevant. Compile from SKILL.md only when the target rule type is explicit. | Validate nested `.cursor/rules` precedence in a sample repo before generating cross-directory recommendations. |
| Grok skills and `AGENTS.md` | Local install docs at `~/.grok/README.md` say Grok appends discovered agent rule files to the system prompt, scanning `~/.grok/` and then repo-root-to-cwd files. The same docs list skill roots in priority order: `./.grok/skills/`, `<repo_root>/.grok/skills/`, `~/.grok/skills/`, and `~/.claude/skills/`. Skills can be listed or injected with `/skills`, and the model can invoke them automatically from the `description`. | Rules: `always-loaded`; skills: `requested/on-demand`, `project-scoped`, `global-scoped` | Treat Grok rule files as always-on cost. Recommend project Grok skills for repo-specific workflows and global Grok skills for compact reusable workflows. Be cautious about recommending duplicate Claude/Grok global installs because Grok can also see `~/.claude/skills`. | Replace local-doc evidence with an official public URL if one becomes available. Test whether Grok includes only skill metadata initially or any full skill bodies before automatic invocation. |
| Gemini CLI `GEMINI.md` | Gemini CLI loads `~/.gemini/GEMINI.md`, project ancestor `GEMINI.md` files, and subdirectory `GEMINI.md` files, concatenating all found context and sending it with every prompt. It can be configured to use other filenames such as `AGENTS.md`. Sources: [Gemini CLI context docs](https://google-gemini.github.io/gemini-cli/docs/cli/gemini-md.html). | `always-loaded`, `project-scoped`, `global-scoped` | Keep global Gemini context minimal. Recommend project or subdirectory files for repo-specific conventions. When recommending AGENTS.md reuse for Gemini, include the required `context.fileName` configuration rather than assuming default support. | Validate import expansion cost for large `@file.md` trees before recommending assembled context files. |
| Antigravity skills and rules | Antigravity skills use progressive disclosure: the agent starts with names and descriptions, then reads full `SKILL.md` when relevant. Workspace skills live in `.agents/skills`; global skills live in `~/.gemini/antigravity/skills`. Antigravity rules can be global in `~/.gemini/GEMINI.md` or workspace-scoped in `.agents/rules`, with activation modes including Manual, Always On, Model Decision, and Glob. Sources: [Antigravity skills docs](https://antigravity.google/docs/skills), [Antigravity rules docs](https://antigravity.google/docs/ide-rules). | Skills: `requested/on-demand`; rules: `always-loaded` or conditional; `project-scoped`, `global-scoped` | Recommend global Antigravity skills only for general utilities. Prefer `.agents/skills` for project workflows. Prefer `.agents/rules` with conditional activation for repo rules; reserve global `GEMINI.md` for short personal preferences. | Validate the current default between `.agents/skills` and legacy `.agent/skills` before writing install plans. |
| OpenClaw skills | OpenClaw loads skill roots in a documented precedence order, including workspace `skills`, workspace `.agents/skills`, personal `~/.agents/skills`, managed `~/.openclaw/skills`, bundled skills, and extras/plugins. It builds a compact XML prompt block for eligible skills with deterministic per-skill token impact, and loads full behavior through the eligible skill catalog and slash-command model. Sources: [OpenClaw skills docs](https://docs.openclaw.ai/tools/skills). | Catalog is `always-loaded`; skill execution is `requested/on-demand`; `project-scoped`, `global-scoped` | Recommend workspace `skills` or `.agents/skills` for agent/project-specific workflows. Recommend `~/.openclaw/skills` only for shared local utilities that justify catalog overhead. Factor OpenClaw's per-skill prompt cost into global recommendations. | Validate whether manager-installed `skills/` should target OpenClaw only or remain shared with Hermes in mixed projects. |
| Hermes | Current public evidence is incomplete. Local inventory shows `~/.hermes/skills/<name>/SKILL.md` and some grouped skill folders, but there is no verified local CLI binary or local official README in this environment. | `unknown`, likely `requested/on-demand`, `global-scoped` | Do not make automatic global Hermes recommendations from discovery facts alone. Present Hermes findings as inventory and ask for validation before install or migration advice. | Run a Hermes session in a controlled repo with one global and one project `skills/` entry, then ask it to list visible skills and invoke one. Record roots, precedence, and whether full skill content is initially loaded. |
| AGENTS.md-style shared instructions | The AGENTS.md standard defines Markdown instructions for coding agents and recommends root and nested files for monorepos. It is a shared format, not one universal loading implementation. Sources: [AGENTS.md](https://agents.md/). | Usually `always-loaded`, but tool-specific | Recommend AGENTS.md when the same short project guidance should apply across multiple agents. Do not assemble long skill bodies into AGENTS.md by default; use links or tool-specific on-demand skills for detailed workflows. | Recommendation code must branch by target tool because conflict resolution, byte caps, and supported filenames differ. |

## Recommendation rules

Global recommendations are appropriate when all of these are true:

- The target tool uses progressive disclosure or a compact catalog, not full
  always-loaded text.
- The skill is useful across many repositories.
- The `description` is short, specific, and safe to expose in every session.
- The skill does not encode project credentials, internal deployment paths,
  repo-specific commands, or broad tool permissions.
- The global location is documented for that tool, or the recommendation is
  explicitly marked as a compatibility/legacy finding.

Project-local recommendations are preferred when any of these are true:

- The guidance is tied to one repo, service, product, customer, deployment
  flow, test suite, or design system.
- The instruction body is large or references large supporting files.
- The target tool treats the content as always-loaded.
- The skill requires trust in a repository before granting tools, scripts, or
  permissions.
- The skill should be versioned and reviewed with the project.

Always-loaded instructions should be treated as a scarce resource:

- Keep global always-loaded files to personal defaults and working agreements.
- Put project-specific commands and safety rules in repo or nested scope.
- Prefer conditional rule modes when the tool supports them.
- Never recommend copying every discovered skill into AGENTS.md, GEMINI.md, or
  an always-on Cursor/Antigravity rule.

For unknown tools or unverified roots:

- Preserve the inventory fact.
- Mark recommendation confidence `low`.
- Prefer `review_drift`, `needs_port`, or "validate first" over
  `install_global`.
- Attach the validation test from the matrix to the recommendation reason.

## Research evidence notes

- Current docs were checked on 2026-06-01.
- Codex and Cursor behavior were verified against current public docs.
- Grok behavior is based on the locally installed `~/.grok/README.md` because a
  public canonical page was not found during this pass.
- Hermes remains intentionally unknown until an executable or official docs can
  be tested.
