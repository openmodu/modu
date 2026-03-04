package providers

import (
	"fmt"
	"sync"
)

// -------------------------------------------------------------------
// EventStream — Stream 的通用实现，provider goroutine 通过 Push 推事件
// -------------------------------------------------------------------
type streamResult struct {
	response *ChatResponse
	err      error
}

// EventStream 是 Stream 接口的实现，线程安全
type EventStream struct {
	ch         chan StreamEvent
	done       chan struct{}
	mu         sync.Mutex
	closed     bool
	result     chan streamResult
	resultOnce sync.Once
}

func NewEventStream() *EventStream {
	return &EventStream{
		ch:     make(chan StreamEvent),
		done:   make(chan struct{}),
		result: make(chan streamResult, 1),
	}
}

func (s *EventStream) Push(event StreamEvent) {
	switch event.Type {
	case EventDone:
		s.resolveResult(event.Partial, nil)
	case EventError:
		err := event.Err
		if err == nil {
			msg := "stream error"
			if event.Partial != nil && event.Partial.ErrorMessage != "" {
				msg = event.Partial.ErrorMessage
			}
			err = fmt.Errorf("%s", msg)
		}
		s.resolveResult(event.Partial, err)
	}
	select {
	case s.ch <- event:
	case <-s.done:
	}
}

func (s *EventStream) Events() <-chan StreamEvent {
	return s.ch
}

func (s *EventStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.resolveResult(nil, fmt.Errorf("stream closed"))
		close(s.done)
		close(s.ch)
		s.closed = true
	}
}

func (s *EventStream) Result() (*ChatResponse, error) {
	res := <-s.result
	return res.response, res.err
}

func (s *EventStream) resolveResult(response *ChatResponse, err error) {
	s.resultOnce.Do(func() {
		s.result <- streamResult{response: response, err: err}
	})
}
