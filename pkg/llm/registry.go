package llm

import (
	"fmt"
	"sync"
)

type ApiProvider interface {
	Api() Api
	Stream(model *Model, ctx *Context, opts *StreamOptions) (AssistantMessageEventStream, error)
	StreamSimple(model *Model, ctx *Context, opts *SimpleStreamOptions) (AssistantMessageEventStream, error)
}

var (
	registryMu sync.RWMutex
	registry   = make(map[Api]registeredApiProvider)
)

type registeredApiProvider struct {
	provider ApiProvider
	sourceID string
}

func RegisterApiProvider(provider ApiProvider) {
	RegisterApiProviderWithSource(provider, "")
}

func RegisterApiProviderWithSource(provider ApiProvider, sourceID string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[provider.Api()] = registeredApiProvider{
		provider: provider,
		sourceID: sourceID,
	}
}

func GetApiProvider(api Api) (ApiProvider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[api]
	return p.provider, ok
}

func GetApiProviders() []ApiProvider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]ApiProvider, 0, len(registry))
	for _, entry := range registry {
		out = append(out, entry.provider)
	}
	return out
}

func UnregisterApiProviders(sourceID string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for api, entry := range registry {
		if entry.sourceID == sourceID {
			delete(registry, api)
		}
	}
}

func ClearApiProviders() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[Api]registeredApiProvider)
}

func Stream(model *Model, ctx *Context, opts *StreamOptions) (AssistantMessageEventStream, error) {
	provider, ok := GetApiProvider(model.Api)
	if !ok {
		return nil, fmt.Errorf("no provider registered for api: %s", model.Api)
	}
	if model.Api != provider.Api() {
		return nil, fmt.Errorf("mismatched api: %s expected %s", model.Api, provider.Api())
	}
	return provider.Stream(model, ctx, opts)
}

func StreamSimple(model *Model, ctx *Context, opts *SimpleStreamOptions) (AssistantMessageEventStream, error) {
	provider, ok := GetApiProvider(model.Api)
	if !ok {
		return nil, fmt.Errorf("no provider registered for api: %s", model.Api)
	}
	if model.Api != provider.Api() {
		return nil, fmt.Errorf("mismatched api: %s expected %s", model.Api, provider.Api())
	}
	return provider.StreamSimple(model, ctx, opts)
}
