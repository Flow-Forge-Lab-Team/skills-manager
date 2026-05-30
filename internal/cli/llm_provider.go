package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

var llmHTTPClient = &http.Client{Timeout: 60 * time.Second}
var llmCommandRunner = runLLMCommand

const (
	llmCommandTimeout      = 5 * time.Minute
	llmProviderOutputLimit = 2 << 20
)

type llmProviderResult struct {
	Output string
	Usage  llmUsage
}

func runConfiguredLLMProvider(home, prompt string) (string, error) {
	cfg, err := loadManagerConfig(home)
	if err != nil {
		return "", err
	}
	result, err := callConfiguredLLMProvider(cfg.LLM, prompt)
	if err != nil {
		return "", err
	}
	if err := recordLLMUsage(home, result.Usage); err != nil {
		return "", err
	}
	return result.Output, nil
}

func callConfiguredLLMProvider(cfg llmConfig, prompt string) (llmProviderResult, error) {
	if cfg.Provider == "" {
		return llmProviderResult{}, fmt.Errorf("no LLM provider configured; set llm.provider or use --handoff")
	}
	switch cfg.Provider {
	case "anthropic":
		apiKey, err := apiProviderAPIKey(cfg)
		if err != nil {
			return llmProviderResult{}, err
		}
		return callAnthropic(cfg, apiKey, prompt)
	case "openai":
		apiKey, err := apiProviderAPIKey(cfg)
		if err != nil {
			return llmProviderResult{}, err
		}
		return callOpenAI(cfg, apiKey, prompt)
	case "codex-cli":
		return callCodexCLI(cfg, prompt)
	case "cursor-cli":
		return callCursorCLI(cfg, prompt)
	default:
		return llmProviderResult{}, fmt.Errorf("unsupported LLM provider %q", cfg.Provider)
	}
}

func apiProviderAPIKey(cfg llmConfig) (string, error) {
	if cfg.APIKeyEnv == "" || cfg.Model == "" {
		return "", fmt.Errorf("no LLM provider configured; set llm.provider, llm.api_key-env, and llm.model or use --handoff")
	}
	apiKey := strings.TrimSpace(os.Getenv(cfg.APIKeyEnv))
	if apiKey == "" {
		return "", fmt.Errorf("environment variable %s is not set", cfg.APIKeyEnv)
	}
	return apiKey, nil
}

func callAnthropic(cfg llmConfig, apiKey, prompt string) (llmProviderResult, error) {
	body := map[string]interface{}{
		"model":      cfg.Model,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := postLLMJSON(anthropicBaseURL()+"/v1/messages", body, map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": "2023-06-01",
	}, &resp); err != nil {
		return llmProviderResult{}, err
	}
	var out strings.Builder
	for _, part := range resp.Content {
		if part.Text != "" {
			out.WriteString(part.Text)
		}
	}
	return llmProviderResult{
		Output: out.String(),
		Usage: llmUsage{
			InputTokens:      resp.Usage.InputTokens,
			OutputTokens:     resp.Usage.OutputTokens,
			EstimatedCostUSD: estimateLLMCostUSD(cfg.Provider, cfg.Model, resp.Usage.InputTokens, resp.Usage.OutputTokens),
		},
	}, nil
}

func callOpenAI(cfg llmConfig, apiKey, prompt string) (llmProviderResult, error) {
	body := map[string]interface{}{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := postLLMJSON(openAIBaseURL()+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer " + apiKey,
	}, &resp); err != nil {
		return llmProviderResult{}, err
	}
	output := ""
	if len(resp.Choices) > 0 {
		output = resp.Choices[0].Message.Content
	}
	return llmProviderResult{
		Output: output,
		Usage: llmUsage{
			InputTokens:      resp.Usage.PromptTokens,
			OutputTokens:     resp.Usage.CompletionTokens,
			EstimatedCostUSD: estimateLLMCostUSD(cfg.Provider, cfg.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens),
		},
	}, nil
}

func callCodexCLI(cfg llmConfig, prompt string) (llmProviderResult, error) {
	args := []string{"exec", "--skip-git-repo-check"}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	args = append(args, "-")
	out, err := llmCommandRunner(llmCommandTimeout, "codex", args, prompt)
	if err != nil {
		return llmProviderResult{}, err
	}
	return llmProviderResult{Output: extractLLMCommandOutput(out)}, nil
}

