// Package goal is a /goal extension for modu's CodingSession. It keeps one
// session-scoped objective and drives hidden continuation turns until the
// objective is complete, paused, cleared, or limited by an explicit token
// budget.
package goal

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openmodu/modu/pkg/types"
)

const storeVersion = 1

// Status mirrors pi-goal's GoalStatus.
type Status string

const (
	StatusActive        Status = "active"
	StatusPaused        Status = "paused"
	StatusBudgetLimited Status = "budgetLimited"
	StatusComplete      Status = "complete"
)

// StoreRef points at the file-backed goal store for one session.
type StoreRef struct {
	BaseDir  string
	ThreadID string
}

// Goal is one persistent objective the agent is driving toward. Timestamps are
// Unix seconds so the on-disk shape stays close to pi-goal.
type Goal struct {
	ID              string `json:"id"`
	ThreadID        string `json:"threadId,omitempty"`
	Objective       string `json:"objective"`
	Status          Status `json:"status"`
	TokenBudget     *int   `json:"tokenBudget,omitempty"`
	TokensUsed      int    `json:"tokensUsed"`
	TimeUsedSeconds int64  `json:"timeUsedSeconds"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	LastStartedAt   *int64 `json:"lastStartedAt,omitempty"`
	CompletedAt     *int64 `json:"completedAt,omitempty"`
}

type goalFile struct {
	Version int   `json:"version"`
	Goal    *Goal `json:"goal"`
}

// Store holds the current Goal. Without a ref provider it is purely in-memory,
// which keeps unit tests and embedded SDK users lightweight. With a ref
// provider every operation reads and writes a session-scoped JSON file.
type Store struct {
	mu          sync.Mutex
	current     *Goal
	refProvider func() StoreRef
}

// NewStore returns an empty Store.
func NewStore() *Store { return &Store{} }

// SetRefProvider enables file-backed persistence. Existing in-memory state is
// not migrated; callers should configure this during extension initialization.
func (s *Store) SetRefProvider(fn func() StoreRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refProvider = fn
}

// Errors returned by Store methods.
var (
	ErrGoalActive    = errors.New("goal: a goal is already active; pause or clear it first")
	ErrNoGoal        = errors.New("goal: no goal is set")
	ErrNotActive     = errors.New("goal: goal is not active")
	ErrNotPaused     = errors.New("goal: goal is not paused")
	ErrEmptyObj      = errors.New("goal: objective must not be empty")
	ErrAlreadyDone   = errors.New("goal: goal is already complete")
	ErrInvalidBudget = errors.New("goal: token budget must be a positive integer")
)

// GoalFilePath returns the JSON file path for a store ref.
func GoalFilePath(ref StoreRef) string {
	return filepath.Join(ref.BaseDir, url.PathEscape(ref.ThreadID)+".json")
}

// Start creates a new active goal without a token budget.
func (s *Store) Start(objective string) (Goal, error) {
	return s.StartWithBudget(objective, nil)
}

// StartWithBudget creates a new active goal with an optional token budget.
func (s *Store) StartWithBudget(objective string, tokenBudget *int) (Goal, error) {
	objective, err := validateObjective(objective)
	if err != nil {
		return Goal{}, err
	}
	if err := validateTokenBudget(tokenBudget); err != nil {
		return Goal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, err
	}
	if current != nil {
		return Goal{}, ErrGoalActive
	}
	now := nowSeconds()
	g := &Goal{
		ID:              uuid.NewString(),
		ThreadID:        s.threadIDLocked(),
		Objective:       objective,
		Status:          StatusActive,
		TokenBudget:     cloneIntPtr(tokenBudget),
		TokensUsed:      0,
		TimeUsedSeconds: 0,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastStartedAt:   &now,
	}
	g.Status = statusAfterBudgetLimit(g.Status, g.TokensUsed, g.TokenBudget)
	if g.Status != StatusActive {
		g.LastStartedAt = nil
	}
	if err := s.writeLocked(g); err != nil {
		return Goal{}, err
	}
	return *g, nil
}

// ReplaceObjective creates a fresh active goal when the objective changes, or
// updates the current active goal's budget when the objective is unchanged.
func (s *Store) ReplaceObjective(objective string, tokenBudget *int) (Goal, error) {
	objective, err := validateObjective(objective)
	if err != nil {
		return Goal{}, err
	}
	if err := validateTokenBudget(tokenBudget); err != nil {
		return Goal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, err
	}
	if current == nil {
		now := nowSeconds()
		g := &Goal{
			ID:              uuid.NewString(),
			ThreadID:        s.threadIDLocked(),
			Objective:       objective,
			Status:          StatusActive,
			TokenBudget:     cloneIntPtr(tokenBudget),
			TokensUsed:      0,
			TimeUsedSeconds: 0,
			CreatedAt:       now,
			UpdatedAt:       now,
			LastStartedAt:   &now,
		}
		if err := s.writeLocked(g); err != nil {
			return Goal{}, err
		}
		return *g, nil
	}

	now := nowSeconds()
	replacesGoal := current.Objective != objective || current.Status == StatusComplete
	if replacesGoal {
		g := &Goal{
			ID:              uuid.NewString(),
			ThreadID:        s.threadIDLocked(),
			Objective:       objective,
			Status:          StatusActive,
			TokenBudget:     cloneIntPtr(tokenBudget),
			TokensUsed:      0,
			TimeUsedSeconds: 0,
			CreatedAt:       now,
			UpdatedAt:       now,
			LastStartedAt:   &now,
		}
		g.Status = statusAfterBudgetLimit(g.Status, g.TokensUsed, g.TokenBudget)
		if g.Status != StatusActive {
			g.LastStartedAt = nil
		}
		if err := s.writeLocked(g); err != nil {
			return Goal{}, err
		}
		return *g, nil
	}

	current.Status = statusAfterBudgetLimit(StatusActive, current.TokensUsed, tokenBudget)
	current.TokenBudget = cloneIntPtr(tokenBudget)
	current.UpdatedAt = now
	if current.Status == StatusActive {
		current.LastStartedAt = &now
	} else {
		current.LastStartedAt = nil
	}
	current.CompletedAt = nil
	if err := s.writeLocked(current); err != nil {
		return Goal{}, err
	}
	return *current, nil
}

// Pause moves an active goal to paused.
func (s *Store) Pause() (Goal, error) {
	return s.updateStatus(StatusPaused)
}

// Resume moves a paused goal back to active unless its token budget is already
// exhausted, in which case it remains budget-limited.
func (s *Store) Resume() (Goal, error) {
	return s.updateStatus(StatusActive)
}

func (s *Store) updateStatus(status Status) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, err
	}
	if current == nil {
		return Goal{}, ErrNoGoal
	}
	if current.Status == StatusComplete {
		return Goal{}, ErrAlreadyDone
	}
	if status == StatusActive && current.Status != StatusPaused && current.Status != StatusBudgetLimited {
		return Goal{}, ErrNotPaused
	}
	now := nowSeconds()
	nextStatus := statusAfterBudgetLimit(status, current.TokensUsed, current.TokenBudget)
	if current.Status == StatusBudgetLimited && status == StatusPaused {
		nextStatus = StatusBudgetLimited
	}
	current.Status = nextStatus
	current.UpdatedAt = now
	if nextStatus == StatusActive {
		current.LastStartedAt = &now
	} else {
		current.LastStartedAt = nil
	}
	if err := s.writeLocked(current); err != nil {
		return Goal{}, err
	}
	return *current, nil
}

// Cancel removes the current goal regardless of status.
func (s *Store) Cancel() (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, err
	}
	if current == nil {
		return Goal{}, ErrNoGoal
	}
	g := *current
	if err := s.writeLocked(nil); err != nil {
		return Goal{}, err
	}
	return g, nil
}

// MarkComplete transitions the current goal to complete and stamps CompletedAt.
func (s *Store) MarkComplete() (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, err
	}
	if current == nil {
		return Goal{}, ErrNoGoal
	}
	if current.Status == StatusComplete {
		return Goal{}, ErrAlreadyDone
	}
	now := nowSeconds()
	current.Status = StatusComplete
	current.UpdatedAt = now
	current.CompletedAt = &now
	current.LastStartedAt = nil
	if err := s.writeLocked(current); err != nil {
		return Goal{}, err
	}
	return *current, nil
}

// AccountUsage adds one agent turn's usage to the active goal.
func (s *Store) AccountUsage(usage types.AgentUsage, elapsedSeconds int64, includeComplete bool, expectedGoalID string) (Goal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, false, err
	}
	if current == nil {
		return Goal{}, false, nil
	}
	if expectedGoalID != "" && current.ID != expectedGoalID {
		return *current, true, nil
	}
	if !canAccountUsage(current.Status, includeComplete) {
		return *current, true, nil
	}
	if elapsedSeconds < 0 {
		elapsedSeconds = 0
	}
	current.TokensUsed += tokenDelta(usage)
	current.TimeUsedSeconds += elapsedSeconds
	current.UpdatedAt = nowSeconds()
	current.Status = statusAfterAccounting(current.Status, current.TokensUsed, current.TokenBudget)
	if current.Status == StatusBudgetLimited {
		current.LastStartedAt = nil
	}
	if err := s.writeLocked(current); err != nil {
		return Goal{}, false, err
	}
	return *current, true, nil
}

// Current returns a value copy of the goal.
func (s *Store) Current() (Goal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil || current == nil {
		return Goal{}, false
	}
	return *current, true
}

// Summary returns a human-readable description for slash command output.
func (s *Store) Summary() string {
	g, ok := s.Current()
	if !ok {
		return "(no goal set)"
	}
	out := fmt.Sprintf("Goal %s - status=%s\nObjective: %s\nTime used: %s\nTokens used: %d",
		shortID(g.ID), g.Status, g.Objective, formatElapsed(g.TimeUsedSeconds), g.TokensUsed)
	if g.TokenBudget != nil {
		out += fmt.Sprintf("/%d", *g.TokenBudget)
	}
	out += fmt.Sprintf("\nStarted: %s", formatGoalTimestamp(g.CreatedAt))
	if g.CompletedAt != nil {
		out += fmt.Sprintf("\nCompleted: %s", formatGoalTimestamp(*g.CompletedAt))
	}
	return out
}

func (s *Store) readLocked() (*Goal, error) {
	ref, ok := s.refLocked()
	if !ok {
		return cloneGoalPtr(s.current), nil
	}
	raw, err := os.ReadFile(GoalFilePath(ref))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var file goalFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	if file.Version != storeVersion {
		return nil, fmt.Errorf("goal: unsupported store version %d", file.Version)
	}
	return cloneGoalPtr(file.Goal), nil
}

func (s *Store) writeLocked(goal *Goal) error {
	ref, ok := s.refLocked()
	if !ok {
		s.current = cloneGoalPtr(goal)
		return nil
	}
	if err := os.MkdirAll(ref.BaseDir, 0o755); err != nil {
		return err
	}
	if goal != nil && goal.ThreadID == "" {
		goal.ThreadID = ref.ThreadID
	}
	data, err := json.MarshalIndent(goalFile{Version: storeVersion, Goal: goal}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(GoalFilePath(ref), append(data, '\n'), 0o600)
}

func (s *Store) refLocked() (StoreRef, bool) {
	if s.refProvider == nil {
		return StoreRef{}, false
	}
	ref := s.refProvider()
	if ref.BaseDir == "" || ref.ThreadID == "" {
		return StoreRef{}, false
	}
	return ref, true
}

func (s *Store) threadIDLocked() string {
	if ref, ok := s.refLocked(); ok {
		return ref.ThreadID
	}
	return ""
}

func validateObjective(objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", ErrEmptyObj
	}
	return objective, nil
}

func validateTokenBudget(tokenBudget *int) error {
	if tokenBudget != nil && *tokenBudget <= 0 {
		return ErrInvalidBudget
	}
	return nil
}

func statusAfterBudgetLimit(status Status, tokensUsed int, tokenBudget *int) Status {
	if status == StatusActive && tokenBudget != nil && tokensUsed >= *tokenBudget {
		return StatusBudgetLimited
	}
	return status
}

func statusAfterAccounting(status Status, tokensUsed int, tokenBudget *int) Status {
	if tokenBudget == nil || tokensUsed < *tokenBudget {
		return status
	}
	if status == StatusActive || status == StatusBudgetLimited {
		return StatusBudgetLimited
	}
	return status
}

func canAccountUsage(status Status, includeComplete bool) bool {
	if status == StatusActive || status == StatusBudgetLimited {
		return true
	}
	return includeComplete && status == StatusComplete
}

func tokenDelta(usage types.AgentUsage) int {
	if usage.Input < 0 {
		usage.Input = 0
	}
	if usage.Output < 0 {
		usage.Output = 0
	}
	return usage.Input + usage.Output
}

func cloneGoalPtr(g *Goal) *Goal {
	if g == nil {
		return nil
	}
	cp := *g
	cp.TokenBudget = cloneIntPtr(g.TokenBudget)
	cp.LastStartedAt = cloneInt64Ptr(g.LastStartedAt)
	cp.CompletedAt = cloneInt64Ptr(g.CompletedAt)
	return &cp
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func cloneInt64Ptr(v *int64) *int64 {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func nowSeconds() int64 {
	return time.Now().Unix()
}

func formatGoalTimestamp(seconds int64) string {
	return time.Unix(seconds, 0).Local().Format(time.RFC3339)
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func formatElapsed(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	remainingMinutes := minutes % 60
	if hours >= 24 {
		days := hours / 24
		remainingHours := hours % 24
		return fmt.Sprintf("%dd %dh %dm", days, remainingHours, remainingMinutes)
	}
	if remainingMinutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, remainingMinutes)
}
