// Package client implements a JSON-RPC 2.0 client for ACP agents.
//
// It sits on top of pkg/acp/process (for stdio framing) and pkg/acp/jsonrpc
// (for wire types). A Client owns one agent subprocess over its lifetime:
//
//   - Request(method, params) sends a request and blocks until a correlated
//     response arrives (or the transport closes).
//   - Notify(method, params) sends a fire-and-forget notification.
//   - OnNotification(fn) subscribes to inbound notifications; handlers run
//     on their own goroutines so a slow subscriber cannot stall the reader.
//   - Inbound *requests* from the agent (reverse RPC: permission prompts,
//     fs/read_text_file, fs/write_text_file) are dispatched to the handlers
//     supplied in Config. Unhandled methods receive MethodNotFound.
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/openmodu/modu/pkg/acp/jsonrpc"
)

// Transport is the subset of pkg/acp/process.Process that Client needs.
// Extracting it lets tests drive the client with an in-memory fake instead
// of spawning a real subprocess.
type Transport interface {
	Start() error
	Stop() error
	Write(line []byte) error
	Lines() <-chan []byte
	Done() <-chan struct{}
}

// PermissionRequest mirrors the ACP session/request_permission payload.
type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallSummary    `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// ToolCallSummary describes the tool invocation that needs permission.
type ToolCallSummary struct {
	ToolCallID string         `json:"toolCallId"`
	Title      string         `json:"title"`
	Kind       string         `json:"kind"`
	RawInput   map[string]any `json:"rawInput,omitempty"`
}

// PermissionOption is one button the user can click in the permission UI.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// PermissionHandler decides how to answer a permission prompt. It must
// return the OptionID of the chosen option. By convention an OptionID
// starting with "reject" is surfaced to the agent as outcome="rejected";
// anything else maps to outcome="selected".
type PermissionHandler func(req *PermissionRequest) string

// FSHandler services reverse-RPC file read/write requests from the agent.
// If Config.FS is nil, both methods are rejected with InternalError.
type FSHandler interface {
	ReadTextFile(path string) (string, error)
	WriteTextFile(path, content string) error
}

// Config configures a Client.
type Config struct {
	Transport    Transport
	OnPermission PermissionHandler
	FS           FSHandler
}

// Client is a JSON-RPC client over a single ACP agent transport.
type Client struct {
	tx           Transport
	onPermission PermissionHandler
	fs           FSHandler

	mu      sync.Mutex
	nextID  int
	pending map[int]chan *jsonrpc.Message
	subs    map[int]func(*jsonrpc.Message)
	nextSub int
	started bool
	stopped bool
}

// New builds a Client. Call Start before Request / Notify.
func New(cfg Config) *Client {
	return &Client{
		tx:           cfg.Transport,
		onPermission: cfg.OnPermission,
		fs:           cfg.FS,
		pending:      make(map[int]chan *jsonrpc.Message),
		subs:         make(map[int]func(*jsonrpc.Message)),
	}
}

// Start boots the transport and spins up the read loop. Idempotent.
func (c *Client) Start() error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.mu.Unlock()

	if err := c.tx.Start(); err != nil {
		return err
	}
	go c.readLoop()
	return nil
}

// Stop tears down the transport. Any in-flight Request calls unblock with
// an error once the transport's Lines channel closes. Idempotent.
func (c *Client) Stop() error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true
	c.mu.Unlock()
	return c.tx.Stop()
}

// Request sends a JSON-RPC request and waits for the correlated response.
func (c *Client) Request(method string, params any) (*jsonrpc.Message, error) {
	c.mu.Lock()
	if !c.started || c.stopped {
		c.mu.Unlock()
		return nil, errors.New("acp/client: not running")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan *jsonrpc.Message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req := jsonrpc.NewRequest(id, method, params)
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("acp/client: marshal request: %w", err)
	}
	if err := c.tx.Write(b); err != nil {
		return nil, fmt.Errorf("acp/client: write request: %w", err)
	}

	msg, ok := <-ch
	if !ok {
		return nil, errors.New("acp/client: connection closed before response")
	}
	if msg.Error != nil {
		return msg, msg.Error
	}
	return msg, nil
}

// Notify sends a one-way notification. No response is expected.
func (c *Client) Notify(method string, params any) error {
	c.mu.Lock()
	if !c.started || c.stopped {
		c.mu.Unlock()
		return errors.New("acp/client: not running")
	}
	c.mu.Unlock()

	n := jsonrpc.NewNotification(method, params)
	b, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("acp/client: marshal notification: %w", err)
	}
	return c.tx.Write(b)
}

// OnNotification subscribes fn to inbound notifications. The returned
// function removes the subscription. Handlers run synchronously on the
// read goroutine — keep them fast (channel send, counter bump) or spawn
// your own goroutine. Synchronous dispatch preserves the wire ordering
// between notifications and the subsequent response, which matters when
// a caller interleaves session/update + session/prompt responses.
func (c *Client) OnNotification(fn func(*jsonrpc.Message)) func() {
	c.mu.Lock()
	c.nextSub++
	id := c.nextSub
	c.subs[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}

func (c *Client) readLoop() {
	for line := range c.tx.Lines() {
		var msg jsonrpc.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			// Tolerate stray non-JSON output from the agent.
			continue
		}
		switch {
		case msg.IsResponse():
			c.dispatchResponse(&msg)
		case msg.IsRequest():
			// Reverse RPC may touch the filesystem or block waiting on a
			// user — do not block the read loop on it.
			go c.handleReverse(&msg)
		case msg.IsNotification():
			c.fanoutNotification(&msg)
		}
	}

	// Transport closed. Release any pending request waiters.
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int]chan *jsonrpc.Message)
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
}

func (c *Client) dispatchResponse(msg *jsonrpc.Message) {
	if msg.ID == nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[*msg.ID]
	if ok {
		delete(c.pending, *msg.ID)
	}
	c.mu.Unlock()
	if ok {
		ch <- msg
	}
}

func (c *Client) fanoutNotification(msg *jsonrpc.Message) {
	c.mu.Lock()
	subs := make([]func(*jsonrpc.Message), 0, len(c.subs))
	for _, fn := range c.subs {
		subs = append(subs, fn)
	}
	c.mu.Unlock()
	for _, fn := range subs {
		fn(msg)
	}
}

func (c *Client) sendResponse(id int, result any) {
	resp := jsonrpc.NewResponse(id, result)
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = c.tx.Write(b)
}

func (c *Client) sendError(id int, code int, message string) {
	resp := jsonrpc.NewErrorResponse(id, code, message)
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = c.tx.Write(b)
}
