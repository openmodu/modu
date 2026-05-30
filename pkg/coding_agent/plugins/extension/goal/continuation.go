package goal

// Two pure decision functions for whether the extension should inject a
// hidden continuation. They mirror pi-goal's continuation.ts so the
// behaviour is comparable across harnesses and trivially unit-testable
// without spinning up an Extension or fake API.
//
// The split matters: the agent_end branch must NOT require IsIdle, because
// IsStreaming is still observably true at the exact moment the agent_end
// handler fires (the agent loop flips it after dispatching listeners). The
// idle branch (slash commands, session_start) does require IsIdle so we
// don't double-prompt while the user has work in flight.

// ShouldQueueContinuationAfterAgentEnd returns true when a continuation
// should be injected on every active-goal agent_end event. Only the goal's
// status and any pending user messages gate it.
func ShouldQueueContinuationAfterAgentEnd(g *Goal, hasPendingMessages bool) bool {
	if g == nil {
		return false
	}
	if g.Status != StatusActive {
		return false
	}
	return !hasPendingMessages
}

// ShouldQueueContinuationWhenIdle returns true when a continuation should be
// injected at a moment the agent is meant to be idle (session_start,
// /goal-resume, /goal <new-objective>). Requires both isIdle and
// !hasPendingMessages so we don't preempt user work or double-fire while a
// turn is in flight.
func ShouldQueueContinuationWhenIdle(g *Goal, isIdle, hasPendingMessages bool) bool {
	if g == nil {
		return false
	}
	if g.Status != StatusActive {
		return false
	}
	return isIdle && !hasPendingMessages
}
