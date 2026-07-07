// Package runner wires the scheduler's Runner hook to a CodingSession.
//
// Each tick gets a fresh session — no cross-tick memory. The agent's event
// stream is filtered through summaryWriter and written to a per-run log
// file as a slim NDJSON transcript:
//
//	{"type":"run_start",     "task_id":..., "prompt":..., "trigger":..., "timezone":..., "has_goal":..., "goal_verifier":..., "started_at":...}
//	{"type":"session_start", "session_id":..., "model":...}
//	{"type":"user",          "text":...}
//	{"type":"tool_call",     "name":..., "args":...}
//	{"type":"tool_result",   "name":..., "ok":..., "snippet":...}
//	{"type":"assistant",     "text":...}
//	{"type":"run_end",       "status":..., "duration_ms":..., "ended_at":..., "error":...}
//
// run_end is always written — even on failure — so callers can use it as a
// reliable "tick finished" marker.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	goalext "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/goal"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/runlog"
)

// debugRawEnv, when set to a non-empty value, makes Execute tee the raw
// coding_agent NDJSON event stream into a sibling `.raw.log` file alongside
// the slim transcript. Diagnostic only — lets you see exactly which events
// upstream emitted vs. what summaryWriter kept.
const debugRawEnv = "MODU_CRON_DEBUG_RAW"

// Run statuses recorded in run_end and Result.Status. Everything except
// StatusOK also carries an error; only StatusError is retryable — the cap
// statuses are circuit breakers, and retrying a tripped breaker defeats it.
const (
	StatusOK              = "ok"
	StatusError           = "error"
	StatusTimeout         = "timeout"
	StatusTokenCap        = "token_cap"
	StatusBudgetExceeded  = "budget_exceeded"
	StatusGoalUnavailable = "goal_unavailable"
	StatusGoalPaused      = "goal_paused"
	StatusGoalBudget      = "goal_budget_limited"
)

var (
	errTaskGoalUnavailable   = errors.New("goal extension unavailable")
	errTaskGoalPaused        = errors.New("goal paused before completion")
	errTaskGoalBudgetLimited = errors.New("goal budget limited before completion")
)

// Deps gathers everything a Runner needs to spin up a CodingSession per tick.
type Deps struct {
	Cwd         string
	AgentDir    string
	Model       *types.Model
	GetAPIKey   func(provider string) (string, error)
	Logs        *runlog.Store
	CustomTools []types.Tool
	// Trigger identifies who started this run in the slim log. The embedded
	// scheduler sets "scheduler"; ad hoc harnesses and tests default to
	// "manual" so acceptance scripts can distinguish natural cron evidence
	// from bootstrap/manual runs.
	Trigger string
	// DailyBudgetTokens mirrors config.Config.DailyBudgetTokens: once this
	// task's daily ledger (kept by Logs) reaches it, Execute refuses to start a
	// session for this task. Zero disables the check.
	DailyBudgetTokens int
}

// Result describes one completed execution. LogPath is populated even if the
// run itself errored, so callers can point users at the partial transcript.
type Result struct {
	LogPath string
	Started time.Time
	Ended   time.Time
	// Status is one of the Status* constants.
	Status string
	// GoalStatus records the final status of a task-owned goal, when the task
	// declares goal: and the goal extension was able to create it.
	GoalStatus string
	// Tokens is the run's accumulated input+output tokens.
	Tokens int
}

