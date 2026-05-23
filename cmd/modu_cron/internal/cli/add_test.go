package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

// driveAdd feeds the interactive prompts with a scripted answer stream and
// returns the resulting cfg + the bytes that would have been shown to the
// user. Each "input" line is terminated with \n.
func driveAdd(t *testing.T, cfgPath, script string) (*config.Config, string, error) {
	t.Helper()
	var out bytes.Buffer
	err := Add(cfgPath, strings.NewReader(script), &out)
	if err != nil {
		return nil, out.String(), err
	}
	cfg, lerr := config.Load(cfgPath)
	if lerr != nil {
		t.Fatalf("reload: %v", lerr)
	}
	return cfg, out.String(), nil
}

func TestAddHappyPathPersists(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	script := strings.Join([]string{
		"daily",
		"0 0 9 * * *",
		"summarize yesterday",
		"y",
		"queue",
	}, "\n") + "\n"
	cfg, out, err := driveAdd(t, cfgPath, script)
	if err != nil {
		t.Fatalf("Add: %v\noutput:\n%s", err, out)
	}
	if len(cfg.Tasks) != 1 {
		t.Fatalf("want 1 task, got %d", len(cfg.Tasks))
	}
	got := cfg.Tasks[0]
	if got.ID != "daily" || got.Cron != "0 0 9 * * *" ||
		got.Prompt != "summarize yesterday" || !got.Enabled ||
		got.OnOverlap != config.OverlapQueue {
		t.Errorf("persisted wrong: %+v", got)
	}
}

func TestAddRejectsDuplicateIDAndReprompts(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(cfgPath, &config.Config{Tasks: []config.Task{
		{ID: "existing", Cron: "* * * * * *", Prompt: "p", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	script := strings.Join([]string{
		"existing",           // collides
		"new",                // accepted
		"*/5 * * * * *",      // cron
		"prompt body",        // prompt
		"",                   // enabled default (y)
		"",                   // overlap default (skip)
	}, "\n") + "\n"
	cfg, out, err := driveAdd(t, cfgPath, script)
	if err != nil {
		t.Fatalf("Add: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("expected duplicate warning in output:\n%s", out)
	}
	if len(cfg.Tasks) != 2 || cfg.Tasks[1].ID != "new" {
		t.Errorf("want existing+new, got %+v", cfg.Tasks)
	}
	if cfg.Tasks[1].OnOverlap != config.OverlapSkip {
		t.Errorf("default overlap should normalize to skip, got %q", cfg.Tasks[1].OnOverlap)
	}
}

func TestAddRejectsBadCronAndReprompts(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	script := strings.Join([]string{
		"x",
		"not-a-cron",         // rejected
		"@every 1m",          // descriptor — accepted by parser
		"p",
		"n",
		"skip",
	}, "\n") + "\n"
	cfg, out, err := driveAdd(t, cfgPath, script)
	if err != nil {
		t.Fatalf("Add: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "invalid:") {
		t.Errorf("expected invalid cron warning:\n%s", out)
	}
	if cfg.Tasks[0].Cron != "@every 1m" {
		t.Errorf("cron not preserved: %q", cfg.Tasks[0].Cron)
	}
	if cfg.Tasks[0].Enabled {
		t.Errorf("enabled should be false (script said 'n')")
	}
}

func TestAddInputClosedMidPromptErrors(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	// id ok, cron prompt closes immediately.
	_, _, err := driveAdd(t, cfgPath, "ok\n")
	if err == nil {
		t.Fatal("expected error when stdin closes mid-flow")
	}
	cfg, lerr := config.Load(cfgPath)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(cfg.Tasks) != 0 {
		t.Errorf("nothing should have been persisted, got: %+v", cfg.Tasks)
	}
}
