package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run returned %d, want 0", code)
	}
	if got, want := strings.TrimSpace(stdout.String()), "skills-manager "+Version; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestUnknownArgument(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"nope"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run returned %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown argument: nope") {
		t.Fatalf("stderr = %q, want unknown argument message", stderr.String())
	}
}
