package provider

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/acp/jsonrpc"
)

// mockAgent wires a client.Transport to a scripted ACP responder. It lets
// tests assert exactly which inbound methods the provider produced and
// stage outbound notifications / responses without spawning a subprocess.
//
// Wire picture:
//
//	Client --(Write)--> mockAgent.readLoop --(dispatches)-->
//	    - initialize/session_new handlers (synchronous)
//	    - session/prompt: pushes the scripted updates, then replies
//	    - every inbound is captured in history for assertions
type mockAgent struct {
	tx *mockTransport

	mu              sync.Mutex
	history         []*jsonrpc.Message
	sessionCounter  atomic.Int32
	initializeError string // non-empty → initialize returns a JSON-RPC error
	promptScript    []sessionUpdate
	holdPrompt      bool // when true, session/prompt never replies (for cancel tests)
	cancelled       atomic.Bool
}

type sessionUpdate struct {
	// Simple helpers to build session/update notifications.
	text       string // agent_message_chunk with this text
	stopReason string // if non-empty, sent as session/prompt response
}

// mockTransport implements client.Transport with two channels, mirroring
// the one in pkg/acp/client/client_test.go but local to this package.
type mockTransport struct {
	lines   chan []byte
	done    chan struct{}
	writes  chan []byte
	mu      sync.Mutex
	stopped bool
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		lines:  make(chan []byte, 64),
		done:   make(chan struct{}),
		writes: make(chan []byte, 64),
	}
}

func (m *mockTransport) Start() error { return nil }
func (m *mockTransport) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil
	}
	m.stopped = true
	close(m.lines)
	close(m.done)
	return nil
}

func (m *mockTransport) Write(line []byte) error {
	cp := make([]byte, len(line))
	copy(cp, line)
	m.writes <- cp
	return nil
}
func (m *mockTransport) Lines() <-chan []byte  { return m.lines }
func (m *mockTransport) Done() <-chan struct{} { return m.done }

func (m *mockTransport) sendLine(b []byte) {
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return
	}
	m.lines <- b
}

// newMockAgent creates a transport+agent pair wired up. Call agent.run()
// in a goroutine (or it runs implicitly when the provider starts).
func newMockAgent() *mockAgent {
	return &mockAgent{
		tx: newMockTransport(),
	}
}

// run starts the agent loop. It processes every frame the client writes
// and dispatches per method name.
func (a *mockAgent) run() {
	go a.readLoop()
}

func (a *mockAgent) readLoop() {
	for raw := range a.tx.writes {
		var msg jsonrpc.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		a.mu.Lock()
		a.history = append(a.history, &msg)
		a.mu.Unlock()

		switch {
		case msg.IsRequest():
			a.handleRequest(&msg)
		case msg.IsNotification():
			// session/cancel is the only notification the provider sends.
			if msg.Method == "session/cancel" {
				a.cancelled.Store(true)
			}
		}
	}
}

func (a *mockAgent) handleRequest(msg *jsonrpc.Message) {
	if msg.ID == nil {
		return
	}
	id := *msg.ID
	switch msg.Method {
	case "initialize":
		if a.initializeError != "" {
			a.reply(jsonrpc.NewErrorResponse(id, jsonrpc.InternalError, a.initializeError))
			return
		}
		a.reply(jsonrpc.NewResponse(id, map[string]any{
			"protocolVersion": 1,
			"capabilities":    map[string]any{},
		}))
	case "session/new":
		n := a.sessionCounter.Add(1)
		a.reply(jsonrpc.NewResponse(id, map[string]any{
			"sessionId": fmt.Sprintf("sess-%d", n),
		}))
	case "session/prompt":
		// Pull the sessionId from params so updates can carry it.
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = msg.ParseParams(&p)
		a.mu.Lock()
		script := a.promptScript
		hold := a.holdPrompt
		a.mu.Unlock()

		stopReason := "end_turn"
		for _, u := range script {
			if u.stopReason != "" {
				stopReason = u.stopReason
				continue
			}
			if u.text != "" {
				a.pushUpdate(p.SessionID, u.text)
			}
		}
		if hold {
			return
		}
		a.reply(jsonrpc.NewResponse(id, map[string]any{
			"stopReason": stopReason,
		}))
	default:
		a.reply(jsonrpc.NewErrorResponse(id, jsonrpc.MethodNotFound, "not implemented in mock"))
	}
}

func (a *mockAgent) pushUpdate(sessionID, text string) {
	n := jsonrpc.NewNotification("session/update", map[string]any{
		"sessionId": sessionID,
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{
				"type": "text",
				"text": text,
			},
		},
	})
	b, _ := json.Marshal(n)
	a.tx.sendLine(b)
}

func (a *mockAgent) reply(resp *jsonrpc.Response) {
	b, _ := json.Marshal(resp)
	a.tx.sendLine(b)
}

// methodCalls returns the list of inbound method names from history (in
// arrival order), useful for asserting "agent saw X then Y".
func (a *mockAgent) methodCalls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.history))
	for _, m := range a.history {
		out = append(out, m.Method)
	}
	return out
}

// countCalls returns how many times the agent received a request/notification
// with the given method name.
func (a *mockAgent) countCalls(method string) int {
	n := 0
	for _, m := range a.methodCalls() {
		if m == method {
			n++
		}
	}
	return n
}

// buildProvider is a test helper that assembles agent → client → provider.
func buildProvider(a *mockAgent, cwd string) *Provider {
	c := client.New(client.Config{Transport: a.tx})
	return New(Options{
		ID:     "acp:test",
		Client: c,
		Cwd:    cwd,
		Name:   "test-client",
	})
}
