// Package config loads and saves modu_cron's YAML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// OverlapPolicy decides what to do when a task fires while the previous run
// is still in flight.
//
//   - "skip"  drop the new tick and log a warning (default).
//   - "queue" enqueue up to QueueCapacity runs; further ticks log warning and
//     are dropped.
//   - "kill"  cancel the running execution and start the new one.
type OverlapPolicy string

const (
	OverlapSkip  OverlapPolicy = "skip"
	OverlapQueue OverlapPolicy = "queue"
	OverlapKill  OverlapPolicy = "kill"

	// QueueCapacity caps queued runs per task to avoid unbounded memory
	// when cron frequency exceeds task throughput. Tickets past this are
	// dropped with a warning.
	QueueCapacity = 8
)

// Task describes a single scheduled prompt for the agent to run.
type Task struct {
	ID        string        `yaml:"id"`
	Cron      string        `yaml:"cron"`
	Prompt    string        `yaml:"prompt"`
	Enabled   bool          `yaml:"enabled"`
	Timezone  string        `yaml:"timezone,omitempty"`
	OnOverlap OverlapPolicy `yaml:"on_overlap,omitempty"`
	Channel   string        `yaml:"channel,omitempty"`
	Channels  []string      `yaml:"channels,omitempty"`
}

// Policy returns t.OnOverlap normalized; unknown / empty falls back to skip.
func (t Task) Policy() OverlapPolicy {
	switch t.OnOverlap {
	case OverlapQueue, OverlapKill:
		return t.OnOverlap
	default:
		return OverlapSkip
	}
}

// NotificationChannels returns the task's configured channel names with
// whitespace trimmed and duplicates removed. The singular channel field is
// kept for concise one-channel configs; channels is preferred for many.
func (t Task) NotificationChannels() []string {
	var out []string
	seen := make(map[string]bool)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	add(t.Channel)
	for _, name := range t.Channels {
		add(name)
	}
	return out
}

// Channel describes one outbound completion-notification destination.
//
// Supported types:
//   - webhook: POST a JSON payload to url / url_env.
//   - telegram: send text via Telegram Bot API using token / token_env and
//     chat_id / chat_id_env.
//   - feishu_webhook: send text to a Feishu/Lark custom bot webhook.
type Channel struct {
	Type      string `yaml:"type"`
	URL       string `yaml:"url,omitempty"`
	URLEnv    string `yaml:"url_env,omitempty"`
	Token     string `yaml:"token,omitempty"`
	TokenEnv  string `yaml:"token_env,omitempty"`
	ChatID    string `yaml:"chat_id,omitempty"`
	ChatIDEnv string `yaml:"chat_id_env,omitempty"`
}

// ModelConfig describes the LLM endpoint used by modu_cron.
type ModelConfig struct {
	Provider  string            `yaml:"provider,omitempty"`
	Model     string            `yaml:"model,omitempty"`
	BaseURL   string            `yaml:"base_url,omitempty"`
	APIKey    string            `yaml:"api_key,omitempty"`
	APIKeyEnv string            `yaml:"api_key_env,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
}

// Config is the on-disk shape of modu_cron's runtime config file.
//
// Tasks is kept for backward compatibility with the original single-file
// format. New configs should keep tasks in TasksFile instead.
type Config struct {
	WorkingDir string             `yaml:"working_dir,omitempty"`
	Model      ModelConfig        `yaml:"model,omitempty"`
	Channels   map[string]Channel `yaml:"channels,omitempty"`
	TasksFile  string             `yaml:"tasks_file,omitempty"`
	Tasks      []Task             `yaml:"tasks,omitempty"`
}

// TasksConfig is the on-disk shape of the isolated cron task file.
type TasksConfig struct {
	Tasks []Task `yaml:"tasks"`
}

// DefaultPath returns ~/.modu_cron/config.yaml.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".modu_cron", "config.yaml")
}

// DefaultTasksPath returns the default tasks file next to cfgPath.
func DefaultTasksPath(cfgPath string) string {
	dir := filepath.Dir(cfgPath)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "tasks.yaml")
}

// ResolveTasksPath returns the task file configured by cfg. A relative
// tasks_file is resolved relative to cfgPath's directory.
func ResolveTasksPath(cfgPath string, cfg *Config) string {
	if cfg == nil || strings.TrimSpace(cfg.TasksFile) == "" {
		return DefaultTasksPath(cfgPath)
	}
	path := expandPath(strings.TrimSpace(cfg.TasksFile))
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(cfgPath), path)
}

// ResolveWorkingDir returns the configured working directory. If unset, it
// returns fallback. Relative paths are resolved relative to cfgPath's
// directory.
func ResolveWorkingDir(cfgPath string, cfg *Config, fallback string) string {
	if cfg == nil || strings.TrimSpace(cfg.WorkingDir) == "" {
		return fallback
	}
	path := expandPath(strings.TrimSpace(cfg.WorkingDir))
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(cfgPath), path)
}

// Load reads and parses the runtime config at path plus its isolated task
// file. A missing config yields an empty Config so the daemon can boot with
// zero tasks.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := &Config{}
			taskPath := ResolveTasksPath(path, cfg)
			if fileExists(taskPath) {
				tasksCfg, err := LoadTasks(taskPath)
				if err != nil {
					return nil, err
				}
				cfg.Tasks = tasksCfg.Tasks
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	taskPath := ResolveTasksPath(path, &cfg)
	if cfg.TasksFile != "" || fileExists(taskPath) {
		tasksCfg, err := LoadTasks(taskPath)
		if err != nil {
			return nil, err
		}
		cfg.Tasks = tasksCfg.Tasks
	}
	return &cfg, nil
}

// LoadRuntime reads only the runtime config file, without merging tasks from
// the isolated task file.
func LoadRuntime(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// LoadTasks reads the isolated task file. A missing file yields zero tasks.
func LoadTasks(path string) (*TasksConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TasksConfig{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg TasksConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes cfg to path as-is. It is kept for legacy single-file configs and
// tests; new runtime config writes should use SaveRuntime, and task mutations
// should use SaveTasks.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// SaveRuntime writes cfg to path without embedding tasks.
func SaveRuntime(path string, cfg *Config) error {
	clone := *cfg
	clone.Tasks = nil
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := yaml.Marshal(&clone)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// SaveTasks writes the isolated task file, creating parent directories.
func SaveTasks(path string, tasks []Task) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := yaml.Marshal(TasksConfig{Tasks: tasks})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func expandPath(path string) string {
	path = os.ExpandEnv(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
