package state

import (
	"database/sql"
	"fmt"
	"time"
)

// Invocation is a single recorded skill activation, sourced from Claude Code
// OTEL events, a PreToolUse hook, a watcher, or a manual entry.
type Invocation struct {
	SkillName   string
	ProjectSlug string // empty when the source cannot attribute a project (e.g. OTEL)
	Harness     string
	Trigger     string // user-initiated | proactive | nested; empty when unknown
	InvokedAt   string // RFC3339 timestamp; defaulted to now when empty
	Source      string // otel | hook | watcher | manual
	ToolUseID   string // correlation id shared by the OTEL tool_result event and the hook
}

// insertInvocationSQL inserts one invocation, merging on tool_use_id when the
// same activation is reported by more than one feed. The hook supplies the
// project, OTEL supplies the trigger, and the merge fills whichever field the
// existing row is missing — so enabling both feeds enriches a single row rather
// than double-counting it. The conflict target matches the partial unique index
// from migration 0003, so rows without a tool_use_id always insert fresh.
const insertInvocationSQL = `
	INSERT INTO invocations (skill_name, project_slug, harness, trigger, invoked_at, source, tool_use_id)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (tool_use_id) WHERE tool_use_id IS NOT NULL AND tool_use_id != ''
	DO UPDATE SET
		project_slug = CASE WHEN excluded.project_slug != '' THEN excluded.project_slug ELSE invocations.project_slug END,
		trigger      = CASE WHEN excluded.trigger      != '' THEN excluded.trigger      ELSE invocations.trigger      END,
		harness      = CASE WHEN invocations.harness    =  '' THEN excluded.harness       ELSE invocations.harness      END,
		source       = CASE WHEN instr(invocations.source, excluded.source) = 0
		                    THEN trim(invocations.source || '+' || excluded.source, '+')
		                    ELSE invocations.source END
`

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func recordOne(e execer, inv Invocation) error {
	if inv.SkillName == "" {
		return fmt.Errorf("record invocation: skill name required")
	}
	inv.InvokedAt = normalizeInvokedAt(inv.InvokedAt)
	if _, err := e.Exec(insertInvocationSQL,
		inv.SkillName, inv.ProjectSlug, inv.Harness, inv.Trigger, inv.InvokedAt, inv.Source, inv.ToolUseID); err != nil {
		return fmt.Errorf("record invocation: %w", err)
	}
	return nil
}

// RecordInvocation records a single invocation, merging on tool_use_id (see
// insertInvocationSQL). An empty InvokedAt is defaulted to the current UTC time.
func (db *DB) RecordInvocation(inv Invocation) error {
	return recordOne(db, inv)
}

// RecordInvocations records a batch of invocations in a single transaction.
// Records with an empty SkillName are skipped rather than aborting the batch,
// so a malformed event in an OTEL payload does not discard the valid ones.
// Returns the number of records processed (an upsert that merges into an
// existing row still counts as processed).
func (db *DB) RecordInvocations(invs []Invocation) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("record invocations: begin: %w", err)
	}
	processed := 0
	for _, inv := range invs {
		if inv.SkillName == "" {
			continue
		}
		if err := recordOne(tx, inv); err != nil {
			tx.Rollback()
			return 0, err
		}
		processed++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("record invocations: commit: %w", err)
	}
	return processed, nil
}

// UsageCell is one aggregated bucket of the usage matrix: a count of
// invocations grouped by skill, project, and harness.
type UsageCell struct {
	SkillName   string `json:"skill_name"`
	ProjectSlug string `json:"project_slug"`
	Harness     string `json:"harness"`
	Count       int    `json:"count"`
}

// UsageMatrix returns invocation counts grouped by skill, project, and harness,
// ordered deterministically so callers and tests see stable output. This backs
// the Matrix view (skill × project × harness × count).
func (db *DB) UsageMatrix() ([]UsageCell, error) {
	return db.UsageMatrixSince("")
}

// UsageMatrixSince is like UsageMatrix but only counts invocations at or after
// sinceRFC3339. An empty since includes all rows.
func (db *DB) UsageMatrixSince(sinceRFC3339 string) ([]UsageCell, error) {
	query := `
		SELECT
			COALESCE(skill_name, '') AS skill_name,
			COALESCE(project_slug, '') AS project_slug,
			COALESCE(harness, '') AS harness,
			COUNT(*) AS count
		FROM invocations`
	args := []any{}
	if sinceRFC3339 != "" {
		query += `
		WHERE invoked_at >= ?`
		args = append(args, sinceRFC3339)
	}
	query += `
		GROUP BY skill_name, project_slug, harness
		ORDER BY skill_name, project_slug, harness
	`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("usage matrix: %w", err)
	}
	defer rows.Close()

	var cells []UsageCell
	for rows.Next() {
		var c UsageCell
		if err := rows.Scan(&c.SkillName, &c.ProjectSlug, &c.Harness, &c.Count); err != nil {
			return nil, fmt.Errorf("usage matrix scan: %w", err)
		}
		cells = append(cells, c)
	}
	return cells, rows.Err()
}

// normalizeInvokedAt stores invocation timestamps as UTC RFC3339 so since
// filters compare chronologically. Unparseable values are preserved as-is.
func normalizeInvokedAt(ts string) string {
	if ts == "" {
		return time.Now().UTC().Format(time.RFC3339)
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ts
}
