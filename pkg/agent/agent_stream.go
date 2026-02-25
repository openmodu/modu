package agent

import (
	"fmt"
	"sync"
)

type EventStream struct {
	ch         chan AgentEvent
	done       chan struct{}
	mu         sync.Mutex // protects closed and the close sequence
	closed     bool
	result     chan result
	resultOnce sync.Once
}

func NewEventStream() *EventStream {
	return &EventStream{
		ch:     make(chan AgentEvent),
		done:   make(chan struct{}),
		result: make(chan result, 1),
	}
}

func (s *EventStream) Push(event AgentEvent) {
	if event.Type == EventTypeAgentEnd {
		s.resolveResult(event.Messages, nil)
	}
	select {
	case s.ch <- event:
	case <-s.done:
	}
}

func (s *EventStream) Events() <-chan AgentEvent {
	return s.ch
}

func (s *EventStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.resolveResult(nil, fmt.Errorf("stream closed"))
	close(s.done) // signal Push() to stop sending before closing ch
	close(s.ch)
}

func (s *EventStream) Result() ([]AgentMessage, error) {
	res := <-s.result
	return res.messages, res.err
}

func (s *EventStream) resolveResult(messages []AgentMessage, err error) {
	s.resultOnce.Do(func() {
		s.result <- result{messages: messages, err: err}
	})
}

type result struct {
	messages []AgentMessage
	err      error
}
