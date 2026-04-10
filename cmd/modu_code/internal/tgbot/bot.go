package tgbot

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

// Printer is implemented by the UI layer to surface Telegram events.
type Printer interface {
	ClearLine()
	PrintUser(string)
	PrintInfo(string)
	PrintError(error)
}

// Start launches the Telegram bot as a background goroutine that shares
// the same CodingSession as the TUI. Returns the bot username on success.
func Start(
	ctx context.Context,
	token string,
	attachDir string,
	session *coding_agent.CodingSession,
	renderer Printer,
	promptMu *sync.Mutex,
	approvalCh chan tui.ApprovalRequest,
) (string, error) {
	var bot *telegram.Bot

	handler := func(hCtx context.Context, chCtx channels.ChannelContext) {
		sender := chCtx.SenderName()
		text := chCtx.MessageText()

		renderer.ClearLine()
		renderer.PrintUser(fmt.Sprintf("← telegram · %s: %s", sender, text))

		_ = chCtx.SetWorking(true)

		promptMu.Lock()
		defer promptMu.Unlock()

		thinkCh := make(chan string, 4)

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
				select {
				case thinkCh <- body:
				default:
				}
			} else if body != "" {
				_ = chCtx.RespondInThread(body)
			}
		})
		defer unsub()

		if approvalCh != nil && bot != nil {
			chatID := chCtx.ChatID()

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
				var think string
				select {
				case think = <-thinkCh:
				default:
				}
				if think != "" {
					_ = chCtx.RespondInThread(think)
				}

				cancelCh := make(chan struct{})
				respCh := make(chan string, 1)

				approvalCh <- tui.ApprovalRequest{
					ToolName:   toolName,
					ToolCallID: toolCallID,
					Args:       args,
					Response:   respCh,
					Cancel:     cancelCh,
				}

				kbd, kbdErr := bot.SendApprovalKeyboard(chatID, toolName)
				var tgCh <-chan string
				if kbdErr == nil {
					tgCh = bot.AwaitApproval(chatID)
				}

				var decision string
				select {
				case decision = <-respCh:
					bot.CancelApproval(chatID)
					if kbdErr == nil {
						bot.RemoveKeyboard(chatID, kbd.MessageID)
					}
				case decision = <-tgCh:
					close(cancelCh)
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

	abort := func(_ int64) { session.Abort() }

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
