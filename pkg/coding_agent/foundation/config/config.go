// Package config is the foundation layer holding coding-agent session
// configuration loaded from global config.toml and project settings.json files. It has no
// dependency on the session or any higher layer.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/openmodu/modu/pkg/types"
)

// Config holds coding agent configuration from global and project-level settings.
type Config struct {
	// ThinkingLevel controls LLM reasoning depth.
	ThinkingLevel types.ThinkingLevel `json:"thinkingLevel,omitempty" toml:"thinkingLevel,omitempty"`

	// AutoCompaction enables automatic context compaction.
	AutoCompaction bool `json:"autoCompaction,omitempty" toml:"autoCompaction,omitempty"`

	// DefaultProvider is the default LLM provider name.
	DefaultProvider string `json:"defaultProvider,omitempty" toml:"defaultProvider,omitempty"`

	// DefaultModel is the default model ID.
	DefaultModel string `json:"defaultModel,omitempty" toml:"defaultModel,omitempty"`

	// EnabledModels lists explicitly enabled model IDs.
	EnabledModels []string `json:"enabledModels,omitempty" toml:"enabledModels,omitempty"`

	// CompactionSettings controls compaction behavior.
	CompactionSettings CompactionConfig `json:"compactionSettings,omitempty" toml:"compactionSettings,omitempty"`

	// CustomSystemPrompt overrides the default system prompt.
	CustomSystemPrompt string `json:"customSystemPrompt,omitempty" toml:"customSystemPrompt,omitempty"`

	// AppendPrompts are additional prompt texts appended to the system prompt.
	AppendPrompts []string `json:"appendPrompts,omitempty" toml:"appendPrompts,omitempty"`

	// AutoRetry enables automatic retry on transient errors.
	AutoRetry bool `json:"autoRetry,omitempty" toml:"autoRetry,omitempty"`

	// RetrySettings configures retry behavior.
	RetrySettings RetryConfig `json:"retrySettings,omitempty" toml:"retrySettings,omitempty"`

	// ScopedModels lists models available for cycling.
	ScopedModels []string `json:"scopedModels,omitempty" toml:"scopedModels,omitempty"`

	// SteeringMode controls how steering messages are consumed.
	SteeringMode types.ExecutionMode `json:"steeringMode,omitempty" toml:"steeringMode,omitempty"`

	// FollowUpMode controls how follow-up messages are consumed.
	FollowUpMode types.ExecutionMode `json:"followUpMode,omitempty" toml:"followUpMode,omitempty"`

	// BlockImages prevents image content from being sent to the model.
	BlockImages bool `json:"blockImages,omitempty" toml:"blockImages,omitempty"`

	// DisableWorkflows disables dynamic workflow extensions for the session.
	DisableWorkflows bool `json:"disableWorkflows,omitempty" toml:"disableWorkflows,omitempty"`

	// Harness controls host-side runtime behavior.
	Harness HarnessConfig `json:"harness,omitempty" toml:"harness,omitempty"`

	// Features controls higher-level runtime feature gates.
	Features FeatureConfig `json:"features,omitempty" toml:"features,omitempty"`

	// Permissions controls host-side tool permission policy.
	Permissions PermissionConfig `json:"permissions,omitempty" toml:"permissions,omitempty"`

	// WebSearch configures the opt-in web_search research tool.
	WebSearch WebSearchConfig `json:"webSearch,omitempty" toml:"webSearch,omitempty"`

	// WebFetch configures the opt-in web_fetch research tool.
	WebFetch WebFetchConfig `json:"webFetch,omitempty" toml:"webFetch,omitempty"`

	// MCPServers declares external Model Context Protocol servers. Global TOML
	// config reads this from the Codex-compatible root [mcp_servers] table;
	// project settings use the JSON key mcpServers.
	MCPServers map[string]MCPServerConfig `json:"mcpServers,omitempty" toml:"-"`
}

type FeatureConfig struct {
	MemoryTool     *bool `json:"memoryTool,omitempty" toml:"memoryTool,omitempty"`
	TodoTool       *bool `json:"todoTool,omitempty" toml:"todoTool,omitempty"`
	TaskOutputTool *bool `json:"taskOutputTool,omitempty" toml:"taskOutputTool,omitempty"`
	PlanMode       *bool `json:"planMode,omitempty" toml:"planMode,omitempty"`
	WorktreeMode   *bool `json:"worktreeMode,omitempty" toml:"worktreeMode,omitempty"`
}

