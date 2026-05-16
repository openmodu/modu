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
	Active string        `json:"active,omitempty"`
	Models []ModelConfig `json:"models,omitempty"`

	// Legacy single-model fields. Kept so existing ~/.coding_agent/config.json
	// files continue to work.
	Provider string            `json:"provider,omitempty"`
	Model    string            `json:"model,omitempty"`
	BaseURL  string            `json:"baseUrl,omitempty"`
	APIKey   string            `json:"apiKey,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type ModelConfig struct {
	Name     string            `json:"name,omitempty"`
	Provider string            `json:"provider"`
	Model    string            `json:"model"`
	BaseURL  string            `json:"baseUrl"`
	APIKey   string            `json:"apiKey,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type ConfigValidation struct {
	Path       string
	Active     string
	ModelCount int
	Problems   []string
}

const exampleConfigJSON = `{
  "active": "local-qwen",
  "models": [
    {
      "name": "local-qwen",
      "provider": "lmstudio",
      "model": "qwen/qwen3.6-35b-a3b",
      "baseUrl": "http://127.0.0.1:1234/v1",
      "apiKey": "lm-studio"
    },
    {
      "name": "deepseek",
      "provider": "deepseek",
      "model": "deepseek-chat",
      "baseUrl": "https://api.deepseek.com/v1",
      "apiKey": "..."
    }
  ]
}`

// Resolve returns the model and GetAPIKey function based on env vars.
// Priority: config file > ANTHROPIC_API_KEY > OPENAI_API_KEY > DEEPSEEK_API_KEY > OLLAMA_HOST > LMSTUDIO.
func Resolve() (*types.Model, func(string) (string, error)) {
	if cfg, ok := LoadConfig(); ok {
		return registerConfig(cfg)
	}

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		modelID := os.Getenv("ANTHROPIC_MODEL")
		if modelID == "" {
			fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY set but ANTHROPIC_MODEL is empty")
			return nil, nil
		}
		baseURL := "https://api.anthropic.com/v1"
		providers.Register(openai.New(
			"anthropic",
			openai.WithBaseURL(baseURL),
			openai.WithAPIKey(key),
			openai.WithHeaders(map[string]string{"anthropic-version": "2023-06-01"}),
		))
		model := &types.Model{ID: modelID, Name: modelID + " (Anthropic)", ProviderID: "anthropic", BaseURL: baseURL}
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
		model := &types.Model{ID: modelID, Name: "OpenAI " + modelID, ProviderID: "openai", BaseURL: baseURL}
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
		baseURL := "https://api.deepseek.com/v1"
		providers.Register(openai.New("deepseek", openai.WithBaseURL(baseURL), openai.WithAPIKey(key)))
		model := &types.Model{ID: modelID, Name: "DeepSeek " + modelID, ProviderID: "deepseek", BaseURL: baseURL}
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
		baseURL := strings.TrimRight(host, "/") + "/v1"
		providers.Register(openai.New("ollama", openai.WithBaseURL(baseURL)))
		model := &types.Model{ID: modelID, Name: modelID + " (Ollama)", ProviderID: "ollama", BaseURL: baseURL}
		return model, func(string) (string, error) { return "", nil }
	}

	if lmModel, lmURL := os.Getenv("LMSTUDIO_MODEL"), os.Getenv("LMSTUDIO_BASE_URL"); lmModel != "" || lmURL != "" {
		modelName := lmModel
		if modelName == "" {
			modelName = "qwen/qwen3.6-35b-a3b"
		}
		baseURL := lmURL
		if baseURL == "" {
			baseURL = "http://localhost:1234/v1"
		}
		providers.Register(openai.New("lmstudio", openai.WithBaseURL(baseURL)))
		model := &types.Model{ID: modelName, Name: modelName + " (LM Studio)", ProviderID: "lmstudio"}
		return model, func(string) (string, error) { return "", nil }
	}

	return nil, nil
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

func ConfigPath() string {
	return filepath.Join(coding_agent.DefaultAgentDir(), "config.json")
}

func ExampleConfigJSON() string {
	return exampleConfigJSON + "\n"
}

func InitConfig(force bool) (string, error) {
	path := ConfigPath()
	if _, err := os.Stat(path); err == nil && !force {
		return path, fmt.Errorf("config already exists: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return path, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	return path, os.WriteFile(path, []byte(ExampleConfigJSON()), 0o600)
}

func ValidateConfig() ConfigValidation {
	path := ConfigPath()
	result := ConfigValidation{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		result.Problems = append(result.Problems, err.Error())
		return result
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		result.Problems = append(result.Problems, "invalid JSON: "+err.Error())
		return result
	}
	normalizeConfig(&cfg)
	result.Active = cfg.Active
	result.ModelCount = len(cfg.modelConfigs())
	if result.ModelCount == 0 {
		result.Problems = append(result.Problems, "no valid models configured")
	}
	if len(cfg.Models) > 0 {
		seenNames := make(map[string]struct{}, len(cfg.Models))
		for i, model := range cfg.Models {
			validateModelConfig(i, model, &result)
			if model.Name == "" {
				continue
			}
			if _, ok := seenNames[model.Name]; ok {
				result.Problems = append(result.Problems, fmt.Sprintf("models[%d].name duplicates %q", i, model.Name))
			}
			seenNames[model.Name] = struct{}{}
		}
	}
	if cfg.Active != "" && !activeMatchesAny(cfg) {
		result.Problems = append(result.Problems, "active model does not match any configured model")
	}
	return result
}

func LoadConfig() (Config, bool) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, false
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false
	}
	normalizeConfig(&cfg)
	if len(cfg.modelConfigs()) == 0 {
		return Config{}, false
	}
	return cfg, true
}

