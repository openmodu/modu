package agent

import "github.com/openmodu/modu/pkg/types"

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
	// EventTypeInterrupt is emitted when the agent pauses for a human decision.
	// Callers should call Agent.Resume() to continue or abort.
	EventTypeInterrupt EventType = "interrupt"
)

// AgentEvent union type in Go struct
type AgentEvent struct {
	Type        EventType
	Messages    []AgentMessage
	Message     AgentMessage
	ToolResults []types.ToolResultMessage

	// Tool Execution specific
	ToolCallID string
	ToolName   string
	Args       any
	Result     interface{}
	IsError    bool
	Partial    interface{}
	// Parallel is true when this tool is executing concurrently with others.
	// Renderers should skip cursor-up / placeholder-collapse for parallel tools.
	Parallel    bool
	StreamEvent *types.StreamEvent

	// Interrupt is populated for EventTypeInterrupt events.
	// Call Agent.Resume() to continue or abort.
	Interrupt *InterruptEvent
}
