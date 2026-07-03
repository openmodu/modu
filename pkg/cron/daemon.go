// Package cron is modu_code's cron scheduler: RunScheduler drives task
// execution (via pkg/cron/runner), hot-reload (via pkg/cron/scheduler +
// fsnotify), and completion notifications (via pkg/cron/notify). It has no
// CLI of its own — modu_code's interactive TUI embeds it directly (starts a
// goroutine on launch, tied to the program's lifetime context), and task
// management (add/list/remove) is the builtin 'cron' extension's
// cron_add/cron_list/cron_remove tools, not a command here.
package cron

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"

	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/crontools"
	"github.com/openmodu/modu/pkg/cron/notify"
	"github.com/openmodu/modu/pkg/cron/runlog"
	"github.com/openmodu/modu/pkg/cron/runner"
	"github.com/openmodu/modu/pkg/cron/scheduler"
	"github.com/openmodu/modu/pkg/provider"
)

// reloadDebounce collapses bursts of fsnotify events (editors and YAML
// libraries can produce 2-5 events for a single save) into one reload.
const reloadDebounce = 300 * time.Millisecond

// RunScheduler loads the config and runs the scheduler until ctx is
// cancelled. Config file changes (via fsnotify) trigger a hot reload that
// swaps in a fresh Scheduler instance — in-flight runs of the old one
// continue to completion in the background.
//
// Shutdown and signal handling are the caller's responsibility — this just
// reacts to ctx.Done(). A missing config file is not an error: it starts
// with zero tasks and cwd as the fallback working directory, so callers can
// unconditionally start this alongside a normal session with nothing to
// configure first.
func RunScheduler(ctx context.Context, cfgPath string) error {
	runFn, err := buildRunner(cfgPath)
	if err != nil {
		return err
	}

	sch, err := loadAndStart(cfgPath, runFn)
	if err != nil {
		return err
	}
	defer func() {
		log.Printf("cron scheduler shutting down...")
		<-sch.Stop().Done()
	}()
	log.Printf("cron scheduler started")

	watcher, fsCh := startFSWatch(cfgPath)
	if watcher != nil {
		defer watcher.Close()
	}

	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	armed := false

	for {
		select {
		case <-ctx.Done():
			return nil

		case ev, ok := <-fsCh:
			if !ok {
				fsCh = nil
				continue
			}
			if !shouldReloadFor(ev, cfgPath) {
				continue
			}
			if !armed {
				debounce.Reset(reloadDebounce)
				armed = true
			} else {
				// Already armed; let the running timer fire.
			}

		case <-debounce.C:
			armed = false
			log.Printf("config file changed, reloading...")
			sch = reloadScheduler(sch, cfgPath, runFn)
		}
	}
}

// buildRunner returns the scheduler.Runner to use for every task. Runtime
// settings are loaded per execution so model/workdir/channel config changes
// apply without restarting the daemon.
func buildRunner(cfgPath string) (scheduler.Runner, error) {
	fallbackCwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	log.Printf("agent runner: logs=%s", runlog.DefaultRoot())
	sender := notify.NewSender()
	return func(ctx context.Context, task config.Task) error {
		cfg, cfgErr := config.Load(cfgPath)
		if cfgErr != nil {
			return cfgErr
		}
		model, getAPIKey := provider.Resolve()
		cwd := config.ResolveWorkingDir(cfgPath, cfg, fallbackCwd)
		deps := runner.Deps{
			Cwd:               cwd,
			AgentDir:          coding_agent.DefaultAgentDir(),
			Model:             model,
			GetAPIKey:         getAPIKey,
			Logs:              runlog.New(""),
			CustomTools:       crontools.New(cfgPath),
			DailyBudgetTokens: cfg.DailyBudgetTokens,
		}
		res, runErr := runner.ExecuteWithRetries(ctx, task, func(ctx context.Context, task config.Task) (runner.Result, error) {
			return runner.Execute(ctx, deps, task)
		})
		if len(task.NotificationChannels()) > 0 {
			if notifyErr := sender.Completion(ctx, cfg, task, res, runErr); notifyErr != nil {
				log.Printf("task %s: notify failed: %v", task.ID, notifyErr)
			}
		}
		return runErr
	}, nil
}

