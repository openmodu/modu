package main

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

type moduTUIChannelWizard struct {
	hooks CommandHooks
	send  func(tea.Msg)
}

func newModuTUIChannelWizard(hooks CommandHooks, send func(tea.Msg)) *moduTUIChannelWizard {
	return &moduTUIChannelWizard{hooks: hooks, send: send}
}

func (w *moduTUIChannelWizard) Start(ctx context.Context) {
	choice := w.requestChoice(ctx, "Channel", "Choose a channel to configure", []modutui.HumanPromptOption{
		{Label: "Telegram", Value: "telegram"},
		{Label: "Feishu", Value: "feishu"},
	})
	switch choice {
	case "telegram":
		w.configureTelegram(ctx)
	case "feishu":
		w.configureFeishu(ctx)
	default:
		w.setStatus("channel configuration cancelled")
	}
}

func (w *moduTUIChannelWizard) configureTelegram(ctx context.Context) {
	if w.hooks.ConfigureTelegram == nil {
		w.post("Channel\n\nTelegram configuration is not available.")
		return
	}
	token := w.requestText(ctx, modutui.HumanTextRequest{
		ID:          "channel-telegram-token",
		Title:       "Channel: Telegram",
		Body:        "Telegram bot token. It is masked and will not be added to transcript history.",
		Placeholder: "123456:bot-token",
		Secret:      true,
		Required:    true,
	})
	if strings.TrimSpace(token) == "" {
		w.setStatus("channel configuration cancelled")
		return
	}
	w.setStatus("saving Telegram channel")
	out, err := w.hooks.ConfigureTelegram(TelegramChannelInput{Token: token})
	w.postResult("Channel: Telegram", out, err)
}

func (w *moduTUIChannelWizard) configureFeishu(ctx context.Context) {
	if w.hooks.ConfigureFeishu == nil {
		w.post("Channel\n\nFeishu configuration is not available.")
		return
	}
	appID := w.requestText(ctx, modutui.HumanTextRequest{
		ID:          "channel-feishu-app-id",
		Title:       "Channel: Feishu",
		Body:        "Feishu app ID.",
		Placeholder: "cli_xxx",
		Required:    true,
	})
	if strings.TrimSpace(appID) == "" {
		w.setStatus("channel configuration cancelled")
		return
	}
	appSecret := w.requestText(ctx, modutui.HumanTextRequest{
		ID:          "channel-feishu-app-secret",
		Title:       "Channel: Feishu",
		Body:        "Feishu app secret. It is masked and will not be added to transcript history.",
		Placeholder: "app-secret",
		Secret:      true,
		Required:    true,
	})
	if strings.TrimSpace(appSecret) == "" {
		w.setStatus("channel configuration cancelled")
		return
	}
	chatIDs := w.requestText(ctx, modutui.HumanTextRequest{
		ID:          "channel-feishu-chat-ids",
		Title:       "Channel: Feishu",
		Body:        "Chat IDs separated by commas or spaces, or - to allow all authorized chats.",
		Placeholder: "oc_xxx, oc_yyy or -",
		Required:    true,
	})
	if strings.TrimSpace(chatIDs) == "" || ctx.Err() != nil {
		w.setStatus("channel configuration cancelled")
		return
	}
	w.setStatus("saving Feishu channel")
	out, err := w.hooks.ConfigureFeishu(FeishuChannelInput{
		AppID:     appID,
		AppSecret: appSecret,
		ChatIDs:   parseChannelChatIDs(chatIDs),
	})
	w.postResult("Channel: Feishu", out, err)
}

func (w *moduTUIChannelWizard) requestChoice(ctx context.Context, title, body string, options []modutui.HumanPromptOption) string {
	if w == nil || w.send == nil {
		return ""
	}
	ch := make(chan string, 1)
	w.send(modutui.RequestHumanPromptMsg{
		Request: modutui.HumanPromptRequest{
			ID:           "channel",
			Title:        title,
			Body:         body,
			Options:      options,
			DefaultIndex: -1,
		},
		Respond: ch,
	})
	select {
	case value := <-ch:
		return strings.TrimSpace(value)
	case <-ctx.Done():
		w.send(modutui.CancelHumanPromptMsg{ID: "channel"})
		return ""
	}
}

func (w *moduTUIChannelWizard) requestText(ctx context.Context, req modutui.HumanTextRequest) string {
	if w == nil || w.send == nil {
		return ""
	}
	ch := make(chan string, 1)
	w.send(modutui.RequestHumanTextMsg{Request: req, Respond: ch})
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		w.send(modutui.CancelHumanTextMsg{ID: req.ID})
		return ""
	}
}

func (w *moduTUIChannelWizard) postResult(title, out string, err error) {
	text := strings.TrimSpace(out)
	if err != nil {
		if text != "" {
			text += "\n"
		}
		text += "error: " + err.Error()
	}
	if text == "" {
		text = "completed"
	}
	w.post(title + "\n\n" + text)
}

func (w *moduTUIChannelWizard) post(text string) {
	if w == nil || w.send == nil {
		return
	}
	w.send(modutui.AppendMessageMsg{Message: modutui.Message{
		Role:         modutui.RoleAssistant,
		Text:         text,
		Preformatted: true,
	}})
}

func (w *moduTUIChannelWizard) setStatus(status string) {
	if w == nil || w.send == nil {
		return
	}
	w.send(modutui.SetStatusMsg{Status: status, TransientFor: moduTUITerminalStatusTTL})
}
