package feishubot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
)

const (
	EnvAppID     = "MODU_FEISHU_APP_ID"
	EnvAppSecret = "MODU_FEISHU_APP_SECRET"
	EnvChatIDs   = "MODU_FEISHU_CHAT_IDS"
)

// Config holds Feishu/Lark bot settings stored at
// ~/.modu/channels/feishu/config.toml (0600).
type Config struct {
	AppID     string   `json:"appID" toml:"appID"`
	AppSecret string   `json:"appSecret" toml:"appSecret"`
	ChatIDs   []string `json:"chatIDs,omitempty" toml:"chatIDs,omitempty"`
}

func ConfigPath() string {
	return filepath.Join(resource.DefaultAgentDir(), "channels", "feishu", "config.toml")
}

func DebugLogPath() string {
	return filepath.Join(resource.DefaultAgentDir(), "channels", "feishu", "debug.log")
}

func RuntimeLogf(format string, args ...any) {
	path := DebugLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line := fmt.Sprintf(format, args...)
	line = strings.ReplaceAll(line, "\n", "\\n")
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339Nano), line)
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
	cfg.ChatIDs = cleanChatIDs(cfg.ChatIDs)
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if cfg == nil {
		cfg = &Config{}
	}
	cfg.ChatIDs = cleanChatIDs(cfg.ChatIDs)
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func EffectiveConfig() (*Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if v := strings.TrimSpace(os.Getenv(EnvAppID)); v != "" {
		cfg.AppID = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvAppSecret)); v != "" {
		cfg.AppSecret = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvChatIDs)); v != "" {
		cfg.ChatIDs = parseChatIDs(v)
	}
	cfg.ChatIDs = cleanChatIDs(cfg.ChatIDs)
	return cfg, nil
}

func (c Config) Ready() bool {
	return strings.TrimSpace(c.AppID) != "" && strings.TrimSpace(c.AppSecret) != ""
}

func parseChatIDs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return cleanChatIDs(strings.Split(s, ","))
}

func cleanChatIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
