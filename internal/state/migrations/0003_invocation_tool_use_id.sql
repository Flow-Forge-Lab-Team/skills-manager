-- Correlate the two usage feeds so the same Skill activation is not counted
-- twice. The Claude Code OTEL tool_result event and the PreToolUse hook share a
-- tool_use_id; recording it lets us dedupe across feeds. The unique index is
-- partial so rows without a tool_use_id (manual/watcher entries) never collide.
ALTER TABLE invocations ADD COLUMN tool_use_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_invocations_tool_use_id
  ON invocations (tool_use_id)
  WHERE tool_use_id IS NOT NULL AND tool_use_id != '';
