package extension

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a fresh Extension instance. Each call should return a new
// value — extension state is per-CodingSession, not shared.
type Factory func() Extension

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds a builtin extension factory keyed by name. Intended to be
// called from builtin packages' init() functions.
//
// Re-registering the same name panics — duplicate registrations are a build
// configuration bug (two packages both claim to provide "foo"), and surfacing
// it loudly at process start beats a silent override.
func Register(name string, factory Factory) {
	if name == "" {
		panic("extension.Register: empty name")
	}
	if factory == nil {
		panic(fmt.Sprintf("extension.Register: nil factory for %q", name))
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[name]; ok {
		panic(fmt.Sprintf("extension.Register: %q already registered", name))
	}
	registry[name] = factory
}

// FactoryFor returns the factory registered for name. The second return is
// false when the name is unknown.
func FactoryFor(name string) (Factory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	return f, ok
}

// BuiltinNames returns the registered extension names in lexicographic order.
// Used by LoadEnabled when no configuration file is present, so the default
// load order is deterministic and reproducible.
func BuiltinNames() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// resetRegistryForTest clears the registry. Test-only — production code never
// needs to unregister.
func resetRegistryForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Factory{}
}
