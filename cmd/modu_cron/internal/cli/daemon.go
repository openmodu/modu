package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/crontools"
	"github.com/openmodu/modu/cmd/modu_cron/internal/notify"
	"github.com/openmodu/modu/cmd/modu_cron/internal/provider"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runlog"
	"github.com/openmodu/modu/cmd/modu_cron/internal/runner"
	"github.com/openmodu/modu/cmd/modu_cron/internal/scheduler"
)

// reloadDebounce collapses bursts of fsnotify events (editors and YAML
// libraries can produce 2-5 events for a single save) into one reload.
const reloadDebounce = 300 * time.Millisecond

// Daemon loads the config and runs the scheduler until SIGINT/SIGTERM.
// SIGHUP and config file changes (via fsnotify) trigger a hot reload that
// swaps in a fresh Scheduler instance — in-flight runs of the old one
// continue to completion in the background.
func Daemon(ctx context.Context, cfgPath string) error {
	runFn, err := buildRunner(cfgPath)
	if err != nil {
		return err
	}

	sch, err := loadAndStart(cfgPath, runFn)
	if err != nil {
		return err
	}
	tgInbound := newTelegramInboundManager()
	tgInbound.Reload(ctx, cfgPath)
	defer func() {
		log.Printf("shutting down...")
		tgInbound.Stop()
		<-sch.Stop().Done()
	}()
	log.Printf("modu_cron daemon started")

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

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

		case sig := <-sigs:
			switch sig {
			case syscall.SIGHUP:
				log.Printf("SIGHUP received, reloading...")
				sch = reloadScheduler(sch, cfgPath, runFn)
				tgInbound.Reload(ctx, cfgPath)
			default:
				return nil // SIGINT / SIGTERM
			}

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
			tgInbound.Reload(ctx, cfgPath)
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
		res, runErr := runner.Execute(ctx, deps, task)
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
// channel if fsnotify can't initialize; the daemon then runs with SIGHUP-only
// reloads instead of failing.
func startFSWatch(cfgPath string) (*fsnotify.Watcher, chan fsnotify.Event) {
	dir := filepath.Dir(cfgPath)
	if dir == "" {
		dir = "."
	}
	// Ensure the dir exists even on a fresh install where add/rm haven't
	// run yet — otherwise fsnotify can't subscribe and we silently
	// downgrade to SIGHUP-only reload.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("create config dir %s failed: %v; SIGHUP-only reload", dir, err)
		return nil, nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("fsnotify unavailable (%v); SIGHUP-only reload", err)
		return nil, nil
	}
	if err := w.Add(dir); err != nil {
		log.Printf("fsnotify watch %s failed: %v; SIGHUP-only reload", dir, err)
		_ = w.Close()
		return nil, nil
	}
	if cfg, err := config.LoadRuntime(cfgPath); err == nil {
		taskDir := filepath.Dir(config.ResolveTasksPath(cfgPath, cfg))
		if taskDir != "" && filepath.Clean(taskDir) != filepath.Clean(dir) {
			if err := os.MkdirAll(taskDir, 0o755); err != nil {
				log.Printf("create tasks dir %s failed: %v; task-file changes need SIGHUP", taskDir, err)
			} else if err := w.Add(taskDir); err != nil {
				log.Printf("fsnotify watch %s failed: %v; task-file changes need SIGHUP", taskDir, err)
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
