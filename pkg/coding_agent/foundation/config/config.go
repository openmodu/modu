// Package config is the foundation layer holding coding-agent session
// configuration loaded from global and project settings.json files. It has no
// dependency on the session or any higher layer.
package config

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

	// Harness controls host-side runtime behavior.
	Harness HarnessConfig `json:"harness,omitempty"`

	// Features controls higher-level runtime feature gates.
	Features FeatureConfig `json:"features,omitempty"`

	// Permissions controls host-side tool permission policy.
	Permissions PermissionConfig `json:"permissions,omitempty"`
}

type FeatureConfig struct {
	MemoryTool     *bool `json:"memoryTool,omitempty"`
	TodoTool       *bool `json:"todoTool,omitempty"`
	TaskOutputTool *bool `json:"taskOutputTool,omitempty"`
	PlanMode       *bool `json:"planMode,omitempty"`
	WorktreeMode   *bool `json:"worktreeMode,omitempty"`
}

type PermissionConfig struct {
	AllowTools        []string `json:"allowTools,omitempty"`
	DenyTools         []string `json:"denyTools,omitempty"`
	AllowBashPrefixes []string `json:"allowBashPrefixes,omitempty"`
	DenyBashPrefixes  []string `json:"denyBashPrefixes,omitempty"`
}

type HarnessConfig struct {
	// BlockTools denies matching tool names before execution.
	BlockTools []string `json:"blockTools,omitempty"`
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

// Default returns a config with sensible defaults.
func Default() *Config {
	return &Config{
		ThinkingLevel:  agent.ThinkingLevelMedium,
		AutoCompaction: true,
		CompactionSettings: CompactionConfig{
			MaxContextPercentage:   80.0,
			PreserveRecentMessages: 4,
		},
		Features: FeatureConfig{
			MemoryTool:     boolPtr(true),
			TodoTool:       boolPtr(true),
			TaskOutputTool: boolPtr(false), // opt-in: only needed for background task workflows
			PlanMode:       boolPtr(true),
			WorktreeMode:   boolPtr(true),
		},
		Permissions: PermissionConfig{},
	}
}

// Ptr returns a pointer to v. Handy for setting the *bool feature flags.
func Ptr[T any](v T) *T { return &v }

func boolPtr(v bool) *bool { return &v }

func featureEnabled(flag *bool) bool {
	if flag == nil {
		return true
	}
	return *flag
}

func (c *Config) FeatureMemoryTool() bool { return c == nil || featureEnabled(c.Features.MemoryTool) }
func (c *Config) FeatureTodoTool() bool   { return c == nil || featureEnabled(c.Features.TodoTool) }
func (c *Config) FeatureTaskOutputTool() bool {
	return c == nil || featureEnabled(c.Features.TaskOutputTool)
}
func (c *Config) FeaturePlanMode() bool { return c == nil || featureEnabled(c.Features.PlanMode) }
func (c *Config) FeatureWorktreeMode() bool {
	return c == nil || featureEnabled(c.Features.WorktreeMode)
}

// Load reads configuration from global and project-level settings files.
// Project settings override global settings.
func Load(agentDir, cwd string) (*Config, error) {
	cfg := Default()

	// Load global config
	globalPath := filepath.Join(agentDir, "settings.json")
	if err := loadFile(globalPath, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	} else if os.IsNotExist(err) {
		_ = Save(cfg, globalPath)
	}

	// Load project config (overrides global)
	projectPath := filepath.Join(cwd, ".coding_agent", "settings.json")
	if err := loadFile(projectPath, cfg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return cfg, nil
}

func loadFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}

// Save writes the configuration to path as indented JSON.
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
