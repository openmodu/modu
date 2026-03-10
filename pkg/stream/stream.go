package stream

import (
	"fmt"
	"sync"
)

// streamResult holds the final result of the stream.
type streamResult[R any] struct {
	res R
	err error
}

// EventStream is a generic, thread-safe stream for events of type E, producing a final result of type R.
type EventStream[E any, R any] struct {
	ch         chan E
	done       chan struct{}
	mu         sync.Mutex // protects closed and the close sequence
	closed     bool
	result     chan streamResult[R]
	resultOnce sync.Once
}

// New creates a new instance of EventStream.
func New[E any, R any]() *EventStream[E, R] {
	return &EventStream[E, R]{
		ch:     make(chan E),
		done:   make(chan struct{}),
		result: make(chan streamResult[R], 1),
	}
}

// Push sends an event to the stream.
// It is non-blocking if the stream is closed or being closed.
func (s *EventStream[E, R]) Push(event E) {
	select {
	case s.ch <- event:
	case <-s.done:
	}
}

// Resolve sets the final result of the stream. It can be called exactly once.
func (s *EventStream[E, R]) Resolve(res R, err error) {
	s.resultOnce.Do(func() {
		s.result <- streamResult[R]{res: res, err: err}
	})
}

// Events returns a read-only channel to receive events.
func (s *EventStream[E, R]) Events() <-chan E {
	return s.ch
}

// Close closes the stream. Subsequent calls to Push will be ignored.
func (s *EventStream[E, R]) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true

	// Provide a default error if the sender forgot to call Resolve.
	// Since R can be any struct or pointer, we use the zero value along with an error.
	var zeroR R
	s.Resolve(zeroR, fmt.Errorf("stream closed without a resolution"))

	close(s.done) // signal Push() to stop sending before closing ch
	close(s.ch)
}

// Result blocks until the stream is resolved and returns the final result or error.
func (s *EventStream[E, R]) Result() (R, error) {
	res := <-s.result
	return res.res, res.err
}
