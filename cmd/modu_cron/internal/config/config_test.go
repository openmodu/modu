package config

import (
	"path/filepath"
	"testing"
)

func TestTaskPolicyDefault(t *testing.T) {
	cases := []struct {
		in   OverlapPolicy
		want OverlapPolicy
	}{
		{"", OverlapSkip},
		{"unknown", OverlapSkip},
		{OverlapSkip, OverlapSkip},
		{OverlapQueue, OverlapQueue},
		{OverlapKill, OverlapKill},
	}
	for _, c := range cases {
		got := Task{OnOverlap: c.in}.Policy()
		if got != c.want {
			t.Errorf("Task{OnOverlap=%q}.Policy() = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestTaskNotificationChannels(t *testing.T) {
	got := Task{
		Channel:  "ops",
		Channels: []string{"ops", " mobile ", "", "alerts"},
	}.NotificationChannels()
	want := []string{"ops", "mobile", "alerts"}
	if len(got) != len(want) {
		t.Fatalf("NotificationChannels len=%d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NotificationChannels[%d]=%q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if len(cfg.Tasks) != 0 {
		t.Fatalf("expected empty tasks, got %d", len(cfg.Tasks))
	}
}

func TestLoadMissingRuntimeStillReadsDefaultTasksFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := SaveTasks(DefaultTasksPath(cfgPath), []Task{
		{ID: "a", Cron: "@every 1m", Prompt: "p", Enabled: true},
	}); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].ID != "a" {
		t.Fatalf("default tasks file not loaded: %+v", cfg.Tasks)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	in := &Config{Channels: map[string]Channel{
		"ops": {Type: "webhook", URL: "https://example.invalid/hook"},
	}, Tasks: []Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true, OnOverlap: OverlapQueue, Channels: []string{"ops"}},
		{ID: "b", Cron: "@daily", Prompt: "q", Enabled: false},
	}}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(out.Tasks))
	}
	if out.Tasks[0].OnOverlap != OverlapQueue {
		t.Errorf("first task OnOverlap=%q, want %q", out.Tasks[0].OnOverlap, OverlapQueue)
	}
	if out.Channels["ops"].Type != "webhook" || out.Tasks[0].Channels[0] != "ops" {
		t.Errorf("channel config not persisted correctly: %+v", out)
	}
}

func TestLoadUsesIsolatedTasksFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := SaveRuntime(cfgPath, &Config{
		WorkingDir: "/repo",
		TasksFile:  "tasks.yaml",
		Channels: map[string]Channel{
			"tg": {Type: "telegram", TokenEnv: "TOKEN", ChatIDEnv: "CHAT"},
		},
	}); err != nil {
		t.Fatalf("SaveRuntime: %v", err)
	}
	if err := SaveTasks(filepath.Join(dir, "tasks.yaml"), []Task{
		{ID: "a", Cron: "@every 1m", Prompt: "p", Enabled: true},
	}); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkingDir != "/repo" || cfg.Channels["tg"].Type != "telegram" {
		t.Fatalf("runtime config not preserved: %+v", cfg)
	}
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].ID != "a" {
		t.Fatalf("tasks file not loaded: %+v", cfg.Tasks)
	}
	runtimeCfg, err := LoadRuntime(cfgPath)
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if len(runtimeCfg.Tasks) != 0 {
		t.Fatalf("runtime config should not embed tasks: %+v", runtimeCfg.Tasks)
	}
}

func TestLoadPrefersDefaultTasksFileOverLegacyEmbeddedTasks(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := Save(cfgPath, &Config{Tasks: []Task{
		{ID: "legacy", Cron: "@daily", Prompt: "old", Enabled: true},
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := SaveTasks(DefaultTasksPath(cfgPath), []Task{
		{ID: "isolated", Cron: "@hourly", Prompt: "new", Enabled: true},
	}); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].ID != "isolated" {
		t.Fatalf("expected isolated task file to win, got %+v", cfg.Tasks)
	}
}
