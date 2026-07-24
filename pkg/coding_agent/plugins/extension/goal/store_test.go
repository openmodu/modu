package goal

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestStartRejectsEmpty(t *testing.T) {
	_, err := NewStore().Start("")
	if !errors.Is(err, ErrEmptyObj) {
		t.Fatalf("want ErrEmptyObj, got %v", err)
	}
}

func TestStartRejectsTooLongObjective(t *testing.T) {
	_, err := NewStore().Start(strings.Repeat("x", MaxObjectiveLength+1))
	if err == nil {
		t.Fatal("expected too-long objective to fail")
	}
	if !strings.Contains(err.Error(), "objective too long") ||
		!strings.Contains(err.Error(), "docs/goal.md") {
		t.Fatalf("unexpected error for too-long objective: %v", err)
	}
}

func TestStartThenSecondStartRejected(t *testing.T) {
	s := NewStore()
	if _, err := s.Start("first"); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, err := s.Start("second")
	if !errors.Is(err, ErrGoalActive) {
		t.Fatalf("second Start should fail with ErrGoalActive, got %v", err)
	}
}

func TestLifecyclePauseResumeComplete(t *testing.T) {
	s := NewStore()
	g, _ := s.Start("write a haiku")
	if g.Status != StatusActive {
		t.Fatalf("new goal should be active, got %q", g.Status)
	}

	if _, err := s.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if g, _ := s.Current(); g.Status != StatusPaused {
		t.Errorf("after Pause want paused, got %q", g.Status)
	}

	if _, err := s.Resume(); err != nil {
		t.Fatalf("Resume from paused: %v", err)
	}
	if g, _ := s.Current(); g.Status != StatusActive {
		t.Errorf("after Resume want active, got %q", g.Status)
	}

	if _, err := s.Resume(); err != nil {
		t.Errorf("Resume on active should be a valid pi-goal status update, got %v", err)
	}

	if _, err := s.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if g, _ := s.Current(); g.Status != StatusComplete {
		t.Errorf("after Complete want complete, got %q", g.Status)
	}
	completed, _ := s.Current()
	if completed.CompletedAt == nil {
		t.Error("CompletedAt should be stamped after MarkComplete")
	}

	firstCompletedAt := *completed.CompletedAt
	if _, err := s.MarkComplete(); err != nil {
		t.Errorf("second MarkComplete should be idempotent, got %v", err)
	}
	if g, _ := s.Current(); g.CompletedAt == nil || *g.CompletedAt != firstCompletedAt {
		t.Errorf("second MarkComplete should preserve completedAt, got %+v", g.CompletedAt)
	}
	if _, err := s.Pause(); err != nil {
		t.Errorf("Pause on complete should be a valid pi-goal status update, got %v", err)
	}
	if g, _ := s.Current(); g.Status != StatusPaused || g.CompletedAt != nil {
		t.Errorf("Pause on complete should move to paused and clear completedAt, got %+v", g)
	}
}

func TestAccountUsageSplitsBreakdownAndExcludesCacheFromBudget(t *testing.T) {
	s := NewStore()
	budget := 500
	g, err := s.StartWithBudget("ship it", &budget)
	if err != nil {
		t.Fatalf("StartWithBudget: %v", err)
	}

	usage := types.AgentUsage{Input: 100, Output: 40, CacheRead: 900, CacheWrite: 10}
	if _, _, err := s.AccountUsage(usage, 0, false, g.ID); err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	if _, _, err := s.AccountUsage(usage, 0, false, g.ID); err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}

	got, _ := s.Current()
	// Budget counter is fresh Input+Output only: (100+40)*2 = 280. The 900
	// cache-read tokens per turn must not burn budget, so status stays active
	// well under the 500 budget instead of blowing past it.
	if got.TokensUsed != 280 {
		t.Errorf("TokensUsed = %d, want 280", got.TokensUsed)
	}
	if got.Status != StatusActive {
		t.Errorf("Status = %q, want active (cache must not count toward budget)", got.Status)
	}
	if got.InputTokens != 200 || got.OutputTokens != 80 {
		t.Errorf("input/output = %d/%d, want 200/80", got.InputTokens, got.OutputTokens)
	}
	if got.CacheReadTokens != 1800 || got.CacheWriteTokens != 20 {
		t.Errorf("cache read/write = %d/%d, want 1800/20", got.CacheReadTokens, got.CacheWriteTokens)
	}

	if split := goalTokenSplit(&got); !strings.Contains(split, "cache ") || !strings.Contains(split, "in ") {
		t.Errorf("split = %q, want in + cache", split)
	}
}

