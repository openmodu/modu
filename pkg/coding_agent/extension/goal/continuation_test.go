package goal

import "testing"

// goalWith returns a Goal value with the given status; other fields are not
// inspected by the decision functions.
func goalWith(status Status) *Goal {
	return &Goal{ID: "g1", Objective: "any", Status: status}
}

func TestShouldQueueAfterAgentEnd(t *testing.T) {
	cases := []struct {
		name       string
		goal       *Goal
		hasPending bool
		want       bool
	}{
		// Mirrors pi-goal's "continues an active goal after each agent turn"
		// expectation: status alone is enough; IsIdle deliberately omitted
		// because agent_end fires while IsStreaming is still observably true.
		{"active no-pending fires", goalWith(StatusActive), false, true},
		{"active with-pending suppressed", goalWith(StatusActive), true, false},

		// Non-active states never auto-continue: pi-goal asserts the same.
		{"nil goal no fire", nil, false, false},
		{"paused no fire", goalWith(StatusPaused), false, false},
		{"budgetLimited no fire", goalWith(StatusBudgetLimited), false, false},
		{"complete no fire", goalWith(StatusComplete), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldQueueContinuationAfterAgentEnd(c.goal, c.hasPending); got != c.want {
				t.Errorf("ShouldQueueContinuationAfterAgentEnd(%v, %v) = %v, want %v", c.goal, c.hasPending, got, c.want)
			}
		})
	}
}

func TestShouldQueueWhenIdle(t *testing.T) {
	cases := []struct {
		name       string
		goal       *Goal
		isIdle     bool
		hasPending bool
		want       bool
	}{
		// pi-goal "requires idle state for command and session-start
		// continuation": both conditions must hold for the WhenIdle path.
		{"active idle no-pending fires", goalWith(StatusActive), true, false, true},
		{"active not-idle suppressed", goalWith(StatusActive), false, false, false},
		{"active idle pending suppressed", goalWith(StatusActive), true, true, false},

		{"nil goal no fire", nil, true, false, false},
		{"paused no fire", goalWith(StatusPaused), true, false, false},
		{"budgetLimited no fire", goalWith(StatusBudgetLimited), true, false, false},
		{"complete no fire", goalWith(StatusComplete), true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldQueueContinuationWhenIdle(c.goal, c.isIdle, c.hasPending); got != c.want {
				t.Errorf("ShouldQueueContinuationWhenIdle(%v, %v, %v) = %v, want %v",
					c.goal, c.isIdle, c.hasPending, got, c.want)
			}
		})
	}
}
