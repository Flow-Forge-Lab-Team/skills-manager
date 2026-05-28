package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Flow-Forge-Lab-Team/skills-manager/internal/state"
)

// stageBenignPending writes a skill with a benign pending update and records it
// in the state DB. Returns the skill directory.
func stageBenignPending(t *testing.T, home, skill string) string {
	t.Helper()
	skillDir := filepath.Join(home, "library", skill)
	root := filepath.Join(skillDir, ".update-pending")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: "+skill+"\ndescription: Take notes\n---\nOld body\n")
	writeFile(t, filepath.Join(skillDir, ".skill-meta.yaml"), "version: 1\norigin:\n  type: github\n  commit: oldsha\n")
	writeFile(t, filepath.Join(root, "from-current", "SKILL.md"), "---\nname: "+skill+"\ndescription: Take notes\n---\nOld body\n")
	writeFile(t, filepath.Join(root, "to-incoming", "SKILL.md"), "---\nname: "+skill+"\ndescription: Take notes\n---\nNew body\n")
	db, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertUpdate(skill, "oldsha", "newsha", "github"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return skillDir
}

func TestUpdateAcceptAppliesSingleUpdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	skillDir := stageBenignPending(t, home, "notes")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"update", "--accept", "notes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("accept returned %d\nstdout:%s\nstderr:%s", code, stdout.String(), stderr.String())
	}
	assertFileContent(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: notes\ndescription: Take notes\n---\nNew body\n")
	if _, err := os.Stat(filepath.Join(skillDir, ".update-pending")); !os.IsNotExist(err) {
		t.Fatalf("pending dir should be gone, err=%v", err)
	}
	db, _ := state.Open(home)
	defer db.Close()
	pending, _ := db.ListPendingUpdates()
	if len(pending) != 0 {
		t.Fatalf("expected no pending after accept, got %d", len(pending))
	}
}

func TestUpdateRejectDiscardsUpdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	skillDir := stageBenignPending(t, home, "notes")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"update", "--reject", "notes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reject returned %d\nstderr:%s", code, stderr.String())
	}
	// Live content unchanged.
	assertFileContent(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: notes\ndescription: Take notes\n---\nOld body\n")
	if _, err := os.Stat(filepath.Join(skillDir, ".update-pending")); !os.IsNotExist(err) {
		t.Fatalf("pending dir should be gone after reject, err=%v", err)
	}
	db, _ := state.Open(home)
	defer db.Close()
	pending, _ := db.ListPendingUpdates()
	if len(pending) != 0 {
		t.Fatalf("expected no pending after reject, got %d", len(pending))
	}
}

func TestUpdatePinAndUnpin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	skillDir := stageBenignPending(t, home, "notes")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"update", "--pin", "notes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("pin returned %d\nstderr:%s", code, stderr.String())
	}
	meta, _ := readSkillMeta(filepath.Join(skillDir, ".skill-meta.yaml"))
	if meta.Pinned != "newsha" {
		t.Fatalf("expected pinned to incoming version newsha, got %q", meta.Pinned)
	}
	if _, err := os.Stat(filepath.Join(skillDir, ".update-pending")); !os.IsNotExist(err) {
		t.Fatalf("pin should reject pending update, err=%v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"update", "--unpin", "notes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("unpin returned %d\nstderr:%s", code, stderr.String())
	}
	meta, _ = readSkillMeta(filepath.Join(skillDir, ".skill-meta.yaml"))
	if meta.Pinned != "" {
		t.Fatalf("expected pin cleared, got %q", meta.Pinned)
	}
}

func TestUpdatePinExplicitVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	skillDir := stageBenignPending(t, home, "notes")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"update", "--pin", "notes", "1.4.2"}, &stdout, &stderr); code != 0 {
		t.Fatalf("pin returned %d\nstderr:%s", code, stderr.String())
	}
	meta, _ := readSkillMeta(filepath.Join(skillDir, ".skill-meta.yaml"))
	if meta.Pinned != "1.4.2" {
		t.Fatalf("expected explicit pin 1.4.2, got %q", meta.Pinned)
	}
}

func TestUpdateAcceptMissingPending(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "library", "notes", "SKILL.md"), "---\nname: notes\n---\nBody\n")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"update", "--accept", "notes"}, &stdout, &stderr); code != ExitNoPending {
		t.Fatalf("expected ExitNoPending (%d), got %d", ExitNoPending, code)
	}
}

func TestUpdateActionRejectsBadSkillName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"update", "--accept", "../escape"}, &stdout, &stderr); code != ExitUsageError {
		t.Fatalf("expected ExitUsageError for bad skill name, got %d", code)
	}
	if !strings.Contains(stderr.String(), "invalid skill name") {
		t.Fatalf("stderr should mention invalid skill name:\n%s", stderr.String())
	}
}
