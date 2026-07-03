// Package runner wires the scheduler's Runner hook to a CodingSession.
//
// Each tick gets a fresh session — no cross-tick memory. The agent's event
// stream is filtered through summaryWriter and written to a per-run log
// file as a slim NDJSON transcript:
//
//	{"type":"run_start",     "task_id":..., "prompt":..., "started_at":...}
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
	"sync/atomic"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
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
	StatusOK             = "ok"
	StatusError          = "error"
	StatusTimeout        = "timeout"
	StatusTokenCap       = "token_cap"
	StatusBudgetExceeded = "budget_exceeded"
)

// Deps gathers everything a Runner needs to spin up a CodingSession per tick.
type Deps struct {
	Cwd         string
	AgentDir    string
	Model       *types.Model
	GetAPIKey   func(provider string) (string, error)
	Logs        *runlog.Store
	CustomTools []types.Tool
	// DailyBudgetTokens mirrors config.Config.DailyBudgetTokens: once the
	// day's ledger (kept by Logs) reaches it, Execute refuses to start a
	// session. Zero disables the check.
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
	// Tokens is the run's accumulated input+output tokens.
	Tokens int
}

// Execute runs one task synchronously: opens a log file, writes run_start,
// drives a fresh CodingSession through RunPrint(PrintModeJSON) whose output
// goes through summaryWriter, then writes run_end with status/duration.
// Returns the Result regardless of error so callers can surface the log
// path on failure too.
func Execute(ctx context.Context, deps Deps, task config.Task) (Result, error) {
	res := Result{Started: time.Now(), Status: StatusError}
	if task.Prompt == "" {
		res.Ended = time.Now()
		return res, fmt.Errorf("task %s: empty prompt", task.ID)
	}

	run, err := deps.Logs.Open(task.ID)
	if err != nil {
		res.Ended = time.Now()
		return res, fmt.Errorf("open log: %w", err)
	}
	res.LogPath = run.Path()
	defer func() { _ = run.Close() }()

	writeJSONLine(run, map[string]any{
		"type":       "run_start",
		"task_id":    task.ID,
		"prompt":     task.Prompt,
		"started_at": formatLogTime(res.Started),
	})

	// Daily budget breaker: refuse to start a session once today's ledger is
	// at the ceiling. The run still leaves a run_start/run_end pair behind so
	// "why didn't my task run" is answerable from the logs alone.
	if deps.DailyBudgetTokens > 0 {
		used, lerr := deps.Logs.DailyTokens(res.Started)
		if lerr != nil {
			log.Printf("task %s: daily usage ledger read failed: %v", task.ID, lerr)
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
		Extensions:  loadExtensions(task.ID),
	})
	if err != nil {
		res.Ended = time.Now()
		wrapped := fmt.Errorf("create session: %w", err)
		writeRunEnd(run, res, wrapped)
		return res, wrapped
	}
	defer session.Close("modu_cron_run_done")

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

	err = modes.RunPrint(runCtx, modes.PrintOptions{
		Mode:     modes.PrintModeJSON,
		Messages: []string{task.Prompt},
		Session:  session,
		Output:   output,
	})
	res.Ended = time.Now()
	res.Tokens = int(tokens.Load())
	if lerr := deps.Logs.AddDailyTokens(res.Started, res.Tokens); lerr != nil {
		log.Printf("task %s: daily usage ledger write failed: %v", task.ID, lerr)
	}

	var runErr error
	switch {
	case capTripped.Load():
		res.Status = StatusTokenCap
		runErr = fmt.Errorf("per-run token cap reached: %d tokens >= cap %d", res.Tokens, task.MaxTokensPerRun)
	case errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		res.Status = StatusTimeout
		runErr = fmt.Errorf("run timed out after %s", task.EffectiveTimeout())
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
// times, re-running only after plain errors — timeout / token-cap / budget
// trips are circuit breakers and are never retried. Backoff doubles from
// retryBaseDelay and is cut short when ctx is cancelled.
func ExecuteWithRetries(ctx context.Context, task config.Task, exec func(context.Context, config.Task) (Result, error)) (Result, error) {
	res, err := exec(ctx, task)
	for attempt := 1; attempt <= task.MaxRetries; attempt++ {
		if err == nil || res.Status != StatusError || ctx.Err() != nil {
			break
		}
		delay := min(retryBaseDelay<<(attempt-1), retryMaxDelay)
		log.Printf("task %s: attempt %d/%d failed (%v), retrying in %s", task.ID, attempt, task.MaxRetries+1, err, delay)
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
	if err != nil {
		obj["error"] = err.Error()
	}
	writeJSONLine(w, obj)
}

func formatLogTime(t time.Time) string {
	return t.Local().Format(time.RFC3339Nano)
}
