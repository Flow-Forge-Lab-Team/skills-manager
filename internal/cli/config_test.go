package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSetGetAndShowLLM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	for _, args := range [][]string{
		{"config", "set", "mode", "symlink"},
		{"config", "set", "llm.provider", "anthropic"},
		{"config", "set", "llm.api_key-env", "ANTHROPIC_API_KEY"},
		{"config", "set", "llm.model", "claude-3-5-haiku-latest"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != ExitSuccess {
			t.Fatalf("Run(%v) = %d\nstdout:\n%s\nstderr:\n%s", args, code, stdout.String(), stderr.String())
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"config", "get", "llm.api_key-env"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("config get = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "ANTHROPIC_API_KEY" {
		t.Fatalf("api key env = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "show"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("config show = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"mode: symlink",
		"provider: anthropic",
		"api_key-env: ANTHROPIC_API_KEY",
		"model: claude-3-5-haiku-latest",
		"calls: 0",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("config show missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestConfigRejectsUnsupportedProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"config", "set", "llm.provider", "codex"}, &stdout, &stderr)
	if code != ExitUsageError {
		t.Fatalf("Run returned %d, want usage error\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "anthropic or openai") {
		t.Fatalf("stderr missing provider guidance:\n%s", stderr.String())
	}
}

func TestConfigSetPreservesUnmanagedSections(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "config.yaml"), `version: 1
mode: copy
llm:
  provider: "anthropic"
watcher:
  enabled: true
  paths:
    - ~/.codex/skills
update_check:
  frequency: weekly
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"config", "set", "llm.model", "claude-3-5-haiku-latest"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d, want success\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	config := readFile(t, filepath.Join(home, "config.yaml"))
	for _, want := range []string{
		`model: "claude-3-5-haiku-latest"`,
		"watcher:",
		"  enabled: true",
		"    - ~/.codex/skills",
		"update_check:",
		"  frequency: weekly",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
}

func TestConfigJSONCommandsEmitStructuredOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"--json", "config", "set", "llm.provider", "anthropic"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("config set --json = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var setResult struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Updated bool   `json:"updated"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &setResult); err != nil {
		t.Fatalf("config set --json emitted invalid JSON: %v\n%s", err, stdout.String())
	}
	if setResult.Key != "llm.provider" || setResult.Value != "anthropic" || !setResult.Updated {
		t.Fatalf("unexpected config set JSON: %+v", setResult)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "config", "get", "llm.provider"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("config get --json = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var getResult struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &getResult); err != nil {
		t.Fatalf("config get --json emitted invalid JSON: %v\n%s", err, stdout.String())
	}
	if getResult.Key != "llm.provider" || getResult.Value != "anthropic" {
		t.Fatalf("unexpected config get JSON: %+v", getResult)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "config", "show"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("config show --json = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var cfg managerConfig
	if err := json.Unmarshal(stdout.Bytes(), &cfg); err != nil {
		t.Fatalf("config show --json emitted invalid JSON: %v\n%s", err, stdout.String())
	}
	if cfg.LLM.Provider != "anthropic" {
		t.Fatalf("unexpected config show JSON: %+v", cfg)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--json", "config", "show", "llm.usage"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("config show llm.usage --json = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var usage llmUsage
	if err := json.Unmarshal(stdout.Bytes(), &usage); err != nil {
		t.Fatalf("config show llm.usage --json emitted invalid JSON: %v\n%s", err, stdout.String())
	}
	if usage.Calls != 0 {
		t.Fatalf("unexpected usage JSON: %+v", usage)
	}
}
