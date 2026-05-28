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
	EventTypeInterrupt           EventType = "interrupt"
)

type Event struct {
	Type        EventType
	Reason      string
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
	Interrupt   *InterruptEvent
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

func (s *EventStream) Emit(event Event) {
	s.Push(event)
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

func emitEvent(sink EventSink, event Event) {
	if sink != nil {
		sink.Emit(event)
	}
}

func emitMessageTo(sink EventSink, message AgentMessage) {
	emitEvent(sink, Event{Type: EventTypeMessageStart, Message: message})
	emitEvent(sink, Event{Type: EventTypeMessageEnd, Message: message})
}

type discardEvents struct{}

func (discardEvents) Emit(Event) {}

func resolveEvents(sink EventSink, messages []AgentMessage, err error) {
	if stream, ok := sink.(*EventStream); ok {
		stream.Resolve(messages, err)
	}
}

func closeEvents(sink EventSink) {
	if stream, ok := sink.(*EventStream); ok {
		stream.Close()
	}
}
