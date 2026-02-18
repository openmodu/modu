package eventbus

import (
	"sync"
)

// EventBus provides pub/sub event handling.
type EventBus interface {
	// Emit sends an event on the given channel.
	Emit(channel string, data any)
	// On registers a handler for a channel. Returns an unsubscribe function.
	On(channel string, handler func(data any)) func()
}

// EventBusController extends EventBus with management methods.
type EventBusController interface {
	EventBus
	// Clear removes all handlers from all channels.
	Clear()
}

type handlerEntry struct {
	id int
	fn func(data any)
}

type eventBus struct {
	mu       sync.RWMutex
	handlers map[string][]handlerEntry
	nextID   int
}

// NewEventBus creates a new EventBusController.
func NewEventBus() EventBusController {
	return &eventBus{
		handlers: make(map[string][]handlerEntry),
	}
}

func (eb *eventBus) Emit(channel string, data any) {
	eb.mu.RLock()
	handlers := make([]handlerEntry, len(eb.handlers[channel]))
	copy(handlers, eb.handlers[channel])
	eb.mu.RUnlock()

	for _, h := range handlers {
		func() {
			defer func() {
				recover() // prevent handler panics from crashing
			}()
			h.fn(data)
		}()
	}
}

func (eb *eventBus) On(channel string, handler func(data any)) func() {
	eb.mu.Lock()
	id := eb.nextID
	eb.nextID++
	eb.handlers[channel] = append(eb.handlers[channel], handlerEntry{id: id, fn: handler})
	eb.mu.Unlock()

	return func() {
		eb.mu.Lock()
		defer eb.mu.Unlock()
		entries := eb.handlers[channel]
		for i, e := range entries {
			if e.id == id {
				eb.handlers[channel] = append(entries[:i], entries[i+1:]...)
				break
			}
		}
	}
}

func (eb *eventBus) Clear() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.handlers = make(map[string][]handlerEntry)
}