// loadAndStart reads cfgPath, builds a Scheduler, starts it.
func loadAndStart(cfgPath string, run scheduler.Runner) (*scheduler.Scheduler, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	log.Printf("loaded %d task(s) from %s", len(cfg.Tasks), cfgPath)
	sch := scheduler.New(run)
	if err := sch.LoadAll(cfg); err != nil {
		return nil, err
	}
	sch.Start()
	return sch, nil
}

// reloadScheduler rebuilds a Scheduler from disk and swaps it in. On any
// failure the old Scheduler is left in place, so a transient parse error or
// invalid cron expression doesn't kill the daemon.
//
// The old scheduler is Stop()ped (no new triggers) but its in-flight goroutines
// are NOT waited on — they finish in the background while the new scheduler
// picks up the next tick. This matches the "let in-flight finish, new schedule
// takes over immediately" policy.
func reloadScheduler(old *scheduler.Scheduler, cfgPath string, run scheduler.Runner) *scheduler.Scheduler {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Printf("reload failed (load): %v — keeping current schedule", err)
		return old
	}
	next := scheduler.New(run)
	if err := next.LoadAll(cfg); err != nil {
		log.Printf("reload failed (validate): %v — keeping current schedule", err)
		return old
	}
	next.Start()
	old.Stop()
	log.Printf("reloaded: %d task(s) active", len(cfg.Tasks))
	return next
}

// startFSWatch arms an fsnotify watcher on cfgPath's parent directory. We
// watch the directory (not the file) because some YAML libraries write via
// rename, which detaches a file-level watch. Returns nil watcher + nil
// channel if fsnotify can't initialize; the scheduler then keeps running the
// config it already loaded but won't pick up further edits without a
// restart.
func startFSWatch(cfgPath string) (*fsnotify.Watcher, chan fsnotify.Event) {
	dir := filepath.Dir(cfgPath)
	if dir == "" {
		dir = "."
	}
	// Ensure the dir exists even on a fresh install where no task has been
	// added yet — otherwise fsnotify can't subscribe.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("create config dir %s failed: %v; hot reload disabled", dir, err)
		return nil, nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("fsnotify unavailable (%v); hot reload disabled", err)
		return nil, nil
	}
	if err := w.Add(dir); err != nil {
		log.Printf("fsnotify watch %s failed: %v; hot reload disabled", dir, err)
		_ = w.Close()
		return nil, nil
	}
	if cfg, err := config.LoadRuntime(cfgPath); err == nil {
		taskDir := filepath.Dir(config.ResolveTasksPath(cfgPath, cfg))
		if taskDir != "" && filepath.Clean(taskDir) != filepath.Clean(dir) {
			if err := os.MkdirAll(taskDir, 0o755); err != nil {
				log.Printf("create tasks dir %s failed: %v; task-file changes won't hot reload", taskDir, err)
			} else if err := w.Add(taskDir); err != nil {
				log.Printf("fsnotify watch %s failed: %v; task-file changes won't hot reload", taskDir, err)
			}
		}
	}
	return w, w.Events
}

// shouldReloadFor decides whether a fsnotify event on cfgPath's directory
// should trigger a reload. We watch the dir so unrelated files there fire
// too — filter to cfgPath, and ignore Chmod-only events (touch / mtime
// bumps) which don't change content.
func shouldReloadFor(ev fsnotify.Event, cfgPath string) bool {
	name := filepath.Clean(ev.Name)
	if name != filepath.Clean(cfgPath) && name != filepath.Clean(config.DefaultTasksPath(cfgPath)) {
		runtimeCfg, err := config.LoadRuntime(cfgPath)
		if err != nil || name != filepath.Clean(config.ResolveTasksPath(cfgPath, runtimeCfg)) {
			return false
		}
	}
	if ev.Op == fsnotify.Chmod {
		return false
	}
	return ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0
}
