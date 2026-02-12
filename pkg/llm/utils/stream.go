package utils

import (
	"fmt"
	"sync"

	"github.com/crosszan/modu/pkg/llm"
)

type EventStream struct {
	ch         chan llm.AssistantMessageEvent
	done       chan struct{}
	closed     bool
	result     chan result
	resultOnce sync.Once
}

func NewEventStream() *EventStream {
	return &EventStream{
		ch:     make(chan llm.AssistantMessageEvent),
		done:   make(chan struct{}),
		result: make(chan result, 1),
	}
}

func (s *EventStream) Push(event llm.AssistantMessageEvent) {
	if event.Type == "done" && event.Message != nil {
		s.resolveResult(event.Message, nil)
	}
	if event.Type == "error" && event.ErrorMessage != nil {
		err := fmt.Errorf("stream error")
		if event.ErrorMessage.ErrorMessage != "" {
			err = fmt.Errorf("%s", event.ErrorMessage.ErrorMessage)
		}
		s.resolveResult(event.ErrorMessage, err)
	}
	if event.Type == "error" && event.ErrorMessage == nil && event.Message != nil {
		err := fmt.Errorf("stream error")
		if event.Message.ErrorMessage != "" {
			err = fmt.Errorf("%s", event.Message.ErrorMessage)
		}
		s.resolveResult(event.Message, err)
	}
	select {
	case s.ch <- event:
	case <-s.done:
	}
}

func (s *EventStream) Events() <-chan llm.AssistantMessageEvent {
	return s.ch
}

func (s *EventStream) Close() {
	if !s.closed {
		s.resolveResult(nil, fmt.Errorf("stream closed"))
		close(s.done)
		close(s.ch)
		s.closed = true
	}
}

func (s *EventStream) Result() (*llm.AssistantMessage, error) {
	res := <-s.result
	return res.message, res.err
}

func (s *EventStream) resolveResult(message *llm.AssistantMessage, err error) {
	s.resultOnce.Do(func() {
		s.result <- result{message: message, err: err}
	})
}

type result struct {
	message *llm.AssistantMessage
	err     error
}
