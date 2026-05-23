// Package scheduler wraps robfig/cron with modu_cron's Task type and a Runner
// hook so business logic (agent invocation) can be plugged in later.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

// Runner is invoked when a task fires. Returning an error is logged but does
// not stop the schedule from firing again.
type Runner func(ctx context.Context, task config.Task) error

// Scheduler owns a cron.Cron and the task→entry mapping.
type Scheduler struct {
	cron   *cron.Cron
	runner Runner

	mu      sync.Mutex
	entries map[string]cron.EntryID
}

// New builds a Scheduler. If runner is nil, fired tasks are logged but no
// business work runs — useful for the scaffold phase.
func New(runner Runner) *Scheduler {
	if runner == nil {
		runner = defaultRunner
	}
	return &Scheduler{
		cron:    cron.New(cron.WithSeconds()),
		runner:  runner,
		entries: make(map[string]cron.EntryID),
	}
}

// Add registers a task. The cron expression follows robfig/cron's 6-field
// format (with seconds) when WithSeconds is enabled.
func (s *Scheduler) Add(task config.Task) error {
	if !task.Enabled {
		return nil
	}
	if task.ID == "" {
		return fmt.Errorf("task missing id")
	}
	if task.Cron == "" {
		return fmt.Errorf("task %s: empty cron expression", task.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.entries[task.ID]; exists {
		return fmt.Errorf("task %s: already scheduled", task.ID)
	}
	id, err := s.cron.AddFunc(task.Cron, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := s.runner(ctx, task); err != nil {
			log.Printf("task %s: %v", task.ID, err)
		}
	})
	if err != nil {
		return fmt.Errorf("task %s: invalid cron %q: %w", task.ID, task.Cron, err)
	}
	s.entries[task.ID] = id
	return nil
}

// LoadAll registers every enabled task from cfg.
func (s *Scheduler) LoadAll(cfg *config.Config) error {
	for _, t := range cfg.Tasks {
		if err := s.Add(t); err != nil {
			return err
		}
	}
	return nil
}

// Start begins ticking; non-blocking.
func (s *Scheduler) Start() { s.cron.Start() }

// Stop halts firing and waits for in-flight runs to finish.
func (s *Scheduler) Stop() context.Context { return s.cron.Stop() }

func defaultRunner(_ context.Context, task config.Task) error {
	log.Printf("tick task=%s prompt=%q", task.ID, task.Prompt)
	return nil
}
