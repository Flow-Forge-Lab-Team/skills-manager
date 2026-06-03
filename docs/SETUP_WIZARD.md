# First-run setup wizard UX contract

This document defines the guided first-run setup experience for
`skills-manager serve` before implementation. It is the source of truth for the
follow-up work: setup status API (FLO-407), wizard shell and routing (FLO-408),
scan-scope and discovery step (FLO-409), recommendation review and dry-run
preview (FLO-410), confirmation/apply/completion (FLO-411), onboarding docs and
terminology (FLO-412), and end-to-end QA (FLO-413).

It builds on [DISCOVERY.md](DISCOVERY.md), which defines the discover-first
inventory model, consent scopes, and entity schema. This contract adds the
**first-run setup layer** on top of that model: the state machine, where users
land, the step sequence, and a shared vocabulary.

## Scope

In scope (this is a contract, not an implementation):

- The setup **states** the UI must recognize.
- The **default landing** decision for each state.
- The wizard **step sequence**, primary actions, progress, back/next, and exit
  behavior.
- A consistent **user-facing vocabulary**.
- A **decision record** for where setup lives in the app.

Out of scope (deferred to the implementation issues):

- Implementing any UI or API change.
- Changing CLI command names. The contract reuses existing commands
  (`discover`, `scan`, `assess`, `plan`, `add`, `install`) and existing dashboard
  endpoints; it does not propose new command names.

## What exists today, and the gap

Grounded against the live `skills-manager serve` experience on `main` and the
prior first-time-user critique (FLO-399 Impeccable review).

- The dashboard opens on **Overview** (`currentView = "overview"`): stat cards
  (Library skills, Projects, Pending updates, Unregistered), watcher alerts, and
  recent activity. For a fresh user this screen is almost entirely empty.
- The discover-first assessment lives in the **Discover** tab, fed by
  `/api/v1/assessment`, with tabs Inventory, Drift, Global vs Project, Tool
  Coverage, Recommendations, Actions (per the [DISCOVERY.md](DISCOVERY.md)
  dashboard contract).
- The dashboard reads a **persisted** inventory. It does not run `discover`
  itself; a user must first run `skills-manager discover --global` from the CLI.
- Write-capable actions already require a precomputed dry-run plan plus explicit
  confirmation (`/api/v1/actions/` `plan` → `apply` with `confirm: true`, with an
  audit entry).

The prior first-time-user critique (FLO-399) found that **empty states
under-explain how a user gets from zero to a useful state**, and that a fresh
user's first meaningful screen is an empty Overview. Nothing routes a first-run
user through discover → review → apply.

**The gap this wizard closes:** a first-run user should never land on an empty
Overview. They should be guided, in order, to inspect what they already have,
review what is recommended, and apply only what they explicitly confirm.

## Terminology

Canonical user-facing terms. Use these consistently across the web UI, README,
tutorial, and CLI help (FLO-412). Each term lists what it is and, where it is
easily confused, what it is **not**.

