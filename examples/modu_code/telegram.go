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

// ApprovalFn is the signature for tool-approval callbacks.
type ApprovalFn func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error)

// startTelegramBackground launches the Telegram bot as a background goroutine
// that shares the same CodingSession as the TUI. promptMu must be held by the
// caller whenever session.Prompt() is called, preventing concurrent execution.
//
// Incoming Telegram messages are shown in the TUI, processed through the shared
// session, and each assistant text turn is forwarded back to Telegram.
//
// restoreApproval is the approval callback to reinstall after each Telegram
// prompt completes (typically the TUI approval function, or nil for --no-approve).
// During a Telegram prompt a Telegram-native approval handler is active that
// sends an inline prompt to the user and waits for their reply.
func startTelegramBackground(
	ctx context.Context,
	token string,
	attachDir string,
	session *coding_agent.CodingSession,
	renderer *tui.Renderer,
	promptMu *sync.Mutex,
	restoreApproval ApprovalFn,
) error {
	// bot is captured by the handler closure after NewBot returns.
	var bot *telegram.Bot

	handler := func(hCtx context.Context, chCtx channels.ChannelContext) {
		sender := chCtx.SenderName()
		text := chCtx.MessageText()
		chatID := chCtx.ChatID()

		// Show the incoming message in the TUI as a user turn so the local
		// user is aware. ClearLine erases the ❯ prompt if rawReadLine is
		// currently active, preventing the text from appearing after the prompt.
		renderer.ClearLine()
		renderer.PrintUser(fmt.Sprintf("[Telegram @%s] %s", sender, text))

		_ = chCtx.SetWorking(true)

		// Serialize with TUI prompts: wait until the session is free.
		promptMu.Lock()
		defer promptMu.Unlock()

		// While this Telegram prompt is active, replace the approval callback
		// with one that asks the user via Telegram and waits for their reply.
		// This prevents the deadlock that occurs when the TUI approval handler
		// waits on ApprovalRequests which nobody reads during Telegram processing.
		session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
			_ = bot.SendApprovalKeyboard(chatID, toolName)
			renderer.PrintInfo(fmt.Sprintf("[telegram] waiting for tool approval: %s", toolName))

			respCh := bot.AwaitApproval(chatID)
			select {
			case resp := <-respCh:
				resp = strings.ToLower(strings.TrimSpace(resp))
				renderer.PrintInfo(fmt.Sprintf("[telegram] approval response: %q", resp))
				switch resp {
				case "y", "yes":
					return agent.ToolApprovalAllow, nil
				case "a", "always":
					return agent.ToolApprovalAllowAlways, nil
				case "d", "deny":
					return agent.ToolApprovalDenyAlways, nil
				default: // "n", "no", or anything else → deny once
					return agent.ToolApprovalDeny, nil
				}
			case <-hCtx.Done():
				bot.CancelApproval(chatID)
				return agent.ToolApprovalDeny, hCtx.Err()
			}
		})
		// Restore the previous (TUI) approval callback when done.
		defer session.SetToolApprovalCallback(restoreApproval)

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

	var err error
	bot, err = telegram.NewBot(token, attachDir, handler, abort)
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
