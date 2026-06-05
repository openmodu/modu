package subagent

import (
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

// childActivity is the live progress of one background subagent child,
// accumulated from the host's re-emitted child events.
type childActivity struct {
	Turns       int
	FailedTools int
	Tokens      int
	UpdatedAt   int64
}

// childActivityRegistry tallies per-task child activity from
// "subagent_child_event" events. Safe for concurrent use: the host emits
// child events from background goroutines while status rendering reads them.
type childActivityRegistry struct {
	mu     sync.RWMutex
	byTask map[string]*childActivity
}

func newChildActivityRegistry() *childActivityRegistry {
	return &childActivityRegistry{byTask: map[string]*childActivity{}}
}

// handle folds one re-emitted child event into the task's running tally.
// It reads the original child event type from event.Reason (the host wraps
// child events as type "subagent_child_event" and stashes the source type
// there).
func (r *childActivityRegistry) handle(event types.Event) {
	taskID := event.TaskID
	if taskID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.byTask[taskID]
	if a == nil {
		a = &childActivity{}
		r.byTask[taskID] = a
	}
	switch event.Reason {
	case string(types.EventTypeTurnEnd):
		a.Turns++
		a.Tokens += turnTokens(event.Message)
	case string(types.EventTypeToolExecutionEnd):
		if event.IsError {
			a.FailedTools++
		}
	}
	a.UpdatedAt = time.Now().UnixMilli()
}

// get returns a copy of the activity for a task and whether it exists.
func (r *childActivityRegistry) get(taskID string) (childActivity, bool) {
	if r == nil || taskID == "" {
		return childActivity{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byTask[taskID]
	if !ok {
		return childActivity{}, false
	}
	return *a, true
}

// turnTokens returns the Input+Output token count carried by a turn_end
// assistant message, handling both value and pointer message forms.
func turnTokens(msg types.AgentMessage) int {
	switch m := msg.(type) {
	case types.AssistantMessage:
		return m.Usage.Input + m.Usage.Output
	case *types.AssistantMessage:
		if m != nil {
			return m.Usage.Input + m.Usage.Output
		}
	}
	return 0
}
