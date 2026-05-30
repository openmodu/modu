package bash

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeHost struct{ cwd string }

func (h fakeHost) Cwd() string { return h.cwd }

func TestExecuteRunsInHostCwd(t *testing.T) {
	dir := t.TempDir()
	r := New(fakeHost{cwd: dir})
	res, err := r.Execute(context.Background(), "pwd", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, dir) {
		t.Fatalf("expected pwd %q in output %q", dir, res.Stdout)
	}
}

func TestExecuteNonZeroExit(t *testing.T) {
	r := New(fakeHost{cwd: t.TempDir()})
	res, err := r.Execute(context.Background(), "exit 3", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("expected exit 3, got %d", res.ExitCode)
	}
}

func TestExecuteTimeout(t *testing.T) {
	r := New(fakeHost{cwd: t.TempDir()})
	start := time.Now()
	res, err := r.Execute(context.Background(), "sleep 5", 100)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("command should have been killed by the timeout")
	}
	if res.ExitCode == 0 {
		t.Fatal("timed-out command should not report success")
	}
}
