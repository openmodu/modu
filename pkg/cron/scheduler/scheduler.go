// Package scheduler wraps robfig/cron with modu_cron's Task type, a Runner
// hook for business logic, and per-task overlap policies (skip/queue/kill).
package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/openmodu/modu/pkg/cron/config"
)

// Runner is invoked when a task fires. Returning an error is logged but does
// not stop the schedule from firing again.
type Runner func(ctx context.Context, task config.Task) error

// exprParser matches the parser used by cron.New(cron.WithSeconds()), so
// ValidateCron rejects exactly what a real scheduler Add would reject.
var exprParser = cron.NewParser(
	cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ValidateCron returns nil if expr is a legal 6-field cron expression for
// this scheduler. Useful to validate user input before persisting it.
func ValidateCron(expr string) error {
	_, err := exprParser.Parse(expr)
	return err
}

// ValidateTaskCron returns nil if task's cron expression and optional
// timezone produce a schedule this scheduler can register.
func ValidateTaskCron(task config.Task) error {
	expr, err := taskCronExpression(task)
	if err != nil {
		return err
	}
	_, err = exprParser.Parse(expr)
	return err
}

// Next returns the next fire time for task after from, applying the same cron
// parser and optional task Timezone that Scheduler.Add uses.
func Next(task config.Task, from time.Time) (time.Time, error) {
	expr, err := taskCronExpression(task)
	if err != nil {
		return time.Time{}, err
	}
	schedule, err := exprParser.Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(from), nil
}

// Scheduler owns a cron.Cron and one runState per registered task.
type Scheduler struct {
	cron   *cron.Cron
	runner Runner

	mu    sync.Mutex
	tasks map[string]*runState
}

// runState tracks one task's in-flight execution and overlap accounting.
type runState struct {
	task config.Task

	mu            sync.Mutex
	running       bool
	cancel        context.CancelFunc // non-nil while running
	queue         chan struct{}      // only used by OverlapQueue
	overlapStreak int                // consecutive overlap events; reset on clean tick

	// overlapWarnAt is the streak count at which we warn about
	// "frequency too high for task duration". Re-warn every N.
	overlapWarnAt int
}

const defaultOverlapWarnEvery = 3

// New builds a Scheduler. If runner is nil, fired tasks are logged but no
// business work runs — useful for the scaffold phase.
func New(runner Runner) *Scheduler {
	if runner == nil {
		runner = defaultRunner
	}
	return &Scheduler{
		cron:   cron.New(cron.WithSeconds()),
		runner: runner,
		tasks:  make(map[string]*runState),
	}
}

// Add registers a task. The cron expression follows robfig/cron's 6-field
// format (with seconds). Disabled tasks are skipped silently.
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
	if err := task.ValidateCaps(); err != nil {
		return err
	}
	cronExpr, err := taskCronExpression(task)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if _, exists := s.tasks[task.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("task %s: already scheduled", task.ID)
	}
	state := &runState{
		task:          task,
		overlapWarnAt: defaultOverlapWarnEvery,
	}
	if task.Policy() == config.OverlapQueue {
		state.queue = make(chan struct{}, config.QueueCapacity)
		go s.drainQueue(state)
	}
	s.tasks[task.ID] = state
	s.mu.Unlock()

	if _, err := s.cron.AddFunc(cronExpr, func() { s.onTick(state) }); err != nil {
		s.mu.Lock()
		delete(s.tasks, task.ID)
		s.mu.Unlock()
		return fmt.Errorf("task %s: invalid cron %q: %w", task.ID, cronExpr, err)
	}
	return nil
}

func taskCronExpression(task config.Task) (string, error) {
	expr := strings.TrimSpace(task.Cron)
	tz := strings.TrimSpace(task.Timezone)
	if tz == "" {
		return expr, nil
	}
	if strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(expr, "TZ=") {
		return "", fmt.Errorf("task %s: use timezone field instead of embedding TZ in cron expression", task.ID)
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", fmt.Errorf("task %s: invalid timezone %q: %w", task.ID, task.Timezone, err)
	}
	return "CRON_TZ=" + tz + " " + expr, nil
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

// Stop halts firing and waits for in-flight runs to finish. Queued runs
// (OverlapQueue) are dropped after Stop returns.
func (s *Scheduler) Stop() context.Context { return s.cron.Stop() }

// onTick is fired by cron at each scheduled instant. It applies the task's
// overlap policy and either starts execution, enqueues, or drops it.
func (s *Scheduler) onTick(state *runState) {
	state.mu.Lock()
	policy := state.task.Policy()
	if !state.running {
		state.running = true
		state.overlapStreak = 0
		state.mu.Unlock()
		s.execute(state, nil)
		return
	}

	// Overlap path.
	state.overlapStreak++
	streak := state.overlapStreak
	warnEvery := state.overlapWarnAt
	state.mu.Unlock()

	if streak > 0 && streak%warnEvery == 0 {
		log.Printf("task %s: %d consecutive overlaps — cron frequency may exceed task duration", state.task.ID, streak)
	}

	switch policy {
	case config.OverlapQueue:
		select {
		case state.queue <- struct{}{}:
		default:
			log.Printf("task %s: queue full (cap=%d), dropping tick", state.task.ID, config.QueueCapacity)
		}
	case config.OverlapKill:
		state.mu.Lock()
		cancel := state.cancel
		state.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		// The previous execute() will finish, then we start the new one.
		// To avoid races we let drainAndRun pick it up.
		go s.runAfterPrevious(state)
	default: // OverlapSkip
		log.Printf("task %s: previous run still in flight, skipping tick", state.task.ID)
	}
}

// runAfterPrevious waits for the running flag to clear, then starts a fresh
// run. Used by OverlapKill to chain a new run after cancelling the old one.
func (s *Scheduler) runAfterPrevious(state *runState) {
	for {
		state.mu.Lock()
		if !state.running {
			state.running = true
			state.overlapStreak = 0
			state.mu.Unlock()
			s.execute(state, nil)
			return
		}
		state.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
}

// drainQueue serializes queued ticks (OverlapQueue policy). One goroutine per
// task, lives for the lifetime of the scheduler.
func (s *Scheduler) drainQueue(state *runState) {
	for range state.queue {
		// Wait for the running flag to clear.
		for {
			state.mu.Lock()
			if !state.running {
				state.running = true
				state.mu.Unlock()
				break
			}
			state.mu.Unlock()
			time.Sleep(50 * time.Millisecond)
		}
		s.execute(state, nil)
	}
}

// execute runs the runner. clearOverlap controls whether overlap streak is
// reset on completion (always reset on clean entry; not relevant on exit).
func (s *Scheduler) execute(state *runState, _ context.Context) {
	// The per-run wall-clock cap lives in runner.Execute (task.EffectiveTimeout);
	// here we only need a cancel handle for the kill overlap policy.
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	state.cancel = cancel
	state.mu.Unlock()

	defer func() {
		cancel()
		state.mu.Lock()
		state.cancel = nil
		state.running = false
		state.mu.Unlock()
	}()

	if err := s.runner(ctx, state.task); err != nil {
		log.Printf("task %s: %v", state.task.ID, err)
	}
}

func defaultRunner(_ context.Context, task config.Task) error {
	log.Printf("tick task=%s prompt=%q", task.ID, task.Prompt)
	return nil
}
