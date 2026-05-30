# Scheduling

Running `skills-manager check` on a schedule. v1 supports local OS scheduling
only. Cloud schedulers are deliberately deferred until the local workflow has
proved useful.

## The default position

**The manager does not run a daemon.** It uses external schedulers.

Supported v1 modes:

1. **Manual** — user runs `skills-manager check` when they want. Always works.
2. **Local OS scheduler** — launchd / cron / systemd / Task Scheduler. Free,
   inspectable, runs only when the machine is on.

Deferred beyond v1:

- Claude Code routines
- Codex automations
- Any cloud scheduler that needs remote git writeback, provider billing, OAuth
  state, webhooks, or hosted result storage

## What gets scheduled

A single command:

```bash
skills-manager check --non-interactive --quiet --json
```

This polls sources, updates `state.db`, writes logs, and exits. It does not
accept updates and does not generate summaries unless the user explicitly adds
that follow-up.

Optional follow-up when a local provider is configured:

```bash
skills-manager check --non-interactive --quiet --json &&
skills-manager summarize --pending --auto --non-interactive
```

This is useful only when `llm.provider` is configured. Direct API providers
also need `llm.api_key-env`; CLI providers use the user's existing local CLI
authentication, and the Cursor CLI provider runs headlessly with `--trust` in
ask mode.

## Setup wizard

`skills-manager setup-schedule` walks the user through local scheduling:

```
$ skills-manager setup-schedule

How should the skill check run?

  [l] Local OS scheduler (launchd on macOS)
      ✓ No OAuth, no cloud account
      ✓ Free
      ○ Only runs when this machine is awake
      ○ AI summaries require a configured local provider

  [m] Manual only (no schedule)
      ✓ You run `skills-manager check` when you want

Choose:
```

The wizard should say plainly when local scheduling cannot run because the
machine is asleep. `status` detects missed runs later.

## Local OS scheduling

### macOS (launchd)

The wizard writes `~/Library/LaunchAgents/com.skills-manager.daily-check.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.skills-manager.daily-check</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/skills-manager</string>
        <string>check</string>
        <string>--non-interactive</string>
        <string>--quiet</string>
        <string>--json</string>
    </array>
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key><integer>9</integer>
        <key>Minute</key><integer>0</integer>
    </dict>
    <key>StandardOutPath</key>
    <string>~/.skills-manager/logs/check.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>~/.skills-manager/logs/check.stderr.log</string>
</dict>
</plist>
```

Then runs:

```bash
launchctl load ~/Library/LaunchAgents/com.skills-manager.daily-check.plist
```

### Linux (cron)

```cron
0 9 * * * /usr/local/bin/skills-manager check --non-interactive --quiet --json >> ~/.skills-manager/logs/check.log 2>&1
```

Writes to `~/.skills-manager/cron-fragment` and installs via `crontab`.

### Linux (systemd)

User-level systemd timer:

```ini
# ~/.config/systemd/user/skills-manager-check.timer
[Unit]
Description=skills-manager daily check

[Timer]
OnCalendar=*-*-* 09:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

```ini
# ~/.config/systemd/user/skills-manager-check.service
[Unit]
Description=skills-manager daily check

[Service]
Type=oneshot
ExecStart=/usr/local/bin/skills-manager check --non-interactive --quiet --json
```

Then:

```bash
systemctl --user enable --now skills-manager-check.timer
```

### Windows (Task Scheduler)

PowerShell script that registers a scheduled task via `Register-ScheduledTask`.

### Removal

`skills-manager unschedule` reverses whichever local scheduler was set up.

## What happens if scheduling stops working

Machines sleep, cron environments miss PATH entries, and launch agents can be
unloaded. The manager detects stale runs:

```
$ skills-manager status

⚠ Scheduled check hasn't run in 4 days (expected daily).
  Last successful: 2026-05-18 09:00
  Possible causes:
    • Machine was off
    • Local scheduler disabled
    • Binary path changed

  Run manually: skills-manager check
  Diagnose: skills-manager doctor
```

Threshold: 2x the configured frequency. If schedule is daily, alert after two
missed days.

## Headless mode requirements

Every command that scheduling might invoke must work without a TTY:

- Never prompts; if input is needed, fail with exit code 2
- Writes structured logs
- Returns useful exit codes
- Supports `--json` output

Commands the scheduler is allowed to call in v1:

```bash
skills-manager check --non-interactive --quiet --json
skills-manager summarize --pending --auto --non-interactive  # only if provider configured
```

`scan --auto-ingest` and watcher-driven ingest are not scheduled in v1. They can
create catalog changes without an immediate human review loop.

## Why no manager daemon

Some skills-manager tools run their own background process. We do not by
default, for these reasons:

- Cross-platform complexity: reliable daemons differ across macOS, Linux, and Windows
- Resource consumption: users do not need a resident process for occasional checks
- Inspection and debugging: a timer plus logs is easier to reason about
- Failure modes: long-running processes can die silently

The optional filesystem watcher (`skills-manager watch`) is deferred until v0.3
and must remain opt-in.

## Future: cloud and adaptive scheduling

Ideas worth tracking beyond v1:

- Cloud scheduling through Claude routines or Codex automations
- GitHub webhooks for repos that support them
- Adaptive cadence based on update frequency and recent tool usage
- Backoff on repeated source errors

Cloud scheduling should return only after three conditions are true:

1. Local scheduled checks are useful enough that users ask for off-machine runs.
2. Provider APIs are stable enough to document without churn.
3. Result writeback can be done without broad credentials or fragile webhooks.
