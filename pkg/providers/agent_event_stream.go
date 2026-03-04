package providers

import (
	"fmt"
	"sync"
)

// AssistantMessageEvent is an event emitted during assistant message streaming.
type AssistantMessageEvent struct {
	Type         string
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

// AssistantMessageEventStream is the streaming interface for assistant messages.
type AssistantMessageEventStream interface {
	Events() <-chan AssistantMessageEvent
	Close()
	Result() (*AssistantMessage, error)
}

// AssistantEventStream is the concrete implementation of AssistantMessageEventStream.
type AssistantEventStream struct {
	ch         chan AssistantMessageEvent
	done       chan struct{}
	mu         sync.Mutex
	closed     bool
	result     chan assistantStreamResult
	resultOnce sync.Once
}

// NewAssistantEventStream creates a new AssistantEventStream.
func NewAssistantEventStream() *AssistantEventStream {
	return &AssistantEventStream{
		ch:     make(chan AssistantMessageEvent),
		done:   make(chan struct{}),
		result: make(chan assistantStreamResult, 1),
	}
}

func (s *AssistantEventStream) Push(event AssistantMessageEvent) {
	switch event.Type {
	case "done":
		if event.Message != nil {
			s.resolveResult(event.Message, nil)
		}
	case "error":
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

func (s *AssistantEventStream) Events() <-chan AssistantMessageEvent {
	return s.ch
}

func (s *AssistantEventStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.resolveResult(nil, fmt.Errorf("stream closed"))
		close(s.done)
		close(s.ch)
		s.closed = true
	}
}

func (s *AssistantEventStream) Result() (*AssistantMessage, error) {
	res := <-s.result
	return res.message, res.err
}

func (s *AssistantEventStream) resolveResult(msg *AssistantMessage, err error) {
	s.resultOnce.Do(func() {
		s.result <- assistantStreamResult{message: msg, err: err}
	})
}

type assistantStreamResult struct {
	message *AssistantMessage
	err     error
}
