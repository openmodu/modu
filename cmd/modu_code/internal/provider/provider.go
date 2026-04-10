package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

type Config struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
}

// Resolve returns the model and GetAPIKey function based on env vars.
// Priority: ANTHROPIC_API_KEY > OPENAI_API_KEY > DEEPSEEK_API_KEY > OLLAMA_HOST > LMSTUDIO > config file > built-in default.
func Resolve() (*types.Model, func(string) (string, error)) {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		modelID := os.Getenv("ANTHROPIC_MODEL")
		if modelID == "" {
			modelID = "claude-sonnet-4-6"
		}
		providers.Register(openai.New(
			"anthropic",
			openai.WithBaseURL("https://api.anthropic.com/v1"),
			openai.WithAPIKey(key),
			openai.WithHeaders(map[string]string{"anthropic-version": "2023-06-01"}),
		))
		model := &types.Model{ID: modelID, Name: "Claude " + modelID, ProviderID: "anthropic"}
		return model, func(p string) (string, error) {
			if p == "anthropic" {
				return key, nil
			}
			return "", fmt.Errorf("no key for %s", p)
		}
	}

	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		modelID := os.Getenv("OPENAI_MODEL")
		if modelID == "" {
			modelID = "gpt-4o"
		}
		baseURL := os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		providers.Register(openai.New("openai", openai.WithBaseURL(baseURL), openai.WithAPIKey(key)))
		model := &types.Model{ID: modelID, Name: "OpenAI " + modelID, ProviderID: "openai"}
		return model, func(p string) (string, error) {
			if p == "openai" {
				return key, nil
			}
			return "", fmt.Errorf("no key for %s", p)
		}
	}

	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		modelID := os.Getenv("DEEPSEEK_MODEL")
		if modelID == "" {
			modelID = "deepseek-chat"
		}
		providers.Register(openai.New("deepseek", openai.WithBaseURL("https://api.deepseek.com/v1"), openai.WithAPIKey(key)))
		model := &types.Model{ID: modelID, Name: "DeepSeek " + modelID, ProviderID: "deepseek"}
		return model, func(p string) (string, error) {
			if p == "deepseek" {
				return key, nil
			}
			return "", fmt.Errorf("no key for %s", p)
		}
	}

	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		modelID := os.Getenv("OLLAMA_MODEL")
		if modelID == "" {
			fmt.Fprintln(os.Stderr, "OLLAMA_HOST set but OLLAMA_MODEL is empty")
			return nil, nil
		}
		providers.Register(openai.New("ollama", openai.WithBaseURL(strings.TrimRight(host, "/")+"/v1")))
		model := &types.Model{ID: modelID, Name: modelID + " (Ollama)", ProviderID: "ollama"}
		return model, func(string) (string, error) { return "", nil }
	}

	if lmModel, lmURL := os.Getenv("LMSTUDIO_MODEL"), os.Getenv("LMSTUDIO_BASE_URL"); lmModel != "" || lmURL != "" {
		modelName := lmModel
		if modelName == "" {
			modelName = "qwen/qwen3.5-35b-a3b"
		}
		baseURL := lmURL
		if baseURL == "" {
			baseURL = "http://localhost:1234/v1"
		}
		providers.Register(openai.New("lmstudio", openai.WithBaseURL(baseURL)))
		model := &types.Model{ID: modelName, Name: modelName + " (LM Studio)", ProviderID: "lmstudio"}
		return model, func(string) (string, error) { return "", nil }
	}

	if cfg, ok := loadConfig(); ok {
		return registerConfig(cfg)
	}

	return registerConfig(Config{
		Provider: "lmstudio",
		Model:    "qwen/qwen3.5-35b-a3b",
		BaseURL:  "http://192.168.5.149:1234/v1",
		APIKey:   "lm-studio",
	})
}

// ResolveThinkingLevel maps the THINKING_LEVEL env var to an agent.ThinkingLevel.
func ResolveThinkingLevel() agent.ThinkingLevel {
	switch strings.ToLower(os.Getenv("THINKING_LEVEL")) {
	case "low":
		return agent.ThinkingLevelLow
	case "medium":
		return agent.ThinkingLevelMedium
	case "high":
		return agent.ThinkingLevelHigh
	default:
		return agent.ThinkingLevelOff
	}
}

func loadConfig() (Config, bool) {
	path := filepath.Join(coding_agent.DefaultAgentDir(), "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, false
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false
	}
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.Provider == "" || cfg.Model == "" || cfg.BaseURL == "" {
		return Config{}, false
	}
	return cfg, true
}

func registerConfig(cfg Config) (*types.Model, func(string) (string, error)) {
	baseURL := cfg.BaseURL
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "lm-studio"
	}
	providers.Register(openai.New(cfg.Provider, openai.WithBaseURL(baseURL), openai.WithAPIKey(apiKey)))
	model := &types.Model{
		ID:         cfg.Model,
		Name:       cfg.Model + " (" + cfg.Provider + ")",
		ProviderID: cfg.Provider,
		BaseURL:    baseURL,
	}
	return model, func(p string) (string, error) {
		if p == cfg.Provider {
			return apiKey, nil
		}
		return "", fmt.Errorf("no key for %s", p)
	}
}
