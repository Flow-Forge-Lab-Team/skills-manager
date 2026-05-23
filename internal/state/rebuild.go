package state

// Rebuild wipes derived-from-library tables (skills, requirement_checks) and
// repopulates skills from the snapshot. Per ownership rules, user-owned and
// machine-local-but-not-derived tables (projects, installs, invocations,
// detected, updates) are preserved.
func (db *DB) Rebuild(snap CatalogSnapshot) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM skills`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM requirement_checks`); err != nil {
		return err
	}
	if err := syncCatalogTx(tx, snap); err != nil {
		return err
	}
	return tx.Commit()
}
