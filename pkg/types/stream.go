package types

import (
	"fmt"
	"sync"
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
	ch         chan StreamEvent
	done       chan struct{}
	mu         sync.Mutex
	closed     bool
	result     chan streamResult
	resultOnce sync.Once
}

// NewEventStream creates a new EventStreamImpl.
func NewEventStream() *EventStreamImpl {
	return &EventStreamImpl{
		ch:     make(chan StreamEvent),
		done:   make(chan struct{}),
		result: make(chan streamResult, 1),
	}
}

func (s *EventStreamImpl) Push(event StreamEvent) {
	switch event.Type {
	case EventDone:
		if event.Message != nil {
			s.resolveResult(event.Message, nil)
		}
	case EventError:
		msg := event.ErrorMessage
		if msg == nil {
			msg = event.Message
		}
		err := event.Error
		if err == nil {
			errText := "stream error"
			if msg != nil && msg.ErrorMessage != "" {
				errText = msg.ErrorMessage
			}
			err = fmt.Errorf("%s", errText)
		}
		s.resolveResult(msg, err)
	}
	select {
	case s.ch <- event:
	case <-s.done:
	}
}

func (s *EventStreamImpl) Events() <-chan StreamEvent {
	return s.ch
}

func (s *EventStreamImpl) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.resolveResult(nil, fmt.Errorf("stream closed"))
		close(s.done)
		close(s.ch)
		s.closed = true
	}
}

func (s *EventStreamImpl) Result() (*AssistantMessage, error) {
	res := <-s.result
	return res.message, res.err
}

func (s *EventStreamImpl) resolveResult(msg *AssistantMessage, err error) {
	s.resultOnce.Do(func() {
		s.result <- streamResult{message: msg, err: err}
	})
}

type streamResult struct {
	message *AssistantMessage
	err     error
}
