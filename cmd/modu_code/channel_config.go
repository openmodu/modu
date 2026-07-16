package main

import (
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/channels/feishu"
	"github.com/openmodu/modu/pkg/tgbot"
)

func configureTelegramChannel(input TelegramChannelInput) (string, error) {
	token := strings.TrimSpace(input.Token)
	if token == "" {
		return "", fmt.Errorf("Telegram bot token is required")
	}
	if err := tgbot.SaveConfig(&tgbot.Config{Token: token}); err != nil {
		return "", fmt.Errorf("save Telegram channel config: %w", err)
	}
	return fmt.Sprintf("Telegram channel configured\nconfig: %s\nRestart modu_code to apply.", tgbot.ConfigPath()), nil
}

func configureFeishuChannel(input FeishuChannelInput) (string, error) {
	appID := strings.TrimSpace(input.AppID)
	appSecret := strings.TrimSpace(input.AppSecret)
	if appID == "" {
		return "", fmt.Errorf("Feishu app ID is required")
	}
	if appSecret == "" {
		return "", fmt.Errorf("Feishu app secret is required")
	}
	if err := feishu.SaveConfig(&feishu.Config{
		AppID:     appID,
		AppSecret: appSecret,
		ChatIDs:   cleanChannelChatIDs(input.ChatIDs),
	}); err != nil {
		return "", fmt.Errorf("save Feishu channel config: %w", err)
	}
	return fmt.Sprintf("Feishu channel configured\nconfig: %s\nRestart modu_code to apply.", feishu.ConfigPath()), nil
}

func parseChannelChatIDs(input string) []string {
	if strings.TrimSpace(input) == "-" {
		return nil
	}
	return cleanChannelChatIDs(strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	}))
}

func cleanChannelChatIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
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
