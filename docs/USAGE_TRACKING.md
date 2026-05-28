# Usage tracking

Recording which skills actually get used, per project and harness, so the
Matrix view can show real activity instead of just what is installed. Usage
rows land in the `invocations` table (`skill_name × project_slug × harness ×
trigger`) and are aggregated by `skills-manager usage`.

## The two feeds

There is no daemon. Usage is fed by two opt-in sources, either or both:

| Source | Command | Project attribution | Harness coverage |
| --- | --- | --- | --- |
| Claude Code OTEL | `skills-manager usage receiver` | No (OTEL carries no cwd) | Claude Code |
| PreToolUse hook | `skills-manager usage hook` | Yes (from the hook `cwd`) | Claude Code |

The two feeds are **complementary**, with the hook as the primary counter:

- The **OTEL receiver** parses `claude_code.skill_activated` — the canonical
  event that fires for both Skill-tool calls and `/`-command invocations and
  carries the real `invocation_trigger`. But OTEL has no working directory, so
  it cannot attribute a project.
- The **PreToolUse hook** runs in the project directory, so it supplies the
  project slug. It only sees the Skill *tool*, not `/`-commands, and cannot
  observe the trigger.

Run both: the hook gives you per-project counts, OTEL enriches each row with the
trigger and additionally captures `/`-command invocations the hook never sees.

### How the feeds merge (no double-counting)

`skill_activated` has no `tool_use_id`, but the matching Skill `tool_result`
event does, and the two share a `prompt.id` + skill name. The receiver bridges
them: a `skill_activated` adopts its `tool_result`'s `tool_use_id` (and the
standalone `tool_result` is then dropped, so the activation is counted once).
The PreToolUse hook carries the same `tool_use_id`. Rows are merged on it by a
partial unique index: **project comes from the hook, trigger comes from OTEL**,
and the result is one enriched row regardless of which feed arrives first. Rows
without a `tool_use_id` (a `/`-command, or manual/watcher entries) are exempt
and counted independently.

Because the bridge correlates events within an export batch, a `skill_activated`
and its `tool_result` that land in different OTEL flushes (rare; both occur
within one tool execution and the default flush is 5s) may not merge and could
count twice against the hook. This is a best-effort triage signal, not billing.

## OTEL receiver

```
skills-manager usage receiver --port 4318
```

Then point Claude Code at it:

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=http/json
export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://127.0.0.1:4318/v1/logs
export OTEL_LOG_TOOL_DETAILS=1   # required so skill names appear on tool events
```

The receiver speaks OTLP/HTTP JSON. It accepts a POST of an
`ExportLogsServiceRequest` at `/v1/logs`, extracts skill activations, writes
them with `source = otel`, and returns `{}` (all-accepted).

`OTEL_LOG_TOOL_DETAILS=1` is required: without it Claude Code reports
user-defined and third-party skill names as the placeholder `custom_skill`. The
`invocation_trigger` values (`user-slash`, `claude-proactive`, `nested-skill`)
are normalized to the table's taxonomy (`user-initiated`, `proactive`,
`nested`).

## PreToolUse hook (recommended for project attribution)

```
skills-manager usage hook --print-config
```

Add the printed snippet to your Claude Code `settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Skill",
        "hooks": [
          { "type": "command", "command": "skills-manager usage hook" }
        ]
      }
    ]
  }
}
```

On every `Skill` tool call, Claude Code pipes the hook payload to
`skills-manager usage hook`, which records one invocation with `source = hook`
and the project slug derived from the hook `cwd`. The hook is best-effort: any
error is logged to stderr and it still exits 0, so it can never block a skill.

## Viewing usage

```
skills-manager usage            # human table
skills-manager --json usage     # structured matrix (also served at /api/v1/usage)
```

`skills-manager usage setup` prints both setup paths in one place.
