// Package runner wires the scheduler's Runner hook to a CodingSession.
//
// Each tick gets a fresh session — no cross-tick memory. The whole agent
// event stream (JSON-lines) is written to a per-run log file, so logs <id>
// can replay history later.
package runner

import (
	"context"
	"fmt"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runlog"
)

// Deps gathers everything a Runner needs to spin up a CodingSession per tick.
type Deps struct {
	Cwd       string
	AgentDir  string
	Model     *types.Model
	GetAPIKey func(provider string) (string, error)
	Logs      *runlog.Store
}

// New returns a scheduler.Runner-compatible function. The returned callback
// builds a fresh CodingSession, runs the task's prompt in JSON print mode
// against a new log file, and closes everything when done.
func New(deps Deps) func(ctx context.Context, task config.Task) error {
	return func(ctx context.Context, task config.Task) error {
		if task.Prompt == "" {
			return fmt.Errorf("task %s: empty prompt", task.ID)
		}

		run, err := deps.Logs.Open(task.ID)
		if err != nil {
			return fmt.Errorf("open log: %w", err)
		}
		defer func() { _ = run.Close() }()

		session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
			Cwd:       deps.Cwd,
			AgentDir:  deps.AgentDir,
			Model:     deps.Model,
			GetAPIKey: deps.GetAPIKey,
		})
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		defer session.Close("modu_cron_tick_done")

		err = modes.RunPrint(ctx, modes.PrintOptions{
			Mode:     modes.PrintModeJSON,
			Messages: []string{task.Prompt},
			Session:  session,
			Output:   run,
		})
		if err != nil {
			return fmt.Errorf("run prompt: %w", err)
		}
		return nil
	}
}
