// Package goal is a /goal extension for modu's CodingSession. It keeps one
// session-scoped objective and drives hidden continuation turns until the
// objective is complete, paused, cleared, or limited by an explicit token
// budget.
package goal

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/openmodu/modu/pkg/types"
)

const storeVersion = 1
const MaxObjectiveLength = 4000

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
	ID          string `json:"id"`
	ThreadID    string `json:"threadId"`
	Objective   string `json:"objective"`
	Status      Status `json:"status"`
	TokenBudget *int   `json:"tokenBudget,omitempty"`
	// TokensUsed is the budget counter: fresh Input + Output, excluding
	// cache reads/writes. The breakdown fields below are display-only
	// accumulators and are not compared against TokenBudget.
	TokensUsed       int    `json:"tokensUsed"`
	InputTokens      int    `json:"inputTokens,omitempty"`
	OutputTokens     int    `json:"outputTokens,omitempty"`
	CacheReadTokens  int    `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int    `json:"cacheWriteTokens,omitempty"`
	TimeUsedSeconds  int64  `json:"timeUsedSeconds"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
	LastStartedAt    *int64 `json:"lastStartedAt,omitempty"`
	CompletedAt      *int64 `json:"completedAt,omitempty"`
	// VerifierRejects counts consecutive completion claims the independent
	// goal verifier has rejected. Reset whenever the goal (re)enters active
	// via an explicit status update, so a user resume grants a fresh round.
	VerifierRejects int `json:"verifierRejects,omitempty"`
}

type goalFile struct {
	Version int   `json:"version"`
	Goal    *Goal `json:"goal"`
}

type accountingMode string

const (
	accountActiveStatusOnly accountingMode = "activeStatusOnly"
	accountActive           accountingMode = "active"
	accountActiveOrComplete accountingMode = "activeOrComplete"
	accountActiveOrStopped  accountingMode = "activeOrStopped"
)

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

// Errors returned by Store methods. The sentinels stay deliberately tiny:
// pi-goal's updateGoal flow is idempotent (Pause from Complete clears, Resume
// from Active stays active, MarkComplete is reapplied without error), so the
// "wrong-state" sentinels that earlier MVP drafts carried have no producers
// and are not surfaced here.
var (
	ErrGoalActive    = errors.New("goal: a goal is already active; pause or clear it first")
	ErrNoGoal        = errors.New("goal: no goal is set")
	ErrEmptyObj      = errors.New("goal: objective must not be empty")
	ErrInvalidBudget = errors.New("goal: token budget must be a positive integer")
	ErrInvalidStore  = errors.New("goal: invalid goal store")
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

// ReplaceObjective mirrors pi-goal updateGoal({objective}): it creates a
// fresh active goal when the objective changes, resumes a matching nonterminal
// goal, and preserves an existing budget unless a new budget is explicitly
// provided.
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

	nextTokenBudget := current.TokenBudget
	if tokenBudget != nil {
		nextTokenBudget = cloneIntPtr(tokenBudget)
	}
	previousStatus := current.Status
	nextStatus := statusAfterExplicitStatusUpdate(current.Status, StatusActive, current.TokensUsed, nextTokenBudget)
	current.Status = nextStatus
	current.TokenBudget = cloneIntPtr(nextTokenBudget)
	current.UpdatedAt = now
	if nextStatus == StatusActive {
		if previousStatus != StatusActive {
			current.LastStartedAt = &now
			current.VerifierRejects = 0
		}
	} else {
		current.LastStartedAt = nil
	}
	if nextStatus == StatusComplete {
		if current.CompletedAt == nil {
			current.CompletedAt = &now
		}
	} else {
		current.CompletedAt = nil
	}
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

// SetTokenBudget updates the current goal's token budget without changing the
// objective or forcing a status transition other than budget limiting.
func (s *Store) SetTokenBudget(tokenBudget int) (Goal, error) {
	if err := validateTokenBudget(&tokenBudget); err != nil {
		return Goal{}, err
	}
	return s.updateTokenBudget(&tokenBudget, false)
}

// ClearTokenBudget removes the current goal's token budget.
func (s *Store) ClearTokenBudget() (Goal, error) {
	return s.updateTokenBudget(nil, true)
}

func (s *Store) updateTokenBudget(tokenBudget *int, clear bool) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, err
	}
	if current == nil {
		return Goal{}, ErrNoGoal
	}
	nextTokenBudget := current.TokenBudget
	if clear {
		nextTokenBudget = nil
	} else if tokenBudget != nil {
		nextTokenBudget = cloneIntPtr(tokenBudget)
	}
	now := nowSeconds()
	current.Status = statusAfterBudgetUpdate(current.Status, current.TokensUsed, nextTokenBudget)
	current.TokenBudget = cloneIntPtr(nextTokenBudget)
	current.UpdatedAt = now
	if current.Status != StatusActive {
		current.LastStartedAt = nil
	}
	if current.Status != StatusComplete {
		current.CompletedAt = nil
	}
	if err := s.writeLocked(current); err != nil {
		return Goal{}, err
	}
	return *current, nil
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
	now := nowSeconds()
	previousStatus := current.Status
	nextStatus := statusAfterExplicitStatusUpdate(current.Status, status, current.TokensUsed, current.TokenBudget)
	current.Status = nextStatus
	current.UpdatedAt = now
	if nextStatus == StatusActive {
		if previousStatus != StatusActive {
			current.LastStartedAt = &now
			current.VerifierRejects = 0
		}
	} else {
		current.LastStartedAt = nil
	}
	if nextStatus == StatusComplete {
		if current.CompletedAt == nil {
			current.CompletedAt = &now
		}
	} else {
		current.CompletedAt = nil
	}
	if err := s.writeLocked(current); err != nil {
		return Goal{}, err
	}
	return *current, nil
}

