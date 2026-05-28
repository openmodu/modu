package agent

func (a *Agent) Subscribe(fn func(Event)) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.listenerID
	a.listenerID++
	a.listeners[id] = fn
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.listeners, id)
	}
}

func (a *Agent) emit(event Event) {
	a.mu.RLock()
	listeners := make([]func(Event), 0, len(a.listeners))
	for _, listener := range a.listeners {
		listeners = append(listeners, listener)
	}
	a.mu.RUnlock()
	for _, listener := range listeners {
		listener(event)
	}
}
