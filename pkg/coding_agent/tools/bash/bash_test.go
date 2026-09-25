package bash

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

func newTestTool(t *testing.T) *BashTool {
	t.Helper()
	return &BashTool{cwd: t.TempDir()}
}

func mustText(t *testing.T, res types.ToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content blocks")
	}
	text, ok := res.Content[0].(*types.TextContent)
	if !ok {
		t.Fatalf("content block is %T, want *types.TextContent", res.Content[0])
	}
	return text.Text
}

func mustDetails(t *testing.T, res types.ToolResult) map[string]any {
	t.Helper()
	details, ok := res.Details.(map[string]any)
	if !ok {
		t.Fatalf("Details is %T, want map[string]any", res.Details)
	}
	return details
}

func TestBashToolBasics(t *testing.T) {
	tool := NewTool("/tmp")
	if tool.Name() != "bash" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "bash")
	}
	if tool.Label() == "" {
		t.Error("Label() should not be empty")
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
	params, ok := tool.Parameters().(map[string]any)
	if !ok {
		t.Fatal("Parameters() should return a map")
	}
	required, ok := params["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "command" {
		t.Errorf("required = %#v, want [command]", params["required"])
	}
}

func TestExecuteRequiresCommand(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError && !strings.Contains(mustText(t, res), "required") {
		t.Errorf("expected an error about the missing command, got %q", mustText(t, res))
	}
}

func TestExecuteCapturesStdout(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "echo hello"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := mustText(t, res); !strings.Contains(got, "hello") {
		t.Errorf("output = %q, want it to contain %q", got, "hello")
	}
}

func TestExecuteCombinesStdoutAndStderr(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{
		"command": "echo out; echo err >&2",
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := mustText(t, res)
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Errorf("output = %q, want both stdout and stderr present", got)
	}
}

func TestExecuteReportsNonZeroExitCode(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "exit 3"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	exitCode, ok := mustDetails(t, res)["exitCode"]
	if !ok || exitCode != 3 {
		t.Errorf("Details[exitCode] = %v, want 3", exitCode)
	}
	if !strings.Contains(mustText(t, res), "Exit code: 3") {
		t.Errorf("output should mention the exit code, got %q", mustText(t, res))
	}
}

func TestExecuteRunsInConfiguredCwd(t *testing.T) {
	dir := t.TempDir()
	tool := &BashTool{cwd: dir}
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "pwd"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := strings.TrimSpace(mustText(t, res))
	if runtime.GOOS == "windows" {
		got = windowsBashPathToNative(got)
		if !strings.EqualFold(filepath.Clean(got), filepath.Clean(dir)) {
			t.Errorf("pwd output = %q, want %q", got, dir)
		}
		return
	}
	// macOS temp dirs are often symlinked (/tmp -> /private/tmp); compare
	// suffixes rather than exact equality to stay portable.
	if !strings.HasSuffix(got, strings.TrimSuffix(dir, "/")) {
		t.Errorf("pwd output = %q, want a path ending in %q", got, dir)
	}
}

func windowsBashPathToNative(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if len(path) >= len("/mnt/c/") && strings.HasPrefix(path, "/mnt/") && path[6] == '/' {
		return strings.ToUpper(path[5:6]) + ":" + path[6:]
	}
	if len(path) >= len("/c/") && path[0] == '/' && path[2] == '/' {
		return strings.ToUpper(path[1:2]) + ":" + path[2:]
	}
	return path
}

func TestExecuteNoOutputReportsPlaceholder(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "true"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := mustText(t, res); got != "(no output)" {
		t.Errorf("output = %q, want %q", got, "(no output)")
	}
}

