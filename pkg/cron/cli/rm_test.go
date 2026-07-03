package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/cron/config"
)

func twoTaskFile(t *testing.T) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(cfgPath, &config.Config{Tasks: []config.Task{
		{ID: "a", Cron: "* * * * * *", Prompt: "p", Enabled: true},
		{ID: "b", Cron: "@daily", Prompt: "q", Enabled: false},
	}}); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestRmYesSkipsConfirmation(t *testing.T) {
	cfgPath := twoTaskFile(t)
	var out bytes.Buffer
	err := Rm(cfgPath, "a", RmOptions{Yes: true, Out: &out})
	if err != nil {
		t.Fatalf("Rm: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].ID != "b" {
		t.Errorf("want only b remaining, got %+v", cfg.Tasks)
	}
	if !strings.Contains(out.String(), "removed task \"a\"") {
		t.Errorf("missing confirmation line: %s", out.String())
	}
}

func TestRmNonTTYRequiresYes(t *testing.T) {
	cfgPath := twoTaskFile(t)
	err := Rm(cfgPath, "a", RmOptions{IsTTY: false, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("expected non-TTY refusal, got: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 2 {
		t.Errorf("nothing should have been removed: %+v", cfg.Tasks)
	}
}

func TestRmTTYPromptAcceptedRemoves(t *testing.T) {
	cfgPath := twoTaskFile(t)
	var out bytes.Buffer
	err := Rm(cfgPath, "b", RmOptions{
		IsTTY: true,
		In:    strings.NewReader("y\n"),
		Out:   &out,
	})
	if err != nil {
		t.Fatalf("Rm: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].ID != "a" {
		t.Errorf("expected only a remaining, got %+v", cfg.Tasks)
	}
	if !strings.Contains(out.String(), "remove \"b\"") {
		t.Errorf("expected prompt text, got: %s", out.String())
	}
}

func TestRmTTYPromptDeclinedKeepsTask(t *testing.T) {
	cfgPath := twoTaskFile(t)
	var out bytes.Buffer
	err := Rm(cfgPath, "a", RmOptions{
		IsTTY: true,
		In:    strings.NewReader("n\n"),
		Out:   &out,
	})
	if err != nil {
		t.Fatalf("Rm: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 2 {
		t.Errorf("decline should keep tasks: %+v", cfg.Tasks)
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("missing cancelled message: %s", out.String())
	}
}

func TestRmDefaultPromptIsNo(t *testing.T) {
	cfgPath := twoTaskFile(t)
	// Empty input → default no.
	err := Rm(cfgPath, "a", RmOptions{
		IsTTY: true,
		In:    strings.NewReader("\n"),
		Out:   &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Rm: %v", err)
	}
	cfg, _ := config.Load(cfgPath)
	if len(cfg.Tasks) != 2 {
		t.Errorf("empty answer should default to no-remove")
	}
}

func TestRmUnknownIDErrors(t *testing.T) {
	cfgPath := twoTaskFile(t)
	err := Rm(cfgPath, "ghost", RmOptions{Yes: true, Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}

func TestRmEmptyIDErrors(t *testing.T) {
	err := Rm(filepath.Join(t.TempDir(), "x.yaml"), "", RmOptions{Yes: true})
	if err == nil || !strings.Contains(err.Error(), "id required") {
		t.Errorf("expected 'id required', got: %v", err)
	}
}
