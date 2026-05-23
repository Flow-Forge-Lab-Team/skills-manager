package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type CatalogSkill struct {
	Name              string
	Summary           string
	Categories        []string
	Tags              []string
	CompatibilityMode string
	CompatibilityData map[string]any
	Requirements      map[string]any
	Origin            map[string]any
	Fingerprint       string
}

type CatalogSnapshot struct {
	Skills []CatalogSkill
}

func (db *DB) SyncCatalog(snap CatalogSnapshot) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := syncCatalogTx(tx, snap); err != nil {
		return err
	}
	return tx.Commit()
}

func syncCatalogTx(tx *sql.Tx, snap CatalogSnapshot) error {
	now := time.Now().UTC().Format(time.RFC3339)
	keep := make(map[string]struct{}, len(snap.Skills))
	for _, s := range snap.Skills {
		keep[s.Name] = struct{}{}
	}
	rows, err := tx.Query(`SELECT name FROM skills`)
	if err != nil {
		return fmt.Errorf("list existing skills: %w", err)
	}
	var stale []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		if _, ok := keep[n]; !ok {
			stale = append(stale, n)
		}
	}
	rows.Close()
	for _, n := range stale {
		if _, err := tx.Exec(`DELETE FROM skills WHERE name = ?`, n); err != nil {
			return fmt.Errorf("delete stale skill %s: %w", n, err)
		}
		if _, err := tx.Exec(`DELETE FROM requirement_checks WHERE skill_name = ?`, n); err != nil {
			return fmt.Errorf("delete requirement_checks for %s: %w", n, err)
		}
	}
	for _, s := range snap.Skills {
		categoriesJSON, err := encodeJSON(s.Categories)
		if err != nil {
			return fmt.Errorf("encode categories for %s: %w", s.Name, err)
		}
		tagsJSON, err := encodeJSON(s.Tags)
		if err != nil {
			return fmt.Errorf("encode tags for %s: %w", s.Name, err)
		}
		compatDataJSON, err := encodeJSON(s.CompatibilityData)
		if err != nil {
			return fmt.Errorf("encode compatibility_data for %s: %w", s.Name, err)
		}
		reqsJSON, err := encodeJSON(s.Requirements)
		if err != nil {
			return fmt.Errorf("encode requirements for %s: %w", s.Name, err)
		}
		originJSON, err := encodeJSON(s.Origin)
		if err != nil {
			return fmt.Errorf("encode origin for %s: %w", s.Name, err)
		}

		_, err = tx.Exec(`
INSERT INTO skills (
  name, summary, categories, tags,
  compatibility_mode, compatibility_data, requirements, origin,
  fingerprint, added_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
  summary = excluded.summary,
  categories = excluded.categories,
  tags = excluded.tags,
  compatibility_mode = excluded.compatibility_mode,
  compatibility_data = excluded.compatibility_data,
  requirements = excluded.requirements,
  origin = excluded.origin,
  fingerprint = excluded.fingerprint,
  updated_at = excluded.updated_at
`,
			s.Name, s.Summary, categoriesJSON, tagsJSON,
			s.CompatibilityMode, compatDataJSON, reqsJSON, originJSON,
			s.Fingerprint, now, now,
		)
		if err != nil {
			return fmt.Errorf("upsert skill %s: %w", s.Name, err)
		}
	}
	return nil
}

func encodeJSON(v any) (string, error) {
	if v == nil {
		return "null", nil
	}
	// Treat empty slices/maps as null for compact storage.
	switch t := v.(type) {
	case []string:
		if t == nil {
			return "null", nil
		}
	case map[string]any:
		if t == nil {
			return "null", nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