// IncrementVerifierRejects bumps the consecutive verifier-reject counter on
// the current goal and returns the updated goal.
func (s *Store) IncrementVerifierRejects() (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, err
	}
	if current == nil {
		return Goal{}, ErrNoGoal
	}
	current.VerifierRejects++
	current.UpdatedAt = nowSeconds()
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
	return s.updateStatus(StatusComplete)
}

// AccountUsage adds one agent turn's usage to the active goal.
func (s *Store) AccountUsage(usage types.AgentUsage, elapsedSeconds int64, includeComplete bool, expectedGoalID string) (Goal, bool, error) {
	mode := accountActive
	if includeComplete {
		mode = accountActiveOrComplete
	}
	return s.accountUsage(usage, elapsedSeconds, mode, expectedGoalID)
}

func (s *Store) accountUsage(usage types.AgentUsage, elapsedSeconds int64, mode accountingMode, expectedGoalID string) (Goal, bool, error) {
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
	if !canAccountUsage(current.Status, mode) {
		return *current, true, nil
	}
	if elapsedSeconds < 0 {
		elapsedSeconds = 0
	}
	current.TokensUsed += tokenDelta(usage)
	current.InputTokens += max(usage.Input, 0)
	current.OutputTokens += max(usage.Output, 0)
	current.CacheReadTokens += max(usage.CacheRead, 0)
	current.CacheWriteTokens += max(usage.CacheWrite, 0)
	current.TimeUsedSeconds += elapsedSeconds
	current.UpdatedAt = nowSeconds()
	current.Status = statusAfterAccounting(current.Status, current.TokensUsed, current.TokenBudget, mode)
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
	g, ok, err := s.CurrentErr()
	if err != nil {
		return Goal{}, false
	}
	return g, ok
}

// CurrentErr returns the current goal and propagates file/schema errors.
func (s *Store) CurrentErr() (Goal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return Goal{}, false, err
	}
	if current == nil {
		return Goal{}, false, nil
	}
	return *current, true, nil
}

// Summary returns a human-readable description for slash command output.
func (s *Store) Summary() string {
	g, ok := s.Current()
	if !ok {
		return "No active goal is set."
	}
	return FormatGoalForUser(&g)
}

