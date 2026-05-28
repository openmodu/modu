package agent

import (
	msgstream "github.com/openmodu/modu/pkg/stream"
	"github.com/openmodu/modu/pkg/types"
)

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

type Event struct {
	Type        EventType
	Messages    []AgentMessage
	Message     AgentMessage
	ToolResults []types.ToolResultMessage

	ToolCallID string
	ToolName   string
	Args       any
	Result     any
	IsError    bool
	Partial    any
	Parallel   bool

	StreamEvent *types.StreamEvent
}

type EventStream struct {
	underlying *msgstream.EventStream[Event, []AgentMessage]
}

func NewEventStream() *EventStream {
	return &EventStream{underlying: msgstream.New[Event, []AgentMessage]()}
}

func (s *EventStream) Push(event Event) {
	if s != nil {
		s.underlying.Push(event)
	}
}

func (s *EventStream) Resolve(messages []AgentMessage, err error) {
	if s != nil {
		s.underlying.Resolve(messages, err)
	}
}

func (s *EventStream) Events() <-chan Event {
	return s.underlying.Events()
}

func (s *EventStream) Close() {
	if s != nil {
		s.underlying.Close()
	}
}

func (s *EventStream) Result() ([]AgentMessage, error) {
	return s.underlying.Result()
}

func emitMessage(stream *EventStream, message AgentMessage) {
	stream.Push(Event{Type: EventTypeMessageStart, Message: message})
	stream.Push(Event{Type: EventTypeMessageEnd, Message: message})
}
