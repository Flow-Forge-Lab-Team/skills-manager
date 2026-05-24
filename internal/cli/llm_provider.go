package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var llmHTTPClient = &http.Client{Timeout: 60 * time.Second}

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
	if cfg.Provider == "" || cfg.APIKeyEnv == "" || cfg.Model == "" {
		return llmProviderResult{}, fmt.Errorf("no LLM provider configured; set llm.provider, llm.api_key-env, and llm.model or use --handoff")
	}
	apiKey := strings.TrimSpace(os.Getenv(cfg.APIKeyEnv))
	if apiKey == "" {
		return llmProviderResult{}, fmt.Errorf("environment variable %s is not set", cfg.APIKeyEnv)
	}
	switch cfg.Provider {
	case "anthropic":
		return callAnthropic(cfg, apiKey, prompt)
	case "openai":
		return callOpenAI(cfg, apiKey, prompt)
	default:
		return llmProviderResult{}, fmt.Errorf("unsupported LLM provider %q", cfg.Provider)
	}
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
