package main

import (
	"os"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/channels/feishu"
	"github.com/openmodu/modu/pkg/tgbot"
)

func TestConfigureTelegramChannelSavesTokenWithoutEchoingIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	out, err := configureTelegramChannel(TelegramChannelInput{Token: "  telegram-secret-token  "})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := tgbot.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "telegram-secret-token" {
		t.Fatalf("token = %q, want trimmed token", cfg.Token)
	}
	if strings.Contains(out, cfg.Token) {
		t.Fatalf("configuration output leaked token: %q", out)
	}
	if !strings.Contains(out, tgbot.ConfigPath()) || !strings.Contains(out, "Restart modu_code") {
		t.Fatalf("unexpected output: %q", out)
	}
	info, err := os.Stat(tgbot.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestConfigureFeishuChannelSavesCredentialsAndChatIDs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	out, err := configureFeishuChannel(FeishuChannelInput{
		AppID:     " cli_test ",
		AppSecret: " feishu-secret ",
		ChatIDs:   []string{"oc_a", " oc_b ", "oc_a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := feishu.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppID != "cli_test" || cfg.AppSecret != "feishu-secret" {
		t.Fatalf("credentials = %+v", cfg)
	}
	if got := strings.Join(cfg.ChatIDs, ","); got != "oc_a,oc_b" {
		t.Fatalf("chat IDs = %q, want oc_a,oc_b", got)
	}
	if strings.Contains(out, cfg.AppSecret) {
		t.Fatalf("configuration output leaked app secret: %q", out)
	}
}

func TestParseChannelChatIDs(t *testing.T) {
	got := parseChannelChatIDs("oc_a, oc_b;oc_a\noc_c")
	if strings.Join(got, ",") != "oc_a,oc_b,oc_c" {
		t.Fatalf("chat IDs = %#v", got)
	}
	if got := parseChannelChatIDs("-"); len(got) != 0 {
		t.Fatalf("dash should allow all chats, got %#v", got)
	}
}

func TestModuTUITelegramTokenUsesConfigThenEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MOMS_TG_TOKEN", "env-token")

	got, err := moduTUITelegramToken()
	if err != nil {
		t.Fatal(err)
	}
	if got != "env-token" {
		t.Fatalf("token = %q, want env-token", got)
	}
	if err := tgbot.SaveConfig(&tgbot.Config{Token: "file-token"}); err != nil {
		t.Fatal(err)
	}
	got, err = moduTUITelegramToken()
	if err != nil {
		t.Fatal(err)
	}
	if got != "file-token" {
		t.Fatalf("token = %q, want file-token", got)
	}
}

func TestConfigureChannelsRejectsMissingCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := configureTelegramChannel(TelegramChannelInput{}); err == nil {
		t.Fatal("expected missing Telegram token error")
	}
	if _, err := configureFeishuChannel(FeishuChannelInput{AppID: "cli_test"}); err == nil {
		t.Fatal("expected missing Feishu app secret error")
	}
}
