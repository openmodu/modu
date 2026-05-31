package agent

import "github.com/openmodu/modu/pkg/types"

func NewEventStream() *types.AgentEventStream {
	return types.NewAgentEventStream()
}

func emitEvent(sink types.EventSink, event types.Event) {
	if sink != nil {
		sink.Emit(event)
	}
}

func emitMessageTo(sink types.EventSink, message types.AgentMessage) {
	emitEvent(sink, types.Event{Type: types.EventTypeMessageStart, Message: message})
	emitEvent(sink, types.Event{Type: types.EventTypeMessageEnd, Message: message})
}

type discardEvents struct{}

func (discardEvents) Emit(types.Event) {}

func resolveEvents(sink types.EventSink, messages []types.AgentMessage, err error) {
	if stream, ok := sink.(*types.AgentEventStream); ok {
		stream.Resolve(messages, err)
	}
}

func closeEvents(sink types.EventSink) {
	if stream, ok := sink.(*types.AgentEventStream); ok {
		stream.Close()
	}
}
