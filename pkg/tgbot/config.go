package tgbot

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/openmodu/modu/pkg/coding_agent/resource"
)

// Config holds Telegram bot settings stored at
// ~/.coding_agent/channels/telegram/config.json (0600).
type Config struct {
	Token string `json:"token"`
}

func ConfigPath() string {
	return filepath.Join(resource.DefaultAgentDir(), "channels", "telegram", "config.json")
}

func LoadConfig() (*Config, error) {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
