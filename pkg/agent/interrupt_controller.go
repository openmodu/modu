package agent

import "context"

func (a *Agent) Resume(decision ResumeDecision) bool {
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

func (a *Agent) GetStatus() SessionStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Status
}

func (a *Agent) GetInterrupt() *InterruptEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Interrupt
}

func (a *Agent) waitForResume(ctx context.Context) ResumeDecision {
	a.mu.RLock()
	ch := a.resume
	ready := a.resumeReady
	a.mu.RUnlock()
	if ch == nil && ready != nil {
		select {
		case <-ready:
		case <-ctx.Done():
			return ResumeDecision{Allow: false}
		}
		a.mu.RLock()
		ch = a.resume
		a.mu.RUnlock()
	}
	if ch == nil {
		return ResumeDecision{Allow: false}
	}
	var decision ResumeDecision
	select {
	case decision = <-ch:
	case <-ctx.Done():
		return ResumeDecision{Allow: false}
	}
	a.mu.Lock()
	a.state.Status = SessionStatusRunning
	a.state.Interrupt = nil
	a.resume = nil
	a.resumeReady = make(chan struct{})
	a.mu.Unlock()
	return decision
}
