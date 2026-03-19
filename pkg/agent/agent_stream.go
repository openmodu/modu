package agent

import (
	msgstream "github.com/openmodu/modu/pkg/stream"
)

type EventStream struct {
	underlying *msgstream.EventStream[AgentEvent, []AgentMessage]
}

func NewEventStream() *EventStream {
	return &EventStream{
		underlying: msgstream.New[AgentEvent, []AgentMessage](),
	}
}

func (s *EventStream) Push(event AgentEvent) {
	s.underlying.Push(event)
}

func (s *EventStream) Resolve(messages []AgentMessage, err error) {
	s.underlying.Resolve(messages, err)
}

func (s *EventStream) Events() <-chan AgentEvent {
	return s.underlying.Events()
}

func (s *EventStream) Close() {
	s.underlying.Close()
}

func (s *EventStream) Result() ([]AgentMessage, error) {
	return s.underlying.Result()
}