func TestExecuteTimesOutLongRunningCommand(t *testing.T) {
	tool := newTestTool(t)
	start := time.Now()
	// Wrapped in sh -c so the leading-sleep blocklist (which only matches a
	// command that literally starts with "sleep") doesn't intercept it
	// before the timeout logic ever runs.
	res, err := tool.Execute(context.Background(), "id", map[string]any{
		"command": "sh -c 'sleep 10'",
		"timeout": 1,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took too long to fire: %v", elapsed)
	}
	got := mustText(t, res)
	if !strings.Contains(got, "timed out") {
		t.Errorf("output = %q, want it to mention the timeout", got)
	}
	if mustDetails(t, res)["timedOut"] != true {
		t.Errorf("Details[timedOut] = %v, want true", mustDetails(t, res)["timedOut"])
	}
}

func TestExecuteBackgroundReturnsImmediatelyWithPID(t *testing.T) {
	tool := newTestTool(t)
	start := time.Now()
	res, err := tool.Execute(context.Background(), "id", map[string]any{
		"command":    "sleep 5",
		"background": true,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("background command should return immediately, took %v", elapsed)
	}
	pid, ok := mustDetails(t, res)["pid"]
	if !ok {
		t.Fatal("Details[pid] missing for a background command")
	}
	if n, ok := pid.(int); !ok || n <= 0 {
		t.Errorf("Details[pid] = %#v, want a positive int", pid)
	}
	if mustDetails(t, res)["background"] != true {
		t.Errorf("Details[background] = %v, want true", mustDetails(t, res)["background"])
	}
}

func TestExecuteBackgroundAcceptsRunInBackgroundAlias(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{
		"command":           "sleep 5",
		"run_in_background": true,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if mustDetails(t, res)["background"] != true {
		t.Error("run_in_background alias should behave like background=true")
	}
}

func TestBackgroundJobOutputDrainsIncrementally(t *testing.T) {
	store := NewJobStore()
	t.Cleanup(store.KillAll)
	tool := NewToolWithStore(t.TempDir(), nil, store)
	started, err := tool.Execute(context.Background(), "id", map[string]any{
		"command":           "printf first; sleep 0.05; printf second",
		"run_in_background": true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := mustDetails(t, started)["bash_id"].(string)
	if id == "" {
		t.Fatalf("missing bash_id: %#v", started.Details)
	}

	outputTool := NewBashOutputTool(store)
	var combined string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, err := outputTool.Execute(context.Background(), "out", map[string]any{"bash_id": id}, nil)
		if err != nil {
			t.Fatal(err)
		}
		combined += mustText(t, result)
		details := mustDetails(t, result)
		if details["status"] == JobExited {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{"first", "second", "exited code 0"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("missing %q in drained output:\n%s", want, combined)
		}
	}

	empty, err := outputTool.Execute(context.Background(), "out-2", map[string]any{"bash_id": id}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := mustText(t, empty); strings.Contains(text, "first") || strings.Contains(text, "second") {
		t.Fatalf("output was repeated after drain: %s", text)
	}
}

func TestKillBashTerminatesProcessGroup(t *testing.T) {
	store := NewJobStore()
	t.Cleanup(store.KillAll)
	tool := NewToolWithStore(t.TempDir(), nil, store)
	started, err := tool.Execute(context.Background(), "id", map[string]any{
		"command":           "sleep 30",
		"run_in_background": true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := mustDetails(t, started)["bash_id"].(string)

	killed, err := NewKillBashTool(store).Execute(context.Background(), "kill", map[string]any{"bash_id": id}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mustDetails(t, killed)["killed"] != true {
		t.Fatalf("expected job to be killed: %#v", killed.Details)
	}
	again, err := NewKillBashTool(store).Execute(context.Background(), "kill-again", map[string]any{"bash_id": id}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mustDetails(t, again)["killed"] != false {
		t.Fatalf("second kill should be idempotent: %#v", again.Details)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, _ := store.Get(id)
		if job.snapshot().Status == JobExited {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background job did not exit after kill_bash")
}

func TestBackgroundJobOutputIsBounded(t *testing.T) {
	job := &Job{status: JobRunning}
	large := strings.Repeat("x", maxBackgroundOutputBytes+128)
	if _, err := job.Write([]byte(large)); err != nil {
		t.Fatal(err)
	}
	output, truncated := job.drain()
	if !truncated {
		t.Fatal("expected dropped output to be reported as truncated")
	}
	if len(output) != maxBackgroundOutputBytes {
		t.Fatalf("bounded output length = %d", len(output))
	}
}

func TestExecuteBlocksSedInPlaceEdit(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "sed -i 's/a/b/' file.txt"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(mustText(t, res), "edit tool") {
		t.Errorf("expected a sed -i block message, got %q", mustText(t, res))
	}
}

func TestExecuteBlocksSimpleFileRead(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "cat file.txt"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(mustText(t, res), "read tool") {
		t.Errorf("expected a read-tool block message, got %q", mustText(t, res))
	}
}

func TestExecuteBlocksSimpleContentSearch(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "grep foo file.txt"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(mustText(t, res), "grep tool") {
		t.Errorf("expected a grep-tool block message, got %q", mustText(t, res))
	}
}

func TestExecuteBlocksSimpleFilePatternSearch(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "find . -name '*.go'"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(mustText(t, res), "find tool") {
		t.Errorf("expected a find-tool block message, got %q", mustText(t, res))
	}
}

func TestExecuteBlocksSimpleDirectoryListing(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "ls -la"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(mustText(t, res), "ls tool") {
		t.Errorf("expected an ls-tool block message, got %q", mustText(t, res))
	}
}

func TestExecuteBlocksLongForegroundSleep(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "sleep 3"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(mustText(t, res), "background") {
		t.Errorf("expected a sleep block message, got %q", mustText(t, res))
	}
}

func TestExecuteAllowsShortForegroundSleep(t *testing.T) {
	// Sleeps under 2s are allowed (pacing/rate-limiting use case) and should
	// actually run rather than being blocked.
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "sleep 0.1 && echo done"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(mustText(t, res), "done") {
		t.Errorf("short sleep should run, got %q", mustText(t, res))
	}
}

func TestExecuteAllowsSleepInBackgroundMode(t *testing.T) {
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{
		"command":    "sleep 5",
		"background": true,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, hasPID := mustDetails(t, res)["pid"]; !hasPID {
		t.Errorf("background sleep should be allowed to run, got %q", mustText(t, res))
	}
}

func TestExecuteAllowsPipedCommandsBypassingBlocklists(t *testing.T) {
	// The blocklists only apply to bare "simple" invocations. Once shell
	// metacharacters are involved, the command is not "simple" and must
	// pass through unblocked (bash is still the only way to run it).
	tool := newTestTool(t)
	res, err := tool.Execute(context.Background(), "id", map[string]any{"command": "echo hi | cat"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(mustText(t, res), "hi") {
		t.Errorf("piped cat should run, got %q", mustText(t, res))
	}
}

func TestDetectBlockedSleepPattern(t *testing.T) {
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		{"standalone long sleep", "sleep 5", true},
		{"standalone short sleep", "sleep 1", false},
		{"boundary at exactly 2s", "sleep 2", true},
		{"just under boundary", "sleep 1.99", false},
		{"decimal seconds", "sleep 2.5", true},
		{"with s suffix", "sleep 5s", true},
		{"leading whitespace", "   sleep 5", true},
		{"followed by another command", "sleep 5 && echo done", true},
		{"followed by semicolon", "sleep 5; echo done", true},
		{"not a sleep command", "echo sleep 5", false},
		{"sleepy is not sleep", "sleepy 5", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, blocked := detectBlockedSleepPattern(tt.command)
			if blocked != tt.blocked {
				t.Errorf("detectBlockedSleepPattern(%q) blocked = %v, want %v", tt.command, blocked, tt.blocked)
			}
		})
	}
}

func TestIsSedInPlaceEdit(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"sed -i 's/a/b/' file.txt", true},
		{"sed -i.bak 's/a/b/' file.txt", true},
		{"sed --in-place 's/a/b/' file.txt", true},
		{"sed --in-place=.bak 's/a/b/' file.txt", true},
		{"sed 's/a/b/' file.txt", false},
		{"sed -n '/foo/p' file.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := isSedInPlaceEdit(tt.command); got != tt.want {
				t.Errorf("isSedInPlaceEdit(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsSimpleFileReadCommand(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"cat file.txt", true},
		{"cat -n file.txt", true},
		{"head -n 20 file.txt", true},
		{"tail -f file.txt", false}, // -f is not in the accepted flag set
		{"tail file.txt", true},
		{"cat", false},                     // no path at all
		{"cat file.txt | grep foo", false}, // pipe -> not "simple"
		{"cat *.txt", false},               // glob -> not "simple"
		{"cat file1.txt file2.txt", true},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := isSimpleFileReadCommand(tt.command); got != tt.want {
				t.Errorf("isSimpleFileReadCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsSimpleContentSearchCommand(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"grep foo file.txt", true},
		{"grep -rn foo .", true},
		{"rg foo .", true},
		{"grep foo", true}, // pattern alone is still "simple" (e.g. reads stdin)
		{"grep -e foo file.txt", true},
		{"grep foo file.txt | wc -l", false}, // pipe -> not simple
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := isSimpleContentSearchCommand(tt.command); got != tt.want {
				t.Errorf("isSimpleContentSearchCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsSimpleFilePatternSearchCommand(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"find . -name '*.go'", true},
		{"find . -type f -name '*.go'", true},
		// A literal "$" anywhere in the raw command trips the blanket
		// shell-metacharacter guard even inside quotes (conservative: worst
		// case the command just runs via bash directly instead of being
		// redirected to the specialized tool).
		{"fd '.go$'", false},
		{"fd -e go main", true}, // "main" is the search pattern, -e is a filter
		{"fd -e go", false},     // a filter flag alone is not a search pattern
		{"find .", false},       // no -name
		{"find . -exec rm {} \\;", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := isSimpleFilePatternSearchCommand(tt.command); got != tt.want {
				t.Errorf("isSimpleFilePatternSearchCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsSimpleDirectoryListCommand(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"ls", true},
		{"ls -la", true},
		{"ls -la /tmp", true},
		{"ls /tmp /var", false}, // more than one path
		{"ls -la | head", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := isSimpleDirectoryListCommand(tt.command); got != tt.want {
				t.Errorf("isSimpleDirectoryListCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestSplitSimpleShellFields(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
		ok      bool
	}{
		{"plain words", "cat a.txt", []string{"cat", "a.txt"}, true},
		{"double quoted", `cat "a b.txt"`, []string{"cat", "a b.txt"}, true},
		{"single quoted", `cat 'a b.txt'`, []string{"cat", "a b.txt"}, true},
		{"unterminated quote fails", `cat "a b.txt`, nil, false},
		{"multiple spaces collapse", "cat   a.txt", []string{"cat", "a.txt"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := splitSimpleShellFields(tt.command)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("fields = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("fields[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseTimeoutDefaults(t *testing.T) {
	got := parseTimeout(map[string]any{})
	want := time.Duration(defaultBashTimeoutSeconds) * time.Second
	if got != want {
		t.Errorf("parseTimeout({}) = %v, want %v", got, want)
	}
}

func TestParseTimeoutSecondsWithinBudget(t *testing.T) {
	got := parseTimeout(map[string]any{"timeout": 30})
	if got != 30*time.Second {
		t.Errorf("parseTimeout(timeout=30) = %v, want 30s", got)
	}
}

func TestParseTimeoutAmbiguityHeuristic(t *testing.T) {
	// Values up to maxBashTimeoutSeconds are read as seconds; above that,
	// as Claude-style milliseconds.
	got := parseTimeout(map[string]any{"timeout": maxBashTimeoutSeconds})
	if got != time.Duration(maxBashTimeoutSeconds)*time.Second {
		t.Errorf("parseTimeout(timeout=%d) = %v, want %ds as seconds", maxBashTimeoutSeconds, got, maxBashTimeoutSeconds)
	}

	// One over the boundary flips interpretation to milliseconds — 601 reads
	// as 601ms, not 601s, so it's nowhere near the clamp and comes back
	// unchanged. The heuristic is intentionally discontinuous at this edge
	// (see parseTimeout's comment): a genuine 601-second request would only
	// be expressible via timeout_ms, since raw "timeout" clamps at 600.
	got = parseTimeout(map[string]any{"timeout": maxBashTimeoutSeconds + 1})
	want := time.Duration(maxBashTimeoutSeconds+1) * time.Millisecond
	if got != want {
		t.Errorf("parseTimeout(timeout=%d) = %v, want %v (read as milliseconds)", maxBashTimeoutSeconds+1, got, want)
	}
}

func TestParseTimeoutMSExplicit(t *testing.T) {
	got := parseTimeout(map[string]any{"timeout_ms": 5000})
	if got != 5*time.Second {
		t.Errorf("parseTimeout(timeout_ms=5000) = %v, want 5s", got)
	}
}

func TestParseTimeoutAcceptsNumericStrings(t *testing.T) {
	got := parseTimeout(map[string]any{"timeout": "30"})
	if got != 30*time.Second {
		t.Errorf("parseTimeout(timeout=\"30\") = %v, want 30s", got)
	}
}

func TestParseTimeoutZeroOrNegativeFallsBackToDefault(t *testing.T) {
	want := time.Duration(defaultBashTimeoutSeconds) * time.Second
	for _, v := range []any{0, -5, "0", "-5"} {
		got := parseTimeout(map[string]any{"timeout": v})
		if got != want {
			t.Errorf("parseTimeout(timeout=%v) = %v, want default %v", v, got, want)
		}
	}
}

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"within bounds unchanged", 10 * time.Second, 10 * time.Second},
		{"over max clamps to max", 1000 * time.Second, time.Duration(maxBashTimeoutSeconds) * time.Second},
		{"zero falls back to default", 0, time.Duration(defaultBashTimeoutSeconds) * time.Second},
		{"negative falls back to default", -5 * time.Second, time.Duration(defaultBashTimeoutSeconds) * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampTimeout(tt.in); got != tt.want {
				t.Errorf("clampTimeout(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatTimeout(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{5 * time.Second, "5 seconds"},
		{500 * time.Millisecond, "500 milliseconds"},
		{1500 * time.Millisecond, "1500 milliseconds"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatTimeout(tt.in); got != tt.want {
				t.Errorf("formatTimeout(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
