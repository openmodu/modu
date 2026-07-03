package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/runlog"
)

// TestExecuteDailyBudgetExceeded exercises the budget breaker path, which
// refuses to run before any session (and thus any provider) is needed.
func TestExecuteDailyBudgetExceeded(t *testing.T) {
	logs := runlog.New(t.TempDir())
	if err := logs.AddDailyTokens(time.Now(), 1000); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	deps := Deps{Logs: logs, DailyBudgetTokens: 500}
	task := config.Task{ID: "t", Prompt: "hello"}

	res, err := Execute(context.Background(), deps, task)
	if err == nil {
		t.Fatal("expected budget error, got nil")
	}
	if res.Status != StatusBudgetExceeded {
		t.Fatalf("Status=%q, want %q", res.Status, StatusBudgetExceeded)
	}
	if !strings.Contains(err.Error(), "daily token budget exhausted") {
		t.Fatalf("unexpected error: %v", err)
	}

	// The log still records run_start + run_end with the breaker status.
	data, rerr := os.ReadFile(res.LogPath)
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 log lines, got %d: %s", len(lines), data)
	}
	var end map[string]any
	if jerr := json.Unmarshal([]byte(lines[1]), &end); jerr != nil {
		t.Fatalf("parse run_end: %v", jerr)
	}
	if end["type"] != "run_end" || end["status"] != StatusBudgetExceeded {
		t.Fatalf("unexpected run_end: %v", end)
	}
}

// TestExecuteUnderBudgetStillRuns verifies a seeded-but-under-budget ledger
// does not trip the breaker (the run then fails later at session creation in
// this providerless test env, which is fine — status must not be budget).
func TestExecuteUnderBudgetStillRuns(t *testing.T) {
	logs := runlog.New(t.TempDir())
	if err := logs.AddDailyTokens(time.Now(), 100); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	deps := Deps{Logs: logs, DailyBudgetTokens: 500}
	task := config.Task{ID: "t", Prompt: "hello"}

	res, _ := Execute(context.Background(), deps, task)
	if res.Status == StatusBudgetExceeded {
		t.Fatalf("breaker tripped under budget: %+v", res)
	}
}

// skipBackoff replaces the retry backoff with an instant proceed for the
// duration of a test.
func skipBackoff(t *testing.T) {
	t.Helper()
	orig := retrySleep
	retrySleep = func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }
	t.Cleanup(func() { retrySleep = orig })
}

func TestExecuteWithRetriesRetriesOnlyPlainErrors(t *testing.T) {
	skipBackoff(t)
	cases := []struct {
		name      string
		status    string
		err       error
		retries   int
		wantCalls int
	}{
		{"error retried", StatusError, errors.New("boom"), 2, 3},
		{"ok not retried", StatusOK, nil, 2, 1},
		{"timeout not retried", StatusTimeout, errors.New("timeout"), 2, 1},
		{"token cap not retried", StatusTokenCap, errors.New("cap"), 2, 1},
		{"budget not retried", StatusBudgetExceeded, errors.New("budget"), 2, 1},
		{"zero retries", StatusError, errors.New("boom"), 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			exec := func(ctx context.Context, task config.Task) (Result, error) {
				calls++
				return Result{Status: c.status}, c.err
			}
			task := config.Task{ID: "t", MaxRetries: c.retries}
			_, _ = ExecuteWithRetries(context.Background(), task, exec)
			if calls != c.wantCalls {
				t.Fatalf("calls=%d, want %d", calls, c.wantCalls)
			}
		})
	}
}

func TestExecuteWithRetriesStopsAfterSuccess(t *testing.T) {
	skipBackoff(t)
	calls := 0
	exec := func(ctx context.Context, task config.Task) (Result, error) {
		calls++
		if calls == 1 {
			return Result{Status: StatusError}, errors.New("flaky")
		}
		return Result{Status: StatusOK}, nil
	}
	task := config.Task{ID: "t", MaxRetries: 3}
	res, err := ExecuteWithRetries(context.Background(), task, exec)
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if res.Status != StatusOK || calls != 2 {
		t.Fatalf("status=%q calls=%d, want ok/2", res.Status, calls)
	}
}

func TestExecuteWithRetriesRespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	exec := func(ctx context.Context, task config.Task) (Result, error) {
		calls++
		cancel() // cancel during the first attempt
		return Result{Status: StatusError}, errors.New("boom")
	}
	task := config.Task{ID: "t", MaxRetries: 5}
	_, err := ExecuteWithRetries(ctx, task, exec)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1 (no retries after ctx cancel)", calls)
	}
}