type PermissionConfig struct {
	DefaultMode       string   `json:"defaultMode,omitempty" toml:"defaultMode,omitempty"`
	AllowTools        []string `json:"allowTools,omitempty" toml:"allowTools,omitempty"`
	DenyTools         []string `json:"denyTools,omitempty" toml:"denyTools,omitempty"`
	AllowBashPrefixes []string `json:"allowBashPrefixes,omitempty" toml:"allowBashPrefixes,omitempty"`
	DenyBashPrefixes  []string `json:"denyBashPrefixes,omitempty" toml:"denyBashPrefixes,omitempty"`
}

type WebSearchConfig struct {
	Provider   string `json:"provider,omitempty" toml:"provider,omitempty"`
	Endpoint   string `json:"endpoint,omitempty" toml:"endpoint,omitempty"`
	APIKey     string `json:"apiKey,omitempty" toml:"apiKey,omitempty"`
	APIKeyEnv  string `json:"apiKeyEnv,omitempty" toml:"apiKeyEnv,omitempty"`
	SearchType string `json:"searchType,omitempty" toml:"searchType,omitempty"`
}

type WebFetchConfig struct {
	Provider  string `json:"provider,omitempty" toml:"provider,omitempty"`
	Endpoint  string `json:"endpoint,omitempty" toml:"endpoint,omitempty"`
	APIKey    string `json:"apiKey,omitempty" toml:"apiKey,omitempty"`
	APIKeyEnv string `json:"apiKeyEnv,omitempty" toml:"apiKeyEnv,omitempty"`
}

