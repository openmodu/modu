package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/crontools"
	"github.com/openmodu/modu/cmd/modu_cron/internal/notify"
	"github.com/openmodu/modu/cmd/modu_cron/internal/provider"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runlog"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runner"
)

// Run fires a single execution of taskID without going through the cron
// schedule. The task's enabled flag is ignored — `run` is intended for
// debugging and ad-hoc triggers.
//
// stdout receives only two lines: a "running ..." header (with the log
// path) and a "done in T" footer. The full event stream is in the log file.
func Run(ctx context.Context, cfgPath, taskID string, out io.Writer) error {
	if taskID == "" {
		return fmt.Errorf("run: task id required")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	var task *config.Task
	for i := range cfg.Tasks {
		if cfg.Tasks[i].ID == taskID {
			task = &cfg.Tasks[i]
			break
		}
	}
	if task == nil {
		return fmt.Errorf("run: task %q not found in %s", taskID, cfgPath)
	}

	fallbackCwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	model, getAPIKey := provider.ResolveWithConfig(cfg)
	cwd := config.ResolveWorkingDir(cfgPath, cfg, fallbackCwd)

	deps := runner.Deps{
		Cwd:         cwd,
		AgentDir:    coding_agent.DefaultAgentDir(),
		Model:       model,
		GetAPIKey:   getAPIKey,
		Logs:        runlog.New(""),
		CustomTools: crontools.New(cfgPath),
	}

	fmt.Fprintf(out, "running task %q (prompt=%q, model=%s)…\n", task.ID, truncate(task.Prompt, 80), model.ID)
	res, runErr := runner.Execute(ctx, deps, *task)
	if res.LogPath != "" {
		fmt.Fprintf(out, "  log: %s\n", res.LogPath)
	}
	var notifyErr error
	if len(task.NotificationChannels()) > 0 {
		notifyErr = notify.NewSender().Completion(ctx, cfg, *task, res, runErr)
		if notifyErr != nil {
			fmt.Fprintf(out, "notify failed: %v\n", notifyErr)
		}
	}
	dur := res.Ended.Sub(res.Started).Round(100 * time.Millisecond)
	if runErr != nil {
		fmt.Fprintf(out, "failed after %s\n", dur)
		return runErr
	}
	if notifyErr != nil {
		return notifyErr
	}
	fmt.Fprintf(out, "done in %s — view with: modu_cron logs %s --tail\n", dur, task.ID)
	return nil
}
