package coding_agent

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/openmodu/modu/pkg/agent"
)

// Config holds coding agent configuration from global and project-level settings.
type Config struct {
	// ThinkingLevel controls LLM reasoning depth.
	ThinkingLevel agent.ThinkingLevel `json:"thinkingLevel,omitempty"`

	// AutoCompaction enables automatic context compaction.
	AutoCompaction bool `json:"autoCompaction,omitempty"`

	// DefaultProvider is the default LLM provider name.
	DefaultProvider string `json:"defaultProvider,omitempty"`

	// DefaultModel is the default model ID.
	DefaultModel string `json:"defaultModel,omitempty"`

	// EnabledModels lists explicitly enabled model IDs.
	EnabledModels []string `json:"enabledModels,omitempty"`

	// CompactionSettings controls compaction behavior.
	CompactionSettings CompactionConfig `json:"compactionSettings,omitempty"`

	// CustomSystemPrompt overrides the default system prompt.
	CustomSystemPrompt string `json:"customSystemPrompt,omitempty"`

	// AppendPrompts are additional prompt texts appended to the system prompt.
	AppendPrompts []string `json:"appendPrompts,omitempty"`

	// AutoRetry enables automatic retry on transient errors.
	AutoRetry bool `json:"autoRetry,omitempty"`

	// RetrySettings configures retry behavior.
	RetrySettings RetryConfig `json:"retrySettings,omitempty"`

	// ScopedModels lists models available for cycling.
	ScopedModels []string `json:"scopedModels,omitempty"`

	// SteeringMode controls how steering messages are consumed.
	SteeringMode agent.ExecutionMode `json:"steeringMode,omitempty"`

	// FollowUpMode controls how follow-up messages are consumed.
	FollowUpMode agent.ExecutionMode `json:"followUpMode,omitempty"`

	// BlockImages prevents image content from being sent to the model.
	BlockImages bool `json:"blockImages,omitempty"`
}

// CompactionConfig controls context compaction behavior.
type CompactionConfig struct {
	// MaxContextPercentage triggers compaction when context usage exceeds this percentage.
	MaxContextPercentage float64 `json:"maxContextPercentage,omitempty"`
	// PreserveRecentMessages is the number of recent messages to preserve during compaction.
	PreserveRecentMessages int `json:"preserveRecentMessages,omitempty"`
}

// RetryConfig controls auto-retry behavior.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts. Default: 3.
	MaxRetries int `json:"maxRetries,omitempty"`
	// BaseDelayMs is the base delay in milliseconds for exponential backoff. Default: 1000.
	BaseDelayMs int `json:"baseDelayMs,omitempty"`
	// MaxDelayMs is the maximum delay in milliseconds. Default: 30000.
	MaxDelayMs int `json:"maxDelayMs,omitempty"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ThinkingLevel:  agent.ThinkingLevelMedium,
		AutoCompaction: true,
		CompactionSettings: CompactionConfig{
			MaxContextPercentage:   80.0,
			PreserveRecentMessages: 4,
		},
	}
}

// LoadConfig loads configuration from global and project-level settings files.
// Project settings override global settings.
func LoadConfig(agentDir, cwd string) (*Config, error) {
	cfg := DefaultConfig()

	// Load global config
	globalPath := filepath.Join(agentDir, "settings.json")
	if err := loadConfigFile(globalPath, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Load project config (overrides global)
	projectPath := filepath.Join(cwd, ".coding_agent", "settings.json")
	if err := loadConfigFile(projectPath, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return cfg, nil
}

func loadConfigFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}

// SaveConfig saves the configuration to the given path.
func SaveConfig(cfg *Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// APIKeyStore manages API key storage.
type APIKeyStore struct {
	path string
	keys map[string]string
}

// NewAPIKeyStore creates a new API key store.
func NewAPIKeyStore(agentDir string) *APIKeyStore {
	return &APIKeyStore{
		path: filepath.Join(agentDir, "auth.json"),
		keys: make(map[string]string),
	}
}

// Load reads API keys from disk.
func (s *APIKeyStore) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &s.keys)
}

// Get returns an API key for the given provider.
func (s *APIKeyStore) Get(provider string) (string, bool) {
	key, ok := s.keys[provider]
	return key, ok
}

// Set stores an API key for the given provider.
func (s *APIKeyStore) Set(provider, key string) error {
	s.keys[provider] = key
	return s.save()
}

func (s *APIKeyStore) save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0o600) // Restrictive permissions for secrets
}
