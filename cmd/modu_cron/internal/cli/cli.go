// Package cli implements the modu_cron subcommands. Business logic is
// intentionally stubbed; the scaffold phase only needs the surface area.
package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/provider"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runlog"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runner"
	"github.com/openmodu/modu/cmd/modu_cron/internal/scheduler"
)

// Daemon loads the config and runs the scheduler until SIGINT/SIGTERM.
//
// Provider is resolved from environment variables; if none is configured the
// daemon falls back to a no-op runner that just logs each tick — useful for
// dry-running the schedule without spending API calls.
func Daemon(ctx context.Context, cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	log.Printf("loaded %d task(s) from %s", len(cfg.Tasks), cfgPath)

	var run scheduler.Runner
	model, getAPIKey := provider.Resolve()
	if model == nil {
		log.Printf("no provider configured (set ANTHROPIC_API_KEY / OPENAI_API_KEY / DEEPSEEK_API_KEY / OLLAMA_HOST / LMSTUDIO_BASE_URL) — running in dry mode")
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		logs := runlog.New("")
		run = runner.New(runner.Deps{
			Cwd:       cwd,
			AgentDir:  coding_agent.DefaultAgentDir(),
			Model:     model,
			GetAPIKey: getAPIKey,
			Logs:      logs,
		})
		log.Printf("agent runner: model=%s logs=%s", model.ID, runlog.DefaultRoot())
	}

	sch := scheduler.New(run)
	if err := sch.LoadAll(cfg); err != nil {
		return err
	}
	sch.Start()
	log.Printf("modu_cron daemon started")

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Printf("shutting down...")
	<-sch.Stop().Done()
	return nil
}

// List prints all configured tasks.
func List(cfgPath string, out io.Writer) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if len(cfg.Tasks) == 0 {
		fmt.Fprintln(out, "(no tasks)")
		return nil
	}
	fmt.Fprintf(out, "%-20s %-15s %-8s %s\n", "ID", "CRON", "ENABLED", "PROMPT")
	for _, t := range cfg.Tasks {
		enabled := "no"
		if t.Enabled {
			enabled = "yes"
		}
		fmt.Fprintf(out, "%-20s %-15s %-8s %s\n", t.ID, t.Cron, enabled, t.Prompt)
	}
	return nil
}

// NotImplemented is the temporary handler for subcommands whose business
// logic lands after the scaffold.
func NotImplemented(name string) error {
	return fmt.Errorf("%s: not yet implemented — business phase", name)
}
