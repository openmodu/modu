// Package bridge translates ACP session/update notifications into modu's
// AgentEvent stream.
//
// Translate is a pure function (no IO, no state) so it can be unit-tested
// against JSON fixtures and reused by any caller that wants to render an
// ACP session as modu-style events — not just the Provider adapter.
//
// Unknown sessionUpdate values return (nil, nil) rather than an error:
// ACP is a living protocol and agents routinely add new update kinds that
// older bridges should silently ignore.
package bridge

import (
	"encoding/json"
	"fmt"

	"github.com/openmodu/modu/pkg/acp/jsonrpc"
	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// sessionUpdateParams is the JSON envelope on session/update.
type sessionUpdateParams struct {
	Update sessionUpdate `json:"update"`
}

type sessionUpdate struct {
	SessionUpdate     string          `json:"sessionUpdate"`
	Content           json.RawMessage `json:"content,omitempty"`
	ToolCallID        string          `json:"toolCallId,omitempty"`
	Title             string          `json:"title,omitempty"`
	Status            string          `json:"status,omitempty"`
	Kind              string          `json:"kind,omitempty"`
	RawInput          map[string]any  `json:"rawInput,omitempty"`
	Error             string          `json:"error,omitempty"`
	Meta              *updateMeta     `json:"_meta,omitempty"`
	AvailableCommands []SlashCommand  `json:"availableCommands,omitempty"`
}

type updateMeta struct {
	ClaudeCode *claudeCodeMeta `json:"claudeCode,omitempty"`
}

type claudeCodeMeta struct {
	ToolName     string          `json:"toolName,omitempty"`
	ToolResponse json.RawMessage `json:"toolResponse,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// SlashCommand is one entry in an available_commands_update payload.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Translate converts one ACP session/update notification into zero or more
// modu AgentEvents.
//
//   - nil msg, non-session/update, or unknown sessionUpdate → (nil, nil).
//   - available_commands_update → empty non-nil slice (known-but-ignored).
//   - malformed params → error.
func Translate(msg *jsonrpc.Message) ([]agent.AgentEvent, error) {
	if msg == nil || msg.Method != "session/update" {
		return nil, nil
	}
	var params sessionUpdateParams
	if err := msg.ParseParams(&params); err != nil {
		return nil, fmt.Errorf("acp/bridge: parse params: %w", err)
	}
	u := params.Update

	switch u.SessionUpdate {
	case "agent_message_chunk":
		return textDelta(u.Content, types.EventTextDelta), nil

	case "agent_thought_chunk":
		return textDelta(u.Content, types.EventThinkingDelta), nil

	case "tool_call":
		if u.ToolCallID == "" {
			return nil, nil
		}
		return []agent.AgentEvent{{
			Type:       agent.EventTypeToolExecutionStart,
			ToolCallID: u.ToolCallID,
			ToolName:   toolNameOf(u),
			Args:       u.RawInput,
		}}, nil

	case "tool_call_update":
		if u.ToolCallID == "" {
			return nil, nil
		}
		return translateToolUpdate(u), nil

	case "available_commands_update":
		// Known but produces no AgentEvent — callers who need the command
		// list should read it from a higher layer. Return an empty (non-nil)
		// slice so tests can distinguish this from the unknown case.
		return []agent.AgentEvent{}, nil

	default:
		return nil, nil
	}
}

func translateToolUpdate(u sessionUpdate) []agent.AgentEvent {
	hasError := u.Error != "" ||
		(u.Meta != nil && u.Meta.ClaudeCode != nil && u.Meta.ClaudeCode.Error != "")

	if u.Status == "completed" || hasError {
		ev := agent.AgentEvent{
			Type:       agent.EventTypeToolExecutionEnd,
			ToolCallID: u.ToolCallID,
			ToolName:   toolNameOf(u),
			IsError:    hasError,
		}
		switch {
		case u.Error != "":
			ev.Result = u.Error
		case hasError:
			ev.Result = u.Meta.ClaudeCode.Error
		default:
			ev.Result = u.Content
		}
		return []agent.AgentEvent{ev}
	}

	return []agent.AgentEvent{{
		Type:       agent.EventTypeToolExecutionUpdate,
		ToolCallID: u.ToolCallID,
		ToolName:   toolNameOf(u),
		Partial:    u.Content,
	}}
}

func textDelta(content json.RawMessage, evType types.StreamEventType) []agent.AgentEvent {
	text := extractText(content)
	if text == "" {
		return nil
	}
	return []agent.AgentEvent{{
		Type: agent.EventTypeMessageUpdate,
		StreamEvent: &types.StreamEvent{
			Type:  evType,
			Delta: text,
		},
	}}
}

func toolNameOf(u sessionUpdate) string {
	if u.Meta != nil && u.Meta.ClaudeCode != nil && u.Meta.ClaudeCode.ToolName != "" {
		return u.Meta.ClaudeCode.ToolName
	}
	return u.Kind
}

// extractText walks a content field that may be a {"type":"text","text":"..."}
// object or a nested array. Returns the first text payload found.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Type == "text" {
		return obj.Text
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, item := range arr {
			if s := extractText(item); s != "" {
				return s
			}
		}
	}
	return ""
}
