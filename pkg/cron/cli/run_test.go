package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/cron/config"
)

func TestRunMissingTaskIDErrors(t *testing.T) {
	err := Run(context.Background(), filepath.Join(t.TempDir(), "x.yaml"), "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "task id required") {
		t.Errorf("expected 'task id required', got: %v", err)
	}
}

func TestRunUnknownTaskErrors(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(cfgPath, &config.Config{Tasks: []config.Task{
		{ID: "real", Cron: "* * * * * *", Prompt: "p", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), cfgPath, "ghost", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}
