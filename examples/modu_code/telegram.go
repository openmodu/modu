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
//
// When a tool needs approval during a Telegram-triggered session, both the TUI
// prompt (y/a/n/d keys) and a Telegram inline keyboard (4 buttons) are shown
// simultaneously. Whichever side responds first wins; the other is dismissed.
func startTelegramBackground(
	ctx context.Context,
	token string,
	attachDir string,
	session *coding_agent.CodingSession,
	renderer *tui.BTRenderer,
	promptMu *sync.Mutex,
	approvalCh chan tui.ApprovalRequest,
) (string, error) {
	// bot is assigned after NewBot returns. The handler closure captures the
	// variable by reference, so bot is valid by the time any message arrives.
	var bot *telegram.Bot

	handler := func(hCtx context.Context, chCtx channels.ChannelContext) {
		sender := chCtx.SenderName()
		text := chCtx.MessageText()

		// Show the incoming message in the TUI as a user turn so the local
		// user is aware. ClearLine erases the ❯ prompt if rawReadLine is
		// currently active, preventing the text from appearing after the prompt.
		renderer.ClearLine()
		renderer.PrintUser(fmt.Sprintf("← telegram · %s: %s", sender, text))

		_ = chCtx.SetWorking(true)

		// Serialize with TUI prompts: wait until the session is free.
		promptMu.Lock()
		defer promptMu.Unlock()

		// thinkCh carries the thinking/text from an EventTypeMessageEnd that also
		// contains tool calls. wrappedFn reads it before sending the keyboard so
		// that "thinking" always appears before the approval prompt in Telegram.
		// Buffer of 4 handles bursts of multi-tool turns without blocking.
		thinkCh := make(chan string, 4)

		// Forward each assistant text turn back to Telegram as it arrives.
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
			hasTool := false
			for _, block := range msg.Content {
				if tc, ok := block.(*types.TextContent); ok && tc != nil {
					if t := strings.TrimSpace(tc.Text); t != "" {
						parts = append(parts, t)
					}
				}
				if _, ok := block.(*types.ToolCallContent); ok {
					hasTool = true
				}
			}
			body := ""
			if len(parts) > 0 {
				body = strings.Join(parts, "\n")
				if msg.Usage.TotalTokens > 0 {
					body += fmt.Sprintf("\n\n_%d tokens_", msg.Usage.TotalTokens)
				}
			}
			if hasTool {
				// Tool approval follows: hand the text off to wrappedFn so it
				// can send thinking → keyboard in the correct order.
				select {
				case thinkCh <- body:
				default:
				}
			} else if body != "" {
				// Final response: send directly (no keyboard follows).
				_ = chCtx.RespondInThread(body)
			}
		})
		defer unsub()

		// For Telegram-triggered sessions, override the approval callback so
		// that both the TUI prompt and a Telegram inline keyboard are shown.
		// Whichever side responds first wins; the other is dismissed.
		if approvalCh != nil && bot != nil {
			chatID := chCtx.ChatID()

			// Build a plain TUI-only approval fn for restoring on exit.
			tuiApprovalFn := func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
				respCh := make(chan string, 1)
				approvalCh <- tui.ApprovalRequest{
					ToolName:   toolName,
					ToolCallID: toolCallID,
					Args:       args,
					Response:   respCh,
				}
				return agent.ToolApprovalDecision(<-respCh), nil
			}

			session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
				// Send thinking text first (from the preceding EventTypeMessageEnd),
				// then keyboard — all in this goroutine to guarantee ordering.
				var think string
				select {
				case think = <-thinkCh:
				default:
				}
				if think != "" {
					_ = chCtx.RespondInThread(think)
				}

				// cancelCh is closed to dismiss the TUI prompt when Telegram wins.
				cancelCh := make(chan struct{})
				respCh := make(chan string, 1)

				// Show TUI approval prompt.
				approvalCh <- tui.ApprovalRequest{
					ToolName:   toolName,
					ToolCallID: toolCallID,
					Args:       args,
					Response:   respCh,
					Cancel:     cancelCh,
				}

				// Show Telegram inline keyboard.
				kbd, kbdErr := bot.SendApprovalKeyboard(chatID, toolName)
				var tgCh <-chan string
				if kbdErr == nil {
					tgCh = bot.AwaitApproval(chatID)
				}

				var decision string
				select {
				case decision = <-respCh:
					// TUI won — clean up Telegram keyboard.
					bot.CancelApproval(chatID)
					if kbdErr == nil {
						bot.RemoveKeyboard(chatID, kbd.MessageID)
					}
				case decision = <-tgCh:
					// Telegram won — dismiss TUI prompt.
					close(cancelCh)
					// Map Telegram button short-codes to full decision strings.
					switch decision {
					case "y":
						decision = "allow"
					case "a":
						decision = "allow_always"
					case "n":
						decision = "deny"
					case "d":
						decision = "deny_always"
					}
				}

				d := agent.ToolApprovalDecision(decision)

				// Send result back to Telegram chat.
				var result string
				switch d {
				case agent.ToolApprovalAllow:
					result = fmt.Sprintf("✅ `%s` allowed", toolName)
				case agent.ToolApprovalAllowAlways:
					result = fmt.Sprintf("✅ `%s` always allowed", toolName)
				case agent.ToolApprovalDeny:
					result = fmt.Sprintf("❌ `%s` denied", toolName)
				case agent.ToolApprovalDenyAlways:
					result = fmt.Sprintf("❌ `%s` always denied", toolName)
				}
				if result != "" {
					_ = chCtx.RespondInThread(result)
				}

				return d, nil
			})
			defer session.SetToolApprovalCallback(tuiApprovalFn)
		}

		if err := session.Prompt(hCtx, text); err != nil && hCtx.Err() == nil {
			_ = chCtx.RespondInThread(fmt.Sprintf("Error: %v", err))
		}
	}

	abort := func(_ int64) {
		session.Abort()
	}

	var err error
	bot, err = telegram.NewBot(token, attachDir, handler, abort)
	if err != nil {
		return "", fmt.Errorf("create telegram bot: %w", err)
	}

	go func() {
		if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
			renderer.PrintInfo(fmt.Sprintf("[telegram] bot stopped: %v", err))
		}
	}()

	return bot.Username(), nil
}
