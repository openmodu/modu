package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/channels/telegram"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"
)

// startTelegramBackground launches the Telegram bot as a background goroutine
// that shares the same CodingSession as the TUI. promptMu must be held by the
// caller whenever session.Prompt() is called, preventing concurrent execution.
//
// Incoming Telegram messages are shown in the TUI, processed through the shared
// session, and each assistant text turn is forwarded back to Telegram.
func startTelegramBackground(
	ctx context.Context,
	token string,
	attachDir string,
	session *coding_agent.CodingSession,
	renderer *tui.Renderer,
	promptMu *sync.Mutex,
) error {
	handler := func(hCtx context.Context, chCtx channels.ChannelContext) {
		sender := chCtx.SenderName()
		text := chCtx.MessageText()

		// Show the incoming message in the TUI as a user turn so the local
		// user is aware. ClearLine erases the ❯ prompt if rawReadLine is
		// currently active, preventing the text from appearing after the prompt.
		renderer.ClearLine()
		renderer.PrintUser(fmt.Sprintf("[Telegram @%s] %s", sender, text))

		_ = chCtx.SetWorking(true)

		// Serialize with TUI prompts: wait until the session is free.
		promptMu.Lock()
		defer promptMu.Unlock()

		// Forward each assistant turn back to Telegram as it arrives.
		unsub := session.Subscribe(func(ev agent.AgentEvent) {
			if ev.Type != agent.EventTypeMessageEnd {
				return
			}
			var msg types.AssistantMessage
			switch m := ev.Message.(type) {
			case types.AssistantMessage:
				msg = m
			case *types.AssistantMessage:
				if m == nil {
					return
				}
				msg = *m
			default:
				return
			}
			var parts []string
			for _, block := range msg.Content {
				if tc, ok := block.(*types.TextContent); ok && tc != nil {
					if t := strings.TrimSpace(tc.Text); t != "" {
						parts = append(parts, t)
					}
				}
			}
			if len(parts) > 0 {
				_ = chCtx.Respond(strings.Join(parts, "\n"), true)
			}
		})
		defer unsub()

		if err := session.Prompt(hCtx, text); err != nil && hCtx.Err() == nil {
			_ = chCtx.Respond(fmt.Sprintf("Error: %v", err), true)
		}
	}

	abort := func(_ int64) {
		session.Abort()
	}

	bot, err := telegram.NewBot(token, attachDir, handler, abort)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}

	go func() {
		if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
			renderer.PrintInfo(fmt.Sprintf("[telegram] bot stopped: %v", err))
		}
	}()

	renderer.PrintInfo("[telegram] bot running in background")
	return nil
}
