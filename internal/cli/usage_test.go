package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

func TestUsageHookRecordsSkillWithProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	payload := `{"hook_event_name":"PreToolUse","tool_name":"Skill","cwd":"/work/my-project","tool_input":{"skill":"brainstorming","args":"x"}}`
	old := stdinReader
	stdinReader = strings.NewReader(payload)
	defer func() { stdinReader = old }()

	var stdout, stderr bytes.Buffer
	if code := runUsageHook(nil, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}

	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cells, err := db.UsageMatrix()
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("cells = %+v, want 1", cells)
	}
	c := cells[0]
	if c.SkillName != "brainstorming" || c.Harness != "claude" || c.Count != 1 {
		t.Fatalf("cell = %+v", c)
	}
	if c.ProjectSlug == "" || c.ProjectSlug != projectSlug("/work/my-project") {
		t.Fatalf("project slug = %q, want %q", c.ProjectSlug, projectSlug("/work/my-project"))
	}
}

func TestUsageHookIgnoresNonSkillTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	old := stdinReader
	stdinReader = strings.NewReader(`{"tool_name":"Bash","cwd":"/x","tool_input":{"command":"ls"}}`)
	defer func() { stdinReader = old }()

	var stdout, stderr bytes.Buffer
	if code := runUsageHook(nil, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d", code)
	}

	db, _ := state.Open(home)
	defer db.Close()
	cells, _ := db.UsageMatrix()
	if len(cells) != 0 {
		t.Fatalf("expected no invocations for Bash, got %+v", cells)
	}
}

func TestUsageHookMalformedPayloadDoesNotFail(t *testing.T) {
	t.Setenv("SKILLS_MANAGER_HOME", t.TempDir())
	old := stdinReader
	stdinReader = strings.NewReader(`{not json`)
	defer func() { stdinReader = old }()

	var stdout, stderr bytes.Buffer
	// Best-effort: a malformed hook payload must never block the tool (exit 0).
	if code := runUsageHook(nil, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d, want 0 (best-effort)", code)
	}
}

func TestUsageHookPrintConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runUsageHook([]string{"--print-config"}, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), `"matcher": "Skill"`) ||
		!strings.Contains(stdout.String(), "skills-manager usage hook") {
		t.Fatalf("config snippet missing expected content:\n%s", stdout.String())
	}
}

func TestUsageMatrixCommandJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.RecordInvocations([]state.Invocation{
		{SkillName: "a", ProjectSlug: "p1", Harness: "claude", Source: "hook"},
		{SkillName: "a", ProjectSlug: "p1", Harness: "claude", Source: "hook"},
		{SkillName: "b", ProjectSlug: "p2", Harness: "codex", Source: "otel"},
	})
	db.Close()

	var stdout, stderr bytes.Buffer
	if code := runUsageMatrix(nil, &stdout, &stderr, globalFlags{JSON: true}); code != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	var view usageMatrixView
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if view.Total != 3 {
		t.Fatalf("total = %d, want 3", view.Total)
	}
	if len(view.Skills) != 2 || len(view.Projects) != 2 || len(view.Harnesses) != 2 {
		t.Fatalf("dims: skills=%v projects=%v harnesses=%v", view.Skills, view.Projects, view.Harnesses)
	}
	if len(view.Cells) != 2 {
		t.Fatalf("cells = %+v, want 2 grouped cells", view.Cells)
	}
}

func TestUsageMatrixCommandEmpty(t *testing.T) {
	t.Setenv("SKILLS_MANAGER_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := runUsageMatrix(nil, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), "No skill invocations recorded yet") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestUsageUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runUsage([]string{"bogus"}, &stdout, &stderr, globalFlags{}); code != ExitUsageError {
		t.Fatalf("exit = %d, want %d", code, ExitUsageError)
	}
}

func TestUsageMatrixSince30d(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339)
	recent := time.Now().UTC().AddDate(0, 0, -2).Format(time.RFC3339)
	_, _ = db.RecordInvocations([]state.Invocation{
		{SkillName: "old-skill", ProjectSlug: "p1", Harness: "claude", Source: "hook", InvokedAt: old},
		{SkillName: "new-skill", ProjectSlug: "p1", Harness: "grok", Source: "record", InvokedAt: recent},
	})
	db.Close()

	var stdout, stderr bytes.Buffer
	if code := runUsageMatrix([]string{"--since", "30d"}, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "new-skill") {
		t.Fatalf("expected recent skill in output:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "old-skill") {
		t.Fatalf("did not expect old skill in 30d window:\n%s", stdout.String())
	}
}

func TestUsageRecordFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	var stdout, stderr bytes.Buffer
	if code := runUsageRecord([]string{"--harness", "grok", "--skill", "linear-feature", "--cwd", "/work/demo"}, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}

	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cells, err := db.UsageMatrix()
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 || cells[0].SkillName != "linear-feature" || cells[0].Harness != "grok" {
		t.Fatalf("cells = %+v", cells)
	}
}

func TestUsageRecordRelativeCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := runUsageRecord([]string{"--harness", "grok", "--skill", "linear-feature", "--cwd", "."}, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}

	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cells, err := db.UsageMatrix()
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 {
		t.Fatalf("cells = %+v", cells)
	}
	want := projectSlug(mustAbs(t, "."))
	if cells[0].ProjectSlug != want {
		t.Fatalf("project slug = %q, want %q", cells[0].ProjectSlug, want)
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestUsageRecordStdinJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	old := stdinReader
	stdinReader = strings.NewReader(`{"skill":"help","harness":"codex","cwd":"/tmp/proj"}`)
	defer func() { stdinReader = old }()

	var stdout, stderr bytes.Buffer
	if code := runUsageRecord(nil, &stdout, &stderr, globalFlags{}); code != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}

	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cells, err := db.UsageMatrix()
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 || cells[0].SkillName != "help" || cells[0].Harness != "codex" {
		t.Fatalf("cells = %+v", cells)
	}
}
