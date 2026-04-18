// Package provider adapts an ACP (Agent Client Protocol) agent to modu's
// providers.Provider interface, so Claude Code / Codex / Gemini CLIs can
// be called from anywhere a modu provider is expected.
//
// Lifecycle:
//
//   - The first Stream / Chat call performs the ACP `initialize` handshake
//     and `session/new`. Both happen lazily and are cached for the lifetime
//     of the Provider.
//   - Each subsequent call reuses the same sessionId — ACP agents keep
//     conversation state per-session, so reusing it gives multi-turn
//     memory for free.
//   - ctx.Done() during a Stream sends `session/cancel` and resolves the
//     stream with ctx.Err().
//
// Concurrency: Stream is safe to call once at a time per Provider (ACP
// sessions are single-threaded). Concurrent callers should serialise at
// a higher layer (pkg/acp/manager, M6).
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/acp/bridge"
	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/acp/jsonrpc"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

// Options configures a Provider.
type Options struct {
	// ID is the provider identifier surfaced via providers.Register.
	// e.g. "acp:claude", "acp:codex".
	ID string

	// Client is a *client.Client whose transport has NOT been Started yet.
	// The Provider calls Start on first use.
	Client *client.Client

	// Cwd is the working directory the ACP session should attach to.
	// ACP agents treat cwd as part of session identity — one Provider
	// instance should correspond to one (agent, cwd) pair.
	Cwd string

	// Name is a human-readable label sent in the ACP `initialize`
	// handshake. Falls back to ID if empty.
	Name string

	// Version advertised during `initialize`. Defaults to "0.1.0".
	Version string
}

// Provider is an ACP-backed providers.Provider.
type Provider struct {
	id      string
	client  *client.Client
	cwd     string
	name    string
	version string

	mu          sync.Mutex
	started     bool
	initialized bool
	sessionID   string
}

// New builds a Provider. Start happens lazily on first Stream/Chat.
func New(opts Options) *Provider {
	if opts.Version == "" {
		opts.Version = "0.1.0"
	}
	if opts.Name == "" {
		opts.Name = opts.ID
	}
	return &Provider{
		id:      opts.ID,
		client:  opts.Client,
		cwd:     opts.Cwd,
		name:    opts.Name,
		version: opts.Version,
	}
}

// ID implements providers.Provider.
func (p *Provider) ID() string { return p.id }

// Stream runs one ACP turn and returns a modu EventStream that pipes
// translated events until the turn completes or ctx cancels.
func (p *Provider) Stream(ctx context.Context, req *providers.ChatRequest) (types.EventStream, error) {
	if err := p.ensureReady(); err != nil {
		return nil, err
	}

	prompt := lastUserText(req)
	if prompt == "" {
		return nil, errors.New("acp/provider: empty prompt (no user message)")
	}

	es := types.NewEventStream()
	partial := &types.AssistantMessage{
		Role:       "assistant",
		Content:    []types.ContentBlock{},
		ProviderID: p.id,
	}
	// Push(EventStart) happens inside runTurn — the stream's channel is
	// unbuffered, so emitting it here would block until the caller starts
	// reading Events().
	go p.runTurn(ctx, prompt, es, partial)
	return es, nil
}

// Chat is the non-streaming variant — it drains Stream to completion and
// returns the final assistant message.
func (p *Provider) Chat(ctx context.Context, req *providers.ChatRequest) (*providers.ChatResponse, error) {
	es, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	// Drain events; we only care about the resolved result.
	for range es.Events() {
	}
	msg, err := es.Result()
	if err != nil {
		return nil, err
	}
	return &providers.ChatResponse{
		ID:           "",
		Model:        req.Model,
		Message:      providers.Message{Role: providers.RoleAssistant, Content: assistantText(msg)},
		FinishReason: string(msg.StopReason),
	}, nil
}

