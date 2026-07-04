package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	goalext "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/goal"
	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/runlog"
	"github.com/openmodu/modu/pkg/types"
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
	var start map[string]any
	if jerr := json.Unmarshal([]byte(lines[0]), &start); jerr != nil {
		t.Fatalf("parse run_start: %v", jerr)
	}
	if start["type"] != "run_start" || start["trigger"] != "manual" {
		t.Fatalf("unexpected run_start: %v", start)
	}
	if start["has_goal"] != false {
		t.Fatalf("has_goal=%v, want false", start["has_goal"])
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

func TestExecuteRunStartRecordsTrigger(t *testing.T) {
	logs := runlog.New(t.TempDir())
	if err := logs.AddDailyTokens(time.Now(), 1); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	deps := Deps{Logs: logs, DailyBudgetTokens: 1, Trigger: "scheduler"}
	task := config.Task{ID: "t", Prompt: "hello"}

	res, err := Execute(context.Background(), deps, task)
	if err == nil {
		t.Fatal("expected budget error, got nil")
	}
	data, rerr := os.ReadFile(res.LogPath)
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	line := strings.Split(strings.TrimSpace(string(data)), "\n")[0]
	var start map[string]any
	if jerr := json.Unmarshal([]byte(line), &start); jerr != nil {
		t.Fatalf("parse run_start: %v", jerr)
	}
	if start["trigger"] != "scheduler" {
		t.Fatalf("trigger=%v, want scheduler; start=%v", start["trigger"], start)
	}
}

func TestExecuteRunStartRecordsGoalTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logs := runlog.New(t.TempDir())
	if err := logs.AddDailyTokens(time.Now(), 1); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	deps := Deps{Logs: logs, DailyBudgetTokens: 1}
	task := config.Task{ID: "t", Prompt: "hello", Goal: "finish hello", Timezone: "Asia/Shanghai"}

	res, err := Execute(context.Background(), deps, task)
	if err == nil {
		t.Fatal("expected budget error, got nil")
	}
	data, rerr := os.ReadFile(res.LogPath)
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	line := strings.Split(strings.TrimSpace(string(data)), "\n")[0]
	var start map[string]any
	if jerr := json.Unmarshal([]byte(line), &start); jerr != nil {
		t.Fatalf("parse run_start: %v", jerr)
	}
	if start["has_goal"] != true {
		t.Fatalf("has_goal=%v, want true; start=%v", start["has_goal"], start)
	}
	if start["goal"] != "finish hello" {
		t.Fatalf("goal=%v, want finish hello; start=%v", start["goal"], start)
	}
	if start["timezone"] != "Asia/Shanghai" {
		t.Fatalf("timezone=%v, want Asia/Shanghai; start=%v", start["timezone"], start)
	}
	if start["goal_verifier"] != false {
		t.Fatalf("goal_verifier=%v, want false with default test config; start=%v", start["goal_verifier"], start)
	}
}

func TestExecuteRunStartRecordsEnabledGoalVerifier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	moduDir := home + "/.modu"
	if err := os.MkdirAll(moduDir, 0o755); err != nil {
		t.Fatalf("mkdir .modu: %v", err)
	}
	if err := os.WriteFile(moduDir+"/extensions.yaml", []byte(`
extensions:
  - name: goal
    config:
      verifier:
        enabled: true
`), 0o644); err != nil {
		t.Fatalf("write extensions.yaml: %v", err)
	}

	logs := runlog.New(t.TempDir())
	if err := logs.AddDailyTokens(time.Now(), 1); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	deps := Deps{Logs: logs, DailyBudgetTokens: 1}
	task := config.Task{ID: "t", Prompt: "hello", Goal: "finish hello"}

	res, err := Execute(context.Background(), deps, task)
	if err == nil {
		t.Fatal("expected budget error, got nil")
	}
	data, rerr := os.ReadFile(res.LogPath)
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	line := strings.Split(strings.TrimSpace(string(data)), "\n")[0]
	var start map[string]any
	if jerr := json.Unmarshal([]byte(line), &start); jerr != nil {
		t.Fatalf("parse run_start: %v", jerr)
	}
	if start["has_goal"] != true {
		t.Fatalf("has_goal=%v, want true; start=%v", start["has_goal"], start)
	}
	if start["goal_verifier"] != true {
		t.Fatalf("goal_verifier=%v, want true with enabled verifier config; start=%v", start["goal_verifier"], start)
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
		{"goal unavailable not retried", StatusGoalUnavailable, errors.New("goal unavailable"), 2, 1},
		{"goal paused not retried", StatusGoalPaused, errors.New("goal paused"), 2, 1},
		{"goal budget not retried", StatusGoalBudget, errors.New("goal budget"), 2, 1},
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

func TestBuildGoalTaskPromptIncludesGoalContract(t *testing.T) {
	got := buildGoalTaskPrompt("write state/watchlist.md", "run the report")
	for _, want := range []string{
		"write state/watchlist.md",
		"run the report",
		"call update_goal with status=complete",
		"verifier rejects completion",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}

func TestStartTaskGoalRequiresGoalExtension(t *testing.T) {
	if _, err := startTaskGoal(nil, "objective"); err == nil {
		t.Fatal("expected missing goal extension error")
	} else if !errors.Is(err, errTaskGoalUnavailable) {
		t.Fatalf("err=%v, want errTaskGoalUnavailable", err)
	}
}

func TestStartTaskGoalCreatesGoal(t *testing.T) {
	ext := goalext.New(goalext.Options{})
	got, err := startTaskGoal([]extension.Extension{ext}, "objective")
	if err != nil {
		t.Fatalf("startTaskGoal: %v", err)
	}
	if got != ext {
		t.Fatal("startTaskGoal returned the wrong extension")
	}
	status, ok, err := ext.AutomationGoalStatus()
	if err != nil || !ok || status != goalext.StatusActive {
		t.Fatalf("status=%q ok=%v err=%v, want active", status, ok, err)
	}
}

func TestTaskGoalVerifierEnabled(t *testing.T) {
	ext := goalext.New(goalext.Options{})
	if taskGoalVerifierEnabled([]extension.Extension{ext}) {
		t.Fatal("verifier should default to disabled")
	}
	if err := ext.ApplyConfig(map[string]any{"verifier": map[string]any{"enabled": true}}); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if !taskGoalVerifierEnabled([]extension.Extension{ext}) {
		t.Fatal("verifier should be enabled after config")
	}
	if taskGoalVerifierEnabled(nil) {
		t.Fatal("missing goal extension should report verifier disabled")
	}
}

func TestGoalPromptDriverUsesRunContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	driver := newGoalPromptDriver(ctx)
	driver.Drive(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	cancel()
	if err := driver.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("driver.Wait err=%v, want context.Canceled", err)
	}
}

func TestWriteRunEndIncludesGoalStatus(t *testing.T) {
	var buf strings.Builder
	res := Result{
		Started:    time.Now(),
		Ended:      time.Now(),
		Status:     StatusOK,
		GoalStatus: string(goalext.StatusComplete),
	}
	writeRunEnd(&buf, res, nil)
	var end map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &end); err != nil {
		t.Fatalf("parse run_end: %v", err)
	}
	if end["goal_status"] != string(goalext.StatusComplete) {
		t.Fatalf("goal_status=%v, want complete; end=%v", end["goal_status"], end)
	}
}

