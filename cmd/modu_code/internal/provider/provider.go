package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

type Config struct {
	Version      int                       `json:"version,omitempty"`
	Active       string                    `json:"active,omitempty"`
	Roles        map[string]string         `json:"roles,omitempty"`
	ScopedModels []string                  `json:"scopedModels,omitempty"`
	Reasoning    ReasoningConfig           `json:"reasoning,omitempty"`
	Providers    map[string]ProviderConfig `json:"providers,omitempty"`
	Models       []ModelConfig             `json:"models,omitempty"`

	// Legacy single-model fields. Kept so existing ~/.coding_agent/config.json
	// files continue to work.
	Provider string            `json:"provider,omitempty"`
	Model    string            `json:"model,omitempty"`
	BaseURL  string            `json:"baseUrl,omitempty"`
	APIKey   string            `json:"apiKey,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type ProviderConfig struct {
	Type      string            `json:"type,omitempty"`
	BaseURL   string            `json:"baseUrl,omitempty"`
	APIKey    string            `json:"apiKey,omitempty"`
	APIKeyEnv string            `json:"apiKeyEnv,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type ModelConfig struct {
	Name         string   `json:"name,omitempty"`
	Description  string   `json:"description,omitempty"`
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities,omitempty"`
	// Legacy per-model connection fields. New config files keep these values in
	// Config.Providers, but these remain readable for existing files.
	BaseURL string            `json:"baseUrl,omitempty"`
	APIKey  string            `json:"apiKey,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type ReasoningConfig struct {
	Level string `json:"level,omitempty"`
}

type ConfigValidation struct {
	Path       string
	Active     string
	ModelCount int
	Problems   []string
}

type ModelDiscovery struct {
	Provider string
	Found    int
	Added    int
	Updated  int
	Models   []string
}

var modelDiscoveryHTTPClient = &http.Client{Timeout: 15 * time.Second}

const exampleConfigJSON = `{
  "version": 2,
  "active": "local-qwen",
  "roles": {
    "summary": "local-qwen",
    "dispatcher": "deepseek"
  },
  "scopedModels": [
    "local-qwen",
    "deepseek"
  ],
  "reasoning": {
    "level": "off"
  },
  "providers": {
    "lmstudio": {
      "type": "openai-compatible",
      "baseUrl": "http://127.0.0.1:1234/v1",
      "apiKey": "lm-studio"
    },
    "deepseek": {
      "type": "openai-compatible",
      "baseUrl": "https://api.deepseek.com/v1",
      "apiKeyEnv": "DEEPSEEK_API_KEY"
    }
  },
  "models": [
    {
      "name": "local-qwen",
      "description": "local coding model",
      "provider": "lmstudio",
      "model": "qwen/qwen3.6-35b-a3b",
      "capabilities": ["tools"]
    },
    {
      "name": "deepseek",
      "description": "remote fallback model",
      "provider": "deepseek",
      "model": "deepseek-chat",
      "capabilities": ["tools"]
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

// ResolveThinkingLevel maps the THINKING_LEVEL env var to an types.ThinkingLevel.
func ResolveThinkingLevel() types.ThinkingLevel {
	if cfg, ok := LoadConfig(); ok {
		switch strings.ToLower(cfg.Reasoning.Level) {
		case "low":
			return types.ThinkingLevelLow
		case "medium":
			return types.ThinkingLevelMedium
		case "high":
			return types.ThinkingLevelHigh
		case "off":
			return types.ThinkingLevelOff
		}
	}
	switch strings.ToLower(os.Getenv("THINKING_LEVEL")) {
	case "low":
		return types.ThinkingLevelLow
	case "medium":
		return types.ThinkingLevelMedium
	case "high":
		return types.ThinkingLevelHigh
	default:
		return types.ThinkingLevelOff
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

func LoadConfigFile() (Config, bool, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, true, err
	}
	normalizeConfig(&cfg)
	migrateLegacyConfig(&cfg)
	return cfg, true, nil
}

func SaveConfig(cfg Config) error {
	normalizeConfig(&cfg)
	migrateLegacyConfig(&cfg)
	stripLegacyFields(&cfg)
	cfg.Version = 2
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), append(data, '\n'), 0o600)
}

func stripLegacyFields(cfg *Config) {
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.APIKey = ""
	cfg.Headers = nil
	for i := range cfg.Models {
		cfg.Models[i].BaseURL = ""
		cfg.Models[i].APIKey = ""
		cfg.Models[i].Headers = nil
	}
}

func ValidateConfig() ConfigValidation {
	path := ConfigPath()
	result := ConfigValidation{Path: path}
	cfg, exists, err := LoadConfigFile()
	if err != nil {
		result.Problems = append(result.Problems, "invalid JSON: "+err.Error())
		return result
	}
	if !exists {
		result.Problems = append(result.Problems, "config not found: "+path)
		return result
	}
	result.Active = cfg.Active
	result.ModelCount = len(cfg.modelConfigs())
	if result.ModelCount == 0 {
		result.Problems = append(result.Problems, "no valid models configured")
	}
	if len(cfg.Models) > 0 {
		seenNames := make(map[string]struct{}, len(cfg.Models))
		for i, model := range cfg.Models {
			validateModelConfig(i, model, &result)
			if model.Provider != "" && modelProviderConfig(cfg, model).BaseURL == "" {
				result.Problems = append(result.Problems, fmt.Sprintf("models[%d].provider %q has no baseUrl", i, model.Provider))
			}
			if model.Name == "" {
				continue
			}
			if _, ok := seenNames[model.Name]; ok {
				result.Problems = append(result.Problems, fmt.Sprintf("models[%d].name duplicates %q", i, model.Name))
			}
			seenNames[model.Name] = struct{}{}
		}
	}
	for providerID, pc := range cfg.Providers {
		if strings.TrimSpace(pc.BaseURL) == "" {
			result.Problems = append(result.Problems, fmt.Sprintf("providers.%s.baseUrl is required", providerID))
		}
	}
	if cfg.Active != "" && !activeMatchesAny(cfg) {
		result.Problems = append(result.Problems, "active model does not match any configured model")
	}
	for _, id := range cfg.ScopedModels {
		if !configModelTargetExists(cfg, id) {
			result.Problems = append(result.Problems, "scoped model does not match any configured model: "+id)
		}
	}
	return result
}

func LoadConfig() (Config, bool) {
	cfg, exists, err := LoadConfigFile()
	if err != nil || !exists {
		return Config{}, false
	}
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
	return SaveConfig(cfg)
}

func UpsertModelConfig(entry ModelConfig) (bool, error) {
	normalizeModelConfig(&entry)
	if entry.Name == "" {
		return false, fmt.Errorf("model name is required")
	}
	var validation ConfigValidation
	validateModelConfig(0, entry, &validation)
	if len(validation.Problems) > 0 {
		return false, fmt.Errorf("%s", strings.Join(validation.Problems, "; "))
	}

	cfg, exists, err := LoadConfigFile()
	if err != nil {
		return false, err
	}
	if !exists {
		cfg = Config{}
	}
	if entry.BaseURL == "" && cfg.Providers[entry.Provider].BaseURL == "" {
		return false, fmt.Errorf("provider baseUrl is required")
	}
	upsertProviderForModel(&cfg, entry)
	entry.BaseURL = ""
	entry.APIKey = ""
	entry.Headers = nil

	for i, model := range cfg.Models {
		if model.Name == entry.Name {
			cfg.Models[i] = entry
			if cfg.Active == "" {
				cfg.Active = entry.Name
			}
			if err := SaveConfig(cfg); err != nil {
				return false, err
			}
			registerConfig(cfg)
			return false, nil
		}
	}

	cfg.Models = append(cfg.Models, entry)
	if cfg.Active == "" {
		cfg.Active = entry.Name
	}
	if err := SaveConfig(cfg); err != nil {
		return false, err
	}
	registerConfig(cfg)
	return true, nil
}

func UpsertProviderConfig(providerID string, config ProviderConfig) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return fmt.Errorf("provider is required")
	}
	config.Type = strings.TrimSpace(config.Type)
	if config.Type == "" {
		config.Type = "openai-compatible"
	}
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.APIKeyEnv = strings.TrimSpace(config.APIKeyEnv)
	if config.BaseURL == "" {
		return fmt.Errorf("provider baseUrl is required")
	}
	cfg, exists, err := LoadConfigFile()
	if err != nil {
		return err
	}
	if !exists {
		cfg = Config{}
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	existing := cfg.Providers[providerID]
	if config.APIKey == "" {
		config.APIKey = existing.APIKey
	}
	if config.APIKeyEnv == "" {
		config.APIKeyEnv = existing.APIKeyEnv
	}
	if len(config.Headers) == 0 {
		config.Headers = existing.Headers
	}
	cfg.Providers[providerID] = config
	return SaveConfig(cfg)
}

func DiscoverProviderModels(ctx context.Context, providerID string) (ModelDiscovery, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return ModelDiscovery{}, fmt.Errorf("provider is required")
	}
	cfg, exists, err := LoadConfigFile()
	if err != nil {
		return ModelDiscovery{}, err
	}
	if !exists {
		return ModelDiscovery{}, fmt.Errorf("config not found: %s", ConfigPath())
	}
	pc, ok := cfg.Providers[providerID]
	if !ok {
		return ModelDiscovery{}, fmt.Errorf("provider not found: %s", providerID)
	}
	modelIDs, err := fetchOpenAICompatibleModelIDs(ctx, pc)
	if err != nil {
		return ModelDiscovery{}, err
	}
	discovery := upsertDiscoveredModels(&cfg, providerID, modelIDs)
	if err := SaveConfig(cfg); err != nil {
		return ModelDiscovery{}, err
	}
	registerConfig(cfg)
	return discovery, nil
}

func SetActiveModel(target string) (ModelConfig, error) {
	cfg, ok := LoadConfig()
	if !ok {
		return ModelConfig{}, fmt.Errorf("no valid config found: %s", ConfigPath())
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return ModelConfig{}, fmt.Errorf("model target is required")
	}
	for _, model := range cfg.modelConfigs() {
		if modelMatchesActive(model, target) {
			if model.Name != "" {
				cfg.Active = model.Name
			} else {
				cfg.Active = model.Provider + "/" + model.Model
			}
			if err := SaveConfig(cfg); err != nil {
				return ModelConfig{}, err
			}
			return model, nil
		}
	}
	return ModelConfig{}, fmt.Errorf("model not found: %s", target)
}

func RemoveModelConfig(target string) (ModelConfig, error) {
	cfg, exists, err := LoadConfigFile()
	if err != nil {
		return ModelConfig{}, err
	}
	if !exists {
		return ModelConfig{}, fmt.Errorf("config not found: %s", ConfigPath())
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return ModelConfig{}, fmt.Errorf("model target is required")
	}

	idx := -1
	var removed ModelConfig
	for i, model := range cfg.Models {
		if modelMatchesActive(model, target) {
			idx = i
			removed = model
			break
		}
	}
	if idx < 0 {
		return ModelConfig{}, fmt.Errorf("model not found: %s", target)
	}

	cfg.Models = append(cfg.Models[:idx], cfg.Models[idx+1:]...)
	if cfg.Active != "" && modelMatchesActive(removed, cfg.Active) {
		cfg.Active = ""
		if len(cfg.modelConfigs()) > 0 {
			next := cfg.modelConfigs()[0]
			if next.Name != "" {
				cfg.Active = next.Name
			} else {
				cfg.Active = next.Provider + "/" + next.Model
			}
		}
	}
	if err := SaveConfig(cfg); err != nil {
		return ModelConfig{}, err
	}
	unregisterModel(removed)
	return removed, nil
}

func ConfiguredModelIDs() []string {
	cfg, ok := LoadConfig()
	if !ok {
		return nil
	}
	if len(cfg.ScopedModels) > 0 {
		return configTargetsToModelIDs(cfg, cfg.ScopedModels)
	}
	models := cfg.modelConfigs()
	out := make([]string, 0, len(models))
	for _, model := range models {
		out = append(out, model.Model)
	}
	return out
}

func SetScopedModelIDs(ids []string) error {
	cfg, exists, err := LoadConfigFile()
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("config not found: %s", ConfigPath())
	}
	if len(ids) == 0 {
		cfg.ScopedModels = nil
		return SaveConfig(cfg)
	}
	for _, id := range ids {
		if !configModelTargetExists(cfg, id) {
			return fmt.Errorf("scoped model not found: %s", id)
		}
	}
	cfg.ScopedModels = configTargetsToScopeIDs(cfg, ids)
	return SaveConfig(cfg)
}

