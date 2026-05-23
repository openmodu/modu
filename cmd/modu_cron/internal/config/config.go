// Package config loads and saves modu_cron's YAML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Task describes a single scheduled prompt for the agent to run.
type Task struct {
	ID       string `yaml:"id"`
	Cron     string `yaml:"cron"`
	Prompt   string `yaml:"prompt"`
	Enabled  bool   `yaml:"enabled"`
	Timezone string `yaml:"timezone,omitempty"`
}

// Config is the on-disk shape of modu_cron's config file.
type Config struct {
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
