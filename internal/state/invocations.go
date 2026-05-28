package state

import (
	"fmt"
	"time"
)

// Invocation is a single recorded skill activation, sourced from Claude Code
// OTEL events, a PreToolUse hook, a watcher, or a manual entry.
type Invocation struct {
	SkillName   string
	ProjectSlug string // empty when the source cannot attribute a project (e.g. OTEL)
	Harness     string
	Trigger     string // user-initiated | proactive | nested
	InvokedAt   string // RFC3339 timestamp; defaulted to now when empty
	Source      string // otel | hook | watcher | manual
}

// RecordInvocation inserts a single invocation row. An empty InvokedAt is
// defaulted to the current UTC time so callers ingesting events without a
// timestamp still produce ordered rows.
func (db *DB) RecordInvocation(inv Invocation) error {
	if inv.SkillName == "" {
		return fmt.Errorf("record invocation: skill name required")
	}
	if inv.InvokedAt == "" {
		inv.InvokedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := db.Exec(`
		INSERT INTO invocations (skill_name, project_slug, harness, trigger, invoked_at, source)
		VALUES (?, ?, ?, ?, ?, ?)
	`, inv.SkillName, inv.ProjectSlug, inv.Harness, inv.Trigger, inv.InvokedAt, inv.Source)
	if err != nil {
		return fmt.Errorf("record invocation: %w", err)
	}
	return nil
}

// RecordInvocations inserts a batch of invocations in a single transaction.
// Records with an empty SkillName are skipped rather than aborting the batch,
// so a malformed event in an OTEL payload does not discard the valid ones.
// Returns the number of rows written.
func (db *DB) RecordInvocations(invs []Invocation) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("record invocations: begin: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO invocations (skill_name, project_slug, harness, trigger, invoked_at, source)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("record invocations: prepare: %w", err)
	}
	defer stmt.Close()

	written := 0
	now := time.Now().UTC().Format(time.RFC3339)
	for _, inv := range invs {
		if inv.SkillName == "" {
			continue
		}
		invokedAt := inv.InvokedAt
		if invokedAt == "" {
			invokedAt = now
		}
		if _, err := stmt.Exec(inv.SkillName, inv.ProjectSlug, inv.Harness, inv.Trigger, invokedAt, inv.Source); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("record invocations: exec: %w", err)
		}
		written++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("record invocations: commit: %w", err)
	}
	return written, nil
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
	rows, err := db.Query(`
		SELECT
			COALESCE(skill_name, '') AS skill_name,
			COALESCE(project_slug, '') AS project_slug,
			COALESCE(harness, '') AS harness,
			COUNT(*) AS count
		FROM invocations
		GROUP BY skill_name, project_slug, harness
		ORDER BY skill_name, project_slug, harness
	`)
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
