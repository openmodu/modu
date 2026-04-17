//go:build !windows

package process

import (
	"strings"
	"testing"
	"time"
)

func TestStart_InvalidCommand(t *testing.T) {
	p := New(Config{ID: "x", Command: "/nonexistent/binary-xyz"})
	if err := p.Start(); err == nil {
		t.Fatalf("expected error for missing command")
	}
	if p.Status() != StatusError {
		t.Errorf("status = %v, want %v", p.Status(), StatusError)
	}
}

func TestEchoRoundtrip(t *testing.T) {
	// /bin/cat echoes stdin to stdout line-by-line.
	p := New(Config{ID: "cat", Command: "/bin/cat"})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	if p.Status() != StatusRunning {
		t.Fatalf("status = %v", p.Status())
	}

	if err := p.Write([]byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case line := <-p.Lines():
		if string(line) != `{"hello":"world"}` {
			t.Errorf("got %q", string(line))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for echoed line")
	}
}

func TestMultipleLines_Ordered(t *testing.T) {
	p := New(Config{ID: "cat", Command: "/bin/cat"})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	for i, payload := range []string{"a", "b", "c"} {
		if err := p.Write([]byte(payload)); err != nil {
			t.Fatalf("write[%d]: %v", i, err)
		}
	}
	got := make([]string, 0, 3)
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case line := <-p.Lines():
			got = append(got, string(line))
		case <-deadline:
			t.Fatalf("timeout; got so far: %v", got)
		}
	}
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q want %q", i, got[i], want[i])
		}
	}
}

// TestStderrCapture spawns a small shell command that writes only to stderr.
func TestStderrCapture(t *testing.T) {
	p := New(Config{ID: "err", Command: "/bin/sh", Args: []string{"-c", "echo oops 1>&2"}})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	select {
	case line := <-p.Stderr():
		if string(line) != "oops" {
			t.Errorf("got %q", string(line))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for stderr line")
	}

	// Process should terminate on its own; Done must fire.
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("done never closed after natural exit")
	}
}

func TestGracefulStop_ShortLivedIgnoresSIGINT(t *testing.T) {
	// A process that self-exits immediately: Stop should be fine even
	// though cmd.Process may already be gone.
	p := New(Config{ID: "true", Command: "/usr/bin/true"})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// give it a moment to exit
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("true never exited")
	}
	if err := p.Stop(); err != nil {
		t.Errorf("stop after natural exit: %v", err)
	}
}

func TestGracefulStop_LongRunning(t *testing.T) {
	// sleep 60 — we'll Stop() before it wakes up
	p := New(Config{ID: "sleep", Command: "/bin/sleep", Args: []string{"60"}})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	start := time.Now()
	if err := p.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	elapsed := time.Since(start)
	// SIGINT kills sleep immediately on macOS/Linux, so this should be well
	// under the graceful grace period.
	if elapsed > StopGracePeriod+500*time.Millisecond {
		t.Errorf("stop took %v, expected well under %v", elapsed, StopGracePeriod)
	}
	if p.Status() != StatusStopped {
		t.Errorf("status = %v, want %v", p.Status(), StatusStopped)
	}
}

func TestStop_KillFallback(t *testing.T) {
	// Inline `sh -c` form so the trap is installed before we return from
	// Start(). Using a file-based script races: the shell may not have read
	// the trap line yet when Stop() sends SIGINT, in which case the default
	// handler kills the shell and the grace-period branch never runs.
	p := New(Config{
		ID:      "ignore",
		Command: "/bin/sh",
		Args:    []string{"-c", "trap '' INT TERM; while :; do sleep 0.1; done"},
	})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Give the shell a moment to reach the trap + loop. Without this, on a
	// busy machine the shell can still be in its startup path when SIGINT
	// arrives.
	time.Sleep(150 * time.Millisecond)

	start := time.Now()
	if err := p.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < StopGracePeriod-500*time.Millisecond {
		t.Errorf("stop returned in %v; expected to wait through grace period", elapsed)
	}
	if elapsed > StopGracePeriod+2*time.Second {
		t.Errorf("stop took %v, expected roughly %v", elapsed, StopGracePeriod)
	}
}

func TestLargeLine_BelowScannerMax(t *testing.T) {
	p := New(Config{ID: "cat", Command: "/bin/cat"})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 1 MB of 'x' — well above the default 64 KB scanner cap but well below
	// our 10 MB ceiling.
	payload := strings.Repeat("x", 1024*1024)
	if err := p.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case line := <-p.Lines():
		if len(line) != len(payload) {
			t.Errorf("got %d bytes, want %d", len(line), len(payload))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for large line")
	}
}

func TestWrite_BeforeStart_Errors(t *testing.T) {
	p := New(Config{ID: "x", Command: "/bin/cat"})
	if err := p.Write([]byte("oops")); err == nil {
		t.Error("expected error when writing before start")
	}
}

func TestWrite_AfterStop_Errors(t *testing.T) {
	p := New(Config{ID: "cat", Command: "/bin/cat"})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = p.Stop()
	if err := p.Write([]byte("oops")); err == nil {
		t.Error("expected error writing after stop")
	}
}

func TestDoubleStart_Idempotent(t *testing.T) {
	p := New(Config{ID: "cat", Command: "/bin/cat"})
	if err := p.Start(); err != nil {
		t.Fatalf("start 1: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	if err := p.Start(); err != nil {
		t.Errorf("start 2: %v", err)
	}
	if p.Status() != StatusRunning {
		t.Errorf("status = %v", p.Status())
	}
}

func TestDoubleStop_Idempotent(t *testing.T) {
	p := New(Config{ID: "cat", Command: "/bin/cat"})
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("stop 1: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("stop 2: %v", err)
	}
}
