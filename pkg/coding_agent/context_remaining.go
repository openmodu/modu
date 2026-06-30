package coding_agent

import "sync"

type contextRemainingProvider interface {
	TokensUntilCompaction() (int, bool)
}

type contextRemainingProxy struct {
	mu     sync.RWMutex
	source contextRemainingProvider
}

func (p *contextRemainingProxy) SetSource(source contextRemainingProvider) {
	p.mu.Lock()
	p.source = source
	p.mu.Unlock()
}

func (p *contextRemainingProxy) TokensUntilCompaction() (int, bool) {
	p.mu.RLock()
	source := p.source
	p.mu.RUnlock()
	if source == nil {
		return 0, false
	}
	return source.TokensUntilCompaction()
}
