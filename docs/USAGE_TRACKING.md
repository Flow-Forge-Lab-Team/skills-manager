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

The OTEL receiver gives low-friction, cross-session capture but cannot say
*which project* a skill ran in. The PreToolUse hook fills that gap: it runs in
the project directory and sees the working directory, so it attributes the
project slug. Run both for the most complete matrix.

### Deduplication

Running both feeds does **not** double-count. The OTEL `tool_result` event and
the PreToolUse hook carry the same `tool_use_id`, which is recorded with each
row and constrained by a unique index. The hook fires first (before the tool
runs) and wins, so the surviving row keeps the project attribution; the later
OTEL row for the same activation is ignored. Rows without a `tool_use_id`
(manual or watcher entries) are exempt from the constraint and never collide.

> A note on the spec: earlier design notes referenced a `claude_code.skill_activated`
> OTEL event with an `invocation_trigger` attribute. Claude Code does not emit
> that event. Skill activation is instead observable on the real
> `claude_code.tool_result` event (`tool_name="Skill"`, with `skill_name` in the
> tool parameters when `OTEL_LOG_TOOL_DETAILS=1`). The receiver parses that real
> event and also accepts a `skill_activated` event name for forward-compatibility.

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

The `trigger` is taken from an explicit `invocation_trigger`/`trigger`
attribute when present; otherwise it is inferred as `nested` for
subagent-issued events and `user-initiated` for everything else.

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