func FormatGoalForUser(g *Goal) string {
	if g == nil {
		return "No active goal is set."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", goalStatusIcon(g.Status), goalStatusLabel(g.Status))
	if g.Objective != "" {
		fmt.Fprintf(&b, "\n  %s", g.Objective)
	}
	b.WriteString("\n")

	if g.TokenBudget != nil {
		pct := 0
		if *g.TokenBudget > 0 {
			pct = int(math.Round(100 * float64(g.TokensUsed) / float64(*g.TokenBudget)))
		}
		fmt.Fprintf(&b, "\n%s%s  %s / %s  (%d%%)", goalMetricLabel("Tokens"),
			goalProgressBar(g.TokensUsed, *g.TokenBudget),
			formatTokensCompact(g.TokensUsed), formatTokensCompact(*g.TokenBudget), pct)
	} else {
		fmt.Fprintf(&b, "\n%s%s", goalMetricLabel("Tokens"), formatTokensCompact(g.TokensUsed))
	}
	fmt.Fprintf(&b, "\n%s%s", goalMetricLabel("Time"), formatElapsed(g.TimeUsedSeconds))
	if split := goalTokenSplit(g); split != "" {
		fmt.Fprintf(&b, "\n%s%s", goalMetricLabel("Split"), split)
	}
	if g.CompletedAt != nil && *g.CompletedAt != 0 {
		fmt.Fprintf(&b, "\n%s%s", goalMetricLabel("Done"), time.Unix(*g.CompletedAt, 0).UTC().Format(time.RFC3339))
	}
	return b.String()
}

// goalMetricLabel renders an aligned, indented label column so the metric
// values line up under one another.
func goalMetricLabel(label string) string {
	return fmt.Sprintf("  %-6s  ", label)
}

// goalProgressBar renders a fixed-width usage bar. It clamps at full so an
// over-budget (limited) goal still shows a solid bar rather than overflowing.
func goalProgressBar(used, budget int) string {
	const cells = 10
	filled := 0
	if budget > 0 {
		filled = int(math.Round(float64(cells) * float64(used) / float64(budget)))
	}
	if filled < 0 {
		filled = 0
	}
	if filled > cells {
		filled = cells
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", cells-filled) + "]"
}

// goalTokenSplit renders the input/output/cache breakdown as a compact
// dot-separated value. Returns "" when nothing has been accounted yet (e.g.
// goals created before the breakdown fields existed, whose accumulators read
// back as zero).
func goalTokenSplit(g *Goal) string {
	if g.InputTokens == 0 && g.OutputTokens == 0 && g.CacheReadTokens == 0 && g.CacheWriteTokens == 0 {
		return ""
	}
	parts := []string{
		"in " + formatTokensCompact(g.InputTokens),
		"out " + formatTokensCompact(g.OutputTokens),
	}
	if g.CacheReadTokens > 0 {
		parts = append(parts, "cache "+formatTokensCompact(g.CacheReadTokens))
	}
	if g.CacheWriteTokens > 0 {
		parts = append(parts, "cache-w "+formatTokensCompact(g.CacheWriteTokens))
	}
	return strings.Join(parts, " · ")
}

func goalUsageSummary(g Goal) string {
	parts := []string{"Objective: " + g.Objective}
	if g.TimeUsedSeconds > 0 {
		parts = append(parts, "Time: "+formatElapsed(g.TimeUsedSeconds)+".")
	}
	if g.TokenBudget != nil {
		parts = append(parts, "Tokens: "+formatTokensCompact(g.TokensUsed)+"/"+formatTokensCompact(*g.TokenBudget)+".")
	}
	return strings.Join(parts, " ")
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
	if file.Goal != nil {
		if err := validateGoalFields(file.Goal); err != nil {
			return nil, err
		}
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
	if n := utf8.RuneCountInString(objective); n > MaxObjectiveLength {
		return "", fmt.Errorf("goal: objective too long (%d chars, limit %d) — put longer instructions in a file and refer to it, e.g. /goal follow docs/goal.md", n, MaxObjectiveLength)
	}
	return objective, nil
}

func validateGoalFields(g *Goal) error {
	if g == nil {
		return nil
	}
	if strings.TrimSpace(g.ID) == "" {
		return fmt.Errorf("%w: id must not be empty", ErrInvalidStore)
	}
	if g.ThreadID == "" {
		return fmt.Errorf("%w: threadId must be a string", ErrInvalidStore)
	}
	if _, err := validateObjective(g.Objective); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidStore, err)
	}
	switch g.Status {
	case StatusActive, StatusPaused, StatusBudgetLimited, StatusComplete:
	default:
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidStore, g.Status)
	}
	if g.TokenBudget != nil && *g.TokenBudget <= 0 {
		return fmt.Errorf("%w: tokenBudget must be positive", ErrInvalidStore)
	}
	if g.TokensUsed < 0 {
		return fmt.Errorf("%w: tokensUsed must be non-negative", ErrInvalidStore)
	}
	if g.TimeUsedSeconds < 0 {
		return fmt.Errorf("%w: timeUsedSeconds must be non-negative", ErrInvalidStore)
	}
	if g.CreatedAt < 0 {
		return fmt.Errorf("%w: createdAt must be non-negative", ErrInvalidStore)
	}
	if g.UpdatedAt < 0 {
		return fmt.Errorf("%w: updatedAt must be non-negative", ErrInvalidStore)
	}
	if g.LastStartedAt != nil && *g.LastStartedAt < 0 {
		return fmt.Errorf("%w: lastStartedAt must be non-negative", ErrInvalidStore)
	}
	if g.CompletedAt != nil && *g.CompletedAt < 0 {
		return fmt.Errorf("%w: completedAt must be non-negative", ErrInvalidStore)
	}
	if g.VerifierRejects < 0 {
		return fmt.Errorf("%w: verifierRejects must be non-negative", ErrInvalidStore)
	}
	return nil
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