// Execute runs one task synchronously: opens a log file, writes run_start,
// drives a fresh CodingSession through RunPrint(PrintModeJSON) whose output
// goes through summaryWriter, then writes run_end with status/duration.
// Returns the Result regardless of error so callers can surface the log
// path on failure too.
func Execute(ctx context.Context, deps Deps, task config.Task) (Result, error) {
	task.Normalize()
	taskID := task.Identity()
	taskName := task.DisplayName()
	res := Result{Started: time.Now(), Status: StatusError}
	if task.Prompt == "" {
		res.Ended = time.Now()
		return res, fmt.Errorf("task %s: empty prompt", taskID)
	}

	run, err := deps.Logs.Open(taskID)
	if err != nil {
		res.Ended = time.Now()
		return res, fmt.Errorf("open log: %w", err)
	}
	res.LogPath = run.Path()
	defer func() { _ = run.Close() }()

	goalText := strings.TrimSpace(task.Goal)
	hasGoal := goalText != ""
	extensions := loadExtensions(taskID)
	start := map[string]any{
		"type":       "run_start",
		"task_id":    taskID,
		"task_name":  taskName,
		"prompt":     task.Prompt,
		"trigger":    normalizeTrigger(deps.Trigger),
		"has_goal":   hasGoal,
		"started_at": formatLogTime(res.Started),
	}
	if timezone := strings.TrimSpace(task.Timezone); timezone != "" {
		start["timezone"] = timezone
	}
	if hasGoal {
		start["goal"] = goalText
		start["goal_verifier"] = taskGoalVerifierEnabled(extensions)
	}
	writeJSONLine(run, start)

	// Daily budget breaker: refuse to start a session once this task's daily
	// ledger is at the ceiling. The run still leaves a run_start/run_end pair
	// behind so "why didn't my task run" is answerable from the logs alone.
	if deps.DailyBudgetTokens > 0 {
		used, lerr := deps.Logs.TaskDailyTokens(taskID, res.Started)
		if lerr != nil {
			log.Printf("task %s: daily usage ledger read failed: %v", taskID, lerr)
		} else if used >= deps.DailyBudgetTokens {
			res.Ended = time.Now()
			res.Status = StatusBudgetExceeded
			wrapped := fmt.Errorf("daily token budget exhausted: %d used of %d", used, deps.DailyBudgetTokens)
			writeRunEnd(run, res, wrapped)
			return res, wrapped
		}
	}

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:         deps.Cwd,
		AgentDir:    deps.AgentDir,
		Model:       deps.Model,
		GetAPIKey:   deps.GetAPIKey,
		CustomTools: deps.CustomTools,
		Extensions:  extensions,
	})
	if err != nil {
		res.Ended = time.Now()
		wrapped := fmt.Errorf("create session: %w", err)
		writeRunEnd(run, res, wrapped)
		return res, wrapped
	}
	defer session.Close("modu_cron_run_done")
	// Name the persisted session record after the cron job so it's
	// findable/resumable by job name, not just by the separate NDJSON summary
	// log this function also writes.
	session.SetSessionName("cron:" + taskName + ":" + shortTaskUUID(taskID))

	// Per-run wall-clock cap. Applied here (not in the scheduler) so manual
	// `run <id>` invocations get the same breaker as daemon ticks.
	runCtx, cancelRun := context.WithTimeout(ctx, task.EffectiveTimeout())
	defer cancelRun()

	// Per-run token cap: accumulate assistant usage off the event stream and
	// cancel the run context the moment the cap is crossed.
	var tokens atomic.Int64
	var capTripped atomic.Bool
	unsub := session.Subscribe(func(event types.Event) {
		if event.Type != types.EventTypeMessageEnd {
			return
		}
		usage, ok := assistantUsage(event.Message)
		if !ok {
			return
		}
		total := tokens.Add(int64(usage.Input + usage.Output))
		if task.MaxTokensPerRun > 0 && total >= int64(task.MaxTokensPerRun) && !capTripped.Swap(true) {
			cancelRun()
		}
	})
	defer unsub()

	output := io.Writer(newSummaryWriter(run))
	if os.Getenv(debugRawEnv) != "" {
		rawPath := strings.TrimSuffix(run.Path(), ".log") + ".raw.log"
		if rawFile, ferr := os.Create(rawPath); ferr == nil {
			defer func() { _ = rawFile.Close() }()
			output = io.MultiWriter(output, rawFile)
		}
	}

	prompt := task.Prompt
	var goalRunner *goalext.Extension
	var driver *goalPromptDriver
	if hasGoal {
		driver = newGoalPromptDriver(runCtx)
		session.SetBackgroundPromptDriver(driver.Drive)
		goalRunner, err = startTaskGoal(extensions, goalText)
		if err != nil {
			res.Ended = time.Now()
			wrapped := fmt.Errorf("start goal: %w", err)
			if status := goalCircuitBreakerStatus(wrapped); status != "" {
				res.Status = status
			}
			writeRunEnd(run, res, wrapped)
			return res, wrapped
		}
		prompt = buildGoalTaskPrompt(goalText, task.Prompt)
	}

	err = modes.RunPrint(runCtx, modes.PrintOptions{
		Mode:     modes.PrintModeJSON,
		Messages: []string{prompt},
		Session:  session,
		Output:   output,
	})
	if err == nil && goalRunner != nil {
		err = waitForTaskGoal(runCtx, session, goalRunner, driver)
	}
	if goalRunner != nil {
		if status, ok, serr := goalRunner.AutomationGoalStatus(); serr != nil {
			log.Printf("task %s: goal status read failed: %v", taskID, serr)
		} else if ok {
			res.GoalStatus = string(status)
		}
	}
	res.Ended = time.Now()
	res.Tokens = int(tokens.Load())
	if lerr := deps.Logs.AddTaskDailyTokens(taskID, res.Started, res.Tokens); lerr != nil {
		log.Printf("task %s: daily usage ledger write failed: %v", taskID, lerr)
	}

	var runErr error
	goalStatus := goalCircuitBreakerStatus(err)
	switch {
	case capTripped.Load():
		res.Status = StatusTokenCap
		runErr = fmt.Errorf("per-run token cap reached: %d tokens >= cap %d", res.Tokens, task.MaxTokensPerRun)
	case errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		res.Status = StatusTimeout
		runErr = fmt.Errorf("run timed out after %s", task.EffectiveTimeout())
	case goalStatus != "":
		res.Status = goalStatus
		runErr = err
	case err != nil:
		res.Status = StatusError
		runErr = fmt.Errorf("run prompt: %w", err)
	default:
		res.Status = StatusOK
	}
	writeRunEnd(run, res, runErr)
	if runErr != nil {
		return res, runErr
	}
	return res, nil
}

