package subagent

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/extension"
)

// batchTask is a top-level parallel/chain/tasks call that the extension is
// running in the background. The host's BackgroundTasks() only sees per-
// child ForkSession invocations, so we maintain our own pool for the batch
// layer and merge it into status / doctor output.
type batchTask struct {
	ID        string
	Mode      string // "parallel" | "chain" | "tasks"
	Status    string // "running" | "completed" | "failed"
	Output    string
	Error     string
	CreatedAt int64
	UpdatedAt int64
}

// batchTaskRegistry holds the extension-owned batch tasks. It's safe for
// concurrent use — both tool dispatch (which adds entries) and status
// rendering (which lists them) can run concurrently.
type batchTaskRegistry struct {
	mu     sync.RWMutex
	nextID atomic.Int64
	tasks  map[string]*batchTask
}

func newBatchTaskRegistry() *batchTaskRegistry {
	return &batchTaskRegistry{tasks: map[string]*batchTask{}}
}

// reserve generates a fresh batch task ID and records it as running.
// Callers run the actual work in a goroutine and call complete() or fail()
// when done.
func (r *batchTaskRegistry) reserve(mode string) *batchTask {
	id := fmt.Sprintf("subagent-batch-%d", r.nextID.Add(1))
	now := time.Now().UnixMilli()
	task := &batchTask{
		ID:        id,
		Mode:      mode,
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.mu.Lock()
	r.tasks[id] = task
	r.mu.Unlock()
	return task
}

func (r *batchTaskRegistry) complete(id, output string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task, ok := r.tasks[id]; ok {
		task.Status = "completed"
		task.Output = output
		task.UpdatedAt = time.Now().UnixMilli()
	}
}

func (r *batchTaskRegistry) fail(id string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task, ok := r.tasks[id]; ok {
		task.Status = "failed"
		task.Error = err.Error()
		task.UpdatedAt = time.Now().UnixMilli()
	}
}

// snapshots returns the batch tasks as TaskSnapshot entries so they can be
// merged with the host's snapshot list. Sorted by ID for stable rendering.
func (r *batchTaskRegistry) snapshots() []extension.TaskSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.tasks) == 0 {
		return nil
	}
	out := make([]extension.TaskSnapshot, 0, len(r.tasks))
	for _, task := range r.tasks {
		out = append(out, extension.TaskSnapshot{
			ID:      task.ID,
			Kind:    "subagent",
			Status:  task.Status,
			Summary: fmt.Sprintf("%s batch", task.Mode),
			Output:  task.Output,
			Error:   task.Error,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// shouldBatchAsync inspects the args to decide whether a top-level
// parallel/tasks/chain call should be dispatched as a single background
// batch. Caller-set async:true always wins; ForceTopLevelAsync covers the
// "omitted async" path. Returns the effective decision and whether the
// caller passed async explicitly (so we never auto-async when async:false
// was set).
func shouldBatchAsync(ext *Extension, args map[string]any, mode string) bool {
	if mode != "parallel" && mode != "chain" {
		return false
	}
	if v, ok := args["async"].(bool); ok {
		return v
	}
	return ext != nil && ext.cfg.ForceTopLevelAsync
}

// dispatchBatchAsync reserves a batch task, launches the dispatch in a
// goroutine using a background-rooted context (so the goroutine survives
// tool.Execute returning), and returns a synthetic "task_id=..." reply the
// caller can pass to action=status later. Uses runParallel/runChain
// unchanged — we are just choosing when to wait on them.
func dispatchBatchAsync(ctx context.Context, ext *Extension, mode string, args map[string]any) (string, error) {
	if ext == nil || ext.batchTasks == nil {
		return "", fmt.Errorf("batch async unavailable: extension is not initialized")
	}
	task := ext.batchTasks.reserve(mode)
	// Use a background-rooted context so the goroutine survives Execute
	// returning. Take cancellation cues from the parent only by snapshotting
	// what it needs before we detach (we don't, today — runParallel/runChain
	// only read args).
	go func() {
		bgCtx := context.Background()
		var (
			out string
			err error
		)
		switch mode {
		case "parallel":
			out, err = runParallel(bgCtx, ext, args)
		case "chain":
			out, err = runChain(bgCtx, ext, args)
		default:
			err = fmt.Errorf("unsupported batch mode %q", mode)
		}
		if err != nil {
			ext.batchTasks.fail(task.ID, err)
			return
		}
		out = appendIncludedProgress(ext, args, out)
		ext.batchTasks.complete(task.ID, out)
	}()
	_ = ctx // currently unused; batch async detaches from the parent ctx
	return fmt.Sprintf("Started subagent %s batch in background. Use action=status with task_id=%s to inspect the result.", mode, task.ID), nil
}

// mergeBatchTasks appends the extension's batch tasks to the host-provided
// snapshot list, after de-duplicating by ID. The host should never know
// about batch tasks so the dedup is mostly defensive.
func mergeBatchTasks(host []extension.TaskSnapshot, batch []extension.TaskSnapshot) []extension.TaskSnapshot {
	if len(batch) == 0 {
		return host
	}
	seen := make(map[string]bool, len(host))
	for _, task := range host {
		seen[task.ID] = true
	}
	out := append([]extension.TaskSnapshot(nil), host...)
	for _, task := range batch {
		if !seen[task.ID] {
			out = append(out, task)
		}
	}
	return out
}

