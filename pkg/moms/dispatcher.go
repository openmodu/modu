package moms

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/skills"
	"github.com/openmodu/modu/pkg/types"
)

// Dispatcher manages per-chat Runners and implements channels.MessageHandler / channels.AbortHandler.
// It is the orchestration layer that bridges the channel (Telegram, Feishu…) and the agent runner.
type Dispatcher struct {
	mu          sync.Mutex
	store       *Store
	sandbox     *Sandbox
	workingDir  string
	model       *types.Model
	getAPIKey   func(provider string) (string, error)
	settings    *Settings
	registryMgr *skills.RegistryManager
	searchCache *skills.SearchCache

	runners map[int64]*Runner
	queue   map[int64]chan dispatchedMsg
}

type dispatchedMsg struct {
	ctx        context.Context
	chCtx      channels.ChannelContext
	senderName string
	ts         string
}

// NewDispatcher creates a Dispatcher.
func NewDispatcher(
	sandbox *Sandbox,
	workingDir string,
	model *types.Model,
	getAPIKey func(provider string) (string, error),
	registryMgr *skills.RegistryManager,
	searchCache *skills.SearchCache,
) *Dispatcher {
	if err := InitBootstrapFiles(workingDir); err != nil {
		fmt.Printf("[moms] warning: failed to init bootstrap files: %v\n", err)
	}
	return &Dispatcher{
		store:       NewStore(workingDir),
		sandbox:     sandbox,
		workingDir:  workingDir,
		model:       model,
		getAPIKey:   getAPIKey,
		settings:    NewSettingsManager(workingDir),
		registryMgr: registryMgr,
		searchCache: searchCache,
		runners:     make(map[int64]*Runner),
		queue:       make(map[int64]chan dispatchedMsg),
	}
}

// HandleMessage implements channels.MessageHandler.
// It logs the message then queues it for processing by the per-chat runner goroutine.
func (d *Dispatcher) HandleMessage(ctx context.Context, chCtx channels.ChannelContext) {
	chatID := chCtx.ChatID()
	ts := chCtx.MessageTS()
	senderName := chCtx.SenderName()
	text := chCtx.MessageText()

	// Log the incoming message.
	_ = d.store.LogUserMessage(chatID, ts, senderName, senderName, text, nil)
	fmt.Printf("[moms] chat %d @%s: %s\n", chatID, senderName, TruncateStr(text, 80))

	// Queue to per-chat goroutine.
	d.mu.Lock()
	ch, exists := d.queue[chatID]
	if !exists {
		ch = make(chan dispatchedMsg, 32)
		d.queue[chatID] = ch
		go d.processQueue(ch)
	}
	d.mu.Unlock()

	select {
	case ch <- dispatchedMsg{ctx: ctx, chCtx: chCtx, senderName: senderName, ts: ts}:
	default:
		_ = chCtx.Respond("_Too many messages queued. Please wait._", false)
	}
}

// HandleAbort implements channels.AbortHandler.
func (d *Dispatcher) HandleAbort(chatID int64) {
	d.mu.Lock()
	runner := d.runners[chatID]
	d.mu.Unlock()
	if runner != nil && runner.IsRunning() {
		runner.Abort()
	}
}

// TriggerEvent sends an event-triggered message to a chat — used by EventsWatcher.
func (d *Dispatcher) TriggerEvent(ctx context.Context, chatID int64, filename, text string) {
	runner := d.getOrCreateRunner(chatID)
	synthCtx := &syntheticChannelContext{
		chatID:      chatID,
		messageText: text,
		messageTS:   fmt.Sprintf("event-%s-%d", filename, time.Now().UnixMilli()),
		senderName:  "event",
		store:       d.store,
	}
	result := runner.Run(ctx, synthCtx)
	if !synthCtx.responded || strings.Contains(strings.ToUpper(synthCtx.lastResponse), "[SILENT]") {
		// silent — no output
	}
	_ = result
}

// processQueue processes messages for a single chat sequentially.
func (d *Dispatcher) processQueue(ch chan dispatchedMsg) {
	for msg := range ch {
		runner := d.getOrCreateRunner(msg.chCtx.ChatID())
		result := runner.Run(msg.ctx, msg.chCtx)
		if result.StopReason == "aborted" {
			_ = msg.chCtx.Respond("_Aborted._", false)
		} else if result.Error != nil {
			fmt.Printf("[moms] chat %d error: %v\n", msg.chCtx.ChatID(), result.Error)
		}
		// Log bot response(s) — the runner calls Respond which goes to the channel;
		// we do coarse-grained logging here if needed (runner logs per-block).
	}
}

// getOrCreateRunner returns the persistent Runner for a chat.
func (d *Dispatcher) getOrCreateRunner(chatID int64) *Runner {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r, ok := d.runners[chatID]; ok {
		return r
	}
	r := NewRunner(d.sandbox, d.workingDir, chatID, d.model, d.getAPIKey, d.settings, d.registryMgr, d.searchCache)
	d.runners[chatID] = r
	return r
}

// -----------------------------------------------------------------------
// syntheticChannelContext is a minimal ChannelContext for event-triggered messages.
// It just logs responses without sending anything to a real channel.

type syntheticChannelContext struct {
	chatID       int64
	messageText  string
	messageTS    string
	senderName   string
	store        *Store
	responded    bool
	lastResponse string
}

func (c *syntheticChannelContext) ChatID() int64                     { return c.chatID }
func (c *syntheticChannelContext) MessageText() string               { return c.messageText }
func (c *syntheticChannelContext) MessageTS() string                 { return c.messageTS }
func (c *syntheticChannelContext) SenderName() string                { return c.senderName }
func (c *syntheticChannelContext) Images() []types.ImageContent      { return nil }
func (c *syntheticChannelContext) RespondInThread(text string) error { return c.Respond(text, true) }
func (c *syntheticChannelContext) SendCard(text string) (int, error) {
	_ = c.Respond(text, true)
	return 0, nil
}
func (c *syntheticChannelContext) EditCard(_ int, text string) error { return c.Respond(text, false) }
func (c *syntheticChannelContext) SetWorking(_ bool) error           { return nil }
func (c *syntheticChannelContext) UploadFile(_, _ string) error      { return nil }
func (c *syntheticChannelContext) DeleteMessage() error              { return nil }
func (c *syntheticChannelContext) ReplaceMessage(text string) error  { return c.Respond(text, false) }

func (c *syntheticChannelContext) Respond(text string, shouldLog bool) error {
	c.responded = true
	c.lastResponse = text
	if shouldLog {
		ts := fmt.Sprintf("bot-%d", time.Now().UnixMilli())
		_ = c.store.LogBotResponse(c.chatID, ts, text)
	}
	return nil
}
