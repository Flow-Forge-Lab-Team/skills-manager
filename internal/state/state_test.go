package state

import (
	"encoding/json"
	"testing"
)

func expectedTables() []string {
	return []string{
		"skills", "projects", "installs", "updates",
		"invocations", "detected", "requirement_checks", "schema_version",
	}
}

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func countTable(t *testing.T, db *DB, name string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + name).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", name, err)
	}
	return n
}

func TestOpenAppliesMigrations(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, tbl := range expectedTables() {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", tbl, err)
		}
	}
	var v int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("schema_version count: %v", err)
	}
	if v != 1 {
		t.Errorf("schema_version rows = %d, want 1", v)
	}
	db.Close()

	// Reopen and ensure schema_version row count is stable.
	db2, err := Open(home)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	var v2 int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&v2); err != nil {
		t.Fatalf("schema_version count: %v", err)
	}
	if v2 != v {
		t.Errorf("schema_version changed across reopen: %d -> %d", v, v2)
	}
}

func TestMigrationIdempotent(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	n1 := countTable(t, db, "schema_version")
	db.Close()

	db2, err := Open(home)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	n2 := countTable(t, db2, "schema_version")
	if n1 != n2 {
		t.Errorf("schema_version count drift: %d -> %d", n1, n2)
	}
}

func TestSyncCatalogUpsert(t *testing.T) {
	db := openTest(t)
	snap := CatalogSnapshot{Skills: []CatalogSkill{{
		Name:    "skill-a",
		Summary: "first",
	}}}
	if err := db.SyncCatalog(snap); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	snap.Skills[0].Summary = "second"
	if err := db.SyncCatalog(snap); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := countTable(t, db, "skills"); got != 1 {
		t.Errorf("skills count = %d, want 1", got)
	}
	var summary string
	if err := db.QueryRow(`SELECT summary FROM skills WHERE name=?`, "skill-a").Scan(&summary); err != nil {
		t.Fatalf("query: %v", err)
	}
	if summary != "second" {
		t.Errorf("summary = %q, want %q", summary, "second")
	}
}

func TestRebuildPreservesNonDerived(t *testing.T) {
	db := openTest(t)

	// Seed skills via sync.
	if err := db.SyncCatalog(CatalogSnapshot{Skills: []CatalogSkill{{Name: "old-skill", Summary: "x"}}}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// Seed non-derived tables manually.
	if _, err := db.Exec(`INSERT INTO projects (slug, path, name) VALUES (?, ?, ?)`,
		"proj-a", "/tmp/proj-a", "Project A"); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO invocations (skill_name, project_slug, harness, trigger, invoked_at, source)
VALUES (?, ?, ?, ?, ?, ?)`, "old-skill", "proj-a", "claude", "user-initiated", "2026-01-01T00:00:00Z", "manual"); err != nil {
		t.Fatalf("insert invocation: %v", err)
	}

	// Rebuild with a different skill set.
	if err := db.Rebuild(CatalogSnapshot{Skills: []CatalogSkill{{Name: "new-skill", Summary: "y"}}}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if got := countTable(t, db, "projects"); got != 1 {
		t.Errorf("projects survived = %d, want 1", got)
	}
	if got := countTable(t, db, "invocations"); got != 1 {
		t.Errorf("invocations survived = %d, want 1", got)
	}
	if got := countTable(t, db, "skills"); got != 1 {
		t.Errorf("skills count = %d, want 1", got)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM skills`).Scan(&name); err != nil {
		t.Fatalf("query: %v", err)
	}
	if name != "new-skill" {
		t.Errorf("skill = %q, want new-skill", name)
	}
}

func TestRequirementsStoredAsJSON(t *testing.T) {
	db := openTest(t)
	reqs := map[string]any{
		"tools": []any{
			map[string]any{"name": "ripgrep", "required": true},
		},
	}
	snap := CatalogSnapshot{Skills: []CatalogSkill{{
		Name:         "skill-req",
		Requirements: reqs,
	}}}
	if err := db.SyncCatalog(snap); err != nil {
		t.Fatalf("sync: %v", err)
	}
	var raw string
	if err := db.QueryRow(`SELECT requirements FROM skills WHERE name=?`, "skill-req").Scan(&raw); err != nil {
		t.Fatalf("query: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal %q: %v", raw, err)
	}
	tools, ok := parsed["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools shape wrong: %#v", parsed["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "ripgrep" {
		t.Errorf("tool name = %v", tool["name"])
	}
	if tool["required"] != true {
		t.Errorf("tool required = %v", tool["required"])
	}
}
