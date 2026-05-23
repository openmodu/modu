// Package runner wires the scheduler's Runner hook to a CodingSession.
//
// Each tick gets a fresh session — no cross-tick memory. The whole agent
// event stream (JSON-lines) is written to a per-run log file, so logs <id>
// can replay history later.
package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runlog"
)

// Deps gathers everything a Runner needs to spin up a CodingSession per tick.
type Deps struct {
	Cwd         string
	AgentDir    string
	Model       *types.Model
	GetAPIKey   func(provider string) (string, error)
	Logs        *runlog.Store
	CustomTools []agent.AgentTool
}

// Result describes one completed execution. LogPath is populated even if the
// run itself errored, so callers can point users at the partial transcript.
type Result struct {
	LogPath string
	Started time.Time
	Ended   time.Time
}

// Execute runs one task synchronously: opens a log file, builds a fresh
// CodingSession, submits the prompt in JSON print mode, then closes
// everything. Returns the Result regardless of error so callers can surface
// the log path on failure too.
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

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:         deps.Cwd,
		AgentDir:    deps.AgentDir,
		Model:       deps.Model,
		GetAPIKey:   deps.GetAPIKey,
		CustomTools: deps.CustomTools,
	})
	if err != nil {
		res.Ended = time.Now()
		return res, fmt.Errorf("create session: %w", err)
	}
	defer session.Close("modu_cron_run_done")

	err = modes.RunPrint(ctx, modes.PrintOptions{
		Mode:     modes.PrintModeJSON,
		Messages: []string{task.Prompt},
		Session:  session,
		Output:   run,
	})
	res.Ended = time.Now()
	if err != nil {
		return res, fmt.Errorf("run prompt: %w", err)
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
