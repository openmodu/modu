package tgbot

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
)

// Config holds Telegram bot settings stored at
// ~/.modu/channels/telegram/config.toml (0600).
type Config struct {
	Token string `json:"token" toml:"token"`
}

func ConfigPath() string {
	return filepath.Join(resource.DefaultAgentDir(), "channels", "telegram", "config.toml")
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
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
