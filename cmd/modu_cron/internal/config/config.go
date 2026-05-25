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

// Config is the on-disk shape of modu_cron's config file.
type Config struct {
	Channels map[string]Channel `yaml:"channels,omitempty"`
	Tasks    []Task             `yaml:"tasks"`
}

// DefaultPath returns ~/.modu_cron/config.yaml.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".modu_cron", "config.yaml")
}

// Load reads and parses the YAML config at path. A missing file yields an
// empty Config so the daemon can boot with zero tasks.
func Load(path string) (*Config, error) {
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

// Save writes cfg to path, creating parent directories as needed.
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
