package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
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
