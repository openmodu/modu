package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/openmodu/modu/pkg/coding_agent/resource"
)

// TelegramConfig holds Telegram bot settings stored in the agent dotfile.
type TelegramConfig struct {
	Token string `json:"token"`
}

// telegramConfigPath returns the path to ~/.coding_agent/telegram.json.
func telegramConfigPath() string {
	return filepath.Join(resource.DefaultAgentDir(), "telegram.json")
}

// loadTelegramConfig reads TelegramConfig from the dotfile.
// Returns an empty config (not an error) if the file does not exist yet.
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

// saveTelegramConfig writes cfg to the dotfile with 0600 permissions
// so the bot token is not world-readable.
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
