package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/cron/config"
)

func TestAddRejectsBadInput(t *testing.T) {
	s := New(nil)
	if err := s.Add(config.Task{ID: "", Cron: "* * * * * *", Enabled: true}); err == nil {
		t.Error("empty id should fail")
	}
	if err := s.Add(config.Task{ID: "x", Cron: "", Enabled: true}); err == nil {
		t.Error("empty cron should fail")
	}
	if err := s.Add(config.Task{ID: "x", Cron: "not a cron", Enabled: true}); err == nil {
		t.Error("invalid cron should fail")
	}
	if err := s.Add(config.Task{ID: "y", Cron: "* * * * * *", Enabled: false}); err != nil {
		t.Errorf("disabled task should be silently skipped, got: %v", err)
	}
	if err := s.Add(config.Task{ID: "tz", Cron: "* * * * * *", Timezone: "Not/AZone", Enabled: true}); err == nil {
		t.Error("invalid timezone should fail")
	}
}

func TestTaskCronExpressionAppliesTimezone(t *testing.T) {
	task := config.Task{ID: "sh", Cron: "0 20 10 * * 1-5", Timezone: "Asia/Shanghai"}
	got, err := taskCronExpression(task)
	if err != nil {
		t.Fatalf("taskCronExpression: %v", err)
	}
	if got != "CRON_TZ=Asia/Shanghai 0 20 10 * * 1-5" {
		t.Fatalf("cron expression=%q", got)
	}
	if err := ValidateTaskCron(task); err != nil {
		t.Fatalf("ValidateTaskCron: %v", err)
	}
}

func TestTaskCronExpressionRejectsDuplicateTimezone(t *testing.T) {
	task := config.Task{ID: "tz", Cron: "CRON_TZ=UTC 0 20 10 * * 1-5", Timezone: "Asia/Shanghai"}
	if _, err := taskCronExpression(task); err == nil {
		t.Fatal("expected duplicate timezone error")
	}
}

func TestNextAppliesTaskTimezone(t *testing.T) {
	from := time.Date(2026, 7, 4, 2, 5, 0, 0, time.FixedZone("CST", 8*60*60))
	task := config.Task{ID: "market", Cron: "0 20 10 * * 1-5", Timezone: "Asia/Shanghai"}
	got, err := Next(task, from)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := time.Date(2026, 7, 6, 10, 20, 0, 0, time.FixedZone("CST", 8*60*60))
	if !got.Equal(want) {
		t.Fatalf("Next=%s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestNextRejectsInvalidTaskCron(t *testing.T) {
	task := config.Task{ID: "bad", Cron: "not a cron", Timezone: "Asia/Shanghai"}
	if _, err := Next(task, time.Now()); err == nil {
		t.Fatal("expected invalid cron error")
	}
}

// TestSkipPolicyDropsOverlap drives onTick directly (no real cron) to verify
// the skip policy drops overlapping calls without serializing them.
func TestSkipPolicyDropsOverlap(t *testing.T) {
	var (
		runs    int32
		release = make(chan struct{})
	)
	s := New(func(ctx context.Context, _ config.Task) error {
		atomic.AddInt32(&runs, 1)
		<-release
		return nil
	})
	task := config.Task{ID: "t", Cron: "* * * * * *", Enabled: true, OnOverlap: config.OverlapSkip}
	if err := s.Add(task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	state := s.tasks["t"]

	go s.onTick(state) // first run: blocks on release
	waitFor(t, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.running
	})

	for i := 0; i < 5; i++ {
		s.onTick(state) // all should be dropped
	}
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Errorf("runs=%d during overlap; want 1 (others dropped)", got)
	}

	close(release)
	waitFor(t, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return !state.running
	})

	// After clean exit, the next tick should run again.
	atomic.StoreInt32(&runs, 0)
	released2 := make(chan struct{})
	s2runner := func(ctx context.Context, _ config.Task) error {
		atomic.AddInt32(&runs, 1)
		close(released2)
		return nil
	}
	s.runner = s2runner
	s.onTick(state)
	<-released2
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Errorf("runs after clear=%d; want 1", got)
	}
}

func TestQueuePolicySerializes(t *testing.T) {
	var runs int32
	var mu sync.Mutex
	var order []int32
	gate := make(chan struct{})

	s := New(func(ctx context.Context, _ config.Task) error {
		n := atomic.AddInt32(&runs, 1)
		mu.Lock()
		order = append(order, n)
		mu.Unlock()
		<-gate
		return nil
	})
	task := config.Task{ID: "q", Cron: "* * * * * *", Enabled: true, OnOverlap: config.OverlapQueue}
	if err := s.Add(task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	state := s.tasks["q"]

	go s.onTick(state) // first run starts immediately
	waitFor(t, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.running
	})

	s.onTick(state) // enqueued
	s.onTick(state) // enqueued

	// Release runs one by one.
	for i := 0; i < 3; i++ {
		gate <- struct{}{}
	}
	close(gate)

	waitFor(t, func() bool {
		return atomic.LoadInt32(&runs) >= 3
	})
	if got := atomic.LoadInt32(&runs); got != 3 {
		t.Errorf("runs=%d; want 3 (1 immediate + 2 queued)", got)
	}
}

func TestTickPassesGoalTaskToRunner(t *testing.T) {
	got := make(chan config.Task, 1)
	s := New(func(ctx context.Context, task config.Task) error {
		got <- task
		return nil
	})
	task := config.Task{
		ID:      "goal-task",
		Cron:    "* * * * * *",
		Prompt:  "run loop",
		Goal:    "verify loop completion",
		Enabled: true,
	}
	if err := s.Add(task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.onTick(s.tasks["goal-task"])

	select {
	case fired := <-got:
		if fired.Goal != task.Goal {
			t.Fatalf("Goal=%q, want %q", fired.Goal, task.Goal)
		}
		if fired.Prompt != task.Prompt {
			t.Fatalf("Prompt=%q, want %q", fired.Prompt, task.Prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not receive fired task")
	}
}

// waitFor polls cond up to 2s; fails the test on timeout.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true within 2s")
}
