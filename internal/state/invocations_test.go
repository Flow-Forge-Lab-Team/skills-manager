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