func callCursorCLI(cfg llmConfig, prompt string) (llmProviderResult, error) {
	args := []string{"-p", "--output-format", "json", "--mode", "ask", "--trust"}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	promptArg, cleanup, err := writeCursorPromptArg(prompt)
	if err != nil {
		return llmProviderResult{}, err
	}
	defer cleanup()
	args = append(args, promptArg)
	out, err := llmCommandRunner(llmCommandTimeout, "cursor-agent", args, "")
	if err != nil {
		return llmProviderResult{}, err
	}
	return llmProviderResult{Output: extractLLMCommandOutput(out)}, nil
}

func writeCursorPromptArg(prompt string) (string, func(), error) {
	f, err := os.CreateTemp("", "skills-manager-cursor-prompt-*.md")
	if err != nil {
		return "", func() {}, fmt.Errorf("create cursor prompt file: %w", err)
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := f.WriteString(prompt); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("write cursor prompt file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close cursor prompt file: %w", err)
	}
	return "Read the full skills-manager provider prompt from this UTF-8 file, follow that prompt, and return only the requested output: " + path, cleanup, nil
}

func runLLMCommand(timeout time.Duration, name string, args []string, stdin string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", fmt.Errorf("%s executable not found on PATH; install and authenticate %s, or choose another llm.provider", name, name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	configureCommandProcessGroup(cmd)
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		killCommandProcessGroup(cmd)
		return "", fmt.Errorf("%s timed out after %s", name, timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s failed: %s", name, msg)
	}
	if stdout.Len() > llmProviderOutputLimit {
		return "", fmt.Errorf("%s returned too much output: %d bytes exceeds %d byte limit", name, stdout.Len(), llmProviderOutputLimit)
	}
	return stdout.String(), nil
}

func extractLLMCommandOutput(out string) string {
	trimmed := strings.TrimSpace(out)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err == nil {
		for _, key := range []string{"result", "output", "text", "content", "message", "response"} {
			if value, ok := raw[key]; ok {
				var s string
				if err := json.Unmarshal(value, &s); err == nil {
					return normalizeLLMTextOutput(s)
				}
				var nested map[string]json.RawMessage
				if err := json.Unmarshal(value, &nested); err == nil {
					for _, nestedKey := range []string{"content", "text"} {
						if nestedValue, ok := nested[nestedKey]; ok {
							if err := json.Unmarshal(nestedValue, &s); err == nil {
								return normalizeLLMTextOutput(s)
							}
						}
					}
				}
			}
		}
	}
	return normalizeLLMTextOutput(trimmed)
}

func normalizeLLMTextOutput(out string) string {
	trimmed := strings.TrimSpace(out)
	if jsonObjectText(trimmed) == trimmed {
		return trimmed
	}
	if extracted := jsonObjectText(trimmed); extracted != "" {
		return extracted
	}
	return trimmed
}

func jsonObjectText(out string) string {
	start := strings.IndexByte(out, '{')
	end := strings.LastIndexByte(out, '}')
	if start < 0 || end <= start {
		return ""
	}
	candidate := strings.TrimSpace(out[start : end+1])
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(candidate), &raw); err != nil {
		return ""
	}
	return candidate
}

func postLLMJSON(url string, body interface{}, headers map[string]string, out interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("parse provider response: %w", err)
	}
	return nil
}

func anthropicBaseURL() string {
	if value := strings.TrimRight(os.Getenv("SKILLS_MANAGER_ANTHROPIC_BASE_URL"), "/"); value != "" {
		return value
	}
	return "https://api.anthropic.com"
}

func openAIBaseURL() string {
	if value := strings.TrimRight(os.Getenv("SKILLS_MANAGER_OPENAI_BASE_URL"), "/"); value != "" {
		return value
	}
	return "https://api.openai.com"
}

func estimateLLMCostUSD(provider, model string, inputTokens, outputTokens int) float64 {
	inPerMillion, outPerMillion := 0.0, 0.0
	model = strings.ToLower(model)
	switch provider {
	case "anthropic":
		if strings.Contains(model, "haiku") {
			inPerMillion, outPerMillion = 0.80, 4.00
		} else if strings.Contains(model, "sonnet") {
			inPerMillion, outPerMillion = 3.00, 15.00
		}
	case "openai":
		if strings.Contains(model, "mini") {
			inPerMillion, outPerMillion = 0.15, 0.60
		} else if strings.Contains(model, "gpt-4") || strings.Contains(model, "gpt-5") {
			inPerMillion, outPerMillion = 2.50, 10.00
		}
	}
	return (float64(inputTokens)*inPerMillion + float64(outputTokens)*outPerMillion) / 1_000_000
}
