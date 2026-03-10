package agent

import "github.com/crosszan/modu/pkg/types"

// --- Events ---

type EventType string

const (
	EventTypeAgentStart          EventType = "agent_start"
	EventTypeAgentEnd            EventType = "agent_end"
	EventTypeTurnStart           EventType = "turn_start"
	EventTypeTurnEnd             EventType = "turn_end"
	EventTypeMessageStart        EventType = "message_start"
	EventTypeMessageUpdate       EventType = "message_update"
	EventTypeMessageEnd          EventType = "message_end"
	EventTypeToolExecutionStart  EventType = "tool_execution_start"
	EventTypeToolExecutionUpdate EventType = "tool_execution_update"
	EventTypeToolExecutionEnd    EventType = "tool_execution_end"
)

// AgentEvent union type in Go struct
type AgentEvent struct {
	Type        EventType
	Messages    []AgentMessage
	Message     AgentMessage
	ToolResults []types.ToolResultMessage

	// Tool Execution specific
	ToolCallID  string
	ToolName    string
	Args        any
	Result      interface{}
	IsError     bool
	Partial     interface{}
	StreamEvent *types.StreamEvent
}