func SaveActiveModel(provider, modelID string) error {
	cfg, ok := LoadConfig()
	if !ok {
		return nil
	}
	active := provider + "/" + modelID
	if entry, ok := cfg.findModel(provider, modelID); ok && entry.Name != "" {
		active = entry.Name
	}
	cfg.Active = active
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), append(data, '\n'), 0o600)
}

func ConfiguredModelIDs() []string {
	cfg, ok := LoadConfig()
	if !ok {
		return nil
	}
	models := cfg.modelConfigs()
	out := make([]string, 0, len(models))
	for _, model := range models {
		out = append(out, model.Model)
	}
	return out
}

func normalizeConfig(cfg *Config) {
	cfg.Active = strings.TrimSpace(cfg.Active)
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	for i := range cfg.Models {
		cfg.Models[i].Name = strings.TrimSpace(cfg.Models[i].Name)
		cfg.Models[i].Provider = strings.TrimSpace(cfg.Models[i].Provider)
		cfg.Models[i].Model = strings.TrimSpace(cfg.Models[i].Model)
		cfg.Models[i].BaseURL = strings.TrimSpace(cfg.Models[i].BaseURL)
		cfg.Models[i].APIKey = strings.TrimSpace(cfg.Models[i].APIKey)
	}
}

func (cfg Config) modelConfigs() []ModelConfig {
	if len(cfg.Models) > 0 {
		out := make([]ModelConfig, 0, len(cfg.Models))
		for _, m := range cfg.Models {
			if m.Provider != "" && m.Model != "" && m.BaseURL != "" {
				out = append(out, m)
			}
		}
		return out
	}
	if cfg.Provider == "" || cfg.Model == "" || cfg.BaseURL == "" {
		return nil
	}
	return []ModelConfig{{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		BaseURL:  cfg.BaseURL,
		APIKey:   cfg.APIKey,
		Headers:  cfg.Headers,
	}}
}

func validateModelConfig(i int, model ModelConfig, result *ConfigValidation) {
	prefix := fmt.Sprintf("models[%d]", i)
	if model.Provider == "" {
		result.Problems = append(result.Problems, prefix+".provider is required")
	}
	if model.Model == "" {
		result.Problems = append(result.Problems, prefix+".model is required")
	}
	if model.BaseURL == "" {
		result.Problems = append(result.Problems, prefix+".baseUrl is required")
	}
}

func activeMatchesAny(cfg Config) bool {
	for _, model := range cfg.modelConfigs() {
		if modelMatchesActive(model, cfg.Active) {
			return true
		}
	}
	return false
}

func (cfg Config) activeModel() (ModelConfig, bool) {
	models := cfg.modelConfigs()
	if len(models) == 0 {
		return ModelConfig{}, false
	}
	active := cfg.Active
	if active == "" {
		return models[0], true
	}
	for _, m := range models {
		if modelMatchesActive(m, active) {
			return m, true
		}
	}
	return models[0], true
}

func (cfg Config) findModel(provider, modelID string) (ModelConfig, bool) {
	for _, m := range cfg.modelConfigs() {
		if m.Provider == provider && m.Model == modelID {
			return m, true
		}
	}
	return ModelConfig{}, false
}

func modelMatchesActive(m ModelConfig, active string) bool {
	return active == m.Name ||
		active == m.Model ||
		active == m.Provider+"/"+m.Model ||
		active == m.Provider+":"+m.Model
}

func registerConfig(cfg Config) (*types.Model, func(string) (string, error)) {
	models := cfg.modelConfigs()
	keys := make(map[string]string, len(models))
	registeredProviders := make(map[string]bool, len(models))
	for _, entry := range models {
		baseURL := normalizedBaseURL(entry.BaseURL)
		apiKey := entry.APIKey
		if apiKey == "" {
			apiKey = "lm-studio"
		}
		keys[entry.Provider] = apiKey
		if !registeredProviders[entry.Provider] {
			opts := []openai.Option{openai.WithBaseURL(baseURL), openai.WithAPIKey(apiKey)}
			if len(entry.Headers) > 0 {
				opts = append(opts, openai.WithHeaders(entry.Headers))
			}
			providers.Register(openai.New(entry.Provider, opts...))
			registeredProviders[entry.Provider] = true
		}
		registerModel(entry, baseURL)
	}

	active, ok := cfg.activeModel()
	if !ok {
		return nil, nil
	}
	model := providers.GetModel(active.Provider, active.Model)
	if model == nil {
		baseURL := normalizedBaseURL(active.BaseURL)
		registerModel(active, baseURL)
		model = providers.GetModel(active.Provider, active.Model)
	}
	return model, func(p string) (string, error) {
		if key, ok := keys[p]; ok {
			return key, nil
		}
		return "", fmt.Errorf("no key for %s", p)
	}
}

func normalizedBaseURL(baseURL string) string {
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func registerModel(cfg ModelConfig, baseURL string) {
	if providers.Models[cfg.Provider] == nil {
		providers.Models[cfg.Provider] = make(map[string]*types.Model)
	}
	name := cfg.Name
	if name == "" {
		name = cfg.Model + " (" + cfg.Provider + ")"
	}
	providers.Models[cfg.Provider][cfg.Model] = &types.Model{
		ID:         cfg.Model,
		Name:       name,
		ProviderID: cfg.Provider,
		BaseURL:    baseURL,
		Headers:    cfg.Headers,
	}
}
