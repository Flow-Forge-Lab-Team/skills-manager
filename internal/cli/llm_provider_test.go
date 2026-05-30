package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCLIProviderCommandConstruction(t *testing.T) {
	tests := []struct {
		name     string
		cfg      llmConfig
		wantName string
		wantArgs []string
		output   string
		want     string
	}{
		{
			name:     "codex with model",
			cfg:      llmConfig{Provider: "codex-cli", Model: "gpt-5.5"},
			wantName: "codex",
			wantArgs: []string{"exec", "--skip-git-repo-check", "--model", "gpt-5.5", "-"},
			output:   `{"skill":"demo"}`,
			want:     `{"skill":"demo"}`,
		},
		{
			name:     "cursor with model",
			cfg:      llmConfig{Provider: "cursor-cli", Model: "composer-2.5"},
			wantName: "cursor-agent",
			wantArgs: []string{"-p", "--output-format", "json", "--mode", "ask", "--trust", "--model", "composer-2.5"},
			output:   `{"result":"{\"skill\":\"demo\"}"}`,
			want:     `{"skill":"demo"}`,
		},
		{
			name:     "cursor without model",
			cfg:      llmConfig{Provider: "cursor-cli"},
			wantName: "cursor-agent",
			wantArgs: []string{"-p", "--output-format", "json", "--mode", "ask", "--trust"},
			output:   `{"output":"{\"skill\":\"demo\"}"}`,
			want:     `{"skill":"demo"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRunner := llmCommandRunner
			t.Cleanup(func() { llmCommandRunner = oldRunner })
			llmCommandRunner = func(timeout time.Duration, name string, args []string, stdin string) (string, error) {
				if timeout != llmCommandTimeout {
					t.Fatalf("timeout = %s, want %s", timeout, llmCommandTimeout)
				}
				if name != tt.wantName {
					t.Fatalf("name = %q, want %q", name, tt.wantName)
				}
				if tt.wantName == "cursor-agent" {
					if len(args) != len(tt.wantArgs)+1 || !reflect.DeepEqual(args[:len(tt.wantArgs)], tt.wantArgs) {
						t.Fatalf("args = %#v, want prefix %#v plus prompt file arg", args, tt.wantArgs)
					}
					const prefix = "Read the full skills-manager provider prompt from this UTF-8 file, follow that prompt, and return only the requested output: "
					if !strings.HasPrefix(args[len(args)-1], prefix) {
						t.Fatalf("cursor prompt arg = %q", args[len(args)-1])
					}
					path := strings.TrimPrefix(args[len(args)-1], prefix)
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatalf("read cursor prompt file: %v", err)
					}
					if string(data) != "prompt text" {
						t.Fatalf("cursor prompt file = %q", string(data))
					}
				} else if !reflect.DeepEqual(args, tt.wantArgs) {
					t.Fatalf("args = %#v, want %#v", args, tt.wantArgs)
				}
				wantStdin := "prompt text"
				if tt.wantName == "cursor-agent" {
					wantStdin = ""
				}
				if stdin != wantStdin {
					t.Fatalf("stdin = %q", stdin)
				}
				return tt.output, nil
			}

			got, err := callConfiguredLLMProvider(tt.cfg, "prompt text")
			if err != nil {
				t.Fatal(err)
			}
			if got.Output != tt.want {
				t.Fatalf("output = %q, want %q", got.Output, tt.want)
			}
		})
	}
}

func TestCLIProviderFailureIsActionable(t *testing.T) {
	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(time.Duration, string, []string, string) (string, error) {
		return "", errors.New("cursor-agent executable not found on PATH; install and authenticate cursor-agent, or choose another llm.provider")
	}

	_, err := callConfiguredLLMProvider(llmConfig{Provider: "cursor-cli"}, "prompt")
	if err == nil {
		t.Fatal("expected provider error")
	}
	if !strings.Contains(err.Error(), "cursor-agent executable not found") || !strings.Contains(err.Error(), "authenticate") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func TestRunLLMCommandTimeout(t *testing.T) {
	_, err := runLLMCommand(time.Nanosecond, "sh", []string{"-c", "sleep 1"}, "")
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunLLMCommandRejectsTooMuchOutput(t *testing.T) {
	_, err := runLLMCommand(time.Second, "sh", []string{"-c", "yes x | head -c 2097153"}, "")
	if err == nil {
		t.Fatal("expected output limit error")
	}
	if !strings.Contains(err.Error(), "too much output") {
		t.Fatalf("error = %v", err)
	}
}

func TestExtractLLMCommandOutput(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{`{"result":"{\"skill\":\"demo\"}"}`, `{"skill":"demo"}`},
		{`{"message":{"content":"{\"skill\":\"demo\"}"}}`, `{"skill":"demo"}`},
		{`{"result":"Reading the file.\n{\"skill\":\"demo\"}"}`, `{"skill":"demo"}`},
		{"  {\"skill\":\"demo\"}\n", `{"skill":"demo"}`},
	} {
		if got := extractLLMCommandOutput(tt.in); got != tt.want {
			t.Fatalf("extractLLMCommandOutput(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCompatCheckAutoUsesCLIProviderAndValidatesOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "config.yaml"), "version: 1\nllm:\n  provider: \"codex-cli\"\n  model: \"gpt-5.5\"\n")
	writeFile(t, filepath.Join(home, "library", "demo", "SKILL.md"), "---\nname: demo\n---\n# demo\n")

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	calls := 0
	llmCommandRunner = func(_ time.Duration, name string, args []string, stdin string) (string, error) {
		calls++
		if name != "codex" || !reflect.DeepEqual(args, []string{"exec", "--skip-git-repo-check", "--model", "gpt-5.5", "-"}) {
			return "", fmt.Errorf("unexpected command: %s %#v", name, args)
		}
		if !strings.Contains(stdin, "# skills-manager compat-check handoff for demo") {
			return "", fmt.Errorf("prompt missing compat-check content")
		}
		return validCompatCheckProviderJSON("demo", "codex"), nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"compat-check", "demo", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("Run returned %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !strings.Contains(stdout.String(), "codex: compatible=true") {
		t.Fatalf("stdout missing parsed assessment:\n%s", stdout.String())
	}
}

func TestCompatCheckAutoRejectsInvalidCLIOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SKILLS_MANAGER_HOME", home)
	writeFile(t, filepath.Join(home, "config.yaml"), "version: 1\nllm:\n  provider: \"cursor-cli\"\n")
	writeFile(t, filepath.Join(home, "library", "demo", "SKILL.md"), "---\nname: demo\n---\n# demo\n")

	oldRunner := llmCommandRunner
	t.Cleanup(func() { llmCommandRunner = oldRunner })
	llmCommandRunner = func(time.Duration, string, []string, string) (string, error) {
		return `{"result":"not json"}`, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"compat-check", "demo", "--to", "codex", "--auto"}, &stdout, &stderr)
	if code != ExitUsageError {
		t.Fatalf("Run returned %d, want usage error\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "validate provider output") {
		t.Fatalf("stderr missing validation error:\n%s", stderr.String())
	}
}

func validCompatCheckProviderJSON(skill, target string) string {
	return fmt.Sprintf(`{
  "skill": %q,
  "assessments": {
    %q: {
      "compatible": true,
      "confidence": "high",
      "notes": ["portable"]
    }
  },
  "recommendation": "Use as-is.",
  "requirements": {
    "model": {
      "tool_use": "none"
    },
    "tools": [],
    "mcp_servers": []
  }
}`, skill, target)
}
