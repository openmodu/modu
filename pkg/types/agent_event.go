package types

import msgstream "github.com/openmodu/modu/pkg/stream"

type EventSink interface {
	Emit(event Event)
}

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
	EventTypeInterrupt           EventType = "interrupt"
)

type Event struct {
	Type        EventType
	Reason      string
	Messages    []AgentMessage
	Message     AgentMessage
	ToolResults []ToolResultMessage

	ToolCallID string
	ToolName   string
	Args       any
	Result     any
	IsError    bool
	Partial    any
	Parallel   bool
	BatchSize  int

	// TaskID names the background subagent task an event belongs to when the
	// host re-emits a child agent's events to extensions. Empty for the main
	// agent's own events.
	TaskID string

	StreamEvent *StreamEvent
	Interrupt   *InterruptEvent
}

type AgentEventStream struct {
	underlying *msgstream.EventStream[Event, []AgentMessage]
}

func NewAgentEventStream() *AgentEventStream {
	return &AgentEventStream{underlying: msgstream.New[Event, []AgentMessage]()}
}

func (s *AgentEventStream) Push(event Event) {
	if s != nil {
		s.underlying.Push(event)
	}
}

func (s *AgentEventStream) Emit(event Event) {
	s.Push(event)
}

func (s *AgentEventStream) Resolve(messages []AgentMessage, err error) {
	if s != nil {
		s.underlying.Resolve(messages, err)
	}
}

func (s *AgentEventStream) Events() <-chan Event {
	return s.underlying.Events()
}

func (s *AgentEventStream) Close() {
	if s != nil {
		s.underlying.Close()
	}
}

func (s *AgentEventStream) Result() ([]AgentMessage, error) {
	return s.underlying.Result()
}
