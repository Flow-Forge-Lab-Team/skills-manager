package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitLibraryInitializesGitRepoAndMachineMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	t.Setenv("SKILLS_MANAGER_MACHINE", "alpha-laptop")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"init-library", "--local-only"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("init-library returned %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	library := filepath.Join(home, "library")
	for _, rel := range []string{".git", ".gitignore", "catalog.yaml", ".machines.yaml"} {
		if _, err := os.Stat(filepath.Join(library, rel)); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
	assertFileContent(t, filepath.Join(library, ".gitignore"), "notifications/\nlogs/\ncache/\n")
	machines := readFile(t, filepath.Join(library, ".machines.yaml"))
	assertStringContains(t, machines, "alpha-laptop:")
	if strings.Contains(machines, "fatal:") {
		t.Fatalf(".machines.yaml recorded git error output:\n%s", machines)
	}
	parent, err := runGit(library, "rev-parse", "--short", "HEAD^")
	if err != nil {
		t.Fatalf("read parent commit: %v", err)
	}
	parsed, err := readMachines(filepath.Join(library, ".machines.yaml"))
	if err != nil {
		t.Fatalf("read machines: %v", err)
	}
	if got := parsed.Machines["alpha-laptop"].LastCommit; got != strings.TrimSpace(parent) {
		t.Fatalf("last_commit = %q, want %q", got, strings.TrimSpace(parent))
	}
	assertStringContains(t, stdout.String(), "Initialized library")
}

func TestJoinAndSyncLibraryPullsFromRemote(t *testing.T) {
	remote := initBareRemote(t)

	homeA := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", homeA)
	t.Setenv("SKILLS_MANAGER_MACHINE", "alpha")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init-library", "--remote", remote}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("init-library returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	writeFile(t, filepath.Join(homeA, "library", "demo", "SKILL.md"), "---\nname: demo\n---\n")
	writeFile(t, filepath.Join(homeA, "library", "catalog.yaml"), "version: 1\nskills:\n  - name: demo\n")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync-library", "--push"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("sync-library --push returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	homeB := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", homeB)
	t.Setenv("SKILLS_MANAGER_MACHINE", "beta")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"join", remote}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("join returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(homeB, "library", "demo", "SKILL.md")); err != nil {
		t.Fatalf("joined library missing synced skill: %v", err)
	}
	inspect := t.TempDir()
	gitCmd(t, "", "clone", "--branch", "main", remote, inspect)
	machines := readFile(t, filepath.Join(inspect, ".machines.yaml"))
	assertStringContains(t, machines, "beta:")
	assertStringContains(t, machines, "last_commit:")

	t.Setenv("SKILLS_MANAGER_HOME", homeA)
	t.Setenv("SKILLS_MANAGER_MACHINE", "alpha")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync-library", "--status"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("sync-library --status returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assertStringContains(t, stdout.String(), "behind")

	writeFile(t, filepath.Join(homeA, "library", "second", "SKILL.md"), "---\nname: second\n---\n")
	writeFile(t, filepath.Join(homeA, "library", "catalog.yaml"), "version: 1\nskills:\n  - name: demo\n  - name: second\n")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync-library", "--push"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("second push returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	t.Setenv("SKILLS_MANAGER_HOME", homeB)
	t.Setenv("SKILLS_MANAGER_MACHINE", "beta")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync-library"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("sync-library pull returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(homeB, "library", "second", "SKILL.md")); err != nil {
		t.Fatalf("pulled library missing new skill: %v", err)
	}
	status, err := runGit(filepath.Join(homeB, "library"), "status", "--short", "--branch")
	if err != nil {
		t.Fatalf("read library status: %v", err)
	}
	if strings.Contains(status, "ahead") {
		t.Fatalf("sync-library pull left unpushed commits:\n%s", status)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync-library", "--status", "--json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("sync-library --status --json returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("sync-library --json wrote invalid JSON %q: %v", stdout.String(), err)
	}
	if payload["mode"] != "status" {
		t.Fatalf("json mode = %#v, want status", payload["mode"])
	}
}

func TestSyncLibraryPushRebuildsGeneratedCatalogConflict(t *testing.T) {
	remote := initBareRemote(t)

	homeA := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", homeA)
	t.Setenv("SKILLS_MANAGER_MACHINE", "alpha")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init-library", "--remote", remote}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("init-library returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	homeB := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", homeB)
	t.Setenv("SKILLS_MANAGER_MACHINE", "beta")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"join", remote}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("join returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	t.Setenv("SKILLS_MANAGER_HOME", homeA)
	t.Setenv("SKILLS_MANAGER_MACHINE", "alpha")
	writeFile(t, filepath.Join(homeA, "library", "alpha-skill", "SKILL.md"), "---\nname: alpha-skill\n---\n")
	writeFile(t, filepath.Join(homeA, "library", "catalog.yaml"), "version: 1\nskills:\n  - name: alpha-skill\n")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync-library", "--push"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("alpha push returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	t.Setenv("SKILLS_MANAGER_HOME", homeB)
	t.Setenv("SKILLS_MANAGER_MACHINE", "beta")
	writeFile(t, filepath.Join(homeB, "library", "beta-skill", "SKILL.md"), "---\nname: beta-skill\n---\n")
	writeFile(t, filepath.Join(homeB, "library", "catalog.yaml"), "version: 1\nskills:\n  - name: beta-skill\n")
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"sync-library", "--push"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("beta push returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	inspect := t.TempDir()
	gitCmd(t, "", "clone", "--branch", "main", remote, inspect)
	catalog := readFile(t, filepath.Join(inspect, "catalog.yaml"))
	assertStringContains(t, catalog, "alpha-skill")
	assertStringContains(t, catalog, "beta-skill")
	if _, err := os.Stat(filepath.Join(inspect, "alpha-skill", "SKILL.md")); err != nil {
		t.Fatalf("remote missing alpha skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inspect, "beta-skill", "SKILL.md")); err != nil {
		t.Fatalf("remote missing beta skill: %v", err)
	}
}

func TestMachinesListsRegisteredMachines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	t.Setenv("SKILLS_MANAGER_MACHINE", "alpha")

	writeFile(t, filepath.Join(home, "library", ".machines.yaml"), `version: 1
machines:
  alpha:
    last_synced: 2026-05-24T10:00:00Z
    last_commit: abc123
  beta:
    last_synced: 2026-05-24T09:00:00Z
    last_commit: def456
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"machines"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("machines returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assertStringContains(t, stdout.String(), "alpha (this)")
	assertStringContains(t, stdout.String(), "beta")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"machines", "--json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("machines --json returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assertStringContains(t, stdout.String(), "\"version\"")
	assertStringContains(t, stdout.String(), "\"machines\"")
	assertStringContains(t, stdout.String(), "\"last_synced\"")
	assertStringContains(t, stdout.String(), "\"last_commit\"")
	if strings.Contains(stdout.String(), "\"Version\"") || strings.Contains(stdout.String(), "\"LastSynced\"") {
		t.Fatalf("machines --json used Go field names:\n%s", stdout.String())
	}
}

func initBareRemote(t *testing.T) string {
	t.Helper()
	configureTestGitEnv(t)
	remote := filepath.Join(t.TempDir(), "skills.git")
	gitCmd(t, "", "init", "--bare", remote)
	return remote
}

func configureTestGitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_0", "0")
	t.Setenv("GIT_CONFIG_KEY_1", "maintenance.auto")
	t.Setenv("GIT_CONFIG_VALUE_1", "false")
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func assertStringContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("got %q, want substring %q", got, want)
	}
}
