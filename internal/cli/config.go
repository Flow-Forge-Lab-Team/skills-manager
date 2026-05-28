package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type managerConfig struct {
	Mode   string       `json:"mode"`
	LLM    llmConfig    `json:"llm"`
	Update updateConfig `json:"update"`
}

type updateConfig struct {
	// FrequencyHours is the minimum age before `check` re-polls a skill.
	// Zero means use the default (defaultUpdateFrequencyHours).
	FrequencyHours int `json:"frequency_hours"`
}

const defaultUpdateFrequencyHours = 24

// updateFrequency returns the configured re-poll interval, or the default.
func (c managerConfig) updateFrequency() time.Duration {
	hours := c.Update.FrequencyHours
	if hours < 1 {
		hours = defaultUpdateFrequencyHours
	}
	return time.Duration(hours) * time.Hour
}

type llmConfig struct {
	Provider  string   `json:"provider"`
	APIKeyEnv string   `json:"api_key_env"`
	Model     string   `json:"model"`
	Usage     llmUsage `json:"usage"`
}

type llmUsage struct {
	Calls            int     `json:"calls"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	LastCalledAt     string  `json:"last_called_at"`
}

func runConfig(args []string, realStdout io.Writer, stderr io.Writer, gf globalFlags) int {
	stdout := gf.outWriter(realStdout)
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, helpText("config"))
		if len(args) == 0 {
			return ExitUsageError
		}
		return ExitSuccess
	}

	home, err := managerHome()
	if err != nil {
		fmt.Fprintf(stderr, "manager home: %v\n", err)
		return ExitOpError
	}

	switch args[0] {
	case "set":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "usage: skills-manager config set <key> <value>")
			return ExitUsageError
		}
		cfg, err := loadManagerConfig(home)
		if err != nil {
			fmt.Fprintf(stderr, "read config: %v\n", err)
			return ExitOpError
		}
		if err := setConfigValue(&cfg, args[1], args[2]); err != nil {
			fmt.Fprintln(stderr, err)
			return ExitUsageError
		}
		if err := saveManagerConfig(home, cfg); err != nil {
			fmt.Fprintf(stderr, "write config: %v\n", err)
			return ExitOpError
		}
		key := canonicalConfigKey(args[1])
		if gf.JSON {
			if err := writeJSON(realStdout, map[string]interface{}{
				"key":     key,
				"value":   args[2],
				"updated": true,
			}); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
			return ExitSuccess
		}
		fmt.Fprintf(stdout, "Set %s\n", key)
		return ExitSuccess
	case "get":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: skills-manager config get <key>")
			return ExitUsageError
		}
		cfg, err := loadManagerConfig(home)
		if err != nil {
			fmt.Fprintf(stderr, "read config: %v\n", err)
			return ExitOpError
		}
		value, err := getConfigValue(cfg, args[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return ExitUsageError
		}
		if gf.JSON {
			if err := writeJSON(realStdout, map[string]string{
				"key":   canonicalConfigKey(args[1]),
				"value": value,
			}); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
			return ExitSuccess
		}
		fmt.Fprintln(stdout, value)
		return ExitSuccess
	case "show":
		if len(args) > 2 {
			fmt.Fprintln(stderr, "usage: skills-manager config show [llm.usage]")
			return ExitUsageError
		}
		cfg, err := loadManagerConfig(home)
		if err != nil {
			fmt.Fprintf(stderr, "read config: %v\n", err)
			return ExitOpError
		}
		if len(args) == 2 {
			if canonicalConfigKey(args[1]) != "llm.usage" {
				fmt.Fprintln(stderr, "usage: skills-manager config show [llm.usage]")
				return ExitUsageError
			}
			if gf.JSON {
				if err := writeJSON(realStdout, cfg.LLM.Usage); err != nil {
					fmt.Fprintln(stderr, err)
					return ExitOpError
				}
				return ExitSuccess
			}
			printLLMUsage(stdout, cfg.LLM.Usage)
			return ExitSuccess
		}
		if gf.JSON {
			if err := writeJSON(realStdout, cfg); err != nil {
				fmt.Fprintln(stderr, err)
				return ExitOpError
			}
			return ExitSuccess
		}
		printManagerConfig(stdout, cfg)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "unknown config command: %s\n", args[0])
		return ExitUsageError
	}
}

func configPath(home string) string {
	return filepath.Join(home, "config.yaml")
}

func loadManagerConfig(home string) (managerConfig, error) {
	var cfg managerConfig
	data, err := os.ReadFile(configPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	section := ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(raw, " ") && strings.HasSuffix(line, ":") {
			section = strings.TrimSuffix(line, ":")
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		if value == "~" {
			value = ""
		}
		if section == "" && key == "mode" {
			cfg.Mode = value
			continue
		}
		switch section + "." + key {
		case "llm.provider":
			cfg.LLM.Provider = value
		case "llm.api_key_env", "llm.api_key-env", "llm.api-key-env":
			cfg.LLM.APIKeyEnv = value
		case "llm.model":
			cfg.LLM.Model = value
		case "llm.calls":
			cfg.LLM.Usage.Calls, _ = strconv.Atoi(value)
		case "llm.input_tokens":
			cfg.LLM.Usage.InputTokens, _ = strconv.Atoi(value)
		case "llm.output_tokens":
			cfg.LLM.Usage.OutputTokens, _ = strconv.Atoi(value)
		case "llm.estimated_cost_usd":
			cfg.LLM.Usage.EstimatedCostUSD, _ = strconv.ParseFloat(value, 64)
		case "llm.last_called_at":
			cfg.LLM.Usage.LastCalledAt = value
		case "update.frequency_hours":
			cfg.Update.FrequencyHours, _ = strconv.Atoi(value)
		}
	}
	return cfg, scanner.Err()
}

func saveManagerConfig(home string, cfg managerConfig) error {
	if err := os.MkdirAll(home, 0755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("version: 1\n")
	writeConfigString(&b, "", "mode", cfg.Mode)
	b.WriteString("llm:\n")
	writeConfigString(&b, "  ", "provider", cfg.LLM.Provider)
	writeConfigString(&b, "  ", "api_key_env", cfg.LLM.APIKeyEnv)
	writeConfigString(&b, "  ", "model", cfg.LLM.Model)
	fmt.Fprintf(&b, "  calls: %d\n", cfg.LLM.Usage.Calls)
	fmt.Fprintf(&b, "  input_tokens: %d\n", cfg.LLM.Usage.InputTokens)
	fmt.Fprintf(&b, "  output_tokens: %d\n", cfg.LLM.Usage.OutputTokens)
	fmt.Fprintf(&b, "  estimated_cost_usd: %.8f\n", cfg.LLM.Usage.EstimatedCostUSD)
	writeConfigString(&b, "  ", "last_called_at", cfg.LLM.Usage.LastCalledAt)
	b.WriteString("update:\n")
	fmt.Fprintf(&b, "  frequency_hours: %d\n", cfg.Update.FrequencyHours)
	if preserved := preservedConfigSections(home); preserved != "" {
		b.WriteByte('\n')
		b.WriteString(preserved)
		if !strings.HasSuffix(preserved, "\n") {
			b.WriteByte('\n')
		}
	}
	return os.WriteFile(configPath(home), []byte(b.String()), 0600)
}

func preservedConfigSections(home string) string {
	data, err := os.ReadFile(configPath(home))
	if err != nil {
		return ""
	}
	var kept []string
	skip := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !skip {
				kept = append(kept, line)
			}
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			key, _, ok := strings.Cut(trimmed, ":")
			if ok {
				switch strings.TrimSpace(key) {
				case "version", "mode", "llm", "update":
					skip = true
					continue
				default:
					skip = false
				}
			}
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	return strings.Trim(strings.Join(kept, "\n"), "\n")
}

func writeConfigString(b *strings.Builder, indent, key, value string) {
	if value == "" {
		fmt.Fprintf(b, "%s%s: ~\n", indent, key)
		return
	}
	fmt.Fprintf(b, "%s%s: %q\n", indent, key, value)
}

func setConfigValue(cfg *managerConfig, key, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("config value must not be empty")
	}
	switch canonicalConfigKey(key) {
	case "mode":
		if value != "copy" && value != "symlink" {
			return fmt.Errorf("mode must be copy or symlink")
		}
		cfg.Mode = value
	case "llm.provider":
		provider := strings.ToLower(value)
		if provider != "anthropic" && provider != "openai" {
			return fmt.Errorf("llm.provider must be anthropic or openai")
		}
		cfg.LLM.Provider = provider
	case "llm.api_key-env":
		cfg.LLM.APIKeyEnv = value
	case "llm.model":
		cfg.LLM.Model = value
	case "update.frequency-hours":
		hours, err := strconv.Atoi(value)
		if err != nil || hours < 1 {
			return fmt.Errorf("update.frequency_hours must be a positive integer")
		}
		cfg.Update.FrequencyHours = hours
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

func getConfigValue(cfg managerConfig, key string) (string, error) {
	switch canonicalConfigKey(key) {
	case "mode":
		return cfg.Mode, nil
	case "llm.provider":
		return cfg.LLM.Provider, nil
	case "llm.api_key-env":
		return cfg.LLM.APIKeyEnv, nil
	case "llm.model":
		return cfg.LLM.Model, nil
	case "update.frequency-hours":
		return strconv.Itoa(cfg.Update.FrequencyHours), nil
	default:
		return "", fmt.Errorf("unknown config key: %s", key)
	}
}

func canonicalConfigKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	key = strings.ReplaceAll(key, "_", "-")
	if key == "llm.api-key-env" {
		return "llm.api_key-env"
	}
	return key
}

func llmProviderConfigured(home string) bool {
	cfg, err := loadManagerConfig(home)
	return err == nil && cfg.LLM.Provider != "" && cfg.LLM.APIKeyEnv != "" && cfg.LLM.Model != ""
}

func recordLLMUsage(home string, usage llmUsage) error {
	cfg, err := loadManagerConfig(home)
	if err != nil {
		return err
	}
	cfg.LLM.Usage.Calls++
	cfg.LLM.Usage.InputTokens += usage.InputTokens
	cfg.LLM.Usage.OutputTokens += usage.OutputTokens
	cfg.LLM.Usage.EstimatedCostUSD += usage.EstimatedCostUSD
	cfg.LLM.Usage.LastCalledAt = time.Now().UTC().Format(time.RFC3339)
	return saveManagerConfig(home, cfg)
}

func printManagerConfig(w io.Writer, cfg managerConfig) {
	fmt.Fprintf(w, "mode: %s\n", emptyAsTilde(cfg.Mode))
	fmt.Fprintln(w, "llm:")
	fmt.Fprintf(w, "  provider: %s\n", emptyAsTilde(cfg.LLM.Provider))
	fmt.Fprintf(w, "  api_key-env: %s\n", emptyAsTilde(cfg.LLM.APIKeyEnv))
	fmt.Fprintf(w, "  model: %s\n", emptyAsTilde(cfg.LLM.Model))
	fmt.Fprintln(w, "  usage:")
	printLLMUsageIndented(w, cfg.LLM.Usage, "    ")
	fmt.Fprintln(w, "update:")
	fmt.Fprintf(w, "  frequency_hours: %d\n", cfg.Update.FrequencyHours)
}

func printLLMUsage(w io.Writer, usage llmUsage) {
	printLLMUsageIndented(w, usage, "")
}

func printLLMUsageIndented(w io.Writer, usage llmUsage, indent string) {
	fmt.Fprintf(w, "%scalls: %d\n", indent, usage.Calls)
	fmt.Fprintf(w, "%sinput_tokens: %d\n", indent, usage.InputTokens)
	fmt.Fprintf(w, "%soutput_tokens: %d\n", indent, usage.OutputTokens)
	fmt.Fprintf(w, "%sestimated_cost_usd: %.8f\n", indent, usage.EstimatedCostUSD)
	fmt.Fprintf(w, "%slast_called_at: %s\n", indent, emptyAsTilde(usage.LastCalledAt))
}

func emptyAsTilde(value string) string {
	if value == "" {
		return "~"
	}
	return value
}
