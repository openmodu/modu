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

	// Resume only works from paused.
	if _, err := s.Resume(); err != nil {
		t.Fatalf("Resume from paused: %v", err)
	}
	if g, _ := s.Current(); g.Status != StatusActive {
		t.Errorf("after Resume want active, got %q", g.Status)
	}

	// Resume on active should fail.
	if _, err := s.Resume(); !errors.Is(err, ErrNotPaused) {
		t.Errorf("Resume on active want ErrNotPaused, got %v", err)
	}

	if _, err := s.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if g, _ := s.Current(); g.Status != StatusComplete {
		t.Errorf("after Complete want complete, got %q", g.Status)
	}
	if g, _ := s.Current(); g.CompletedAt == nil {
		t.Error("CompletedAt should be stamped after MarkComplete")
	}

	// Double complete forbidden.
	if _, err := s.MarkComplete(); !errors.Is(err, ErrAlreadyDone) {
		t.Errorf("second MarkComplete want ErrAlreadyDone, got %v", err)
	}

	// Pause on complete forbidden.
	if _, err := s.Pause(); !errors.Is(err, ErrAlreadyDone) {
		t.Errorf("Pause on complete want ErrAlreadyDone, got %v", err)
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
	if s.Summary() != "(no goal set)" {
		t.Errorf("empty Summary mismatch: %q", s.Summary())
	}
	s.Start("ship modu_cron v1")
	if !strings.Contains(s.Summary(), "ship modu_cron v1") ||
		!strings.Contains(s.Summary(), "active") {
		t.Errorf("Summary missing fields: %q", s.Summary())
	}
}

// TestConcurrentLifecycle stresses the store's mutex by running pause /
// resume / current pairs from many goroutines. The point isn't to validate
// any specific terminal state — we expect either active or paused at the
// end — but to catch races / deadlocks via the race detector when run with
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
