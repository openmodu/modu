package coding_agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// Harness controls host-side runtime behavior.
	Harness HarnessConfig `json:"harness,omitempty"`
}

type HarnessConfig struct {
	// EnableActions allows host-side action dispatch. Defaults to true.
	EnableActions bool `json:"enableActions,omitempty"`
	// BlockTools denies matching tool names before execution.
	BlockTools []string `json:"blockTools,omitempty"`
	// CaptureHints strips and stores harness-only hint tags from tool output.
	CaptureHints *bool `json:"captureHints,omitempty"`
	// PersistToolResults writes plain-text tool artifacts to the runtime tree.
	PersistToolResults *bool `json:"persistToolResults,omitempty"`
	// LogFiles appends JSONL event records for selected harness lifecycle events.
	LogFiles HarnessLogFiles `json:"logFiles,omitempty"`
	// ArtifactFiles overwrites the latest structured event snapshot for selected lifecycle events.
	ArtifactFiles HarnessArtifactFiles `json:"artifactFiles,omitempty"`
	// BridgeDirs writes one structured event file per occurrence for external watchers.
	BridgeDirs HarnessBridgeDirs `json:"bridgeDirs,omitempty"`
	// Actions executes host-side actions for selected lifecycle events.
	Actions HarnessActions `json:"actions,omitempty"`
	// ActionPolicy constrains allowed host-side actions.
	ActionPolicy HarnessActionPolicy `json:"actionPolicy,omitempty"`
}

type HarnessLogFiles struct {
	ToolUse  string `json:"toolUse,omitempty"`
	Compact  string `json:"compact,omitempty"`
	Subagent string `json:"subagent,omitempty"`
}

type HarnessArtifactFiles struct {
	ToolUse  string `json:"toolUse,omitempty"`
	Compact  string `json:"compact,omitempty"`
	Subagent string `json:"subagent,omitempty"`
}

type HarnessBridgeDirs struct {
	ToolUse  string `json:"toolUse,omitempty"`
	Compact  string `json:"compact,omitempty"`
	Subagent string `json:"subagent,omitempty"`
}

type HarnessActions struct {
	ToolUse  []HarnessAction `json:"toolUse,omitempty"`
	Compact  []HarnessAction `json:"compact,omitempty"`
	Subagent []HarnessAction `json:"subagent,omitempty"`
}

type HarnessAction struct {
	Type      string             `json:"type,omitempty"`
	Command   string             `json:"command,omitempty"`
	Args      []string           `json:"args,omitempty"`
	Dir       string             `json:"dir,omitempty"`
	TimeoutMs int                `json:"timeoutMs,omitempty"`
	Retry     HarnessActionRetry `json:"retry,omitempty"`
	OnFailure string             `json:"onFailure,omitempty"`
}

type HarnessActionRetry struct {
	MaxAttempts int `json:"maxAttempts,omitempty"`
	DelayMs     int `json:"delayMs,omitempty"`
}

type HarnessActionPolicy struct {
	RequireAbsoluteCommand bool     `json:"requireAbsoluteCommand,omitempty"`
	AllowCommandPrefixes   []string `json:"allowCommandPrefixes,omitempty"`
	DenyCommandPrefixes    []string `json:"denyCommandPrefixes,omitempty"`
	AllowDirPrefixes       []string `json:"allowDirPrefixes,omitempty"`
	DenyDirPrefixes        []string `json:"denyDirPrefixes,omitempty"`
	MaxTimeoutMs           int      `json:"maxTimeoutMs,omitempty"`
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
	enableActions := true
	captureHints := true
	persistToolResults := true
	return &Config{
		ThinkingLevel:  agent.ThinkingLevelMedium,
		AutoCompaction: true,
		CompactionSettings: CompactionConfig{
			MaxContextPercentage:   80.0,
			PreserveRecentMessages: 4,
		},
		Harness: HarnessConfig{
			EnableActions:      enableActions,
			CaptureHints:       &captureHints,
			PersistToolResults: &persistToolResults,
			ActionPolicy: HarnessActionPolicy{
				RequireAbsoluteCommand: true,
			},
			LogFiles: HarnessLogFiles{
				ToolUse:  "logs/tool-use.jsonl",
				Compact:  "logs/compact.jsonl",
				Subagent: "logs/subagent.jsonl",
			},
			ArtifactFiles: HarnessArtifactFiles{
				ToolUse:  "artifacts/tool-use-latest.json",
				Compact:  "artifacts/compact-latest.json",
				Subagent: "artifacts/subagent-latest.json",
			},
			BridgeDirs: HarnessBridgeDirs{
				ToolUse:  "bridge/tool-use",
				Compact:  "bridge/compact",
				Subagent: "bridge/subagent",
			},
		},
	}
}

