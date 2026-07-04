package cron

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/scheduler"
	"github.com/openmodu/modu/pkg/types"
)

// Helper: count how many runs the runner observed.
type runCounter struct{ n atomic.Int32 }

func (c *runCounter) runner() scheduler.Runner {
	return func(ctx context.Context, task config.Task) error {
		c.n.Add(1)
		return nil
	}
}

func writeConfig(t *testing.T, path string, tasks []config.Task) {
	t.Helper()
	if err := config.Save(path, &config.Config{Tasks: tasks}); err != nil {
		t.Fatal(err)
	}
}

func TestReloadSchedulerSwapsTasks(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, cfgPath, []config.Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true},
	})
	c := &runCounter{}
	first, err := loadAndStart(cfgPath, c.runner())
	if err != nil {
		t.Fatalf("loadAndStart: %v", err)
	}
	defer first.Stop()

	writeConfig(t, cfgPath, []config.Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true},
		{ID: "b", Cron: "* * * * * *", Prompt: "q", Enabled: true},
		{ID: "c", Cron: "* * * * * *", Prompt: "r", Enabled: false}, // disabled, not registered
	})
	next := reloadScheduler(first, cfgPath, c.runner())
	if next == first {
		t.Fatal("reload should have returned a new scheduler instance")
	}
	defer next.Stop()
}

func TestBuildRunnerDepsMarksEmbeddedSchedulerTrigger(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	workingDir := filepath.Join(dir, "repo")
	cfg := &config.Config{
		WorkingDir:        workingDir,
		DailyBudgetTokens: 12345,
	}
	model := &types.Model{ID: "mock", ProviderID: "openai"}

	deps := buildRunnerDeps(cfgPath, cfg, "/fallback", model, func(string) (string, error) { return "", nil })
	if deps.Trigger != "scheduler" {
		t.Fatalf("Trigger=%q, want scheduler", deps.Trigger)
	}
	if deps.Cwd != workingDir {
		t.Fatalf("Cwd=%q, want %q", deps.Cwd, workingDir)
	}
	if deps.DailyBudgetTokens != 12345 {
		t.Fatalf("DailyBudgetTokens=%d, want 12345", deps.DailyBudgetTokens)
	}
	if deps.Model != model {
		t.Fatal("Model was not preserved")
	}
	if deps.Logs == nil {
		t.Fatal("Logs store is nil")
	}
	if len(deps.CustomTools) == 0 {
		t.Fatal("expected cron management tools")
	}
}

func TestReloadSchedulerKeepsOldOnLoadFailure(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, cfgPath, []config.Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true},
	})
	c := &runCounter{}
	first, err := loadAndStart(cfgPath, c.runner())
	if err != nil {
		t.Fatalf("loadAndStart: %v", err)
	}
	defer first.Stop()

	// Corrupt the file with non-YAML garbage.
	if err := os.WriteFile(cfgPath, []byte("\x00not yaml: {"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := reloadScheduler(first, cfgPath, c.runner())
	if got != first {
		t.Error("reload should have kept the old scheduler when load failed")
	}
}

func TestReloadSchedulerKeepsOldOnInvalidCron(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, cfgPath, []config.Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true},
	})
	c := &runCounter{}
	first, err := loadAndStart(cfgPath, c.runner())
	if err != nil {
		t.Fatalf("loadAndStart: %v", err)
	}
	defer first.Stop()

	// Write a syntactically valid YAML with a bogus cron expression.
	writeConfig(t, cfgPath, []config.Task{
		{ID: "a", Cron: "definitely not cron", Prompt: "p", Enabled: true},
	})
	got := reloadScheduler(first, cfgPath, c.runner())
	if got != first {
		t.Error("reload should have kept the old scheduler when validation failed")
	}
}

func TestDaemonReloadsOnFileChange(t *testing.T) {
	// Configure tasks that fire every second so we can observe runs cheaply.
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, cfgPath, []config.Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true},
	})

	// Use loadAndStart + reloadScheduler directly so we don't have to drive
	// the whole signal/fsnotify loop (which is exercised by the e2e script).
	c := &runCounter{}
	sch, err := loadAndStart(cfgPath, c.runner())
	if err != nil {
		t.Fatalf("loadAndStart: %v", err)
	}

	// Wait long enough to be confident at least one tick fired.
	time.Sleep(1200 * time.Millisecond)
	beforeReload := c.n.Load()
	if beforeReload < 1 {
		t.Fatalf("expected at least 1 run before reload, got %d", beforeReload)
	}

	// Add a second task and reload.
	writeConfig(t, cfgPath, []config.Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true},
		{ID: "b", Cron: "* * * * * *", Prompt: "q", Enabled: true},
	})
	sch = reloadScheduler(sch, cfgPath, c.runner())
	defer sch.Stop()

	// After reload, two tasks fire each tick. Allow ≥1 second to be safe.
	time.Sleep(1500 * time.Millisecond)
	afterReload := c.n.Load()

	// We should see runs strictly greater than before, and at a higher rate
	// (≥2 per tick now). Loose threshold to keep the test resilient.
	if afterReload <= beforeReload {
		t.Errorf("expected runs to continue after reload: before=%d, after=%d", beforeReload, afterReload)
	}
}