| Term | Meaning | Surface | Not |
|---|---|---|---|
| **Discover** | The read-only, consented inspection that builds your inventory: which skills you have, where, their ownership, drift, duplicates, and coverage gaps. Writes only to `~/.skills-manager`. | `skills-manager discover`; the Discover tab / `/api/v1/assessment` | Not a write operation; does not install or modify skills. |
| **Scan** | The ingest-oriented pass that finds candidate skills to add to your library. | `skills-manager scan`, `scan --auto-ingest` | Not a synonym for *discover*. First-run copy says "discover" for read-only inspection; "scan" is reserved for the ingest path. |
| **Assess** | Optional AI advisory commentary on a skill, run only on explicit request. Never automatic, never required to finish setup. | `skills-manager assess … --handoff` or `--auto` | Not part of deterministic inventory or recommendations; always labeled separately. |
| **Recommendations** | Deterministic proposals derived from inventory facts. Kinds: `ingest`, `install_global`, `install_project`, `review_drift`, `ignore`, `remove`, `needs_port`. They feed dry-run plans; they never write. | Recommendations tab; recommendation engine | Not AI output. AI advisory is shown in a separate, labeled area. |
| **Managed** | A skill the manager created and owns (manifest evidence). Ownership `manager`. Only managed paths are removed on uninstall. | Inventory "managed" badge; manifests | Not every skill on disk — most discovered skills start unmanaged. |
| **Unmanaged** | A skill found on disk that the manager did not create. Ownership `unmanaged` or `unknown`. A candidate to **ingest**, never modified or removed without an explicit dry-run plan and confirmation. | Inventory "unmanaged" badge | Not "unknown to the app"; it is discovered, just not owned. |
| **Dry-run** | A previewed action plan listing the exact files to create, update, preserve, skip, or remove, with **no** filesystem writes. Required before any write-capable action. | `skills-manager plan`; Actions "Preview plan" | Not a partial apply; nothing is written. |
| **Apply** | Executing a previously previewed dry-run plan **after explicit confirmation** — the only step that writes to disk for a recommendation. | Actions "Apply" (`/api/v1/actions/` `apply`, `confirm: true`); on the CLI, the underlying write commands (`add`, `install`) after `plan` | Not a CLI command named `apply`. "Apply" is the conceptual/UI verb for committing a confirmed plan. |

Supporting terms reused from [DISCOVERY.md](DISCOVERY.md): **ingest** (`add`,
copy an unmanaged skill into your managed library), **library** (your managed
collection), **scope** (`global` vs project-local), **drift** (same name,
different content), **duplicate content** (same content, different name),
**overlap** (the same skill global and project-local), **coverage gap** (a
detected tool missing a skill it could load).

**Review state** of a recommended action (persisted in
`dashboard_action_reviews`, surfaced as badges): `new` → `accepted` | `ignored`
→ `applied` | `failed`.

## Setup states

The UI recognizes four setup states. They are derived **read-only** from
already-persisted state — discovery snapshots, managed library/install counts,
and action review state — and computing them must never write to disk or run an
action (the FLO-407 contract).

Three signals drive the state:

- **A — Inventory exists:** a discovery snapshot is persisted (`discover` has
  completed at least once; `/api/v1/assessment` has a `generated_at` and ≥1
  installation).
- **B — Managed library exists:** the manager owns ≥1 skill (a non-empty managed
  library and/or ≥1 manager-owned install).
- **C — Open actions remain:** ≥1 actionable recommendation is still in review
  state `new` (not yet accepted, ignored, or applied).

| State | A | B | C | Meaning |
|---|:--:|:--:|:--:|---|
| **`no_discovery`** | ✗ | — | — | Discovery has never run. We cannot yet say what the user has. |
| **`discovered_unmanaged`** | ✓ | ✗ | ✓ | Inventory exists and surfaces actionable recommendations, but the manager owns nothing yet. |
| **`partially_managed`** | ✓ | ✓ | ✓ | The manager owns some skills, but open recommendations remain unreviewed. Setup began but is not finished. |
| **`completed`** | ✓ | — | ✗ | Inventory exists and no actionable recommendation is left in `new`. Nothing requires first-run attention. |

Notes:

- `completed` does not strictly require a managed library: if discovery surfaced
  no actionable recommendations (everything already covered, or only ignorable),
  the user is effectively done. B is therefore "don't care" for `completed`.
- A user who managed skills via the CLI without ever running `discover` reads as
  `no_discovery` for the assessment surface; routing still sends them to build an
  inventory first, because the assessment cannot guide review without it.
- Error/empty cases must be explicit enough for the UI to show a helpful next
  step rather than a blank screen (the FLO-407 contract). An assessment that
  fails to load is treated as `no_discovery` with an error banner, not as an
  empty success.

## Default landing and routing

The core invariant, from the first-time-user critique:

> **Fresh users — `no_discovery` and `discovered_unmanaged` — never land on an
> empty dashboard. They enter the setup wizard. Returning users with a useful,
> non-empty dashboard (`partially_managed`, `completed`) land on the dashboard.**

Overview and Discover are status-driven entry points: on load they read setup
status and either render normally or hand off to the wizard.

