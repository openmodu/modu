// Package goal is a /goal extension for modu's CodingSession — a port of
// pi-goal's persistent long-horizon driver (github.com/code-yeongyu/pi-goal)
// for the modu extension API.
//
// One Store holds at most one Goal at a time. The CodingSession owns one
// Store via its extension, and only commands routed through that Store can
// transition the state machine. Concurrent agent_end events and CLI slash
// commands are serialized by a single mutex; transitions are validated, so
// "start while already active" or "pause when complete" surface as errors
// instead of silent overwrites.
package goal

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status mirrors pi-goal's GoalStatus, minus budgetLimited — that lives
// behind a token-budget feature we deliberately don't ship in MVP.
type Status string

const (
	StatusActive   Status = "active"
	StatusPaused   Status = "paused"
	StatusComplete Status = "complete"
)

// Goal is one persistent objective the agent is driving toward. Times are
// UTC. The struct is value-copied out of Store so callers can read freely
// without holding the mutex.
type Goal struct {
	ID          string
	Objective   string
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// Store holds the single active Goal (if any). The zero value is usable.
type Store struct {
	mu      sync.Mutex
	current *Goal
}

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{} }

// Errors returned by Store methods — exported so callers (extension command
// handlers, tools) can map them to user-facing messages.
var (
	ErrGoalActive   = errors.New("goal: a goal is already active; pause or cancel it first")
	ErrNoGoal       = errors.New("goal: no goal is set")
	ErrNotActive    = errors.New("goal: goal is not active")
	ErrNotPaused    = errors.New("goal: goal is not paused")
	ErrEmptyObj     = errors.New("goal: objective must not be empty")
	ErrAlreadyDone  = errors.New("goal: goal is already complete")
)

// Start creates a new goal. Refuses if any goal already exists in the store
// (active/paused/complete) — caller must Cancel first. This matches the
// "one goal at a time, last one wins only if explicitly dropped" semantics
// requested for MVP.
func (s *Store) Start(objective string) (Goal, error) {
	if objective == "" {
		return Goal{}, ErrEmptyObj
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		return Goal{}, ErrGoalActive
	}
	now := time.Now().UTC()
	g := &Goal{
		ID:        uuid.NewString(),
		Objective: objective,
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.current = g
	return *g, nil
}

// Pause moves an active goal to paused. No-op error on already-paused so the
// CLI surface is forgiving; only fails if there's no goal or it's complete.
func (s *Store) Pause() (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return Goal{}, ErrNoGoal
	}
	if s.current.Status == StatusComplete {
		return Goal{}, ErrAlreadyDone
	}
	s.current.Status = StatusPaused
	s.current.UpdatedAt = time.Now().UTC()
	return *s.current, nil
}

// Resume moves a paused goal back to active.
func (s *Store) Resume() (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return Goal{}, ErrNoGoal
	}
	if s.current.Status != StatusPaused {
		return Goal{}, ErrNotPaused
	}
	s.current.Status = StatusActive
	s.current.UpdatedAt = time.Now().UTC()
	return *s.current, nil
}

// Cancel removes the current goal regardless of status. Returns the removed
// goal so the caller can echo a confirmation.
func (s *Store) Cancel() (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return Goal{}, ErrNoGoal
	}
	g := *s.current
	s.current = nil
	return g, nil
}

// MarkComplete transitions an active or paused goal to complete and stamps
// CompletedAt. The update_goal tool calls this when the model decides it's
// done.
func (s *Store) MarkComplete() (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return Goal{}, ErrNoGoal
	}
	if s.current.Status == StatusComplete {
		return Goal{}, ErrAlreadyDone
	}
	now := time.Now().UTC()
	s.current.Status = StatusComplete
	s.current.UpdatedAt = now
	s.current.CompletedAt = &now
	return *s.current, nil
}

// Current returns a value copy of the goal (or zero+false if none). Safe
// to call concurrently with mutations.
func (s *Store) Current() (Goal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return Goal{}, false
	}
	return *s.current, true
}

// Summary returns a one-paragraph description suitable for printing back to
// the user or feeding to the model via get_goal.
func (s *Store) Summary() string {
	g, ok := s.Current()
	if !ok {
		return "(no goal set)"
	}
	out := fmt.Sprintf("Goal %s — status=%s\nObjective: %s\nStarted: %s",
		g.ID[:8], g.Status, g.Objective, g.CreatedAt.Format(time.RFC3339))
	if g.CompletedAt != nil {
		out += fmt.Sprintf("\nCompleted: %s", g.CompletedAt.Format(time.RFC3339))
	}
	return out
}
