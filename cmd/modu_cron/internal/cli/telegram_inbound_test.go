package cli

import (
	"testing"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

func TestTelegramBindingsFromConfig(t *testing.T) {
	t.Setenv("TG_TOKEN", "token-from-env")
	t.Setenv("TG_CHAT", "-100123")

	got := telegramBindings(&config.Config{Channels: map[string]config.Channel{
		"tg":  {Type: "telegram", TokenEnv: "TG_TOKEN", ChatIDEnv: "TG_CHAT"},
		"out": {Type: "webhook", URL: "https://example.invalid"},
	}})
	if len(got) != 1 {
		t.Fatalf("bindings len=%d, want 1: %+v", len(got), got)
	}
	if got[0].token != "token-from-env" || got[0].chatID != -100123 || got[0].name != "tg" {
		t.Fatalf("unexpected binding: %+v", got[0])
	}
}

func TestTelegramBindingsSkipsInvalidChatID(t *testing.T) {
	got := telegramBindings(&config.Config{Channels: map[string]config.Channel{
		"tg": {Type: "telegram", Token: "token", ChatID: "not-int"},
	}})
	if len(got) != 0 {
		t.Fatalf("bindings len=%d, want 0: %+v", len(got), got)
	}
}