| State | Default landing | Setup surfacing |
|---|---|---|
| `no_discovery` | **Wizard — Step 1 (Scope)** | Wizard is the first screen. |
| `discovered_unmanaged` | **Wizard — Step 3 (Review)** | Skip scope/discover; resume at review. |
| `partially_managed` | **Dashboard (Overview)** | Persistent, dismissible "Resume setup" affordance; wizard resumes at the first incomplete step. |
| `completed` | **Dashboard (Overview)** | No first-run surfacing. Discovery is re-runnable from the Discover tab. |

Rationale for `partially_managed` landing on the dashboard rather than trapping
the user in the wizard: once the manager owns skills, the dashboard is
meaningful and the user has signaled they know what they are doing. Finishing
must stay one click away, but it should not block the dashboard.

## Wizard flow

A linear five-step flow with a persistent stepper. Steps 2 (Discover) and 4
(Apply) are **operation** steps that can be in progress; the others are
**review** steps.

| # | Step | Primary action | Advance when |
|---|---|---|---|
| 1 | **Scope** | Choose what to inspect: global skills, the current project, or both. Disclose that scanning is local-only and which paths may be read. → **Start discovery** | A scope is chosen and discovery is started. |
| 2 | **Discover** | Run read-only discovery (or guide the user to `skills-manager discover` and offer Refresh). Show progress, then a summary: detected tools, global/project skills, unmanaged count, drift, duplicates, coverage gaps. → **Review recommendations** | A discovery snapshot exists. |
| 3 | **Review** | Show recommendations grouped by kind (`ingest`, `install_global`, `install_project`, `review_drift`, `ignore`, no-action). Each explains *why* and whether the skill is discovered, unmanaged, or managed. Select actions and preview their dry-run plans (exact files). → **Continue to apply** | Selected actions have a previewed dry-run plan, or the user selects none. |
| 4 | **Apply** | Require explicit confirmation after dry-run. Apply only selected actions, showing each plan's exact file changes. Report per-action success/failure. → **Apply selected** | The apply pass resolves (partial success allowed). |
| 5 | **Done** | Summarize what was applied, ignored, and failed; record completion. → **Go to dashboard** | — (terminal) |

### Progress behavior

- The stepper shows all five steps; the current step is highlighted and
  completed steps are checked.
- Operation steps (Discover, Apply) show an in-progress state and **block
  forward navigation until the operation resolves**. Progress never advances past
  an unfinished discovery or apply.
- On entry the wizard maps setup status to a starting step (see routing table)
  and marks earlier steps complete.

### Back / Next behavior

- **Back** is always available on steps 2–5 and is **non-destructive**: it
  preserves the chosen scope, the inventory snapshot, action selections, and any
  already-applied state.
