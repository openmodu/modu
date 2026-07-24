package main

import (
	"context"

	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

type moduTUIChannelWizard struct {
	hooks  CommandHooks
	dialog codetui.Dialog
}

func newModuTUIChannelWizard(hooks CommandHooks, client modutui.Client) *moduTUIChannelWizard {
	return &moduTUIChannelWizard{
		hooks:  hooks,
		dialog: codetui.NewDialog(client, moduTUITerminalStatusTTL),
	}
}

func (w *moduTUIChannelWizard) Start(ctx context.Context) {
	selection, completed := w.run(ctx, codetui.Flow{
		ID: "channel-select",
		Steps: []codetui.FlowStep{{
			ID:    "channel",
			Kind:  codetui.FlowStepChoice,
			Title: "Channel",
			Body:  "Choose a channel to configure",
			Options: []modutui.HumanPromptOption{
				{Label: "Telegram", Value: "telegram"},
				{Label: "Feishu", Value: "feishu"},
			},
		}},
	})
	if !completed {
		w.dialog.Status("channel configuration cancelled")
		return
	}
	switch selection.Value("channel") {
	case "telegram":
		w.configureTelegram(ctx)
	case "feishu":
		w.configureFeishu(ctx)
	}
}

func (w *moduTUIChannelWizard) configureTelegram(ctx context.Context) {
	if w.hooks.ConfigureTelegram == nil {
		w.dialog.Post("Channel\n\nTelegram configuration is not available.")
		return
	}
	result, completed := w.run(ctx, codetui.Flow{
		ID: "channel-telegram",
		Steps: []codetui.FlowStep{{
			ID:          "token",
			Kind:        codetui.FlowStepText,
			Title:       "Channel: Telegram",
			Body:        "Telegram bot token. It is masked and will not be added to transcript history.",
			Placeholder: "123456:bot-token",
			Secret:      true,
			Required:    true,
		}},
	})
	if !completed {
		w.dialog.Status("channel configuration cancelled")
		return
	}
	w.dialog.Status("saving Telegram channel")
	out, err := w.hooks.ConfigureTelegram(TelegramChannelInput{Token: result.Value("token")})
	w.dialog.PostResult("Channel: Telegram", out, err)
}

func (w *moduTUIChannelWizard) configureFeishu(ctx context.Context) {
	if w.hooks.ConfigureFeishu == nil {
		w.dialog.Post("Channel\n\nFeishu configuration is not available.")
		return
	}
	result, completed := w.run(ctx, codetui.Flow{
		ID: "channel-feishu",
		Steps: []codetui.FlowStep{
			{
				ID:          "app-id",
				Kind:        codetui.FlowStepText,
				Title:       "Channel: Feishu",
				Body:        "Feishu app ID.",
				Placeholder: "cli_xxx",
				Required:    true,
			},
			{
				ID:          "app-secret",
				Kind:        codetui.FlowStepText,
				Title:       "Channel: Feishu",
				Body:        "Feishu app secret. It is masked and will not be added to transcript history.",
				Placeholder: "app-secret",
				Secret:      true,
				Required:    true,
			},
			{
				ID:          "chat-ids",
				Kind:        codetui.FlowStepText,
				Title:       "Channel: Feishu",
				Body:        "Chat IDs separated by commas or spaces, or - to allow all authorized chats.",
				Placeholder: "oc_xxx, oc_yyy or -",
				Required:    true,
			},
		},
	})
	if !completed {
		w.dialog.Status("channel configuration cancelled")
		return
	}
	w.dialog.Status("saving Feishu channel")
	out, err := w.hooks.ConfigureFeishu(FeishuChannelInput{
		AppID:     result.Value("app-id"),
		AppSecret: result.Value("app-secret"),
		ChatIDs:   parseChannelChatIDs(result.Value("chat-ids")),
	})
	w.dialog.PostResult("Channel: Feishu", out, err)
}

func (w *moduTUIChannelWizard) run(ctx context.Context, flow codetui.Flow) (codetui.FlowResult, bool) {
	if w == nil {
		return codetui.FlowResult{}, false
	}
	result, completed, err := w.dialog.RunFlow(ctx, flow)
	if err != nil && err != context.Canceled {
		w.dialog.Post(flow.ID + "\n\nerror: " + err.Error())
	}
	return result, completed
}