func TestWriteRunEndRecordsGoalCircuitBreakerStatuses(t *testing.T) {
	for _, status := range []string{StatusGoalPaused, StatusGoalBudget} {
		t.Run(status, func(t *testing.T) {
			var buf strings.Builder
			res := Result{
				Started: time.Now(),
				Ended:   time.Now(),
				Status:  status,
			}
			writeRunEnd(&buf, res, errors.New(status))
			var end map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &end); err != nil {
				t.Fatalf("parse run_end: %v", err)
			}
			if end["status"] != status {
				t.Fatalf("status=%v, want %s; end=%v", end["status"], status, end)
			}
		})
	}
}

func TestGoalCircuitBreakerStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"unavailable", errTaskGoalUnavailable, StatusGoalUnavailable},
		{"paused", errTaskGoalPaused, StatusGoalPaused},
		{"budget", errTaskGoalBudgetLimited, StatusGoalBudget},
		{"plain error", errors.New("plain"), ""},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := goalCircuitBreakerStatus(c.err); got != c.want {
				t.Fatalf("goalCircuitBreakerStatus=%q, want %q", got, c.want)
			}
		})
	}
	if got := goalCircuitBreakerStatus(errors.Join(errors.New("context"), errTaskGoalPaused)); got != StatusGoalPaused {
		t.Fatalf("joined paused status=%q, want %q", got, StatusGoalPaused)
	}
}

func TestExecuteGoalUnavailableWritesCircuitBreakerStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	moduDir := home + "/.modu"
	if err := os.MkdirAll(moduDir, 0o755); err != nil {
		t.Fatalf("mkdir .modu: %v", err)
	}
	if err := os.WriteFile(moduDir+"/extensions.yaml", []byte(`
extensions:
  - name: goal
    enabled: false
`), 0o644); err != nil {
		t.Fatalf("write extensions.yaml: %v", err)
	}

	root := t.TempDir()
	logs := runlog.New(t.TempDir())
	deps := Deps{
		Cwd:      root,
		AgentDir: root + "/.coding_agent",
		Model:    &types.Model{ID: "mock", ProviderID: "openai"},
		Logs:     logs,
	}
	task := config.Task{ID: "t", Prompt: "hello", Goal: "finish hello"}

	res, err := Execute(context.Background(), deps, task)
	if err == nil {
		t.Fatal("expected goal unavailable error, got nil")
	}
	if res.Status != StatusGoalUnavailable {
		t.Fatalf("Status=%q, want %q", res.Status, StatusGoalUnavailable)
	}
	data, rerr := os.ReadFile(res.LogPath)
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected run_start/run_end lines, got %d: %s", len(lines), data)
	}
	var end map[string]any
	if jerr := json.Unmarshal([]byte(lines[len(lines)-1]), &end); jerr != nil {
		t.Fatalf("parse run_end: %v", jerr)
	}
	if end["status"] != StatusGoalUnavailable {
		t.Fatalf("run_end status=%v, want %s; end=%v", end["status"], StatusGoalUnavailable, end)
	}
}