func normalizeConfig(cfg *Config) {
	if cfg.Version == 0 {
		cfg.Version = 2
	}
	cfg.Active = strings.TrimSpace(cfg.Active)
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Reasoning.Level = strings.TrimSpace(strings.ToLower(cfg.Reasoning.Level))
	for k, v := range cfg.Providers {
		id := strings.TrimSpace(k)
		v.Type = strings.TrimSpace(v.Type)
		v.BaseURL = strings.TrimSpace(v.BaseURL)
		v.APIKey = strings.TrimSpace(v.APIKey)
		v.APIKeyEnv = strings.TrimSpace(v.APIKeyEnv)
		if id != k {
			if cfg.Providers == nil {
				cfg.Providers = map[string]ProviderConfig{}
			}
			delete(cfg.Providers, k)
			cfg.Providers[id] = v
		} else {
			cfg.Providers[k] = v
		}
	}
	for i := range cfg.ScopedModels {
		cfg.ScopedModels[i] = strings.TrimSpace(cfg.ScopedModels[i])
	}
	for i := range cfg.Models {
		normalizeModelConfig(&cfg.Models[i])
	}
}

func normalizeModelConfig(model *ModelConfig) {
	model.Name = strings.TrimSpace(model.Name)
	model.Description = strings.TrimSpace(model.Description)
	model.Provider = strings.TrimSpace(model.Provider)
	model.Model = strings.TrimSpace(model.Model)
	model.BaseURL = strings.TrimSpace(model.BaseURL)
	model.APIKey = strings.TrimSpace(model.APIKey)
	for i := range model.Capabilities {
		model.Capabilities[i] = strings.TrimSpace(model.Capabilities[i])
	}
}

