package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

// Printer surfaces inbound channel activity to a host UI without tying the
// channel package to any concrete TUI implementation.
type Printer interface {
	PrintUser(string)
	PrintInfo(string)
	PrintError(error)
}

type CodingBridgeOptions struct {
	Channel  Channel
	Session  *coding_agent.CodingSession
	Printer  Printer
	PromptMu *sync.Mutex
	Logf     func(format string, args ...any)
}

// StartCodingBridge wires a messaging channel to a CodingSession. Any inbound
// channel message becomes a prompt, follow-up, or steer request, and the final
// assistant text is sent back through the same ChannelContext.
func StartCodingBridge(ctx context.Context, opts CodingBridgeOptions) error {
	if opts.Channel == nil {
		return fmt.Errorf("channel is required")
	}
	if opts.Session == nil {
		return fmt.Errorf("coding session is required")
	}
	if opts.PromptMu == nil {
		opts.PromptMu = &sync.Mutex{}
	}
	active := &activeChannelPrompt{}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	deduper := newMessageDeduper(1024)

	opts.Channel.SetMessageHandler(func(hCtx context.Context, chCtx ChannelContext) {
		if key := channelMessageKey(opts.Channel.Name(), chCtx); key != "" && deduper.Seen(key) {
			logf("duplicate inbound ignored: channel=%s chat_id=%d message_ts=%s", opts.Channel.Name(), chCtx.ChatID(), chCtx.MessageTS())
			return
		}
		sender := chCtx.SenderName()
		text := strings.TrimSpace(chCtx.MessageText())
		if opts.Printer != nil {
			opts.Printer.PrintUser(fmt.Sprintf("<- %s · %s: %s", opts.Channel.Name(), sender, text))
		}

		if handleQueuedInput(chCtx, opts.Session, active, text) {
			return
		}

		if err := chCtx.SetWorking(true); err != nil {
			logf("set working failed: channel=%s chat_id=%d err=%v", opts.Channel.Name(), chCtx.ChatID(), err)
		}

		opts.PromptMu.Lock()
		defer opts.PromptMu.Unlock()

		beforeMessages := len(opts.Session.GetMessages())
		if err := runPromptTurns(hCtx, opts.Session, text, active); err != nil && hCtx.Err() == nil {
			if sendErr := chCtx.RespondInThread(fmt.Sprintf("Error: %v", err)); sendErr != nil {
				logf("send error response failed: channel=%s chat_id=%d err=%v", opts.Channel.Name(), chCtx.ChatID(), sendErr)
			}
			return
		}

		body := lastAssistantTextAfter(opts.Session.GetMessages(), beforeMessages)
		if strings.TrimSpace(body) == "" {
			body = strings.TrimSpace(opts.Session.GetLastAssistantText())
		}
		if strings.TrimSpace(body) == "" {
			logf("send final response skipped: channel=%s chat_id=%d empty assistant text", opts.Channel.Name(), chCtx.ChatID())
			return
		}
		if err := chCtx.RespondInThread(body); err != nil {
			logf("send final response failed: channel=%s chat_id=%d err=%v", opts.Channel.Name(), chCtx.ChatID(), err)
		} else {
			logf("send final response ok: channel=%s chat_id=%d body_len=%d", opts.Channel.Name(), chCtx.ChatID(), len(body))
		}
	})
	opts.Channel.SetAbortHandler(func(int64) {
		opts.Session.Abort()
	})

	go func() {
		if err := opts.Channel.Run(ctx); err != nil && ctx.Err() == nil {
			logf("channel stopped: channel=%s err=%v", opts.Channel.Name(), err)
			if opts.Printer != nil {
				opts.Printer.PrintError(fmt.Errorf("%s channel stopped: %w", opts.Channel.Name(), err))
			}
		}
	}()
	return nil
}

type messageDeduper struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	order []string
	max   int
}

func newMessageDeduper(max int) *messageDeduper {
	if max <= 0 {
		max = 1
	}
	return &messageDeduper{
		seen: make(map[string]struct{}, max),
		max:  max,
	}
}

func (d *messageDeduper) Seen(key string) bool {
	if key == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[key]; ok {
		return true
	}
	d.seen[key] = struct{}{}
	d.order = append(d.order, key)
	for len(d.order) > d.max {
		delete(d.seen, d.order[0])
		d.order = d.order[1:]
	}
	return false
}

func channelMessageKey(channelName string, chCtx ChannelContext) string {
	messageTS := strings.TrimSpace(chCtx.MessageTS())
	if messageTS == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d:%s", channelName, chCtx.ChatID(), messageTS)
}

type activeChannelPrompt struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	token  uint64
}

func (a *activeChannelPrompt) Set(cancel context.CancelFunc) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token++
	a.cancel = cancel
	return a.token
}

func (a *activeChannelPrompt) Clear(token uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token == token {
		a.cancel = nil
	}
}

func (a *activeChannelPrompt) Active() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancel != nil
}

func (a *activeChannelPrompt) Cancel() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel == nil {
		return false
	}
	a.cancel()
	return true
}

func handleQueuedInput(chCtx ChannelContext, session *coding_agent.CodingSession, active *activeChannelPrompt, text string) bool {
	if session == nil || text == "" {
		return false
	}
	if arg, ok := queueCommandArg(text, "/steer", "/s"); ok {
		if arg == "" {
			_ = chCtx.RespondInThread("steer requires a message")
			return true
		}
		if !taskActive(session, active) {
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
	if arg, ok := queueCommandArg(text, "/followup", "/f"); ok {
		if arg == "" {
			_ = chCtx.RespondInThread("follow up requires a message")
			return true
		}
		if !taskActive(session, active) {
			_ = chCtx.RespondInThread("no active task for follow up")
			return true
		}
		session.FollowUp(arg)
		_ = chCtx.RespondInThread("queued follow up")
		return true
	}
	if taskActive(session, active) {
		session.FollowUp(text)
		_ = chCtx.RespondInThread("queued follow up")
		return true
	}
	return false
}

func queueCommandArg(line string, names ...string) (string, bool) {
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

func taskActive(session *coding_agent.CodingSession, active *activeChannelPrompt) bool {
	if active != nil && active.Active() {
		return true
	}
	if session == nil || session.GetAgent() == nil {
		return false
	}
	return session.GetAgent().GetState().IsStreaming
}

func runPromptTurns(parent context.Context, session *coding_agent.CodingSession, text string, active *activeChannelPrompt) error {
	err := runPromptTurn(parent, active, func(ctx context.Context) error {
		return session.Prompt(ctx, text)
	})
	if err != nil && !shouldContinueQueue(parent, session, err) {
		return err
	}
	for parent.Err() == nil && session.GetAgent() != nil && session.GetAgent().HasQueuedMessages() {
		err = runPromptTurn(parent, active, func(ctx context.Context) error {
			return session.Continue(ctx)
		})
		if err != nil && !shouldContinueQueue(parent, session, err) {
			return err
		}
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return nil
}

func runPromptTurn(parent context.Context, active *activeChannelPrompt, run func(context.Context) error) error {
	ctx, cancel := context.WithCancel(parent)
	if active != nil {
		token := active.Set(cancel)
		defer active.Clear(token)
	}
	defer cancel()
	return run(ctx)
}

func shouldContinueQueue(parent context.Context, session *coding_agent.CodingSession, err error) bool {
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