// MCPServerConfig configures one standard MCP transport: command selects
// stdio, while url selects Streamable HTTP. The two are mutually exclusive.
type MCPServerConfig struct {
	Command           string            `json:"command,omitempty" toml:"command,omitempty"`
	Args              []string          `json:"args,omitempty" toml:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty" toml:"env,omitempty"`
	Cwd               string            `json:"cwd,omitempty" toml:"cwd,omitempty"`
	URL               string            `json:"url,omitempty" toml:"url,omitempty"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var,omitempty" toml:"bearer_token_env_var,omitempty"`
	HTTPHeaders       map[string]string `json:"http_headers,omitempty" toml:"http_headers,omitempty"`
	EnvHTTPHeaders    map[string]string `json:"env_http_headers,omitempty" toml:"env_http_headers,omitempty"`
	Enabled           *bool             `json:"enabled,omitempty" toml:"enabled,omitempty"`
	Required          bool              `json:"required,omitempty" toml:"required,omitempty"`
	StartupTimeoutSec float64           `json:"startup_timeout_sec,omitempty" toml:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec    float64           `json:"tool_timeout_sec,omitempty" toml:"tool_timeout_sec,omitempty"`
	EnabledTools      []string          `json:"enabled_tools,omitempty" toml:"enabled_tools,omitempty"`
	DisabledTools     []string          `json:"disabled_tools,omitempty" toml:"disabled_tools,omitempty"`
}

// IsEnabled reports whether a server should start. Omitted enabled defaults
// to true, matching Codex and MCP host configuration conventions.
func (c MCPServerConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

type HarnessConfig struct {
	// BlockTools denies matching tool names before execution.
	BlockTools []string `json:"blockTools,omitempty" toml:"blockTools,omitempty"`
}

// CompactionConfig controls context compaction behavior.
type CompactionConfig struct {
	// MaxContextPercentage triggers compaction when context usage exceeds this percentage.
	MaxContextPercentage float64 `json:"maxContextPercentage,omitempty" toml:"maxContextPercentage,omitempty"`
	// PreserveRecentMessages is the number of recent messages to preserve during compaction.
	PreserveRecentMessages int `json:"preserveRecentMessages,omitempty" toml:"preserveRecentMessages,omitempty"`
	// PreserveUserMessagesTokens is an approximate token budget for retaining
	// recent real user messages from the compacted range. A negative value
	// disables this preservation.
	PreserveUserMessagesTokens int `json:"preserveUserMessagesTokens,omitempty" toml:"preserveUserMessagesTokens,omitempty"`
}

// RetryConfig controls auto-retry behavior.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts. Default: 3.
	MaxRetries int `json:"maxRetries,omitempty" toml:"maxRetries,omitempty"`
	// BaseDelayMs is the base delay in milliseconds for exponential backoff. Default: 1000.
	BaseDelayMs int `json:"baseDelayMs,omitempty" toml:"baseDelayMs,omitempty"`
	// MaxDelayMs is the maximum delay in milliseconds. Default: 30000.
	MaxDelayMs int `json:"maxDelayMs,omitempty" toml:"maxDelayMs,omitempty"`
}

// Default returns a config with sensible defaults.
func Default() *Config {
	return &Config{
		ThinkingLevel:  types.ThinkingLevelMedium,
		AutoCompaction: true,
		CompactionSettings: CompactionConfig{
			MaxContextPercentage:       80.0,
			PreserveRecentMessages:     4,
			PreserveUserMessagesTokens: 1024,
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

// GlobalConfigPath returns the global config.toml path that owns model config
// plus the optional [settings] table.
func GlobalConfigPath(agentDir string) string {
	return filepath.Join(agentDir, "config.toml")
}

// LegacyGlobalSettingsPath returns the old JSON settings path. It remains
// readable for compatibility, but new global writes go to config.toml.
func LegacyGlobalSettingsPath(agentDir string) string {
	return filepath.Join(agentDir, "settings.json")
}

// ProjectSettingsPath returns the project-local settings override path.
func ProjectSettingsPath(cwd string) string {
	return filepath.Join(cwd, ".coding_agent", "settings.json")
}

// Load reads configuration from global config.toml [settings], legacy global
// settings.json, and project-level settings.json. Project settings override
// global settings.
func Load(agentDir, cwd string) (*Config, error) {
	cfg := Default()

	loadedGlobal, err := loadGlobalTOMLSettings(GlobalConfigPath(agentDir), cfg)
	if err != nil {
		return nil, err
	}
	if !loadedGlobal {
		legacyPath := LegacyGlobalSettingsPath(agentDir)
		if err := loadJSONFile(legacyPath, cfg); err != nil && !os.IsNotExist(err) {
			return nil, err
		} else if err == nil {
			_ = Save(cfg, GlobalConfigPath(agentDir))
		}
	}

	if strings.TrimSpace(cwd) != "" {
		// Load project config (overrides global)
		projectPath := ProjectSettingsPath(cwd)
		if err := loadJSONFile(projectPath, cfg); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	return cfg, nil
}

func loadJSONFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}

func loadGlobalTOMLSettings(path string, cfg *Config) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	wrapper := struct {
		Settings   Config                     `toml:"settings"`
		MCPServers map[string]MCPServerConfig `toml:"mcp_servers"`
	}{Settings: *cfg}
	meta, err := toml.DecodeFile(path, &wrapper)
	if err != nil {
		return false, err
	}
	if !meta.IsDefined("settings") {
		cfg.MCPServers = wrapper.MCPServers
		return false, nil
	}
	*cfg = wrapper.Settings
	cfg.MCPServers = wrapper.MCPServers
	return true, nil
}

// Save writes the configuration to path as indented JSON.
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if filepath.Base(path) == "config.toml" {
		return saveGlobalTOMLSettings(cfg, path)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func saveGlobalTOMLSettings(cfg *Config, path string) error {
	doc := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := toml.Unmarshal(data, &doc); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	settings := CompactSettingsMap(cfg)
	if len(settings) == 0 {
		delete(doc, "settings")
	} else {
		doc["settings"] = settings
	}
	var b strings.Builder
	enc := toml.NewEncoder(&b)
	enc.Indent = ""
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// CompactSettingsMap returns only values that differ from built-in defaults.
// It is used for config.toml [settings] so default and empty sections are not
// written back as noisy configuration.
func CompactSettingsMap(cfg *Config) map[string]any {
	if cfg == nil {
		return nil
	}
	def := Default()
	out := map[string]any{}
	putString(out, "thinkingLevel", string(cfg.ThinkingLevel), string(def.ThinkingLevel))
	putBool(out, "autoCompaction", cfg.AutoCompaction, def.AutoCompaction)
	putString(out, "defaultProvider", cfg.DefaultProvider, def.DefaultProvider)
	putString(out, "defaultModel", cfg.DefaultModel, def.DefaultModel)
	putStringSlice(out, "enabledModels", cfg.EnabledModels, def.EnabledModels)
	putCompaction(out, cfg.CompactionSettings, def.CompactionSettings)
	putString(out, "customSystemPrompt", cfg.CustomSystemPrompt, def.CustomSystemPrompt)
	putStringSlice(out, "appendPrompts", cfg.AppendPrompts, def.AppendPrompts)
	putBool(out, "autoRetry", cfg.AutoRetry, def.AutoRetry)
	putRetry(out, cfg.RetrySettings, def.RetrySettings)
	putStringSlice(out, "scopedModels", cfg.ScopedModels, def.ScopedModels)
	putString(out, "steeringMode", string(cfg.SteeringMode), string(def.SteeringMode))
	putString(out, "followUpMode", string(cfg.FollowUpMode), string(def.FollowUpMode))
	putBool(out, "blockImages", cfg.BlockImages, def.BlockImages)
	putBool(out, "disableWorkflows", cfg.DisableWorkflows, def.DisableWorkflows)
	putHarness(out, cfg.Harness, def.Harness)
	putFeatures(out, cfg, def)
	putPermissions(out, cfg.Permissions)
	putWebSearch(out, cfg.WebSearch)
	putWebFetch(out, cfg.WebFetch)
	return out
}

func putString(out map[string]any, key, value, def string) {
	value = strings.TrimSpace(value)
	if value != "" && value != def {
		out[key] = value
	}
}

func putBool(out map[string]any, key string, value, def bool) {
	if value != def {
		out[key] = value
	}
}

func putStringSlice(out map[string]any, key string, value, def []string) {
	if len(value) > 0 && !reflect.DeepEqual(value, def) {
		out[key] = value
	}
}

func putCompaction(out map[string]any, value, def CompactionConfig) {
	section := map[string]any{}
	if value.MaxContextPercentage != 0 && value.MaxContextPercentage != def.MaxContextPercentage {
		section["maxContextPercentage"] = value.MaxContextPercentage
	}
	if value.PreserveRecentMessages != 0 && value.PreserveRecentMessages != def.PreserveRecentMessages {
		section["preserveRecentMessages"] = value.PreserveRecentMessages
	}
	if value.PreserveUserMessagesTokens != def.PreserveUserMessagesTokens {
		section["preserveUserMessagesTokens"] = value.PreserveUserMessagesTokens
	}
	if len(section) > 0 {
		out["compactionSettings"] = section
	}
}

func putRetry(out map[string]any, value, def RetryConfig) {
	section := map[string]any{}
	if value.MaxRetries != 0 && value.MaxRetries != def.MaxRetries {
		section["maxRetries"] = value.MaxRetries
	}
	if value.BaseDelayMs != 0 && value.BaseDelayMs != def.BaseDelayMs {
		section["baseDelayMs"] = value.BaseDelayMs
	}
	if value.MaxDelayMs != 0 && value.MaxDelayMs != def.MaxDelayMs {
		section["maxDelayMs"] = value.MaxDelayMs
	}
	if len(section) > 0 {
		out["retrySettings"] = section
	}
}

func putHarness(out map[string]any, value, def HarnessConfig) {
	if len(value.BlockTools) == 0 || reflect.DeepEqual(value, def) {
		return
	}
	out["harness"] = map[string]any{"blockTools": value.BlockTools}
}

func putFeatures(out map[string]any, cfg, def *Config) {
	section := map[string]any{}
	if cfg.FeatureMemoryTool() != def.FeatureMemoryTool() {
		section["memoryTool"] = cfg.FeatureMemoryTool()
	}
	if cfg.FeatureTodoTool() != def.FeatureTodoTool() {
		section["todoTool"] = cfg.FeatureTodoTool()
	}
	if cfg.FeatureTaskOutputTool() != def.FeatureTaskOutputTool() {
		section["taskOutputTool"] = cfg.FeatureTaskOutputTool()
	}
	if cfg.FeaturePlanMode() != def.FeaturePlanMode() {
		section["planMode"] = cfg.FeaturePlanMode()
	}
	if cfg.FeatureWorktreeMode() != def.FeatureWorktreeMode() {
		section["worktreeMode"] = cfg.FeatureWorktreeMode()
	}
	if len(section) > 0 {
		out["features"] = section
	}
}

func putPermissions(out map[string]any, value PermissionConfig) {
	section := map[string]any{}
	if strings.TrimSpace(value.DefaultMode) != "" {
		section["defaultMode"] = strings.TrimSpace(value.DefaultMode)
	}
	if len(value.AllowTools) > 0 {
		section["allowTools"] = value.AllowTools
	}
	if len(value.DenyTools) > 0 {
		section["denyTools"] = value.DenyTools
	}
	if len(value.AllowBashPrefixes) > 0 {
		section["allowBashPrefixes"] = value.AllowBashPrefixes
	}
	if len(value.DenyBashPrefixes) > 0 {
		section["denyBashPrefixes"] = value.DenyBashPrefixes
	}
	if len(section) > 0 {
		out["permissions"] = section
	}
}

func putWebSearch(out map[string]any, value WebSearchConfig) {
	section := map[string]any{}
	putString(section, "provider", value.Provider, "")
	putString(section, "endpoint", value.Endpoint, "")
	putString(section, "apiKey", value.APIKey, "")
	putString(section, "apiKeyEnv", value.APIKeyEnv, "")
	putString(section, "searchType", value.SearchType, "")
	if len(section) > 0 {
		out["webSearch"] = section
	}
}

func putWebFetch(out map[string]any, value WebFetchConfig) {
	section := map[string]any{}
	putString(section, "provider", value.Provider, "")
	putString(section, "endpoint", value.Endpoint, "")
	putString(section, "apiKey", value.APIKey, "")
	putString(section, "apiKeyEnv", value.APIKeyEnv, "")
	if len(section) > 0 {
		out["webFetch"] = section
	}
}
