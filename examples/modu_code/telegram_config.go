package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/openmodu/modu/pkg/coding_agent/resource"
)

// TelegramConfig holds Telegram bot settings.
// Stored at ~/.coding_agent/channels/telegram/config.json (0600).
type TelegramConfig struct {
	Token string `json:"token"`
}

// telegramConfigPath returns ~/.coding_agent/channels/telegram/config.json.
func telegramConfigPath() string {
	return filepath.Join(resource.DefaultAgentDir(), "channels", "telegram", "config.json")
}

func loadTelegramConfig() (*TelegramConfig, error) {
	data, err := os.ReadFile(telegramConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &TelegramConfig{}, nil
		}
		return nil, err
	}
	var cfg TelegramConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func saveTelegramConfig(cfg *TelegramConfig) error {
	path := telegramConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