func migrateLegacyConfig(cfg *Config) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	for i := range cfg.Models {
		model := &cfg.Models[i]
		if model.Provider == "" {
			continue
		}
		pc := cfg.Providers[model.Provider]
		if pc.Type == "" {
			pc.Type = "openai-compatible"
		}
		if pc.BaseURL == "" {
			pc.BaseURL = model.BaseURL
		}
		if pc.APIKey == "" {
			pc.APIKey = model.APIKey
		}
		if len(pc.Headers) == 0 && len(model.Headers) > 0 {
			pc.Headers = model.Headers
		}
		cfg.Providers[model.Provider] = pc
	}
	if cfg.Provider != "" && cfg.Model != "" && cfg.BaseURL != "" && len(cfg.Models) == 0 {
		cfg.Providers[cfg.Provider] = ProviderConfig{
			Type:    "openai-compatible",
			BaseURL: cfg.BaseURL,
			APIKey:  cfg.APIKey,
			Headers: cfg.Headers,
		}
		cfg.Models = []ModelConfig{{
			Provider: cfg.Provider,
			Model:    cfg.Model,
		}}
	}
}

func (cfg Config) modelConfigs() []ModelConfig {
	if len(cfg.Models) > 0 {
		out := make([]ModelConfig, 0, len(cfg.Models))
		for _, m := range cfg.Models {
			if m.Provider != "" && m.Model != "" && modelProviderConfig(cfg, m).BaseURL != "" {
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
		pc := modelProviderConfig(cfg, entry)
		baseURL := normalizedBaseURL(pc.BaseURL)
		apiKey := resolveProviderAPIKey(pc)
		if apiKey == "" {
			apiKey = "lm-studio"
		}
		keys[entry.Provider] = apiKey
		if !registeredProviders[entry.Provider] {
			opts := []openai.Option{openai.WithBaseURL(baseURL), openai.WithAPIKey(apiKey)}
			if len(pc.Headers) > 0 {
				opts = append(opts, openai.WithHeaders(pc.Headers))
			}
			providers.Register(openai.New(entry.Provider, opts...))
			registeredProviders[entry.Provider] = true
		}
		registerModel(entry, baseURL, pc.Headers)
	}

	active, ok := cfg.activeModel()
	if !ok {
		return nil, nil
	}
	model := providers.GetModel(active.Provider, active.Model)
	if model == nil {
		pc := modelProviderConfig(cfg, active)
		baseURL := normalizedBaseURL(pc.BaseURL)
		registerModel(active, baseURL, pc.Headers)
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

func modelProviderConfig(cfg Config, model ModelConfig) ProviderConfig {
	pc := cfg.Providers[model.Provider]
	if pc.BaseURL == "" {
		pc.BaseURL = model.BaseURL
	}
	if pc.APIKey == "" {
		pc.APIKey = model.APIKey
	}
	if len(pc.Headers) == 0 && len(model.Headers) > 0 {
		pc.Headers = model.Headers
	}
	if pc.Type == "" {
		pc.Type = "openai-compatible"
	}
	return pc
}

func resolveProviderAPIKey(pc ProviderConfig) string {
	if pc.APIKey != "" {
		return pc.APIKey
	}
	if pc.APIKeyEnv != "" {
		return os.Getenv(pc.APIKeyEnv)
	}
	return ""
}

func fetchOpenAICompatibleModelIDs(ctx context.Context, pc ProviderConfig) ([]string, error) {
	if pc.Type != "" && pc.Type != "openai-compatible" {
		return nil, fmt.Errorf("provider type %q does not support model discovery", pc.Type)
	}
	if strings.TrimSpace(pc.BaseURL) == "" {
		return nil, fmt.Errorf("provider baseUrl is required")
	}
	baseURL := normalizedBaseURL(pc.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if key := resolveProviderAPIKey(pc); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range pc.Headers {
		req.Header.Set(k, v)
	}
	resp, err := modelDiscoveryHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET /models returned %s", resp.Status)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		ids = append(ids, id)
		seen[id] = true
	}
	sort.Strings(ids)
	return ids, nil
}

func upsertDiscoveredModels(cfg *Config, providerID string, modelIDs []string) ModelDiscovery {
	discovery := ModelDiscovery{
		Provider: providerID,
		Found:    len(modelIDs),
		Models:   append([]string(nil), modelIDs...),
	}
	if len(modelIDs) == 0 {
		return discovery
	}
	usedNames := map[string]bool{}
	for _, model := range cfg.Models {
		if model.Name != "" {
			usedNames[model.Name] = true
		}
	}
	for _, modelID := range modelIDs {
		if idx := findRawModelIndex(cfg.Models, providerID, modelID); idx >= 0 {
			if cfg.Models[idx].Name == "" {
				name := uniqueDiscoveredModelName(usedNames, providerID, modelID)
				cfg.Models[idx].Name = name
				usedNames[name] = true
				discovery.Updated++
			}
			continue
		}
		name := uniqueDiscoveredModelName(usedNames, providerID, modelID)
		usedNames[name] = true
		cfg.Models = append(cfg.Models, ModelConfig{
			Name:        name,
			Description: "discovered from " + providerID,
			Provider:    providerID,
			Model:       modelID,
		})
		discovery.Added++
		if cfg.Active == "" {
			cfg.Active = name
		}
	}
	return discovery
}

func findRawModelIndex(models []ModelConfig, providerID, modelID string) int {
	for i, model := range models {
		if model.Provider == providerID && model.Model == modelID {
			return i
		}
	}
	return -1
}

func uniqueDiscoveredModelName(used map[string]bool, providerID, modelID string) string {
	for _, name := range []string{modelID, providerID + "/" + modelID} {
		if !used[name] {
			return name
		}
	}
	base := providerID + "/" + modelID
	for i := 2; ; i++ {
		name := fmt.Sprintf("%s-%d", base, i)
		if !used[name] {
			return name
		}
	}
}

func upsertProviderForModel(cfg *Config, model ModelConfig) {
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	pc := cfg.Providers[model.Provider]
	if pc.Type == "" {
		pc.Type = "openai-compatible"
	}
	if model.BaseURL != "" {
		pc.BaseURL = model.BaseURL
	}
	if model.APIKey != "" {
		pc.APIKey = model.APIKey
	}
	if len(model.Headers) > 0 {
		pc.Headers = model.Headers
	}
	cfg.Providers[model.Provider] = pc
}

func configModelTargetExists(cfg Config, target string) bool {
	for _, model := range cfg.Models {
		if modelMatchesActive(model, target) || modelScopeID(model) == target {
			return true
		}
	}
	return false
}

func modelScopeID(model ModelConfig) string {
	if model.Name != "" {
		return model.Name
	}
	return model.Model
}

func configTargetsToModelIDs(cfg Config, targets []string) []string {
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		for _, model := range cfg.Models {
			if modelMatchesActive(model, target) || modelScopeID(model) == target {
				out = append(out, model.Model)
				break
			}
		}
	}
	return out
}

func configTargetsToScopeIDs(cfg Config, targets []string) []string {
	out := make([]string, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		for _, model := range cfg.Models {
			if modelMatchesActive(model, target) || modelScopeID(model) == target {
				scopeID := modelScopeID(model)
				if !seen[scopeID] {
					out = append(out, scopeID)
					seen[scopeID] = true
				}
				break
			}
		}
	}
	return out
}

func registerModel(cfg ModelConfig, baseURL string, headers map[string]string) {
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
		Headers:    headers,
	}
}

func unregisterModel(cfg ModelConfig) {
	if providers.Models[cfg.Provider] == nil {
		return
	}
	delete(providers.Models[cfg.Provider], cfg.Model)
	if len(providers.Models[cfg.Provider]) == 0 {
		delete(providers.Models, cfg.Provider)
	}
}