func statusAfterExplicitStatusUpdate(currentStatus, requestedStatus Status, tokensUsed int, tokenBudget *int) Status {
	if currentStatus == StatusBudgetLimited && requestedStatus == StatusPaused {
		return StatusBudgetLimited
	}
	return statusAfterBudgetLimit(requestedStatus, tokensUsed, tokenBudget)
}

func statusAfterBudgetUpdate(currentStatus Status, tokensUsed int, tokenBudget *int) Status {
	if currentStatus == StatusActive {
		return statusAfterBudgetLimit(currentStatus, tokensUsed, tokenBudget)
	}
	return currentStatus
}

func statusAfterAccounting(status Status, tokensUsed int, tokenBudget *int, mode accountingMode) Status {
	if tokenBudget == nil || tokensUsed < *tokenBudget {
		return status
	}
	switch mode {
	case accountActiveStatusOnly, accountActive, accountActiveOrComplete:
		if status == StatusActive {
			return StatusBudgetLimited
		}
	case accountActiveOrStopped:
		if status == StatusActive || status == StatusPaused || status == StatusBudgetLimited {
			return StatusBudgetLimited
		}
	}
	return status
}

func canAccountUsage(status Status, mode accountingMode) bool {
	switch mode {
	case accountActiveStatusOnly:
		return status == StatusActive
	case accountActive:
		return status == StatusActive || status == StatusBudgetLimited
	case accountActiveOrComplete:
		return status == StatusActive || status == StatusBudgetLimited || status == StatusComplete
	case accountActiveOrStopped:
		return status == StatusActive || status == StatusPaused || status == StatusBudgetLimited
	default:
		return false
	}
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

func cwdStoreKey(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "unknown"
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(cwd)))[:24]
}

func formatTokensCompact(v int) string {
	abs := math.Abs(float64(v))
	if abs < 1000 {
		return fmt.Sprintf("%d", v)
	}
	if abs < 1_000_000 {
		return formatCompactFloat(float64(v)/1000, "K")
	}
	return formatCompactFloat(float64(v)/1_000_000, "M")
}

func formatCompactFloat(v float64, suffix string) string {
	rounded := fmt.Sprintf("%.1f", v)
	rounded = strings.TrimSuffix(rounded, ".0")
	return rounded + suffix
}

func formatElapsed(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
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
