package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/crosszan/modu/pkg/agent"
	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
)

// RpcMode implements a JSON-line based RPC protocol over stdin/stdout.
type RpcMode struct {
	session *coding_agent.CodingSession
	input   io.Reader
	output  io.Writer
	mu      sync.Mutex
}

// NewRpcMode creates a new RPC mode handler.
func NewRpcMode(session *coding_agent.CodingSession) *RpcMode {
	return &RpcMode{
		session: session,
		input:   os.Stdin,
		output:  os.Stdout,
	}
}

// SetIO sets custom input/output for testing.
func (r *RpcMode) SetIO(input io.Reader, output io.Writer) {
	r.input = input
	r.output = output
}

// Run starts the RPC mode main loop.
func (r *RpcMode) Run(ctx context.Context) error {
	// Subscribe to agent events and forward as JSON lines
	unsubAgent := r.session.Subscribe(func(event agent.AgentEvent) {
		r.writeEvent("agent_event", map[string]any{
			"eventType": string(event.Type),
			"toolName":  event.ToolName,
			"isError":   event.IsError,
		})
	})
	defer unsubAgent()

	// Subscribe to session events
	unsubSession := r.session.SubscribeSession(func(event coding_agent.SessionEvent) {
		r.writeEvent("session_event", event)
	})
	defer unsubSession()

	// Read commands from stdin
	scanner := bufio.NewScanner(r.input)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var cmd RpcCommand
		if err := json.Unmarshal(line, &cmd); err != nil {
			r.writeResponse(RpcResponse{
				Type:    "response",
				Command: "",
				Error:   fmt.Sprintf("invalid JSON: %v", err),
			})
			continue
		}

		resp := r.handleCommand(ctx, cmd)
		r.writeResponse(resp)
	}

	return scanner.Err()
}

func (r *RpcMode) handleCommand(ctx context.Context, cmd RpcCommand) RpcResponse {
	resp := RpcResponse{
		ID:      cmd.ID,
		Type:    "response",
		Command: cmd.Command,
	}

	switch cmd.Command {
	case RpcCmdPrompt:
		var data PromptData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid prompt data: %v", err)
			return resp
		}
		// Run prompt in background
		go func() {
			if err := r.session.Prompt(ctx, data.Message); err != nil {
				r.writeEvent("prompt_error", map[string]string{"error": err.Error()})
			}
		}()
		resp.Success = true

	case RpcCmdSteer:
		var data PromptData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid steer data: %v", err)
			return resp
		}
		r.session.Steer(data.Message)
		resp.Success = true

	case RpcCmdFollowUp:
		var data PromptData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid follow_up data: %v", err)
			return resp
		}
		r.session.FollowUp(data.Message)
		resp.Success = true

	case RpcCmdAbort:
		r.session.Abort()
		resp.Success = true

	case RpcCmdGetState:
		state := r.session.GetAgent().GetState()
		model := r.session.GetModel()
		rpcState := RpcSessionState{
			Model:          model.ID,
			Provider:       string(model.Provider),
			ThinkingLevel:  string(r.session.GetThinkingLevel()),
			IsStreaming:     state.IsStreaming,
			SessionID:      r.session.GetSessionID(),
			AutoCompaction: r.session.GetConfig().AutoCompaction,
			AutoRetry:      r.session.GetConfig().AutoRetry,
			MessageCount:   len(state.Messages),
		}
		resp.Success = true
		resp.Data = rpcState

	case RpcCmdSetModel:
		var data SetModelData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid set_model data: %v", err)
			return resp
		}
		if err := r.session.SetModelByID(data.Provider, data.ModelID); err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Success = true

	case RpcCmdCycleModel:
		model := r.session.CycleModel()
		if model != nil {
			resp.Data = map[string]string{"model": model.ID, "provider": string(model.Provider)}
		}
		resp.Success = true

	case RpcCmdSetThinkingLevel:
		var data SetThinkingLevelData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid thinking level data: %v", err)
			return resp
		}
		r.session.SetThinkingLevel(agent.ThinkingLevel(data.Level))
		resp.Success = true

	case RpcCmdCycleThinking:
		level := r.session.CycleThinkingLevel()
		resp.Success = true
		resp.Data = map[string]string{"level": string(level)}

	case RpcCmdCompact:
		if err := r.session.Compact(ctx); err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Success = true

	case RpcCmdSetAutoCompaction:
		var data SetBoolData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid data: %v", err)
			return resp
		}
		r.session.SetAutoCompaction(data.Enabled)
		resp.Success = true

	case RpcCmdSetAutoRetry:
		var data SetBoolData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid data: %v", err)
			return resp
		}
		r.session.SetAutoRetry(data.Enabled)
		resp.Success = true

	case RpcCmdAbortRetry:
		r.session.AbortRetry()
		resp.Success = true

	case RpcCmdGetMessages:
		msgs := r.session.GetMessages()
		resp.Success = true
		resp.Data = msgs

	case RpcCmdGetCommands:
		names := r.session.GetActiveToolNames()
		resp.Success = true
		resp.Data = names

	default:
		resp.Error = fmt.Sprintf("unknown command: %s", cmd.Command)
	}

	return resp
}

func (r *RpcMode) writeResponse(resp RpcResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintln(r.output, string(data))
}

func (r *RpcMode) writeEvent(eventType string, eventData any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	evt := RpcEvent{
		Type: eventType,
		Data: eventData,
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	fmt.Fprintln(r.output, string(data))
}
