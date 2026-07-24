package subagent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
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

// rebuildFromTasks repopulates per-task tallies from persisted child session
// files. The live "subagent_child_event" stream that normally feeds the
// registry only exists in the session that ran the child, so after a resume
// the registry starts empty; this reconstructs it from each task's on-disk
// session.jsonl. Best-effort: unreadable or empty sessions are skipped, and
// tasks already present (a live child in this session) are never overwritten.
func (r *childActivityRegistry) rebuildFromTasks(tasks []extension.TaskSnapshot) {
	if r == nil {
		return
	}
	for _, task := range tasks {
		if task.Kind != "subagent" || task.ID == "" || task.SessionFile == "" {
			continue
		}
		a, ok := childActivityFromSessionFile(task.SessionFile)
		if !ok {
			continue
		}
		r.mu.Lock()
		if _, exists := r.byTask[task.ID]; !exists {
			r.byTask[task.ID] = a
		}
		r.mu.Unlock()
	}
}

// childActivityFromSessionFile tallies a child's turns, tokens, and failed
// tools by scanning its persisted session JSONL. It mirrors the live
// event-folding in handle: one turn per assistant message (with its
// Input+Output tokens) and one failed tool per error tool result.
func childActivityFromSessionFile(path string) (*childActivity, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	a := &childActivity{}
	saw := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m struct {
			Role  string `json:"role"`
			Usage struct {
				Input  int `json:"input"`
				Output int `json:"output"`
			} `json:"usage"`
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		saw = true
		if m.Role == types.RoleAssistant {
			a.Turns++
			a.Tokens += m.Usage.Input + m.Usage.Output
		}
		if m.IsError {
			a.FailedTools++
		}
	}
	// A scan error (e.g. an over-long line) leaves a partial tally; keep it
	// only if we managed to read some activity, otherwise treat as unusable.
	if err := sc.Err(); err != nil && a.Turns == 0 && a.FailedTools == 0 {
		return nil, false
	}
	if !saw {
		return nil, false
	}
	a.UpdatedAt = time.Now().UnixMilli()
	return a, true
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
