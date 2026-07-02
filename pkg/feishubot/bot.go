package feishubot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/channels/feishu"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

// Printer is implemented by the UI layer to surface Feishu events.
type Printer interface {
	PrintUser(string)
	PrintInfo(string)
	PrintError(error)
}

// Start launches the Feishu bot as a background goroutine that shares the same
// CodingSession as the TUI.
func Start(
	ctx context.Context,
	cfg Config,
	session *coding_agent.CodingSession,
	renderer Printer,
	promptMu *sync.Mutex,
) error {
	if !cfg.Ready() {
		return fmt.Errorf("feishu appID/appSecret are required")
	}
	if promptMu == nil {
		promptMu = &sync.Mutex{}
	}

	active := &activeFeishuPrompt{}
	debugf := feishuRuntimeLogger()

	handler := func(hCtx context.Context, chCtx channels.ChannelContext) {
		sender := chCtx.SenderName()
		text := strings.TrimSpace(chCtx.MessageText())

		if renderer != nil {
			renderer.PrintUser(fmt.Sprintf("<- feishu · %s: %s", sender, text))
		}

		if handleFeishuQueuedInput(chCtx, session, active, text) {
			return
		}

		if err := chCtx.SetWorking(true); err != nil {
			debugf("set working failed: chat_id=%d err=%v", chCtx.ChatID(), err)
		}

		promptMu.Lock()
		defer promptMu.Unlock()

		beforeMessages := len(session.GetMessages())
		if err := runFeishuPromptTurns(hCtx, session, text, active); err != nil && hCtx.Err() == nil {
			if sendErr := chCtx.RespondInThread(fmt.Sprintf("Error: %v", err)); sendErr != nil {
				debugf("send error response failed: chat_id=%d err=%v", chCtx.ChatID(), sendErr)
			}
			return
		}

		body := lastAssistantTextAfter(session.GetMessages(), beforeMessages)
		if strings.TrimSpace(body) == "" {
			body = strings.TrimSpace(session.GetLastAssistantText())
		}
		if strings.TrimSpace(body) == "" {
			debugf("send final response skipped: chat_id=%d empty assistant text", chCtx.ChatID())
			return
		}
		if err := chCtx.RespondInThread(body); err != nil {
			debugf("send final response failed: chat_id=%d err=%v", chCtx.ChatID(), err)
		} else {
			debugf("send final response ok: chat_id=%d body_len=%d", chCtx.ChatID(), len(body))
		}
	}

	abort := func(_ int64) { session.Abort() }

	bot, err := feishu.NewBot(cfg.AppID, cfg.AppSecret, handler, abort)
	if err != nil {
		return fmt.Errorf("create feishu bot: %w", err)
	}
	bot.SetAllowedChatIDs(cfg.ChatIDs)
	bot.SetDebugLogger(debugf)
	debugf("bot configured: app_id=%s chat_allowlist=%d", cfg.AppID, len(cfg.ChatIDs))

	go func() {
		if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
			debugf("bot stopped: %v", err)
		}
	}()

	return nil
}

func feishuRuntimeLogger() func(format string, args ...any) {
	return RuntimeLogf
}

type activeFeishuPrompt struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	token  uint64
}

func (a *activeFeishuPrompt) Set(cancel context.CancelFunc) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token++
	a.cancel = cancel
	return a.token
}

func (a *activeFeishuPrompt) Clear(token uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token == token {
		a.cancel = nil
	}
}

func (a *activeFeishuPrompt) Active() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancel != nil
}

func (a *activeFeishuPrompt) Cancel() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel == nil {
		return false
	}
	a.cancel()
	return true
}

func handleFeishuQueuedInput(chCtx channels.ChannelContext, session *coding_agent.CodingSession, active *activeFeishuPrompt, text string) bool {
	if session == nil || text == "" {
		return false
	}
	if arg, ok := feishuQueueCommandArg(text, "/steer", "/s"); ok {
		if arg == "" {
			_ = chCtx.RespondInThread("steer requires a message")
			return true
		}
		if !feishuTaskActive(session, active) {
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
	if arg, ok := feishuQueueCommandArg(text, "/followup", "/f"); ok {
		if arg == "" {
			_ = chCtx.RespondInThread("follow up requires a message")
			return true
		}
		if !feishuTaskActive(session, active) {
			_ = chCtx.RespondInThread("no active task for follow up")
			return true
		}
		session.FollowUp(arg)
		_ = chCtx.RespondInThread("queued follow up")
		return true
	}
	if feishuTaskActive(session, active) {
		session.FollowUp(text)
		_ = chCtx.RespondInThread("queued follow up")
		return true
	}
	return false
}

func feishuQueueCommandArg(line string, names ...string) (string, bool) {
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

func feishuTaskActive(session *coding_agent.CodingSession, active *activeFeishuPrompt) bool {
	if active != nil && active.Active() {
		return true
	}
	if session == nil || session.GetAgent() == nil {
		return false
	}
	return session.GetAgent().GetState().IsStreaming
}

func runFeishuPromptTurns(parent context.Context, session *coding_agent.CodingSession, text string, active *activeFeishuPrompt) error {
	err := runFeishuTurn(parent, active, func(ctx context.Context) error {
		return session.Prompt(ctx, text)
	})
	if err != nil && !shouldContinueFeishuQueue(parent, session, err) {
		return err
	}
	for parent.Err() == nil && session.GetAgent() != nil && session.GetAgent().HasQueuedMessages() {
		err = runFeishuTurn(parent, active, func(ctx context.Context) error {
			return session.Continue(ctx)
		})
		if err != nil && !shouldContinueFeishuQueue(parent, session, err) {
			return err
		}
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return nil
}

func runFeishuTurn(parent context.Context, active *activeFeishuPrompt, run func(context.Context) error) error {
	ctx, cancel := context.WithCancel(parent)
	if active != nil {
		token := active.Set(cancel)
		defer active.Clear(token)
	}
	defer cancel()
	return run(ctx)
}

func shouldContinueFeishuQueue(parent context.Context, session *coding_agent.CodingSession, err error) bool {
	if parent.Err() != nil || !errors.Is(err, context.Canceled) {
		return false
	}
	return session != nil && session.GetAgent() != nil && session.GetAgent().HasQueuedMessages()
}

func lastAssistantTextAfter(messages []types.AgentMessage, start int) string {
	if start < 0 {
		start = 0
	}
	if start > len(messages) {
		start = len(messages)
	}
	for i := len(messages) - 1; i >= start; i-- {
		if text := assistantTextFromMessage(messages[i]); text != "" {
			return text
		}
	}
	return ""
}

func assistantTextFromMessage(raw types.AgentMessage) string {
	var msg types.AssistantMessage
	switch m := raw.(type) {
	case types.AssistantMessage:
		msg = m
	case *types.AssistantMessage:
		if m == nil {
			return ""
		}
		msg = *m
	default:
		return ""
	}
	var parts []string
	for _, block := range msg.Content {
		tc, ok := block.(*types.TextContent)
		if !ok || tc == nil {
			continue
		}
		if t := strings.TrimSpace(tc.Text); t != "" {
			parts = append(parts, t)
		}
	}
	body := strings.Join(parts, "\n")
	if body != "" && msg.Usage.TotalTokens > 0 {
		body += fmt.Sprintf("\n\n%d tokens", msg.Usage.TotalTokens)
	}
	return body
}