func TestCancelClearsStore(t *testing.T) {
	s := NewStore()
	s.Start("draft a PR")
	if _, err := s.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, ok := s.Current(); ok {
		t.Error("Current should report no goal after Cancel")
	}
	// After Cancel a new Start should succeed.
	if _, err := s.Start("a new one"); err != nil {
		t.Fatalf("Start after Cancel: %v", err)
	}
}

func TestOpsOnEmptyStore(t *testing.T) {
	s := NewStore()
	for name, fn := range map[string]func() (Goal, error){
		"Pause":        s.Pause,
		"Resume":       s.Resume,
		"Cancel":       s.Cancel,
		"MarkComplete": s.MarkComplete,
	} {
		if _, err := fn(); !errors.Is(err, ErrNoGoal) {
			t.Errorf("%s on empty store want ErrNoGoal, got %v", name, err)
		}
	}
}

func TestSummaryWithAndWithoutGoal(t *testing.T) {
	s := NewStore()
	if s.Summary() != "No active goal is set." {
		t.Errorf("empty Summary mismatch: %q", s.Summary())
	}
	budget := 2500
	if _, err := s.StartWithBudget("ship modu_cron v1", &budget); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AccountUsage(types.AgentUsage{Input: 1200, Output: 34}, 90, false, ""); err != nil {
		t.Fatal(err)
	}
	summary := s.Summary()
	for _, want := range []string{
		"● active",
		"ship modu_cron v1",
		"1.2K / 2.5K",
		"(49%)",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("Summary missing %q:\n%s", want, summary)
		}
	}
}

func TestGoalUsageSummaryMatchesPiGoal(t *testing.T) {
	budget := 50_000
	g := Goal{
		Objective:       "Port /goal as a pi extension",
		Status:          StatusActive,
		TokenBudget:     &budget,
		TokensUsed:      63_876,
		TimeUsedSeconds: 120,
	}
	if got, want := goalUsageSummary(g), "Objective: Port /goal as a pi extension Time: 2m. Tokens: 63.9K/50K."; got != want {
		t.Fatalf("goalUsageSummary() = %q, want %q", got, want)
	}
}

func TestFormatGoalForUserMatchesPiGoalCompletedTimestamp(t *testing.T) {
	completed := int64(1714525200)
	s := &Store{current: &Goal{
		ID:              "goal-local-time",
		Objective:       "check timezone",
		Status:          StatusComplete,
		CreatedAt:       1714521600,
		UpdatedAt:       completed,
		CompletedAt:     &completed,
		TimeUsedSeconds: 3600,
	}}
	got := s.Summary()
	if strings.Contains(got, "Started:") {
		t.Fatalf("pi-goal format should not include Started, got:\n%s", got)
	}
	if want := "2024-05-01T01:00:00Z"; !strings.Contains(got, want) {
		t.Fatalf("expected summary to contain completion timestamp %q, got:\n%s", want, got)
	}
	if !strings.Contains(got, "✓ complete") {
		t.Fatalf("expected completed goal to lead with ✓ complete, got:\n%s", got)
	}
}

// TestConcurrentLifecycle stresses the store's mutex by running pause /
// resume / current pairs from many goroutines. The point isn't to validate
// any specific terminal state - we expect either active or paused at the
// end - but to catch races / deadlocks via the race detector when run with
// `go test -race`.
func TestConcurrentLifecycle(t *testing.T) {
	s := NewStore()
	s.Start("concurrency probe")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Pause()
			_, _ = s.Current()
			_, _ = s.Resume()
		}()
	}
	wg.Wait()
	if _, ok := s.Current(); !ok {
		t.Fatal("goal should still exist after concurrent toggles")
	}
}

