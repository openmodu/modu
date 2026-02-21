package rpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RpcClient provides a typed Go client for the RPC protocol.
type RpcClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	stderr  *bytes.Buffer
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

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	c := &RpcClient{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewScanner(stdout),
		stderr:  &bytes.Buffer{},
		pending: make(map[string]chan *RpcResponse),
		done:    make(chan struct{}),
	}

	c.stdout.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Collect stderr in background
	go func() {
		io.Copy(c.stderr, stderrPipe)
	}()

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

// GetStderr returns the collected stderr output.
func (c *RpcClient) GetStderr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stderr.String()
}

// Send sends an RPC command and waits for the response with a 30s default timeout.
func (c *RpcClient) Send(command RpcCommandType, data any) (*RpcResponse, error) {
	return c.SendWithTimeout(command, data, 30*time.Second)
}

// SendWithTimeout sends an RPC command and waits for the response with the specified timeout.
func (c *RpcClient) SendWithTimeout(command RpcCommandType, data any, timeout time.Duration) (*RpcResponse, error) {
	c.mu.Lock()
	id := strconv.Itoa(c.nextID)
	c.nextID++
	ch := make(chan *RpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	cmd := RpcCommand{
		ID:   id,
		Type: command,
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

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-ch:
		return resp, nil
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		stderrContent := c.GetStderr()
		errMsg := fmt.Sprintf("timeout waiting for response to %s (id=%s)", command, id)
		if stderrContent != "" {
			errMsg += "\nstderr: " + stderrContent
		}
		return nil, fmt.Errorf("%s", errMsg)
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}
}

// --- Typed convenience methods ---

// Prompt sends a prompt command.
func (c *RpcClient) Prompt(message string) error {
	_, err := c.Send(RpcCmdPrompt, PromptData{Message: message})
	return err
}

// Steer sends a steer command.
func (c *RpcClient) Steer(message string) error {
	_, err := c.Send(RpcCmdSteer, PromptData{Message: message})
	return err
}

// FollowUp sends a follow_up command.
func (c *RpcClient) FollowUp(message string) error {
	_, err := c.Send(RpcCmdFollowUp, PromptData{Message: message})
	return err
}

// Abort sends an abort command.
func (c *RpcClient) Abort() error {
	_, err := c.Send(RpcCmdAbort, nil)
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

// SetModel sends a set_model command.
func (c *RpcClient) SetModel(provider, modelID string) error {
	resp, err := c.Send(RpcCmdSetModel, SetModelData{Provider: provider, ModelID: modelID})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("set_model failed: %s", resp.Error)
	}
	return nil
}

// CycleModel sends a cycle_model command.
func (c *RpcClient) CycleModel() (*RpcResponse, error) {
	return c.Send(RpcCmdCycleModel, nil)
}

// SetThinkingLevel sends a set_thinking_level command.
func (c *RpcClient) SetThinkingLevel(level string) error {
	resp, err := c.Send(RpcCmdSetThinkingLevel, SetThinkingLevelData{Level: level})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("set_thinking_level failed: %s", resp.Error)
	}
	return nil
}

// CycleThinkingLevel sends a cycle_thinking_level command.
func (c *RpcClient) CycleThinkingLevel() (*RpcResponse, error) {
	return c.Send(RpcCmdCycleThinking, nil)
}

// Compact sends a compact command.
func (c *RpcClient) Compact() error {
	resp, err := c.Send(RpcCmdCompact, nil)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("compact failed: %s", resp.Error)
	}
	return nil
}

// SetAutoCompaction sends a set_auto_compaction command.
func (c *RpcClient) SetAutoCompaction(enabled bool) error {
	_, err := c.Send(RpcCmdSetAutoCompaction, SetBoolData{Enabled: enabled})
	return err
}

// SetAutoRetry sends a set_auto_retry command.
func (c *RpcClient) SetAutoRetry(enabled bool) error {
	_, err := c.Send(RpcCmdSetAutoRetry, SetBoolData{Enabled: enabled})
	return err
}

// AbortRetry sends an abort_retry command.
func (c *RpcClient) AbortRetry() error {
	_, err := c.Send(RpcCmdAbortRetry, nil)
	return err
}

// GetMessages sends a get_messages command.
func (c *RpcClient) GetMessages() (*RpcResponse, error) {
	return c.Send(RpcCmdGetMessages, nil)
}

// NewSession sends a new_session command.
func (c *RpcClient) NewSession() error {
	_, err := c.Send(RpcCmdNewSession, nil)
	return err
}

// GetCommands sends a get_commands command.
func (c *RpcClient) GetCommands() (*RpcResponse, error) {
	return c.Send(RpcCmdGetCommands, nil)
}

// GetAvailableModels sends a get_available_models command.
func (c *RpcClient) GetAvailableModels() (*RpcResponse, error) {
	return c.Send(RpcCmdGetAvailableModels, nil)
}

// SetSteeringMode sends a set_steering_mode command.
func (c *RpcClient) SetSteeringMode(mode string) error {
	_, err := c.Send(RpcCmdSetSteeringMode, SetModeData{Mode: mode})
	return err
}

// SetFollowUpMode sends a set_follow_up_mode command.
func (c *RpcClient) SetFollowUpMode(mode string) error {
	_, err := c.Send(RpcCmdSetFollowUpMode, SetModeData{Mode: mode})
	return err
}