// loadExtensions resolves the builtin extension set (goal, workflow,
// subagent, ...) for one tick's session, so cron tasks can e.g. create a
// goal and have the verifier gate its completion. Fresh instances per tick —
// extension state is per-session. The cron extension is excluded: this
// runner already injects crontools bound to the daemon's -c config path.
// A broken extensions.yaml downgrades to "no extensions" with a log line
// rather than failing the tick — the caps still protect the run.
func loadExtensions(taskID string) []extension.Extension {
	exts, err := extension.LoadEnabled(extension.LoadOptions{})
	if err != nil {
		log.Printf("task %s: load extensions failed: %v — running without extensions", taskID, err)
		return nil
	}
	kept := exts[:0]
	for _, ext := range exts {
		if ext.Name() != "cron" {
			kept = append(kept, ext)
		}
	}
	return kept
}

// assistantUsage extracts usage from a message_end event's message, which the
// in-process event bus delivers as either a value or a pointer.
func assistantUsage(message any) (types.AgentUsage, bool) {
	switch m := message.(type) {
	case types.AssistantMessage:
		return m.Usage, true
	case *types.AssistantMessage:
		if m != nil {
			return m.Usage, true
		}
	}
	return types.AgentUsage{}, false
}

// ExecuteWithRetries runs exec (normally Execute) up to task.MaxRetries+1
// times, re-running only after plain errors — timeout / token-cap / budget /
// goal-cap/config trips are circuit breakers and are never retried. Backoff
// doubles from retryBaseDelay and is cut short when ctx is cancelled.
func ExecuteWithRetries(ctx context.Context, task config.Task, exec func(context.Context, config.Task) (Result, error)) (Result, error) {
	res, err := exec(ctx, task)
	for attempt := 1; attempt <= task.MaxRetries; attempt++ {
		if err == nil || res.Status != StatusError || ctx.Err() != nil {
			break
		}
		delay := min(retryBaseDelay<<(attempt-1), retryMaxDelay)
		log.Printf("task %s: attempt %d/%d failed (%v), retrying in %s", task.Identity(), attempt, task.MaxRetries+1, err, delay)
		if !retrySleep(ctx, delay) {
			return res, err
		}
		res, err = exec(ctx, task)
	}
	return res, err
}

const (
	retryBaseDelay = 30 * time.Second
	retryMaxDelay  = 5 * time.Minute
)

// retrySleep waits for d or until ctx is cancelled, reporting whether the
// caller should proceed with the retry. A package var so tests can skip the
// real backoff.
var retrySleep = func(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func startTaskGoal(extensions []extension.Extension, objective string) (*goalext.Extension, error) {
	for _, ext := range extensions {
		g, ok := ext.(*goalext.Extension)
		if !ok {
			continue
		}
		if _, err := g.StartAutomationGoal(objective); err != nil {
			return nil, err
		}
		return g, nil
	}
	return nil, fmt.Errorf("%w: task declares goal but goal extension is not enabled", errTaskGoalUnavailable)
}

func taskGoalVerifierEnabled(extensions []extension.Extension) bool {
	for _, ext := range extensions {
		g, ok := ext.(*goalext.Extension)
		if ok {
			return g.AutomationVerifierEnabled()
		}
	}
	return false
}

func normalizeTrigger(trigger string) string {
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		return "manual"
	}
	return trigger
}

