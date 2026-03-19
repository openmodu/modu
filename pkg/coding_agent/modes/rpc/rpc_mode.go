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

// pendingApproval holds a channel waiting for the client's approval decision.
type pendingApproval struct {
	ch chan agent.ToolApprovalDecision
}

// RpcMode implements a JSON-line based RPC protocol over stdin/stdout.
type RpcMode struct {
	session *coding_agent.CodingSession
	input   io.Reader
	output  io.Writer
	mu      sync.Mutex

	// pendingApprovals maps toolCallID → channel awaiting client decision.
	pendingApprovalsMu sync.Mutex
	pendingApprovals   map[string]*pendingApproval
}

// NewRpcMode creates a new RPC mode handler.
func NewRpcMode(session *coding_agent.CodingSession) *RpcMode {
	return &RpcMode{
		session:          session,
		input:            os.Stdin,
		output:           os.Stdout,
		pendingApprovals: make(map[string]*pendingApproval),
	}
}

// SetIO sets custom input/output for testing.
func (r *RpcMode) SetIO(input io.Reader, output io.Writer) {
	r.input = input
	r.output = output
}

// Run starts the RPC mode main loop.
func (r *RpcMode) Run(ctx context.Context) error {
	// Register interactive approval callback: block until client responds.
	r.session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
		ch := make(chan agent.ToolApprovalDecision, 1)
		r.pendingApprovalsMu.Lock()
		r.pendingApprovals[toolCallID] = &pendingApproval{ch: ch}
		r.pendingApprovalsMu.Unlock()

		defer func() {
			r.pendingApprovalsMu.Lock()
			delete(r.pendingApprovals, toolCallID)
			r.pendingApprovalsMu.Unlock()
		}()

		r.writeEvent("tool_approval_request", map[string]any{
			"toolCallId": toolCallID,
			"toolName":   toolName,
			"args":       args,
		})

		select {
		case decision := <-ch:
			return decision, nil
		case <-ctx.Done():
			return agent.ToolApprovalDeny, ctx.Err()
		}
	})

	// Subscribe to agent events and forward full event data
	unsubAgent := r.session.Subscribe(func(event agent.AgentEvent) {
		data := map[string]any{"eventType": string(event.Type)}
		if event.ToolName != "" {
			data["toolName"] = event.ToolName
		}
		if event.ToolCallID != "" {
			data["toolCallId"] = event.ToolCallID
		}
		if event.IsError {
			data["isError"] = true
		}
		if event.Args != nil {
			data["args"] = event.Args
		}
		if event.Result != nil {
			data["result"] = event.Result
		}
		if event.Message != nil {
			data["message"] = event.Message
		}
		if len(event.Messages) > 0 {
			data["messages"] = event.Messages
		}
		if len(event.ToolResults) > 0 {
			data["toolResults"] = event.ToolResults
		}
		if event.StreamEvent != nil {
			data["assistantMessageEvent"] = event.StreamEvent
		}
		if event.Partial != nil {
			data["partial"] = event.Partial
		}
		r.writeEvent("agent_event", data)
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

// resolveMessage extracts the message string from either the flat Message field or the Data payload.
func resolveMessage(cmd RpcCommand) (string, error) {
	if cmd.Message != "" {
		return cmd.Message, nil
	}
	if cmd.Data != nil {
		var data PromptData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return "", err
		}
		return data.Message, nil
	}
	return "", fmt.Errorf("missing message")
}

func (r *RpcMode) handleCommand(ctx context.Context, cmd RpcCommand) RpcResponse {
	cmdType := cmd.CommandType()
	resp := RpcResponse{
		ID:      cmd.ID,
		Type:    "response",
		Command: cmdType,
	}

	switch cmdType {
	case RpcCmdPrompt:
		msg, err := resolveMessage(cmd)
		if err != nil {
			resp.Error = fmt.Sprintf("invalid prompt data: %v", err)
			return resp
		}
		// Run prompt in background
		go func() {
			if err := r.session.Prompt(ctx, msg); err != nil {
				r.writeEvent("prompt_error", map[string]string{"error": err.Error()})
			}
		}()
		resp.Success = true

	case RpcCmdSteer:
		msg, err := resolveMessage(cmd)
		if err != nil {
			resp.Error = fmt.Sprintf("invalid steer data: %v", err)
			return resp
		}
		r.session.Steer(msg)
		resp.Success = true

	case RpcCmdFollowUp:
		msg, err := resolveMessage(cmd)
		if err != nil {
			resp.Error = fmt.Sprintf("invalid follow_up data: %v", err)
			return resp
		}
		r.session.FollowUp(msg)
		resp.Success = true

	case RpcCmdAbort:
		r.session.Abort()
		resp.Success = true

	case RpcCmdGetState:
		state := r.session.GetAgent().GetState()
		model := r.session.GetModel()
		rpcState := RpcSessionState{
			Model:               model.ID,
			Provider:            model.ProviderID,
			ThinkingLevel:       string(r.session.GetThinkingLevel()),
			IsStreaming:         state.IsStreaming,
			SessionID:           r.session.GetSessionID(),
			AutoCompaction:      r.session.GetConfig().AutoCompaction,
			AutoRetry:           r.session.GetConfig().AutoRetry,
			MessageCount:        len(state.Messages),
			IsCompacting:        r.session.IsCompacting(),
			SteeringMode:        string(r.session.GetAgent().GetSteeringMode()),
			FollowUpMode:        string(r.session.GetAgent().GetFollowUpMode()),
			SessionFile:         r.session.GetSessionFile(),
			SessionName:         r.session.GetSessionName(),
			PendingMessageCount: r.session.GetAgent().QueuedMessageCount(),
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
			resp.Data = map[string]string{"model": model.ID, "provider": model.ProviderID}
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

	case RpcCmdNewSession:
		r.session.GetAgent().ClearMessages()
		resp.Success = true

	case RpcCmdGetCommands:
		var commands []RpcSlashCommand
		// Tool names
		for _, name := range r.session.GetActiveToolNames() {
			commands = append(commands, RpcSlashCommand{
				Name:   name,
				Source: "tool",
			})
		}
		resp.Success = true
		resp.Data = commands

	// --- New commands (pi-mono parity) ---

	case RpcCmdGetAvailableModels:
		models := r.session.GetAvailableModels()
		resp.Success = true
		resp.Data = models

	case RpcCmdSetSteeringMode:
		var data SetModeData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid set_steering_mode data: %v", err)
			return resp
		}
		r.session.GetAgent().SetSteeringMode(agent.ExecutionMode(data.Mode))
		resp.Success = true

	case RpcCmdSetFollowUpMode:
		var data SetModeData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid set_follow_up_mode data: %v", err)
			return resp
		}
		r.session.GetAgent().SetFollowUpMode(agent.ExecutionMode(data.Mode))
		resp.Success = true

	case RpcCmdBash:
		var data BashData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid bash data: %v", err)
			return resp
		}
		result, err := r.session.ExecuteBash(ctx, data.Command, data.TimeoutMs)
		if err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Success = true
		resp.Data = result

	case RpcCmdAbortBash:
		r.session.AbortBash()
		resp.Success = true

	case RpcCmdGetSessionStats:
		stats := r.session.GetSessionStats()
		resp.Success = true
		resp.Data = stats

	case RpcCmdExportHTML:
		var data ExportHTMLData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid export_html data: %v", err)
			return resp
		}
		if err := r.session.ExportHTML(data.Path); err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Success = true

	case RpcCmdSwitchSession:
		var data SwitchSessionData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid switch_session data: %v", err)
			return resp
		}
		if err := r.session.SwitchSession(data.SessionFile); err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Success = true

	case RpcCmdFork:
		var data ForkData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid fork data: %v", err)
			return resp
		}
		if err := r.session.Fork(data.EntryID); err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Success = true

	case RpcCmdGetForkMessages:
		msgs := r.session.GetForkMessages()
		resp.Success = true
		resp.Data = msgs

	case RpcCmdGetLastAssistantText:
		text := r.session.GetLastAssistantText()
		resp.Success = true
		resp.Data = map[string]string{"text": text}

	case RpcCmdSetSessionName:
		var data SetSessionNameData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid set_session_name data: %v", err)
			return resp
		}
		r.session.SetSessionName(data.Name)
		resp.Success = true

	case RpcCmdToolApprovalResponse:
		var data ToolApprovalResponseData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			resp.Error = fmt.Sprintf("invalid tool_approval_response data: %v", err)
			return resp
		}
		r.pendingApprovalsMu.Lock()
		pending, ok := r.pendingApprovals[data.ToolCallID]
		r.pendingApprovalsMu.Unlock()
		if !ok {
			resp.Error = fmt.Sprintf("no pending approval for toolCallId: %s", data.ToolCallID)
			return resp
		}
		pending.ch <- agent.ToolApprovalDecision(data.Decision)
		resp.Success = true

	default:
		resp.Error = fmt.Sprintf("unknown command: %s", cmdType)
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
