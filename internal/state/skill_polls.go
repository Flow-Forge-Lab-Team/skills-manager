package state

import (
	"fmt"
	"time"
)

// SkillPollRecord holds cached polling info for a GitHub-sourced skill.
type SkillPollRecord struct {
	SkillName     string
	ETag          string
	LastCheckedAt string // RFC3339 timestamp
	LastCommit    string
}

// UpsertSkillPoll inserts or updates the poll record for a skill.
func (db *DB) UpsertSkillPoll(skillName, commit, etag string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO skill_polls (skill_name, etag, last_checked_at, last_commit)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(skill_name) DO UPDATE SET
			etag = excluded.etag,
			last_checked_at = excluded.last_checked_at,
			last_commit = excluded.last_commit
	`, skillName, etag, now, commit)
	return err
}

// GetSkillPoll retrieves the cached poll record for a skill.
// Returns nil if the skill has no poll record.
func (db *DB) GetSkillPoll(skillName string) (*SkillPollRecord, error) {
	var record SkillPollRecord
	err := db.QueryRow(`
		SELECT skill_name, etag, last_checked_at, last_commit
		FROM skill_polls
		WHERE skill_name = ?
	`, skillName).Scan(&record.SkillName, &record.ETag, &record.LastCheckedAt, &record.LastCommit)

	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &record, nil
}

// PendingUpdate holds info about a staged update waiting for review.
type PendingUpdate struct {
	SkillName   string
	FromVersion string
	ToVersion   string
	Source      string
	DetectedAt  string // RFC3339 timestamp
	Status      string
}

// ListPendingUpdates returns all updates with status='pending'.
func (db *DB) ListPendingUpdates() ([]PendingUpdate, error) {
	rows, err := db.Query(`
		SELECT skill_name, from_version, to_version, source, detected_at, status
		FROM updates
		WHERE status = 'pending'
		ORDER BY skill_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var updates []PendingUpdate
	for rows.Next() {
		var u PendingUpdate
		if err := rows.Scan(&u.SkillName, &u.FromVersion, &u.ToVersion, &u.Source, &u.DetectedAt, &u.Status); err != nil {
			return nil, err
		}
		updates = append(updates, u)
	}
	return updates, rows.Err()
}

// GetPendingUpdate returns a single pending update by skill name.
func (db *DB) GetPendingUpdate(skillName string) (*PendingUpdate, error) {
	var u PendingUpdate
	err := db.QueryRow(`
		SELECT skill_name, from_version, to_version, source, detected_at, status
		FROM updates
		WHERE skill_name = ? AND status = 'pending'
	`, skillName).Scan(&u.SkillName, &u.FromVersion, &u.ToVersion, &u.Source, &u.DetectedAt, &u.Status)

	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// InsertUpdate records a new or updated pending update.
func (db *DB) InsertUpdate(skillName, fromVersion, toVersion, source string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT OR REPLACE INTO updates
		(skill_name, from_version, to_version, source, detected_at, summary_status, status)
		VALUES (?, ?, ?, ?, ?, 'pending', 'pending')
	`, skillName, fromVersion, toVersion, source, now)
	if err != nil {
		return fmt.Errorf("insert update: %w", err)
	}
	return nil
}

// MarkUpdateAccepted marks a pending update as accepted, removing it from the list.
func (db *DB) MarkUpdateAccepted(skillName string) error {
	_, err := db.Exec(`
		UPDATE updates SET status='accepted' WHERE skill_name=?
	`, skillName)
	return err
}