func buildGoalTaskPrompt(goal, prompt string) string {
	return fmt.Sprintf(`This cron task has already created the following goal:

%s

Work on the task prompt below. When the goal is actually achieved and no required work remains, call update_goal with status=complete. If the verifier rejects completion, continue fixing the specific rejection reasons.

Task prompt:
%s`, strings.TrimSpace(goal), strings.TrimSpace(prompt))
}

type goalPromptDriver struct {
	ctx context.Context

	mu      sync.Mutex
	running int
	done    chan struct{}
	err     error
}

func newGoalPromptDriver(ctx context.Context) *goalPromptDriver {
	return &goalPromptDriver{
		ctx:  ctx,
		done: make(chan struct{}),
	}
}

func (d *goalPromptDriver) Drive(run func(context.Context) error) bool {
	d.mu.Lock()
	if d.running == 0 {
		d.done = make(chan struct{})
	}
	d.running++
	d.mu.Unlock()

	go func() {
		err := run(d.ctx)
		d.mu.Lock()
		if err != nil && d.err == nil {
			d.err = err
		}
		d.running--
		if d.running == 0 {
			close(d.done)
		}
		d.mu.Unlock()
	}()
	return true
}

func (d *goalPromptDriver) Wait(ctx context.Context) error {
	for {
		d.mu.Lock()
		if d.running == 0 {
			err := d.err
			d.mu.Unlock()
			return err
		}
		done := d.done
		d.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *goalPromptDriver) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running > 0
}

func waitForTaskGoal(ctx context.Context, session *coding_agent.CodingSession, goalRunner *goalext.Extension, driver *goalPromptDriver) error {
	for {
		status, ok, err := goalRunner.AutomationGoalStatus()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("goal disappeared before completion")
		}
		switch status {
		case goalext.StatusComplete:
			return nil
		case goalext.StatusPaused:
			return errTaskGoalPaused
		case goalext.StatusBudgetLimited:
			return errTaskGoalBudgetLimited
		}
		if agent := session.GetAgent(); agent != nil && agent.HasQueuedMessages() {
			if err := session.Continue(ctx); err != nil {
				return fmt.Errorf("continue queued goal prompt: %w", err)
			}
			session.WaitForIdle()
			continue
		}
		if driver.Running() {
			if err := driver.Wait(ctx); err != nil {
				return err
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return fmt.Errorf("goal remained active without a queued continuation")
		}
	}
}

func goalCircuitBreakerStatus(err error) string {
	switch {
	case errors.Is(err, errTaskGoalUnavailable):
		return StatusGoalUnavailable
	case errors.Is(err, errTaskGoalPaused):
		return StatusGoalPaused
	case errors.Is(err, errTaskGoalBudgetLimited):
		return StatusGoalBudget
	default:
		return ""
	}
}

// New returns a scheduler.Runner-compatible function — a thin wrapper around
// Execute that discards the Result.
func New(deps Deps) func(ctx context.Context, task config.Task) error {
	return func(ctx context.Context, task config.Task) error {
		_, err := Execute(ctx, deps, task)
		return err
	}
}

// writeJSONLine encodes obj as one NDJSON line. Errors are silently
// dropped — the surrounding tick should not fail because the bookkeeping
// log line couldn't be written.
func writeJSONLine(w io.Writer, obj map[string]any) {
	b, err := json.Marshal(obj)
	if err != nil {
		return
	}
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}

func writeRunEnd(w io.Writer, res Result, err error) {
	status := res.Status
	if status == "" {
		status = StatusOK
		if err != nil {
			status = StatusError
		}
	}
	obj := map[string]any{
		"type":        "run_end",
		"status":      status,
		"duration_ms": res.Ended.Sub(res.Started).Milliseconds(),
		"ended_at":    formatLogTime(res.Ended),
	}
	if res.Tokens > 0 {
		obj["tokens"] = res.Tokens
	}
	if res.GoalStatus != "" {
		obj["goal_status"] = res.GoalStatus
	}
	if err != nil {
		obj["error"] = err.Error()
	}
	writeJSONLine(w, obj)
}

func formatLogTime(t time.Time) string {
	return t.Local().Format(time.RFC3339Nano)
}

func shortTaskUUID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
