package agent

import (
	"context"

	"github.com/openmodu/modu/pkg/types"
)

func (a *Agent) Resume(decision types.ResumeDecision) bool {
	a.mu.RLock()
	ch := a.resume
	a.mu.RUnlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- decision:
		return true
	default:
		return false
	}
}

func (a *Agent) GetStatus() types.SessionStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Status
}

func (a *Agent) GetInterrupt() *types.InterruptEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Interrupt
}

func (a *Agent) waitForResume(ctx context.Context) types.ResumeDecision {
	a.mu.RLock()
	ch := a.resume
	ready := a.resumeReady
	a.mu.RUnlock()
	if ch == nil && ready != nil {
		select {
		case <-ready:
		case <-ctx.Done():
			return types.ResumeDecision{Allow: false}
		}
		a.mu.RLock()
		ch = a.resume
		a.mu.RUnlock()
	}
	if ch == nil {
		return types.ResumeDecision{Allow: false}
	}
	var decision types.ResumeDecision
	select {
	case decision = <-ch:
	case <-ctx.Done():
		return types.ResumeDecision{Allow: false}
	}
	a.mu.Lock()
	a.state.Status = types.SessionStatusRunning
	a.state.Interrupt = nil
	a.resume = nil
	a.resumeReady = make(chan struct{})
	a.mu.Unlock()
	return decision
}
