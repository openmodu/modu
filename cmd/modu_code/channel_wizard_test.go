package main

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func TestModuTUIChannelWizardConfiguresTelegramWithSecretInput(t *testing.T) {
	var saved TelegramChannelInput
	var prompts []modutui.HumanPromptRequest
	var textPrompts []modutui.HumanTextRequest
	var messages []tea.Msg
	wizard := newModuTUIChannelWizard(CommandHooks{
		ConfigureTelegram: func(input TelegramChannelInput) (string, error) {
			saved = input
			return "Telegram channel configured", nil
		},
	}, func(msg tea.Msg) {
		messages = append(messages, msg)
		switch req := msg.(type) {
		case modutui.RequestHumanPromptMsg:
			prompts = append(prompts, req.Request)
			req.Respond <- "telegram"
		case modutui.RequestHumanTextMsg:
			textPrompts = append(textPrompts, req.Request)
			req.Respond <- "telegram-secret"
		}
	})

	wizard.Start(context.Background())

	if len(prompts) != 1 || len(prompts[0].Options) != 2 || prompts[0].Options[0].Value != "telegram" || prompts[0].Options[1].Value != "feishu" {
		t.Fatalf("unexpected channel prompt: %#v", prompts)
	}
	if len(textPrompts) != 1 || !textPrompts[0].Secret || !textPrompts[0].Required {
		t.Fatalf("Telegram token prompt must be required and secret: %#v", textPrompts)
	}
	if saved.Token != "telegram-secret" {
		t.Fatalf("saved input = %#v", saved)
	}
	if output := joinedModuTUIAppendMessages(messages); strings.Contains(output, saved.Token) {
		t.Fatalf("wizard output leaked Telegram token: %q", output)
	}
}

func TestModuTUIChannelWizardConfiguresFeishu(t *testing.T) {
	var saved FeishuChannelInput
	var textPrompts []modutui.HumanTextRequest
	responses := []string{"cli_test", "feishu-secret", "oc_a, oc_b oc_a"}
	wizard := newModuTUIChannelWizard(CommandHooks{
		ConfigureFeishu: func(input FeishuChannelInput) (string, error) {
			saved = input
			return "Feishu channel configured", nil
		},
	}, func(msg tea.Msg) {
		switch req := msg.(type) {
		case modutui.RequestHumanPromptMsg:
			req.Respond <- "feishu"
		case modutui.RequestHumanTextMsg:
			textPrompts = append(textPrompts, req.Request)
			if len(responses) == 0 {
				t.Fatalf("unexpected text prompt: %#v", req.Request)
			}
			next := responses[0]
			responses = responses[1:]
			req.Respond <- next
		}
	})

	wizard.Start(context.Background())

	if len(textPrompts) != 3 {
		t.Fatalf("text prompts = %#v", textPrompts)
	}
	if textPrompts[0].Secret || !textPrompts[1].Secret || textPrompts[2].Secret {
		t.Fatalf("only Feishu app secret should use secret input: %#v", textPrompts)
	}
	if saved.AppID != "cli_test" || saved.AppSecret != "feishu-secret" || strings.Join(saved.ChatIDs, ",") != "oc_a,oc_b" {
		t.Fatalf("saved input = %#v", saved)
	}
}

func TestModuTUIChannelWizardCancelDoesNotSave(t *testing.T) {
	called := false
	wizard := newModuTUIChannelWizard(CommandHooks{
		ConfigureTelegram: func(TelegramChannelInput) (string, error) {
			called = true
			return "", nil
		},
		ConfigureFeishu: func(FeishuChannelInput) (string, error) {
			called = true
			return "", nil
		},
	}, func(msg tea.Msg) {
		if req, ok := msg.(modutui.RequestHumanPromptMsg); ok {
			req.Respond <- ""
		}
	})

	wizard.Start(context.Background())
	if called {
		t.Fatal("cancelled channel wizard should not save configuration")
	}
}