func TestFileBackedStorePersistsBySession(t *testing.T) {
	dir := t.TempDir()
	ref := StoreRef{BaseDir: dir, ThreadID: "thread/one"}
	store := NewStore()
	store.SetRefProvider(func() StoreRef { return ref })

	g, err := store.Start("persist this goal")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(GoalFilePath(ref)); err != nil {
		t.Fatalf("goal file not written: %v", err)
	}

	again := NewStore()
	again.SetRefProvider(func() StoreRef { return ref })
	got, ok := again.Current()
	if !ok {
		t.Fatal("persisted goal not loaded")
	}
	if got.ID != g.ID || got.Objective != "persist this goal" {
		t.Fatalf("persisted goal mismatch: %+v", got)
	}
}

func TestFileBackedStoreRejectsInvalidGoalSchema(t *testing.T) {
	dir := t.TempDir()
	ref := StoreRef{BaseDir: dir, ThreadID: "thread/invalid"}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{
  "version": 1,
  "goal": {
    "id": "bad",
    "objective": "broken",
    "status": "mystery",
    "tokensUsed": -1,
    "timeUsedSeconds": 0,
    "createdAt": 1,
    "updatedAt": 1
  }
}`
	if err := os.WriteFile(GoalFilePath(ref), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	store.SetRefProvider(func() StoreRef { return ref })
	if _, ok := store.Current(); ok {
		t.Fatal("invalid goal store should not load")
	}
	if _, err := store.Start("new goal"); err == nil || !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("expected invalid store error when mutating bad store, got %v", err)
	}
}

func TestCwdStoreKeyUsesStableHash(t *testing.T) {
	key := cwdStoreKey("/Users/ityike/Code/go/src/github.com/openmodu/modu")
	if len(key) != 24 {
		t.Fatalf("expected 24-char cwd key, got %q", key)
	}
	if strings.ContainsAny(key, `/\:`) {
		t.Fatalf("cwd key should be path-safe, got %q", key)
	}
	if key != cwdStoreKey("/Users/ityike/Code/go/src/github.com/openmodu/modu") {
		t.Fatal("cwd key should be stable")
	}
}

// TestResumeFromBudgetLimitedStaysBudgetLimited mirrors pi-goal's
// statusAfterExplicitStatusUpdate: a Resume against a goal that has already
// exhausted its token budget must keep it in budgetLimited, otherwise the
// model can paper over the budget gate by toggling status.
func TestResumeFromBudgetLimitedStaysBudgetLimited(t *testing.T) {
	store := NewStore()
	budget := 4
	g, err := store.StartWithBudget("tight budget", &budget)
	if err != nil {
		t.Fatalf("StartWithBudget: %v", err)
	}
	if _, _, err := store.AccountUsage(types.AgentUsage{Input: 2, Output: 3}, 0, false, g.ID); err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	got, _ := store.Current()
	if got.Status != StatusBudgetLimited {
		t.Fatalf("setup precondition: want budgetLimited after over-spend, got %q", got.Status)
	}
	resumed, err := store.Resume()
	if err != nil {
		t.Fatalf("Resume from budgetLimited: %v", err)
	}
	if resumed.Status != StatusBudgetLimited {
		t.Errorf("Resume should not escape budgetLimited while tokens are still over budget, got %q", resumed.Status)
	}
}

// TestReplaceObjectiveBudgetTransitions covers the two budget-edge paths the
// pi-goal updateGoal contract guarantees when the objective is unchanged:
// going from no-budget to a budget, and clearing a budget. (Objective
// changes always reset to a fresh goal with whatever budget was passed in;
// the unchanged-objective path is the one with non-obvious semantics.)
func TestReplaceObjectiveBudgetTransitions(t *testing.T) {
	store := NewStore()
	if _, err := store.Start("steady objective"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// nil -> budget set
	budget := 99
	g, err := store.ReplaceObjective("steady objective", &budget)
	if err != nil {
		t.Fatalf("set budget: %v", err)
	}
	if g.TokenBudget == nil || *g.TokenBudget != 99 {
		t.Fatalf("budget not applied: %+v", g.TokenBudget)
	}
	if g.Status != StatusActive {
		t.Errorf("status should remain active after budget set, got %q", g.Status)
	}

	// omitted budget preserves the current budget.
	g, err = store.ReplaceObjective("steady objective", nil)
	if err != nil {
		t.Fatalf("preserve budget: %v", err)
	}
	if g.TokenBudget == nil || *g.TokenBudget != 99 {
		t.Errorf("budget should be preserved, got %+v", g.TokenBudget)
	}
	if g.Status != StatusActive {
		t.Errorf("status should remain active after budget-preserving objective update, got %q", g.Status)
	}

	g, err = store.ClearTokenBudget()
	if err != nil {
		t.Fatalf("clear budget: %v", err)
	}
	if g.TokenBudget != nil {
		t.Errorf("budget should clear to nil, got %+v", g.TokenBudget)
	}
}

func TestTokenBudgetUpdateCanBudgetLimitActiveGoal(t *testing.T) {
	store := NewStore()
	budget := 100
	g, err := store.StartWithBudget("budget update", &budget)
	if err != nil {
		t.Fatalf("StartWithBudget: %v", err)
	}
	if _, _, err := store.AccountUsage(types.AgentUsage{Input: 50}, 1, false, g.ID); err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	g, err = store.SetTokenBudget(40)
	if err != nil {
		t.Fatalf("SetTokenBudget: %v", err)
	}
	if g.Status != StatusBudgetLimited || g.TokenBudget == nil || *g.TokenBudget != 40 {
		t.Fatalf("lowered budget should budget-limit active goal: %+v", g)
	}
}

// TestAccountUsageRejectsMismatchedGoalID guards the optimistic-locking path:
// if a stale handler tries to account a turn for a goal that has since been
// replaced, the new goal must not be silently mutated.
func TestAccountUsageRejectsMismatchedGoalID(t *testing.T) {
	store := NewStore()
	g, err := store.Start("first")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, ok, err := store.AccountUsage(types.AgentUsage{Input: 5, Output: 5}, 1, false, "stale-goal-id")
	if err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	if !ok {
		t.Fatal("expected current goal returned even on mismatch")
	}
	if got.ID != g.ID {
		t.Fatalf("returned goal ID = %q, want current %q", got.ID, g.ID)
	}
	// Tokens and time must NOT have been applied to the live goal.
	if got.TokensUsed != 0 || got.TimeUsedSeconds != 0 {
		t.Fatalf("stale accounting leaked into live goal: tokens=%d time=%d", got.TokensUsed, got.TimeUsedSeconds)
	}
}

func TestAccountUsageBudgetLimited(t *testing.T) {
	store := NewStore()
	budget := 10
	g, err := store.StartWithBudget("stay within budget", &budget)
	if err != nil {
		t.Fatalf("StartWithBudget: %v", err)
	}
	usage := types.AgentUsage{Input: 4, Output: 6}
	got, ok, err := store.AccountUsage(usage, 7, false, g.ID)
	if err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	if !ok {
		t.Fatal("expected goal after accounting")
	}
	if got.Status != StatusBudgetLimited {
		t.Fatalf("status = %q, want budgetLimited", got.Status)
	}
	if got.TokensUsed != 10 || got.TimeUsedSeconds != 7 {
		t.Fatalf("usage not accounted: %+v", got)
	}
}

func TestAccountUsageActiveOrStoppedCanPromotePausedGoalOverBudget(t *testing.T) {
	store := NewStore()
	budget := 20
	if _, err := store.StartWithBudget("stopped", &budget); err != nil {
		t.Fatalf("StartWithBudget: %v", err)
	}
	if _, err := store.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	activeOnly, ok, err := store.accountUsage(types.AgentUsage{Input: 25}, 3, accountActive, "")
	if err != nil {
		t.Fatalf("account active: %v", err)
	}
	if !ok {
		t.Fatal("expected paused goal returned")
	}
	if activeOnly.Status != StatusPaused || activeOnly.TokensUsed != 0 || activeOnly.TimeUsedSeconds != 0 {
		t.Fatalf("active mode should not account paused goals: %+v", activeOnly)
	}
	stopped, ok, err := store.accountUsage(types.AgentUsage{Input: 25}, 3, accountActiveOrStopped, "")
	if err != nil {
		t.Fatalf("account activeOrStopped: %v", err)
	}
	if !ok {
		t.Fatal("expected paused goal returned")
	}
	if stopped.Status != StatusBudgetLimited || stopped.TokensUsed != 25 || stopped.TimeUsedSeconds != 3 {
		t.Fatalf("activeOrStopped should account and budget-limit paused goal: %+v", stopped)
	}
}
