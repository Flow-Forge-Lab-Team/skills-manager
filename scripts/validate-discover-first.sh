#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap '[[ -n "${SERVER_PID:-}" ]] && kill "$SERVER_PID" 2>/dev/null || true; rm -rf "$WORK"' EXIT

BIN="$WORK/skills-manager"
HOME_DIR="$WORK/home"
MANAGER_HOME="$HOME_DIR/.skills-manager"
PROJECT="$HOME_DIR/dev/demo-repo"
PORT="${SKILLS_MANAGER_VALIDATE_PORT:-18765}"

mkdir -p "$HOME_DIR"
go build -o "$BIN" "$ROOT/cmd/skills-manager"

write_skill() {
  local dir="$1"
  local name="$2"
  local body="$3"
  mkdir -p "$dir/$name"
  printf '%s\n' "---" "name: $name" "---" "$body" > "$dir/$name/SKILL.md"
}

write_skill "$HOME_DIR/.claude/skills" "build" "# Build"
write_skill "$HOME_DIR/.claude/skills" "review" "# Review v1"
write_skill "$HOME_DIR/.grok/skills" "review" "# Review v2"
mkdir -p "$HOME_DIR/.codex/skills/.system/openai-docs" "$HOME_DIR/.openclaw/skills"
printf '%s\n' "---" "name: openai-docs" "---" "# OpenAI Docs" > "$HOME_DIR/.codex/skills/.system/openai-docs/SKILL.md"

mkdir -p "$PROJECT/.git" "$PROJECT/.cursor/rules"
write_skill "$PROJECT/.codex/skills" "project-skill" "# Project Skill"
write_skill "$PROJECT/.claude/skills" "project-skill" "# Project Skill"
write_skill "$PROJECT/.grok/skills" "grok-local" "# Grok Local"
write_skill "$PROJECT/.agents/skills" "agent-local" "# Agent Local"
printf '%s\n' "# Project agent instructions" > "$PROJECT/AGENTS.md"
printf '%s\n' "# Cursor project rule" > "$PROJECT/.cursor/rules/project.mdc"

export HOME="$HOME_DIR"
export SKILLS_MANAGER_HOME="$MANAGER_HOME"
export SKILLS_MANAGER_MACHINE="release-fixture"

"$BIN" --json discover --global --projects "$PROJECT" --save-project-roots > "$WORK/discover.json"
python3 - "$WORK/discover.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
assert data["summary"]["tools_found"] >= 5, data["summary"]
assert data["summary"]["global_skills"] >= 3, data["summary"]
assert data["summary"]["project_local_skills"] >= 4, data["summary"]
assert data["summary"]["drift_groups"] >= 1, data["summary"]
assert data["summary"]["missing_tool_coverage"] >= 1, data["summary"]
formats = {i["format"] for i in data["installations"]}
for expected in ("skill_md", "cursor_rule", "agents_md"):
    assert expected in formats, formats
assert any(r["kind"] == "install_global" and r["skill_name"] == "build" and r["target_tool_id"] == "openclaw" for r in data["report"]["recommendations"])
assert any(g["group_type"] == "same_name_different_hash" and g.get("skill_name") == "review" for g in data["drift_groups"])
PY

REC_ID="$(python3 - "$WORK/discover.json" <<'PY'
import json, sys
for rec in json.load(open(sys.argv[1]))["report"]["recommendations"]:
    if rec["kind"] == "install_global" and rec["skill_name"] == "build" and rec["target_tool_id"] == "openclaw":
        print(rec["recommendation_id"])
        break
else:
    raise SystemExit("install_global build -> openclaw recommendation missing")
PY
)"

"$BIN" --json plan --inventory "$WORK/discover.json" --recommendation "$REC_ID" > "$WORK/plan.json"
python3 - "$WORK/plan.json" <<'PY'
import json, sys
plan = json.load(open(sys.argv[1]))["plans"][0]
assert plan["status"] == "ready", plan
assert plan["files"]["create"], plan
PY
"$BIN" plan --inventory "$WORK/discover.json" --recommendation "$REC_ID" --apply --confirm
test -f "$HOME_DIR/.openclaw/skills/build/SKILL.md"

"$BIN" init-library --local-only
"$BIN" sync-library
SNAPSHOT="$MANAGER_HOME/library/inventory-snapshots/release-fixture.json"
test -f "$SNAPSHOT"
if grep -q "$HOME_DIR" "$SNAPSHOT"; then
  echo "inventory snapshot leaked absolute HOME path" >&2
  exit 1
fi
grep -q '\$HOME/.claude/skills/build' "$SNAPSHOT"

"$BIN" serve --host 127.0.0.1 --port "$PORT" > "$WORK/server.log" 2>&1 &
SERVER_PID="$!"
for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:$PORT/api/v1/session" > "$WORK/session.json"; then
    break
  fi
  sleep 0.1
done
curl -fsS "http://127.0.0.1:$PORT/api/v1/assessment" > "$WORK/assessment.json"
curl -fsS "http://127.0.0.1:$PORT/api/v1/machines" > "$WORK/machines.json"
python3 - "$WORK/assessment.json" "$WORK/machines.json" <<'PY'
import json, sys
assessment = json.load(open(sys.argv[1]))
machines = json.load(open(sys.argv[2]))
assert assessment["summary"]["global_skills"] >= 3, assessment["summary"]
assert assessment["recommendations"], assessment
assert machines["current_machine"] == "release-fixture", machines
assert machines["machines"], machines
PY

grep -q 'skills-manager discover --global' "$ROOT/README.md"
grep -q 'skills-manager serve' "$ROOT/README.md"
grep -q 'Discover assessment' "$ROOT/docs/TUTORIAL.md"
grep -q 'scripts/validate-discover-first.sh' "$ROOT/docs/RELEASE_CHECKLIST.md"

echo "discover-first release validation passed"
