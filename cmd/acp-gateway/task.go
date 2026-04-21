package main

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/types"
)

// TaskStatus is the lifecycle state of a gateway task.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

// Task describes one unit of work submitted to the gateway.
type Task struct {
	ID        string     `json:"id"`
	Agent     string     `json:"agent"`
	Prompt    string     `json:"prompt"`
	Cwd       string     `json:"cwd"`
	Status    TaskStatus `json:"status"`
	Result    string     `json:"result,omitempty"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// SSEEvent is one frame sent over /api/tasks/:id/stream. Type matches the
// SSE "event:" field; Data is JSON-marshaled for the "data:" field.
type SSEEvent struct {
	Type string `json:"-"`
	Data any    `json:"data"`
}

// PermissionPrompt mirrors the payload the gateway surfaces to SSE clients
// when an agent asks for permission. The client replies via POST /approve.
type PermissionPrompt struct {
	ToolCallID string                    `json:"toolCallId"`
	Title      string                    `json:"title"`
	Kind       string                    `json:"kind"`
	Options    []client.PermissionOption `json:"options"`
}

// Store owns tasks, their event queues, and a work queue for the worker pool.
// All methods are safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	counter atomic.Uint64
	tasks   map[string]*taskEntry
	queue   chan string

	db *sql.DB // nil = no persistence

	// perms routes permission prompts from workers → SSE subscribers and back:
	// - Worker calls AwaitPermission which publishes an SSEEvent and blocks on ch.
	// - HTTP handler calls Approve which sends the optionID on ch.
	perms map[string]chan string // key = taskID|toolCallID

	// active maps (agent|cwd) → taskID so the permission hook can look up
	// which task a reverse-RPC belongs to. One slot per (agent, cwd) because
	// ACP sessions are single-threaded.
	active map[string]string
}

// taskEntry tracks a Task plus its SSE subscribers and cancel hook.
type taskEntry struct {
	task   *Task
	subs   map[int]chan SSEEvent
	next   int
	done   bool // closed: no further events will be emitted
	cancel func()

	// buffer is a bounded history of events emitted so far. New subscribers
	// receive the whole buffer before the first live event — this covers the
	// small window between POST /tasks returning and the client opening
	// /stream, during which an impatient agent can easily produce several
	// events (notably permission prompts) that would otherwise be lost.
	buffer []SSEEvent
}

// eventBufferCap caps per-task replay history. Plenty for the handshake +
// approval window; for long streaming turns, live subscribers see every
// event, and only the late-joiner's first N are capped.
const eventBufferCap = 256

// NewStore builds a Store with a work-queue capacity of cap tasks.
// db may be nil (no persistence).
func NewStore(cap int, db *sql.DB) *Store {
	if cap <= 0 {
		cap = 64
	}
	return &Store{
		tasks:  make(map[string]*taskEntry),
		queue:  make(chan string, cap),
		perms:  make(map[string]chan string),
		active: make(map[string]string),
		db:     db,
	}
}

// SetActive pins a taskID to (agent, cwd) for the duration of one ACP turn.
// The permission hook looks up this map so it can surface approvals on the
// right task's SSE channel.
func (s *Store) SetActive(agent, cwd, taskID string) {
	s.mu.Lock()
	s.active[agent+"|"+cwd] = taskID
	s.mu.Unlock()
}

// ClearActive releases the (agent, cwd) slot.
func (s *Store) ClearActive(agent, cwd string) {
	s.mu.Lock()
	delete(s.active, agent+"|"+cwd)
	s.mu.Unlock()
}

// ActiveTaskFor returns the current task claim for (agent, cwd), or "".
func (s *Store) ActiveTaskFor(agent, cwd string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[agent+"|"+cwd]
}

// Publish creates a new task, queues it for a worker, and returns the ID.
func (s *Store) Publish(agent, prompt, cwd string) (*Task, error) {
	if agent == "" || prompt == "" {
		return nil, errors.New("agent and prompt are required")
	}
	n := s.counter.Add(1)
	id := fmt.Sprintf("task-%d", n)
	now := time.Now().UTC()
	t := &Task{
		ID:        id,
		Agent:     agent,
		Prompt:    prompt,
		Cwd:       cwd,
		Status:    TaskPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	s.tasks[id] = &taskEntry{task: t, subs: make(map[int]chan SSEEvent)}
	// Snapshot the task under the lock so the caller gets a stable copy even
	// if the worker picks the task up immediately and mutates the live entry.
	snap := *t
	s.mu.Unlock()

	dbInsertTask(s.db, t)

	// Non-blocking send: if queue is full, reject rather than stall the HTTP
	// request. In practice 64 is plenty for hobby deployments.
	select {
	case s.queue <- id:
	default:
		s.Fail(id, "queue full")
		return nil, errors.New("queue full")
	}
	return &snap, nil
}

// Queue returns the channel workers pull task IDs from.
func (s *Store) Queue() <-chan string { return s.queue }

// Get returns a copy of the task.
func (s *Store) Get(id string) (*Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tasks[id]
	if !ok {
		return nil, false
	}
	cp := *e.task
	return &cp, true
}

// List returns a snapshot of all tasks.
func (s *Store) List() []*Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Task, 0, len(s.tasks))
	for _, e := range s.tasks {
		cp := *e.task
		out = append(out, &cp)
	}
	return out
}

// Subscribe opens an SSE subscription. The returned cancel must be called to
// release the subscriber slot. If the task is already done, the channel is
// closed immediately after any replayed events drain.
//
// Replay semantics: the subscriber receives every event currently in the
// task's replay buffer (up to eventBufferCap) before seeing live events.
// This closes the race between POST /tasks and GET /tasks/:id/stream, which
// otherwise lets early events (notably permission prompts) slip past.
func (s *Store) Subscribe(id string) (<-chan SSEEvent, func(), bool) {
	s.mu.Lock()
	e, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return nil, nil, false
	}
	e.next++
	subID := e.next
	// Buffer large enough to hold full replay + some live runway. Drops are
	// OK — a slow reader loses live events but still saw the terminal state.
	ch := make(chan SSEEvent, len(e.buffer)+64)
	for _, ev := range e.buffer {
		ch <- ev
	}
	if e.done {
		close(ch)
		s.mu.Unlock()
		// Nothing else to deliver — caller drains and returns.
		return ch, func() {}, true
	}
	e.subs[subID] = ch
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if cur, ok := s.tasks[id]; ok {
			if _, ok := cur.subs[subID]; ok {
				delete(cur.subs, subID)
				close(ch)
			}
		}
	}
	return ch, cancel, true
}

// Publishes an event to every current subscriber. Non-blocking — a slow
// subscriber drops events rather than stalling the worker. Events are also
// appended to the task's replay buffer so late subscribers can catch up.
func (s *Store) publish(id string, ev SSEEvent) {
	s.mu.Lock()
	e, ok := s.tasks[id]
	if !ok || e.done {
		s.mu.Unlock()
		return
	}
	e.buffer = append(e.buffer, ev)
	if len(e.buffer) > eventBufferCap {
		e.buffer = e.buffer[len(e.buffer)-eventBufferCap:]
	}
	subs := make([]chan SSEEvent, 0, len(e.subs))
	for _, c := range e.subs {
		subs = append(subs, c)
	}
	s.mu.Unlock()
	for _, c := range subs {
		select {
		case c <- ev:
		default:
		}
	}
}

// markStatus updates status/error and emits a "status" SSE event.
func (s *Store) markStatus(id string, status TaskStatus, errStr, result string) {
	s.mu.Lock()
	e, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	e.task.Status = status
	e.task.Error = errStr
	if result != "" {
		e.task.Result = result
	}
	e.task.UpdatedAt = time.Now().UTC()
	snap := *e.task
	s.mu.Unlock()
	dbUpdateTask(s.db, &snap)
	s.publish(id, SSEEvent{Type: "status", Data: snap})
}

// Start marks a task running and attaches a cancel hook for Cancel().
func (s *Store) Start(id string, cancel func()) {
	s.mu.Lock()
	if e, ok := s.tasks[id]; ok {
		e.cancel = cancel
	}
	s.mu.Unlock()
	s.markStatus(id, TaskRunning, "", "")
}

// Complete marks a task completed with the final assistant text.
func (s *Store) Complete(id, result string) {
	s.markStatus(id, TaskCompleted, "", result)
	s.finalize(id)
}

// Fail marks a task failed with the given error message.
func (s *Store) Fail(id, reason string) {
	s.markStatus(id, TaskFailed, reason, "")
	s.closePendingPermissions(id)
	s.finalize(id)
}

// Cancel invokes the task's cancel hook (if any). The worker will observe
// ctx.Err() and call Fail or Complete.
func (s *Store) Cancel(id string) bool {
	s.mu.Lock()
	e, ok := s.tasks[id]
	if !ok || e.cancel == nil {
		s.mu.Unlock()
		return false
	}
	cancel := e.cancel
	s.mu.Unlock()
	cancel()
	return true
}

// finalize closes all subscribers and marks the task done so no further
// events will be published.
func (s *Store) finalize(id string) {
	s.mu.Lock()
	e, ok := s.tasks[id]
	if !ok || e.done {
		s.mu.Unlock()
		return
	}
	e.done = true
	subs := e.subs
	e.subs = map[int]chan SSEEvent{}
	s.mu.Unlock()
	for _, c := range subs {
		close(c)
	}
}

// PushEvent broadcasts a provider StreamEvent to subscribers.
func (s *Store) PushEvent(id string, ev types.StreamEvent) {
	s.publish(id, SSEEvent{Type: "event", Data: streamEventPayload(ev)})
}

// AwaitPermission publishes a permission prompt and blocks until the matching
// Approve (or Cancel) resolves it. Returns the chosen optionID.
func (s *Store) AwaitPermission(id string, prompt PermissionPrompt) string {
	key := id + "|" + prompt.ToolCallID
	ch := make(chan string, 1)

	s.mu.Lock()
	s.perms[key] = ch
	s.mu.Unlock()

	s.publish(id, SSEEvent{Type: "permission", Data: prompt})

	opt := <-ch // closed channel → zero string, handled below
	s.mu.Lock()
	delete(s.perms, key)
	s.mu.Unlock()
	if opt == "" {
		// Default to the first reject option if one exists, else empty.
		for _, o := range prompt.Options {
			if o.Kind == "reject_once" || o.Kind == "reject_always" {
				return o.OptionID
			}
		}
	}
	return opt
}

// Approve delivers an optionID to an in-flight permission prompt.
func (s *Store) Approve(id, toolCallID, optionID string) bool {
	key := id + "|" + toolCallID
	s.mu.Lock()
	ch, ok := s.perms[key]
	s.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- optionID:
		return true
	default:
		return false
	}
}

// closePendingPermissions unblocks any AwaitPermission callers when a task is
// cancelled or the server is shutting down.
func (s *Store) closePendingPermissions(id string) {
	prefix := id + "|"
	s.mu.Lock()
	keys := make([]string, 0)
	for k := range s.perms {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		ch := s.perms[k]
		delete(s.perms, k)
		close(ch)
	}
	s.mu.Unlock()
}

// streamEventPayload renders a StreamEvent into a JSON-friendly map. The raw
// StreamEvent struct contains interface fields (ContentBlock) that aren't
// cleanly serializable, so we flatten to the bits a client cares about.
func streamEventPayload(ev types.StreamEvent) map[string]any {
	m := map[string]any{
		"type": ev.Type,
	}
	if ev.Delta != "" {
		m["delta"] = ev.Delta
	}
	if ev.Content != "" {
		m["content"] = ev.Content
	}
	if ev.Reason != "" {
		m["reason"] = ev.Reason
	}
	if ev.Error != nil {
		m["error"] = ev.Error.Error()
	}
	if ev.ToolCall != nil {
		m["toolCall"] = ev.ToolCall
	}
	return m
}
