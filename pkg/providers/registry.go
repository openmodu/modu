package providers

import "sync"

var (
	registryMu sync.RWMutex
	registry   = map[string]Provider{}
)

// Register adds a provider to the global registry.
// If a provider with the same ID already exists, it is replaced.
func Register(p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.ID()] = p
}

// Get returns the registered provider with the given ID.
func Get(id string) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[id]
	return p, ok
}

// List returns all registered providers in unspecified order.
func List() []Provider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Provider, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	return out
}