func (c *Config) HarnessCaptureHints() bool {
	if c == nil || c.Harness.CaptureHints == nil {
		return true
	}
	return *c.Harness.CaptureHints
}

func (c *Config) HarnessPersistToolResults() bool {
	if c == nil || c.Harness.PersistToolResults == nil {
		return true
	}
	return *c.Harness.PersistToolResults
}

func (a HarnessAction) normalizedType() string {
	return strings.ToLower(strings.TrimSpace(a.Type))
}

// LoadConfig loads configuration from global and project-level settings files.
// Project settings override global settings.
func LoadConfig(agentDir, cwd string) (*Config, error) {
	cfg := DefaultConfig()

	// Load global config
	globalPath := filepath.Join(agentDir, "settings.json")
	if err := loadConfigFile(globalPath, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	} else if os.IsNotExist(err) {
		_ = SaveConfig(cfg, globalPath)
	}

	// Load project config (overrides global)
	projectPath := filepath.Join(cwd, ".coding_agent", "settings.json")
	if err := loadConfigFile(projectPath, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func ValidateConfig(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	return validateHarnessConfig(cfg.Harness)
}

func validateHarnessConfig(cfg HarnessConfig) error {
	check := func(actions []HarnessAction) error {
		for _, action := range actions {
			if action.normalizedType() != "exec" {
				continue
			}
			if action.TimeoutMs < 0 {
				return fmt.Errorf("harness action timeoutMs must be >= 0")
			}
			switch strings.ToLower(strings.TrimSpace(action.OnFailure)) {
			case "", "continue", "stop":
			default:
				return fmt.Errorf("harness action onFailure must be continue or stop")
			}
			if action.Retry.MaxAttempts < 0 {
				return fmt.Errorf("harness action retry.maxAttempts must be >= 0")
			}
			if action.Retry.DelayMs < 0 {
				return fmt.Errorf("harness action retry.delayMs must be >= 0")
			}
			if cfg.ActionPolicy.MaxTimeoutMs > 0 && action.TimeoutMs > cfg.ActionPolicy.MaxTimeoutMs {
				return fmt.Errorf("harness action timeout exceeds policy: %d > %d", action.TimeoutMs, cfg.ActionPolicy.MaxTimeoutMs)
			}
			cmd := strings.TrimSpace(action.Command)
			if cmd == "" || strings.Contains(cmd, "{{") {
				goto checkDir
			}
			if err := validateHarnessActionCommand(cfg.ActionPolicy, cmd); err != nil {
				return err
			}
		checkDir:
			dir := strings.TrimSpace(action.Dir)
			if dir == "" || strings.Contains(dir, "{{") {
				continue
			}
			if !filepath.IsAbs(dir) {
				continue
			}
			for _, denied := range cfg.ActionPolicy.DenyDirPrefixes {
				if denied = strings.TrimSpace(denied); denied != "" && strings.HasPrefix(dir, denied) {
					return fmt.Errorf("harness action dir denied by policy: %s", dir)
				}
			}
			if len(cfg.ActionPolicy.AllowDirPrefixes) > 0 {
				allowed := false
				for _, prefix := range cfg.ActionPolicy.AllowDirPrefixes {
					if prefix = strings.TrimSpace(prefix); prefix != "" && strings.HasPrefix(dir, prefix) {
						allowed = true
						break
					}
				}
				if !allowed {
					return fmt.Errorf("harness action dir not allowed by policy: %s", dir)
				}
			}
		}
		return nil
	}
	if err := check(cfg.Actions.ToolUse); err != nil {
		return err
	}
	if err := check(cfg.Actions.Compact); err != nil {
		return err
	}
	return check(cfg.Actions.Subagent)
}

func validateHarnessActionCommand(policy HarnessActionPolicy, cmd string) error {
	if policy.RequireAbsoluteCommand && !filepath.IsAbs(cmd) {
		return fmt.Errorf("harness action command must be absolute: %s", cmd)
	}
	for _, denied := range policy.DenyCommandPrefixes {
		if denied = strings.TrimSpace(denied); denied != "" && strings.HasPrefix(cmd, denied) {
			return fmt.Errorf("harness action command denied by policy: %s", cmd)
		}
	}
	if len(policy.AllowCommandPrefixes) > 0 {
		allowed := false
		for _, prefix := range policy.AllowCommandPrefixes {
			if prefix = strings.TrimSpace(prefix); prefix != "" && strings.HasPrefix(cmd, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("harness action command not allowed by policy: %s", cmd)
		}
	}
	return nil
}

func DefaultConfigTemplate() string {
	data, err := json.MarshalIndent(DefaultConfig(), "", "  ")
	if err != nil {
		return ""
	}
	return string(data) + "\n"
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
