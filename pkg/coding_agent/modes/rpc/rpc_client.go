package rpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// RpcClient provides a typed Go client for the RPC protocol.
type RpcClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	pending map[string]chan *RpcResponse
	events  []func(json.RawMessage)
	mu      sync.Mutex
	nextID  int
	done    chan struct{}
}

// NewRpcClient starts a subprocess and connects via stdin/stdout JSON-line protocol.
func NewRpcClient(binPath string, args ...string) (*RpcClient, error) {
	cmd := exec.Command(binPath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	c := &RpcClient{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewScanner(stdout),
		pending: make(map[string]chan *RpcResponse),
		done:    make(chan struct{}),
	}

	c.stdout.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Start reading responses in background
	go c.readLoop()

	return c, nil
}

func (c *RpcClient) readLoop() {
	defer close(c.done)
	for c.stdout.Scan() {
		line := c.stdout.Bytes()
		if len(line) == 0 {
			continue
		}

		// Try to parse as response
		var resp RpcResponse
		if err := json.Unmarshal(line, &resp); err == nil && resp.Type == "response" {
			c.mu.Lock()
			if ch, ok := c.pending[resp.ID]; ok {
				ch <- &resp
				delete(c.pending, resp.ID)
			}
			c.mu.Unlock()
			continue
		}

		// Otherwise treat as event
		c.mu.Lock()
		for _, fn := range c.events {
			raw := make(json.RawMessage, len(line))
			copy(raw, line)
			fn(raw)
		}
		c.mu.Unlock()
	}
}

// Send sends an RPC command and waits for the response.
func (c *RpcClient) Send(command RpcCommandType, data any) (*RpcResponse, error) {
	c.mu.Lock()
	id := strconv.Itoa(c.nextID)
	c.nextID++
	ch := make(chan *RpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	cmd := RpcCommand{
		ID:      id,
		Command: command,
	}

	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, fmt.Errorf("failed to marshal data: %w", err)
		}
		cmd.Data = raw
	}

	line, err := json.Marshal(cmd)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("failed to marshal command: %w", err)
	}

	if _, err := fmt.Fprintln(c.stdin, string(line)); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("failed to write command: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}
}

// Prompt sends a prompt command.
func (c *RpcClient) Prompt(message string) error {
	_, err := c.Send(RpcCmdPrompt, PromptData{Message: message})
	return err
}

// GetState retrieves the current session state.
func (c *RpcClient) GetState() (*RpcSessionState, error) {
	resp, err := c.Send(RpcCmdGetState, nil)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("get_state failed: %s", resp.Error)
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, err
	}
	var state RpcSessionState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// OnEvent registers an event handler. Returns an unsubscribe function.
func (c *RpcClient) OnEvent(fn func(json.RawMessage)) func() {
	c.mu.Lock()
	idx := len(c.events)
	c.events = append(c.events, fn)
	c.mu.Unlock()

	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if idx < len(c.events) {
			c.events = append(c.events[:idx], c.events[idx+1:]...)
		}
	}
}

// WaitForIdle waits until the session reports non-streaming state, or until timeout.
func (c *RpcClient) WaitForIdle(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := c.GetState()
		if err != nil {
			return err
		}
		if !state.IsStreaming {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for idle")
}

// Close stops the subprocess and cleans up.
func (c *RpcClient) Close() error {
	c.stdin.Close()
	return c.cmd.Wait()
}