// runTurn drives one session/prompt exchange. It subscribes to the client
// for the duration of the turn, translates session/update notifications
// via bridge.Translate, pushes StreamEvents to es, and resolves es once
// session/prompt returns (or ctx cancels).
func (p *Provider) runTurn(ctx context.Context, prompt string, es *types.EventStreamImpl, partial *types.AssistantMessage) {
	defer es.Close()

	es.Push(types.StreamEvent{Type: types.EventStart, Partial: partial})

	// All notifications funnel through an internal channel. The callback
	// never touches es directly — if it did, it could race with es.Close()
	// when the turn finishes, because client fanout spawns a goroutine per
	// delivery and those may still be in flight after unsub().
	updates := make(chan *jsonrpc.Message, 64)
	unsub := p.client.OnNotification(func(msg *jsonrpc.Message) {
		if msg.Method != "session/update" {
			return
		}
		// Filter by sessionId so two Providers sharing one client don't
		// cross-talk. In practice the manager pairs each client with one
		// Provider, so this is belt-and-suspenders.
		if sid := extractSessionID(msg); sid != "" && sid != p.sessionID {
			return
		}
		select {
		case updates <- msg:
		default:
			// Channel full → drop. 64 is generous for a single turn.
		}
	})

	respCh := make(chan *jsonrpc.Message, 1)
	errCh := make(chan error, 1)
	go func() {
		msg, err := p.client.Request("session/prompt", map[string]any{
			"sessionId": p.sessionID,
			"prompt": []map[string]any{
				{"type": "text", "text": prompt},
			},
		})
		if err != nil {
			errCh <- err
			return
		}
		respCh <- msg
	}()

	var textBuf strings.Builder
	flush := func(msg *jsonrpc.Message) {
		events, err := bridge.Translate(msg)
		if err != nil {
			return
		}
		for _, ev := range events {
			if ev.StreamEvent == nil {
				continue
			}
			if ev.StreamEvent.Type == types.EventTextDelta {
				textBuf.WriteString(ev.StreamEvent.Delta)
			}
			es.Push(*ev.StreamEvent)
		}
	}

	var finalErr error
	var stopReason string

loop:
	for {
		select {
		case u := <-updates:
			flush(u)
		case <-ctx.Done():
			_ = p.cancelSession()
			finalErr = ctx.Err()
			break loop
		case err := <-errCh:
			finalErr = err
			break loop
		case resp := <-respCh:
			var result struct {
				StopReason string `json:"stopReason"`
			}
			_ = resp.ParseResult(&result)
			stopReason = result.StopReason
			break loop
		}
	}

	// Stop receiving new notifications, then drain anything already buffered
	// so trailing text chunks (those that arrived between the final update
	// and session/prompt's response) are not lost.
	unsub()
	for draining := true; draining; {
		select {
		case u := <-updates:
			flush(u)
		default:
			draining = false
		}
	}

	partial.Content = []types.ContentBlock{
		&types.TextContent{Type: "text", Text: textBuf.String()},
	}
	if stopReason != "" {
		partial.StopReason = types.StopReason(stopReason)
	}
	es.Push(types.StreamEvent{
		Type:    types.EventDone,
		Reason:  partial.StopReason,
		Message: partial,
	})
	es.Resolve(partial, finalErr)
}

// ensureReady Starts the underlying client, performs ACP initialize, and
// creates a session — all gated behind a single mutex and cached for the
// lifetime of this Provider.
func (p *Provider) ensureReady() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		if err := p.client.Start(); err != nil {
			return fmt.Errorf("acp/provider: start client: %w", err)
		}
		p.started = true
	}
	if !p.initialized {
		if err := p.initialize(); err != nil {
			return err
		}
		p.initialized = true
	}
	if p.sessionID == "" {
		id, err := p.newSession()
		if err != nil {
			return err
		}
		p.sessionID = id
	}
	return nil
}

// lastUserText extracts the last user message as a plain string.
func lastUserText(req *providers.ChatRequest) string {
	if req == nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != providers.RoleUser {
			continue
		}
		switch c := m.Content.(type) {
		case string:
			return c
		case []any:
			// Multimodal — concatenate text parts.
			var parts []string
			for _, item := range c {
				if obj, ok := item.(map[string]any); ok {
					if t, _ := obj["type"].(string); t == "text" {
						if s, _ := obj["text"].(string); s != "" {
							parts = append(parts, s)
						}
					}
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func assistantText(msg *types.AssistantMessage) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range msg.Content {
		if tc, ok := b.(*types.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// extractSessionID pulls params.sessionId from a session/update notification.
func extractSessionID(msg *jsonrpc.Message) string {
	if len(msg.Params) == 0 {
		return ""
	}
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return ""
	}
	return p.SessionID
}

// Ensure Provider satisfies the providers.Provider contract.
var _ providers.Provider = (*Provider)(nil)
