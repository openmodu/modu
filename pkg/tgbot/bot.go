package tgbot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/approval"
	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/channels/telegram"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
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
	approvalCh chan approval.Request,
) (string, error) {
	var bot *telegram.Bot
	active := &activeTelegramPrompt{}

	handler := func(hCtx context.Context, chCtx channels.ChannelContext) {
		sender := chCtx.SenderName()
		text := strings.TrimSpace(chCtx.MessageText())

		renderer.ClearLine()
		renderer.PrintUser(fmt.Sprintf("← telegram · %s: %s", sender, text))

		if handleTelegramQueuedInput(chCtx, session, active, text) {
			return
		}

		_ = chCtx.SetWorking(true)

		promptMu.Lock()
		defer promptMu.Unlock()

		thinkCh := make(chan string, 4)

		unsub := session.Subscribe(func(ev types.Event) {
			if ev.Type != types.EventTypeMessageEnd {
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

			tuiApprovalFn := func(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error) {
				respCh := make(chan string, 1)
				req := approval.Request{
					ToolName:   toolName,
					ToolCallID: toolCallID,
					Args:       args,
					Response:   respCh,
				}
				select {
				case approvalCh <- req:
				case <-hCtx.Done():
					return types.ToolApprovalDeny, hCtx.Err()
				}
				select {
				case decision := <-respCh:
					return types.ToolApprovalDecision(decision), nil
				case <-hCtx.Done():
					return types.ToolApprovalDeny, hCtx.Err()
				}
			}

			session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error) {
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

				req := approval.Request{
					ToolName:   toolName,
					ToolCallID: toolCallID,
					Args:       args,
					Response:   respCh,
					Cancel:     cancelCh,
				}
				select {
				case approvalCh <- req:
				case <-hCtx.Done():
					return types.ToolApprovalDeny, hCtx.Err()
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
				case <-hCtx.Done():
					close(cancelCh)
					bot.CancelApproval(chatID)
					if kbdErr == nil {
						bot.RemoveKeyboard(chatID, kbd.MessageID)
					}
					return types.ToolApprovalDeny, hCtx.Err()
				}

				d := types.ToolApprovalDecision(decision)

				var result string
				switch d {
				case types.ToolApprovalAllow:
					result = fmt.Sprintf("✅ `%s` allowed", toolName)
				case types.ToolApprovalAllowAlways:
					result = fmt.Sprintf("✅ `%s` always allowed", toolName)
				case types.ToolApprovalDeny:
					result = fmt.Sprintf("❌ `%s` denied", toolName)
				case types.ToolApprovalDenyAlways:
					result = fmt.Sprintf("❌ `%s` always denied", toolName)
				}
				if result != "" {
					_ = chCtx.RespondInThread(result)
				}

				return d, nil
			})
			defer session.SetToolApprovalCallback(tuiApprovalFn)
		}

		if err := runTelegramPromptTurns(hCtx, session, text, active); err != nil && hCtx.Err() == nil {
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

type activeTelegramPrompt struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	token  uint64
}

func (a *activeTelegramPrompt) Set(cancel context.CancelFunc) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token++
	a.cancel = cancel
	return a.token
}

func (a *activeTelegramPrompt) Clear(token uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token == token {
		a.cancel = nil
	}
}

func (a *activeTelegramPrompt) Active() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancel != nil
}

func (a *activeTelegramPrompt) Cancel() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel == nil {
		return false
	}
	a.cancel()
	return true
}

func handleTelegramQueuedInput(chCtx channels.ChannelContext, session *coding_agent.CodingSession, active *activeTelegramPrompt, text string) bool {
	if session == nil || text == "" {
		return false
	}
	if arg, ok := telegramQueueCommandArg(text, "/steer", "/s"); ok {
		if arg == "" {
			_ = chCtx.RespondInThread("steer requires a message")
			return true
		}
		if !telegramTaskActive(session, active) {
			_ = chCtx.RespondInThread("no active task to steer")
			return true
		}
		session.Steer(arg)
		active.Cancel()
		session.Abort()
		session.AbortBash()
		_ = chCtx.RespondInThread("queued steer")
		return true
	}
	if arg, ok := telegramQueueCommandArg(text, "/followup", "/f"); ok {
		if arg == "" {
			_ = chCtx.RespondInThread("follow up requires a message")
			return true
		}
		if !telegramTaskActive(session, active) {
			_ = chCtx.RespondInThread("no active task for follow up")
			return true
		}
		session.FollowUp(arg)
		_ = chCtx.RespondInThread("queued follow up")
		return true
	}
	if telegramTaskActive(session, active) {
		session.FollowUp(text)
		_ = chCtx.RespondInThread("queued follow up")
		return true
	}
	return false
}

func telegramQueueCommandArg(line string, names ...string) (string, bool) {
	for _, name := range names {
		if line == name {
			return "", true
		}
		if strings.HasPrefix(line, name+" ") {
			return strings.TrimSpace(strings.TrimPrefix(line, name)), true
		}
	}
	return "", false
}

func telegramTaskActive(session *coding_agent.CodingSession, active *activeTelegramPrompt) bool {
	if active != nil && active.Active() {
		return true
	}
	if session == nil || session.GetAgent() == nil {
		return false
	}
	return session.GetAgent().GetState().IsStreaming
}

func runTelegramPromptTurns(parent context.Context, session *coding_agent.CodingSession, text string, active *activeTelegramPrompt) error {
	err := runTelegramTurn(parent, active, func(ctx context.Context) error {
		return session.Prompt(ctx, text)
	})
	if err != nil && !shouldContinueTelegramQueue(parent, session, err) {
		return err
	}
	for parent.Err() == nil && session.GetAgent() != nil && session.GetAgent().HasQueuedMessages() {
		err = runTelegramTurn(parent, active, func(ctx context.Context) error {
			return session.GetAgent().Continue(ctx)
		})
		if err != nil && !shouldContinueTelegramQueue(parent, session, err) {
			return err
		}
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return nil
}

func runTelegramTurn(parent context.Context, active *activeTelegramPrompt, run func(context.Context) error) error {
	ctx, cancel := context.WithCancel(parent)
	if active != nil {
		token := active.Set(cancel)
		defer active.Clear(token)
	}
	defer cancel()
	return run(ctx)
}

func shouldContinueTelegramQueue(parent context.Context, session *coding_agent.CodingSession, err error) bool {
	if parent.Err() != nil || !errors.Is(err, context.Canceled) {
		return false
	}
	return session != nil && session.GetAgent() != nil && session.GetAgent().HasQueuedMessages()
}