- **Next** is contextual (its label is the step's primary action). Forward
  navigation past an unrun operation step is disabled until that operation runs.
- Re-running discovery is explicit (a button on Step 2); it is never triggered
  automatically by navigation.
- Navigation is keyboard-accessible with visible focus (carry over the FLO-399
  accessibility fixes: focusable controls, arrow/Home/End where a tablist is
  used, visible focus states).

### Exit / cancel behavior

- **Cancel / Exit** is available on every step and returns the user to the
  dashboard (Overview).
- Exit is **safe and non-destructive**: it never rolls back already-applied
  changes (they are real, audited filesystem writes) and never discards the
  persisted inventory snapshot. Only ephemeral, un-applied selections are
  dropped.
- Exiting mid-flow leaves the user in whatever state their persisted data
  implies (`discovered_unmanaged` or `partially_managed`); re-entry recomputes
  status and resumes at the right step.
- If an apply is in progress, the wizard lets it settle before exiting so the
  audit trail stays consistent.

### Reload and resume

- On reload, recompute setup status from persisted state and resume at the first
  incomplete step. Never auto-run discovery and never auto-apply.
- Ephemeral selections reset to the persisted review state; applied and
  ignored statuses survive because they are persisted (the FLO-408
  "reloads without corrupting saved discovery/action data" requirement).

### Error handling

- **Discovery error:** show the error with a Retry; stay on Step 2; do not
  fabricate inventory or advance.
- **Plan (dry-run) error:** show the error on that action; do not advance to
  Apply for it.
- **Apply failure:** record review state `failed` with the error detail, show an
  actionable message, and **do not mark setup complete**. Other selected actions
  may still succeed; failures are retryable or repairable manually.

## Decision records

### DR-1 — Setup is its own wizard state, with Overview and Discover as entry points

**Decision.** First-run setup is a dedicated wizard surface (its own
view/state), not content embedded in the Overview or Discover tabs. Overview and
Discover act as **status-driven entry points**: on load they read setup status
and either render normally or route into the wizard.

**Alternatives considered.**

- *Inline in Overview.* Make Overview adaptive: render the guided flow when the
  dashboard is empty. Rejected: a multi-step flow with a progress indicator,
  back/next, and exit semantics conflicts with a persistent dashboard surface;
  it muddies focus management and makes "where am I" ambiguous.
- *Inline in Discover.* Add a guided mode to the assessment tab. Rejected for the
  same reason, and because Discover is also the steady-state assessment view a
  returning user needs unobstructed.

**Consequences.**

- A stable wizard shell owns step layout, progress, back/next, and exit (FLO-408).
- The wizard **reuses** existing discover/assessment data and the
  `/api/v1/actions/` plan/apply endpoints — it does not duplicate that logic.
- The dashboard remains the destination after completion; the wizard is a
  transient, resumable overlay/route, not a fourth data surface.
- Routing is read-only and client-driven off the FLO-407 setup status; no new
  CLI command and no write occurs to decide where a user lands.

### DR-2 — Name it "Setup," distinct from the CLI wizards

The first-run web flow is **Setup** / **first-run setup**. Do not call it
`init`: `skills-manager init` is the CLI project-categorization wizard and
`skills-manager setup-schedule` is the OS scheduling wizard. Keeping the names
distinct avoids conflating three different "wizards."

### DR-3 — Read-only status; never act without explicit confirmation

Setup status is computed read-only from persisted state (FLO-407: no filesystem
writes, no action execution while computing status). The wizard never runs
`discover` or `assess`, and never applies a recommendation, without an explicit
user action, a dry-run preview, and confirmation. This preserves the local-first,
safety-first framing.

### DR-4 — "Discover" for read-only inspection; "scan" stays ingest-only; "assess" stays optional

First-run copy uses **discover** for the read-only inventory step and reserves
**scan** for the ingest-oriented `scan` command, so users are not told to "scan"
when the safe, read-only action is `discover`. **Assess** (AI advisory) is always
optional and labeled separately; it is never on the critical path to completing
setup.

## Traceability

| Issue | Consumes from this contract |
|---|---|
| FLO-407 | The four states and their read-only derivation signals (A/B/C). |
| FLO-408 | Wizard shell, stepper, back/next, exit, and the routing table / fresh-user invariant. |
| FLO-409 | Step 1 (Scope) and Step 2 (Discover): scan-scope choices and local-only disclosure. |
| FLO-410 | Step 3 (Review): recommendation grouping by kind and dry-run preview. |
| FLO-411 | Step 4 (Apply) and Step 5 (Done): confirm-after-dry-run, partial success, completion recording, failure handling. |
| FLO-412 | The terminology table and DR-4 wording rules. |
| FLO-413 | The four states as the QA fixture matrix and the fresh-user invariant as the headline assertion. |

## Verification

This contract was reviewed against:

- **The live `skills-manager serve` experience** on `main`: default landing
  (Overview), the eight sidebar views, the Discover/assessment tabs and their
  empty-state copy, and the existing `/api/v1/actions/` dry-run/apply/audit flow
  and `dashboard_action_reviews` status vocabulary.
- **The prior first-time-user critique** (FLO-399 Impeccable review): the
  empty-Overview first screen and under-explained empty states, which this
  contract's fresh-user invariant and guided step sequence directly address.
- **[DISCOVERY.md](DISCOVERY.md)**: the discover-first model, consent scopes,
  recommendation kinds, and ownership semantics, reused verbatim here.

No code or API was changed; the implementation lands in FLO-407 through FLO-413.