// Bash sends a bash command.
func (c *RpcClient) Bash(command string, timeoutMs int) (*RpcResponse, error) {
	return c.Send(RpcCmdBash, BashData{Command: command, TimeoutMs: timeoutMs})
}

// AbortBash sends an abort_bash command.
func (c *RpcClient) AbortBash() error {
	_, err := c.Send(RpcCmdAbortBash, nil)
	return err
}

// GetSessionStats sends a get_session_stats command.
func (c *RpcClient) GetSessionStats() (*RpcResponse, error) {
	return c.Send(RpcCmdGetSessionStats, nil)
}

// ExportHTML sends an export_html command.
func (c *RpcClient) ExportHTML(path string) error {
	resp, err := c.Send(RpcCmdExportHTML, ExportHTMLData{Path: path})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("export_html failed: %s", resp.Error)
	}
	return nil
}

// SwitchSession sends a switch_session command.
func (c *RpcClient) SwitchSession(sessionFile string) error {
	resp, err := c.Send(RpcCmdSwitchSession, SwitchSessionData{SessionFile: sessionFile})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("switch_session failed: %s", resp.Error)
	}
	return nil
}

// Fork sends a fork command.
func (c *RpcClient) Fork(entryID string) error {
	resp, err := c.Send(RpcCmdFork, ForkData{EntryID: entryID})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("fork failed: %s", resp.Error)
	}
	return nil
}

// GetForkMessages sends a get_fork_messages command.
func (c *RpcClient) GetForkMessages() (*RpcResponse, error) {
	return c.Send(RpcCmdGetForkMessages, nil)
}

// GetLastAssistantText sends a get_last_assistant_text command.
func (c *RpcClient) GetLastAssistantText() (string, error) {
	resp, err := c.Send(RpcCmdGetLastAssistantText, nil)
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("get_last_assistant_text failed: %s", resp.Error)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		return "", err
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	return result.Text, nil
}

// SetSessionName sends a set_session_name command.
func (c *RpcClient) SetSessionName(name string) error {
	_, err := c.Send(RpcCmdSetSessionName, SetSessionNameData{Name: name})
	return err
}

// --- Event handling ---

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

// WaitForIdle waits until an agent_end event is received, or falls back to polling.
func (c *RpcClient) WaitForIdle(timeout time.Duration) error {
	done := make(chan struct{}, 1)

	unsub := c.OnEvent(func(raw json.RawMessage) {
		var evt struct {
			Type string `json:"type"`
			Data struct {
				EventType string `json:"eventType"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &evt); err == nil {
			if evt.Type == "agent_event" && evt.Data.EventType == "agent_end" {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		}
	})
	defer unsub()

	// Also check if already idle
	state, err := c.GetState()
	if err == nil && !state.IsStreaming {
		return nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("timeout waiting for idle")
	case <-c.done:
		return fmt.Errorf("connection closed")
	}
}

// CollectEvents collects all events received within the given timeout duration.
func (c *RpcClient) CollectEvents(timeout time.Duration) ([]json.RawMessage, error) {
	var collected []json.RawMessage
	var mu sync.Mutex

	unsub := c.OnEvent(func(raw json.RawMessage) {
		mu.Lock()
		collected = append(collected, raw)
		mu.Unlock()
	})
	defer unsub()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}

	mu.Lock()
	defer mu.Unlock()
	return collected, nil
}

// PromptAndWait sends a prompt and waits for the agent to become idle,
// collecting all events emitted during processing.
func (c *RpcClient) PromptAndWait(message string, timeout time.Duration) ([]json.RawMessage, error) {
	var collected []json.RawMessage
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	unsub := c.OnEvent(func(raw json.RawMessage) {
		mu.Lock()
		collected = append(collected, raw)
		mu.Unlock()

		var evt struct {
			Type string `json:"type"`
			Data struct {
				EventType string `json:"eventType"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &evt); err == nil {
			if evt.Type == "agent_event" && evt.Data.EventType == "agent_end" {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		}
	})
	defer unsub()

	if err := c.Prompt(message); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for prompt completion")
	case <-c.done:
		return nil, fmt.Errorf("connection closed")
	}

	mu.Lock()
	defer mu.Unlock()
	return collected, nil
}

// --- Lifecycle ---

// Close stops the subprocess with graceful shutdown (SIGTERM -> SIGKILL).
func (c *RpcClient) Close() error {
	// Close stdin to signal EOF
	c.stdin.Close()

	// Wait up to 5s for natural exit
	exited := make(chan error, 1)
	go func() {
		exited <- c.cmd.Wait()
	}()

	timer1 := time.NewTimer(5 * time.Second)
	defer timer1.Stop()

	select {
	case err := <-exited:
		return err
	case <-timer1.C:
	}

	// Send SIGTERM
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
	}

	timer2 := time.NewTimer(3 * time.Second)
	defer timer2.Stop()

	select {
	case err := <-exited:
		return err
	case <-timer2.C:
	}

	// Force kill
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}

	return <-exited
}

// String implements a simple string representation for debugging.
func (c *RpcClient) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("RpcClient{pending=%d, stderr=%s}", len(c.pending), strings.TrimSpace(c.stderr.String()))
}
