# FLO-413 First-run setup wizard end-to-end QA

Date: 2026-06-03

## Scope

This QA validates that the first-run setup wizard in `skills-manager serve` is
intuitive and safe across the states that caused the earlier first-time-user
confusion (FLO-399). It exercises the wizard release slice built in FLO-407
through FLO-411 against the [setup wizard UX contract](SETUP_WIZARD.md).

The headline assertion, from the contract's fresh-user invariant: **fresh users
never land on an empty Overview** — they enter the guided wizard, while returning
users land on the dashboard.

Out of scope (matches the Linear issue): new product behavior beyond validating
the release slice.

## Fixture matrix

All fixtures use isolated `HOME` / `SKILLS_MANAGER_HOME` temp directories and the
binary built from this branch. The four [FLO-406 setup states](SETUP_WIZARD.md)
plus a load-error case were built read-only from already-persisted state:

| State | How it is built | Signals (A inventory / B managed / C open) |
|---|---|---|
| `no_discovery` | Empty manager home, discovery never run | ✗ / — / — |
| `discovered_unmanaged` | `discover --global` over one unmanaged skill | ✓ / ✗ / ✓ |
| `partially_managed` | Discovered inventory **plus** a seeded managed-library skill, with an open recommendation | ✓ / ✓ / ✓ |
| `completed` | Discovered inventory with every actionable recommendation reviewed | ✓ / — / ✗ |
| load error | A corrupt `state.db` (read fails) | treated as `no_discovery` + error banner |

## Automated results

### Fixture-matrix + landing invariant

New end-to-end test ties all four states into one matrix and asserts, over the
real `/api/v1/setup` endpoint, the derived state per fixture. It then checks the
fresh-user landing against the **real client routing predicate**
(`isFreshSetupState` parsed from the app.js served by the test server) rather
than a table duplicated in the test, so a routing change in app.js that left
`/api/v1/setup` unchanged still fails the invariant. It adds the
`partially_managed` endpoint case that no prior test exercised.

```sh
go test ./internal/cli/ -run TestSetupWizardFixtureMatrixAndLandingInvariant -v
```

Result: pass (4 subtests — one per state).

- `no_discovery` → wizard, and computing the status creates no `state.db`
  (read-only contract, FLO-407).
- `discovered_unmanaged` → wizard.
- `partially_managed` → dashboard.
- `completed` → dashboard.

### Full setup-wizard suite

```sh
go test ./internal/cli/ -run 'Setup|Wizard|DashboardAction|DashboardApply|DashboardBatch|Overview|Assessment|Projects|Matrix' -count=1
```

Result: pass. Coverage confirmed across the wizard slice:

- **No writes before confirmation.** Read-only GETs (`/setup`, `/overview`,
  `/assessment`, `/projects`, `/matrix`) never create the manager home or
  `state.db` on a fresh home; dry-run preview writes no skill files; `apply`
  without `confirm: true` is rejected (400) and writes nothing
  (`TestServeWizardReviewFixtureDryRunNoApply`,
  `TestDashboardActionsRequirePreviewConfirmAndRecordState`).
- **Apply tracking.** A confirmed apply of a selected action records review
  status `applied` plus an audit entry and writes the expected
  manifest/library/install; deferred actions are not applied
  (`TestDashboardApplySelectedActionCompletesSetup`,
  `TestDashboardApplySelectedIngestCompletesSetup`).
- **Failed actions do not complete setup.** A failed apply records review status
  `failed` with error detail and stays "open", so setup is not marked complete
  (`TestDashboardActionsRequirePreviewConfirmAndRecordState`,
  `TestSetupStatusFromAssessment` "a failed action stays open").
- **Tamper safety.** An apply whose submitted plan no longer matches the current
  dry-run plan is rejected (409).

## Browser verification

Verified against `skills-manager serve` driven headless (Playwright) with the
fixtures above. No console errors or warnings were observed on any page.

- **`no_discovery` (headline).** Lands on the **Setup wizard, step 1 of 5
  (Scope)** — not an empty Overview. Shows the scan-scope choices and the
  local-only disclosure ("Discovery is local-only … never changes skills on
  disk … Nothing is uploaded").
- **`discovered_unmanaged`.** Resumes the wizard at **step 3 of 5 (Review)** with
  Scope and Discover marked complete; recommendations are grouped by kind and the
  "nothing is written until you confirm on the Apply step" copy is shown.
- **`partially_managed`.** Lands on the **Overview dashboard** with a persistent
  "Resume setup" affordance, per the routing table.
- **Keyboard navigation & focus states.** The scope radios are keyboard
  focusable with a visible 2px focus outline; selecting a scope with the keyboard
  (Space) enables the previously-disabled "Start discovery" action.
- **Mobile layout.** At 390×844 there is no horizontal overflow; the sidebar
  collapses to a top bar and the main column is full width.
- **Empty state.** The fresh-user empty case is the guided wizard itself — a
  helpful next step, never a blank screen.
- **Error state.** A corrupt `state.db` returns `state: no_discovery` with
  `error: "file is not a database (26)"`; the UI shows the banner "Could not load
  setup status: … Showing first-run setup." and falls back to the safe first-run
  wizard rather than a blank screen.

## Acceptance criteria

| Criterion | Evidence |
|---|---|
| Isolated HOME fixtures for the four states | Fixture matrix + `TestSetupWizardFixtureMatrixAndLandingInvariant` |
| App does not open to a confusing empty Overview for fresh users | Browser: `no_discovery` → wizard step 1; routing invariant asserted in the matrix test |
| Keyboard nav, focus, mobile, loading, empty, error states | Browser verification section |
| No writes before explicit confirmation | Read-only GET tests + dry-run/no-apply + apply-requires-confirm tests |
| Apply tracking; failed actions do not mark setup complete | Apply-completion tests + failed-apply test + failed-stays-open derivation |
| Capture release command / browser verification notes | This document |

## Recommended release validation command

Run from a clean checkout of the merged branch:

```sh
make check
```

This runs `gofmt`, `go vet`, the full `go test ./...` suite (including the setup
wizard fixture matrix and apply-flow tests above), and a release build. For a
fast wizard-only re-check:

```sh
go test ./internal/cli/ -run 'Setup|Wizard|DashboardApply|DashboardAction' -count=1
```
