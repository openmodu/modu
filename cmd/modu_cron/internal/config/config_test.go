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

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if len(cfg.Tasks) != 0 {
		t.Fatalf("expected empty tasks, got %d", len(cfg.Tasks))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	in := &Config{Tasks: []Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true, OnOverlap: OverlapQueue},
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
}
