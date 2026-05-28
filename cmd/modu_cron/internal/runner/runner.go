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
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runlog"
)

// debugRawEnv, when set to a non-empty value, makes Execute tee the raw
// coding_agent NDJSON event stream into a sibling `.raw.log` file alongside
// the slim transcript. Diagnostic only — lets you see exactly which events
// upstream emitted vs. what summaryWriter kept.
const debugRawEnv = "MODU_CRON_DEBUG_RAW"

// Deps gathers everything a Runner needs to spin up a CodingSession per tick.
type Deps struct {
	Cwd         string
	AgentDir    string
	Model       *types.Model
	GetAPIKey   func(provider string) (string, error)
	Logs        *runlog.Store
	CustomTools []agent.Tool
}

// Result describes one completed execution. LogPath is populated even if the
// run itself errored, so callers can point users at the partial transcript.
type Result struct {
	LogPath string
	Started time.Time
	Ended   time.Time
}

// Execute runs one task synchronously: opens a log file, writes run_start,
// drives a fresh CodingSession through RunPrint(PrintModeJSON) whose output
// goes through summaryWriter, then writes run_end with status/duration.
// Returns the Result regardless of error so callers can surface the log
// path on failure too.
func Execute(ctx context.Context, deps Deps, task config.Task) (Result, error) {
	res := Result{Started: time.Now()}
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

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:         deps.Cwd,
		AgentDir:    deps.AgentDir,
		Model:       deps.Model,
		GetAPIKey:   deps.GetAPIKey,
		CustomTools: deps.CustomTools,
	})
	if err != nil {
		res.Ended = time.Now()
		wrapped := fmt.Errorf("create session: %w", err)
		writeRunEnd(run, res, wrapped)
		return res, wrapped
	}
	defer session.Close("modu_cron_run_done")

	output := io.Writer(newSummaryWriter(run))
	if os.Getenv(debugRawEnv) != "" {
		rawPath := strings.TrimSuffix(run.Path(), ".log") + ".raw.log"
		if rawFile, ferr := os.Create(rawPath); ferr == nil {
			defer func() { _ = rawFile.Close() }()
			output = io.MultiWriter(output, rawFile)
		}
	}

	err = modes.RunPrint(ctx, modes.PrintOptions{
		Mode:     modes.PrintModeJSON,
		Messages: []string{task.Prompt},
		Session:  session,
		Output:   output,
	})
	res.Ended = time.Now()
	var runErr error
	if err != nil {
		runErr = fmt.Errorf("run prompt: %w", err)
	}
	writeRunEnd(run, res, runErr)
	if runErr != nil {
		return res, runErr
	}
	return res, nil
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
	obj := map[string]any{
		"type":        "run_end",
		"status":      "ok",
		"duration_ms": res.Ended.Sub(res.Started).Milliseconds(),
		"ended_at":    formatLogTime(res.Ended),
	}
	if err != nil {
		obj["status"] = "error"
		obj["error"] = err.Error()
	}
	writeJSONLine(w, obj)
}

func formatLogTime(t time.Time) string {
	return t.Local().Format(time.RFC3339Nano)
}
