package state

import (
	"testing"
)

func TestRecordInvocationAndMatrix(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	invs := []Invocation{
		{SkillName: "brainstorming", ProjectSlug: "proj-a", Harness: "claude", Trigger: "user-initiated", Source: "hook", InvokedAt: "2026-05-01T10:00:00Z"},
		{SkillName: "brainstorming", ProjectSlug: "proj-a", Harness: "claude", Trigger: "user-initiated", Source: "hook", InvokedAt: "2026-05-01T11:00:00Z"},
		{SkillName: "brainstorming", ProjectSlug: "proj-b", Harness: "claude", Trigger: "nested", Source: "otel", InvokedAt: "2026-05-01T12:00:00Z"},
		{SkillName: "debugging", ProjectSlug: "proj-a", Harness: "codex", Trigger: "user-initiated", Source: "hook", InvokedAt: "2026-05-01T13:00:00Z"},
		{SkillName: "", ProjectSlug: "proj-a", Harness: "claude", Source: "otel"}, // skipped: no skill
	}
	written, err := db.RecordInvocations(invs)
	if err != nil {
		t.Fatal(err)
	}
	if written != 4 {
		t.Fatalf("written = %d, want 4 (one record skipped for empty skill)", written)
	}

	cells, err := db.UsageMatrix()
	if err != nil {
		t.Fatal(err)
	}

	want := []UsageCell{
		{SkillName: "brainstorming", ProjectSlug: "proj-a", Harness: "claude", Count: 2},
		{SkillName: "brainstorming", ProjectSlug: "proj-b", Harness: "claude", Count: 1},
		{SkillName: "debugging", ProjectSlug: "proj-a", Harness: "codex", Count: 1},
	}
	if len(cells) != len(want) {
		t.Fatalf("got %d cells, want %d: %+v", len(cells), len(want), cells)
	}
	for i, w := range want {
		if cells[i] != w {
			t.Fatalf("cell[%d] = %+v, want %+v", i, cells[i], w)
		}
	}
}

func TestRecordInvocationMergesByToolUseID(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// The PreToolUse hook fires first (project, no trigger)...
	if err := db.RecordInvocation(Invocation{
		SkillName: "brainstorming", ProjectSlug: "proj-a", Harness: "claude",
		Source: "hook", ToolUseID: "toolu_123",
	}); err != nil {
		t.Fatal(err)
	}
	// ...then the OTEL row arrives for the same activation (trigger, no project).
	if _, err := db.RecordInvocations([]Invocation{
		{SkillName: "brainstorming", Harness: "claude", Trigger: "proactive", Source: "otel", ToolUseID: "toolu_123"},
	}); err != nil {
		t.Fatal(err)
	}

	cells, err := db.UsageMatrix()
	if err != nil {
		t.Fatal(err)
	}
	// One merged cell, keeping the project from the hook.
	if len(cells) != 1 {
		t.Fatalf("cells = %+v, want 1 (merged)", cells)
	}
	if cells[0].ProjectSlug != "proj-a" || cells[0].Count != 1 {
		t.Fatalf("cell = %+v, want proj-a count 1", cells[0])
	}
	var trigger string
	if err := db.QueryRow(`SELECT trigger FROM invocations`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if trigger != "proactive" {
		t.Fatalf("trigger = %q, want proactive (enriched from OTEL)", trigger)
	}
}

func TestRecordInvocationEmptyToolUseIDDoesNotCollide(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Rows without a tool_use_id (manual/watcher) must never dedupe each other.
	for i := 0; i < 3; i++ {
		if err := db.RecordInvocation(Invocation{SkillName: "x", Harness: "claude", Source: "manual"}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM invocations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("rows = %d, want 3 (empty tool_use_id excluded from unique index)", n)
	}
}

func TestRecordInvocationRequiresSkill(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.RecordInvocation(Invocation{Source: "manual"}); err == nil {
		t.Fatal("expected error for empty skill name")
	}
}

func TestUsageMatrixSince(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	old := "2026-01-01T10:00:00Z"
	recent := "2026-06-01T10:00:00Z"
	_, err = db.RecordInvocations([]Invocation{
		{SkillName: "a", Harness: "claude", Source: "hook", InvokedAt: old},
		{SkillName: "b", Harness: "grok", Source: "record", InvokedAt: recent},
	})
	if err != nil {
		t.Fatal(err)
	}

	all, err := db.UsageMatrix()
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %+v err=%v, want 2 cells", all, err)
	}
	since, err := db.UsageMatrixSince("2026-05-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].SkillName != "b" {
		t.Fatalf("since = %+v, want only skill b", since)
	}
}

func TestNormalizeInvokedAtOffset(t *testing.T) {
	got := normalizeInvokedAt("2026-05-06T23:30:00-01:00")
	want := "2026-05-07T00:30:00Z"
	if got != want {
		t.Fatalf("normalizeInvokedAt = %q, want %q", got, want)
	}
}

func TestUsageMatrixSinceOffsetTimestamp(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.RecordInvocation(Invocation{
		SkillName: "a",
		Harness:   "claude",
		Source:    "otel",
		InvokedAt: "2026-05-06T23:30:00-01:00", // 2026-05-07T00:30:00Z
	}); err != nil {
		t.Fatal(err)
	}

	cells, err := db.UsageMatrixSince("2026-05-07T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 || cells[0].Count != 1 {
		t.Fatalf("since cells = %+v, want one recent row", cells)
	}
}

func TestRecordInvocationDefaultsTimestamp(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.RecordInvocation(Invocation{SkillName: "x", Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	var ts string
	if err := db.QueryRow(`SELECT invoked_at FROM invocations WHERE skill_name='x'`).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	if ts == "" {
		t.Fatal("invoked_at was not defaulted")
	}
}
