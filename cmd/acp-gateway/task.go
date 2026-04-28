package main

import (
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openmodu/modu/pkg/acp/client"
	"github.com/openmodu/modu/pkg/types"
)

// TurnStatus is the lifecycle state of a single turn.
type TurnStatus string

const (
	TurnPending   TurnStatus = "pending"
	TurnRunning   TurnStatus = "running"
	TurnCompleted TurnStatus = "completed"
	TurnFailed    TurnStatus = "failed"
)

// Turn is one prompt/response cycle within a Session.
type Turn struct {
	ID        string     `json:"id"`
	SessionID string     `json:"sessionId"`
	Agent     string     `json:"agent"`
	Cwd       string     `json:"cwd"`
	Prompt    string     `json:"prompt"`
	Result    string     `json:"result,omitempty"`
	Error     string     `json:"error,omitempty"`
	Status    TurnStatus `json:"status"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// SSEEvent is one frame sent over a turn's /stream endpoint.
type SSEEvent struct {
	Type string `json:"-"`
	Data any    `json:"data"`
}

// bufferedEvent is an SSEEvent recorded with a timestamp for the events history endpoint.
type bufferedEvent struct {
	At   time.Time `json:"time"`
	Type string    `json:"type"`
	Data any       `json:"data"`
}

// PermissionPrompt is surfaced to SSE clients when an agent needs approval.
type PermissionPrompt struct {
	ToolCallID string                    `json:"toolCallId"`
	Title      string                    `json:"title"`
	Kind       string                    `json:"kind"`
	Options    []client.PermissionOption `json:"options"`
}

// turnEntry is the in-memory record for a Turn.
type turnEntry struct {
	turn   *Turn
	subs   map[int]chan SSEEvent
	next   int
	done   bool
	cancel func()
	buffer []bufferedEvent
}

// TurnFilter is used by ListTurns to filter results.
type TurnFilter struct {
	Status    string // "pending" | "running" | "completed" | "failed" | "" (all)
	Agent     string
	SessionID string
	Limit     int // 0 = no limit
}

const eventBufferCap = 256

// Store owns projects, sessions, turns, the work queue, and permission channels.
// All methods are safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	pctr atomic.Uint64 // project ID counter
	sctr atomic.Uint64 // session ID counter
	tctr atomic.Uint64 // turn ID counter

	projects map[string]*Project
	sessions map[string]*sessionEntry
	turns    map[string]*turnEntry

	queue  chan string        // turn IDs ready to execute
	perms  map[string]chan string // key = turnID|toolCallID
	active map[string]string    // agentID|cwd → turnID (for permission routing)

	db *sql.DB // nil = no persistence
}

// NewStore builds a Store with a work-queue capacity of cap turns.
func NewStore(cap int, db *sql.DB) *Store {
	if cap <= 0 {
		cap = 64
	}
	return &Store{
		projects: make(map[string]*Project),
		sessions: make(map[string]*sessionEntry),
		turns:    make(map[string]*turnEntry),
		queue:    make(chan string, cap),
		perms:    make(map[string]chan string),
		active:   make(map[string]string),
		db:       db,
	}
}

// Queue returns the channel workers pull turn IDs from.
func (s *Store) Queue() <-chan string { return s.queue }

// GetTurn returns a copy of the turn.
func (s *Store) GetTurn(id string) (*Turn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.turns[id]
	if !ok {
		return nil, false
	}
	cp := *e.turn
	return &cp, true
}

// SetActive pins a turnID to (agentID, cwd) for permission routing.
func (s *Store) SetActive(agentID, cwd, turnID string) {
	s.mu.Lock()
	s.active[agentID+"|"+cwd] = turnID
	s.mu.Unlock()
}

// ClearActive releases the (agentID, cwd) slot.
func (s *Store) ClearActive(agentID, cwd string) {
	s.mu.Lock()
	delete(s.active, agentID+"|"+cwd)
	s.mu.Unlock()
}

// ActiveTurnFor returns the current turn for (agentID, cwd), or "".
func (s *Store) ActiveTurnFor(agentID, cwd string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[agentID+"|"+cwd]
}

// Subscribe opens an SSE subscription on a turn. Buffered past events are
// replayed immediately so clients joining late don't miss events.
func (s *Store) Subscribe(turnID string) (<-chan SSEEvent, func(), bool) {
	s.mu.Lock()
	e, ok := s.turns[turnID]
	if !ok {
		s.mu.Unlock()
		return nil, nil, false
	}
	e.next++
	subID := e.next
	ch := make(chan SSEEvent, len(e.buffer)+64)
	for _, be := range e.buffer {
		ch <- SSEEvent{Type: be.Type, Data: be.Data}
	}
	if e.done {
		close(ch)
		s.mu.Unlock()
		return ch, func() {}, true
	}
	e.subs[subID] = ch
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if cur, ok := s.turns[turnID]; ok {
			if _, ok := cur.subs[subID]; ok {
				delete(cur.subs, subID)
				close(ch)
			}
		}
	}
	return ch, cancel, true
}

// Events returns the timestamped event history for a turn (read-only snapshot).
func (s *Store) Events(turnID string) ([]bufferedEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.turns[turnID]
	if !ok {
		return nil, false
	}
	out := make([]bufferedEvent, len(e.buffer))
	copy(out, e.buffer)
	return out, true
}

func (s *Store) publishEvent(turnID string, ev SSEEvent) {
	s.mu.Lock()
	e, ok := s.turns[turnID]
	if !ok || e.done {
		s.mu.Unlock()
		return
	}
	be := bufferedEvent{At: time.Now().UTC(), Type: ev.Type, Data: ev.Data}
	e.buffer = append(e.buffer, be)
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

// ListTurns returns turns matching the filter, newest first.
func (s *Store) ListTurns(f TurnFilter) []*Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Turn, 0, len(s.turns))
	for _, te := range s.turns {
		t := te.turn
		if f.Status != "" && string(t.Status) != f.Status {
			continue
		}
		if f.Agent != "" && t.Agent != f.Agent {
			continue
		}
		if f.SessionID != "" && t.SessionID != f.SessionID {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	// Sort newest first.
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out
}

// CancelTurn cancels a specific turn by calling its cancel hook.
func (s *Store) CancelTurn(turnID string) bool {
	s.mu.Lock()
	te, ok := s.turns[turnID]
	if !ok || te.turn.Status != TurnRunning || te.cancel == nil {
		s.mu.Unlock()
		return false
	}
	cancel := te.cancel
	s.mu.Unlock()
	cancel()
	return true
}

// AgentStats returns activity counters for a given agent.
func (s *Store) AgentStats(agentID string) (activeTurns, totalSessions, totalTurns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, se := range s.sessions {
		if se.session.Agent == agentID {
			totalSessions++
			totalTurns += len(se.turns)
		}
	}
	for _, te := range s.turns {
		if te.turn.Agent == agentID && te.turn.Status == TurnRunning {
			activeTurns++
		}
	}
	return
}

func (s *Store) markTurnStatus(id string, status TurnStatus, errStr, result string) {
	s.mu.Lock()
	e, ok := s.turns[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	e.turn.Status = status
	e.turn.Error = errStr
	if result != "" {
		e.turn.Result = result
	}
	e.turn.UpdatedAt = time.Now().UTC()
	snap := *e.turn
	s.mu.Unlock()

	dbUpdateTurn(s.db, &snap)
	s.publishEvent(id, SSEEvent{Type: "status", Data: snap})
}

// StartTurn marks a turn running and attaches its cancel hook.
func (s *Store) StartTurn(id string, cancel func()) {
	s.mu.Lock()
	if e, ok := s.turns[id]; ok {
		e.cancel = cancel
	}
	// Mark parent session running.
	if e, ok := s.turns[id]; ok {
		if se, ok := s.sessions[e.turn.SessionID]; ok {
			se.session.Status = SessionRunning
			se.session.UpdatedAt = time.Now().UTC()
		}
	}
	s.mu.Unlock()
	s.markTurnStatus(id, TurnRunning, "", "")
}

// CompleteTurn marks a turn completed with its final result.
func (s *Store) CompleteTurn(id, result string) {
	s.markTurnStatus(id, TurnCompleted, "", result)
	s.finalizeTurn(id)
	s.setSessionIdle(id)
}

// FailTurn marks a turn failed.
func (s *Store) FailTurn(id, reason string) {
	s.markTurnStatus(id, TurnFailed, reason, "")
	s.closePendingPermissions(id)
	s.finalizeTurn(id)
	s.setSessionIdle(id)
}

func (s *Store) finalizeTurn(id string) {
	s.mu.Lock()
	e, ok := s.turns[id]
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

func (s *Store) setSessionIdle(turnID string) {
	s.mu.Lock()
	e, ok := s.turns[turnID]
	if !ok {
		s.mu.Unlock()
		return
	}
	sessID := e.turn.SessionID
	se, ok := s.sessions[sessID]
	if ok {
		se.session.Status = SessionIdle
		se.session.UpdatedAt = time.Now().UTC()
	}
	var snap Session
	if ok {
		snap = *se.session
	}
	s.mu.Unlock()
	if ok {
		dbUpdateSession(s.db, &snap)
	}
}

// PushEvent broadcasts a provider StreamEvent to turn subscribers.
func (s *Store) PushEvent(turnID string, ev types.StreamEvent) {
	s.publishEvent(turnID, SSEEvent{Type: "event", Data: streamEventPayload(ev)})
}

// AwaitPermission publishes a permission prompt and blocks until resolved.
func (s *Store) AwaitPermission(turnID string, prompt PermissionPrompt) string {
	key := turnID + "|" + prompt.ToolCallID
	ch := make(chan string, 1)

	s.mu.Lock()
	s.perms[key] = ch
	s.mu.Unlock()

	s.publishEvent(turnID, SSEEvent{Type: "permission", Data: prompt})

	opt := <-ch
	s.mu.Lock()
	delete(s.perms, key)
	s.mu.Unlock()
	if opt == "" {
		for _, o := range prompt.Options {
			if o.Kind == "reject_once" || o.Kind == "reject_always" {
				return o.OptionID
			}
		}
	}
	return opt
}

// Approve delivers an optionID to an in-flight permission prompt.
func (s *Store) Approve(turnID, toolCallID, optionID string) bool {
	key := turnID + "|" + toolCallID
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

func (s *Store) closePendingPermissions(turnID string) {
	prefix := turnID + "|"
	s.mu.Lock()
	var keys []string
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

func streamEventPayload(ev types.StreamEvent) map[string]any {
	m := map[string]any{"type": ev.Type}
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

// parseProjectSeq extracts the numeric suffix from "proj-N".
func parseProjectSeq(id string) uint64 {
	var n uint64
	fmt.Sscanf(id, "proj-%d", &n)
	return n
}

// parseSessionSeq extracts the numeric suffix from "sess-N".
func parseSessionSeq(id string) uint64 {
	var n uint64
	fmt.Sscanf(id, "sess-%d", &n)
	return n
}

// parseTurnSeq extracts the numeric suffix from "turn-N".
func parseTurnSeq(id string) uint64 {
	var n uint64
	fmt.Sscanf(id, "turn-%d", &n)
	return n
}
