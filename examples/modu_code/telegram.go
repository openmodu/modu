package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/channels/telegram"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

// chatEntry holds the CodingSession and a per-chat mutex that serializes
// concurrent incoming messages for the same chat.
type chatEntry struct {
	mu      sync.Mutex
	session *coding_agent.CodingSession
}

// tgManager manages one CodingSession per Telegram chat.
type tgManager struct {
	mu        sync.Mutex
	chats     map[int64]*chatEntry
	model     *types.Model
	getAPIKey func(string) (string, error)
	cwd       string
}

func newTGManager(model *types.Model, getAPIKey func(string) (string, error), cwd string) *tgManager {
	return &tgManager{
		chats:     make(map[int64]*chatEntry),
		model:     model,
		getAPIKey: getAPIKey,
		cwd:       cwd,
	}
}

func (m *tgManager) get(chatID int64) *chatEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ce, ok := m.chats[chatID]; ok {
		return ce
	}
	ce := &chatEntry{}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       m.cwd,
		Model:     m.model,
		GetAPIKey: m.getAPIKey,
	})
	if err != nil {
		fmt.Printf("[telegram] failed to create session for chat %d: %v\n", chatID, err)
	} else {
		ce.session = session
	}
	m.chats[chatID] = ce
	return ce
}

// runTelegram starts the bot in Telegram mode. Each chat gets an independent
// CodingSession. Messages for the same chat are serialized; different chats
// run concurrently.
func runTelegram(token, attachDir string, model *types.Model, getAPIKey func(string) (string, error), cwd string) error {
	mgr := newTGManager(model, getAPIKey, cwd)

	handler := func(ctx context.Context, chCtx channels.ChannelContext) {
		ce := mgr.get(chCtx.ChatID())
		if ce.session == nil {
			_ = chCtx.Respond("Session initialization failed.", true)
			return
		}

		// Serialize messages for this chat.
		ce.mu.Lock()
		defer ce.mu.Unlock()

		_ = chCtx.SetWorking(true)

		// Subscribe to agent events and forward each assistant text turn to Telegram.
		// Prompt() blocks until the full agent loop completes, but the subscriber
		// fires on every intermediate MessageEnd (between tool calls), giving the
		// user incremental updates.
		unsub := ce.session.Subscribe(func(ev agent.AgentEvent) {
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

		if err := ce.session.Prompt(ctx, chCtx.MessageText()); err != nil && ctx.Err() == nil {
			_ = chCtx.Respond(fmt.Sprintf("Error: %v", err), true)
		}
	}

	abort := func(chatID int64) {
		mgr.mu.Lock()
		ce, ok := mgr.chats[chatID]
		mgr.mu.Unlock()
		if ok && ce.session != nil {
			ce.session.Abort()
		}
	}

	bot, err := telegram.NewBot(token, attachDir, handler, abort)
	if err != nil {
		return fmt.Errorf("create bot: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n[telegram] shutting down…")
		cancel()
	}()

	fmt.Printf("[telegram] bot started  cwd=%s  model=%s\n", cwd, model.Name)
	return bot.Run(ctx)
}
