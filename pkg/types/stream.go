package types

import (
	"github.com/crosszan/modu/pkg/stream"
)

// StreamEventType defines the type of a stream event.
type StreamEventType = string

const (
	EventStart         StreamEventType = "start"
	EventTextStart     StreamEventType = "text_start"
	EventTextDelta     StreamEventType = "text_delta"
	EventTextEnd       StreamEventType = "text_end"
	EventThinkingStart StreamEventType = "thinking_start"
	EventThinkingDelta StreamEventType = "thinking_delta"
	EventThinkingEnd   StreamEventType = "thinking_end"
	EventToolCallStart StreamEventType = "toolcall_start"
	EventToolCallDelta StreamEventType = "toolcall_delta"
	EventToolCallEnd   StreamEventType = "toolcall_end"
	EventDone          StreamEventType = "done"
	EventError         StreamEventType = "error"
)

// StreamEvent is an event emitted during assistant message streaming.
type StreamEvent struct {
	Type         StreamEventType
	ContentIndex int
	Delta        string
	Content      string
	ToolCall     *ToolCallContent
	Partial      *AssistantMessage
	Reason       StopReason
	Message      *AssistantMessage
	ErrorMessage *AssistantMessage
	Error        error
}

// EventStream is the streaming interface for assistant messages.
type EventStream interface {
	Events() <-chan StreamEvent
	Close()
	Result() (*AssistantMessage, error)
}

// EventStreamImpl is the concrete implementation of EventStream.
type EventStreamImpl struct {
	underlying *stream.EventStream[StreamEvent, *AssistantMessage]
}

// NewEventStream creates a new EventStreamImpl.
func NewEventStream() *EventStreamImpl {
	return &EventStreamImpl{
		underlying: stream.New[StreamEvent, *AssistantMessage](),
	}
}

func (s *EventStreamImpl) Push(event StreamEvent) {
	s.underlying.Push(event)
}

func (s *EventStreamImpl) Resolve(msg *AssistantMessage, err error) {
	s.underlying.Resolve(msg, err)
}

func (s *EventStreamImpl) Events() <-chan StreamEvent {
	return s.underlying.Events()
}

func (s *EventStreamImpl) Close() {
	s.underlying.Close()
}

func (s *EventStreamImpl) Result() (*AssistantMessage, error) {
	return s.underlying.Result()
}
