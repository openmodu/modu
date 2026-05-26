package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/openmodu/modu/cmd/modu_cron/internal/runlog"
)

// LogsOptions controls the Logs subcommand output.
//
//   - File: a specific log filename to display. Empty means "use --tail" if
//     set, otherwise list.
//   - Tail: when true, display the most recent run.
//   - JSON: when true, dump the raw NDJSON file contents instead of decoding.
type LogsOptions struct {
	File string
	Tail bool
	JSON bool
}

// Logs is the entry point for `modu_cron logs <id>`.
//
// Behavior:
//
//	logs <id>                 → list historical runs (newest first)
//	logs <id> --tail          → display the most recent run, decoded
//	logs <id> --file <name>   → display a specific run, decoded
//	... + --json              → print raw NDJSON instead of decoding
func Logs(taskID string, opts LogsOptions, out io.Writer) error {
	if taskID == "" {
		return errors.New("missing task id")
	}
	store := runlog.New("")

	switch {
	case opts.File != "":
		return printRun(store, taskID, opts.File, opts.JSON, out)
	case opts.Tail:
		entries, err := store.List(taskID)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("task %s: no log files yet (looked in %s)", taskID, store.TaskDir(taskID))
		}
		return printRun(store, taskID, entries[0].Name, opts.JSON, out)
	default:
		return listRuns(store, taskID, out)
	}
}

func listRuns(store *runlog.Store, taskID string, out io.Writer) error {
	entries, err := store.List(taskID)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintf(out, "(no logs for task %q — looked in %s)\n", taskID, store.TaskDir(taskID))
		return nil
	}
	fmt.Fprintf(out, "Task %s — %d run(s) in %s\n\n", taskID, len(entries), store.TaskDir(taskID))
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{e.Name, humanSize(e.Size), e.ModTime.Format("2006-01-02 15:04:05")})
	}
	writeTable(out, []tableColumn{
		{Header: "FILE", Max: 40},
		{Header: "SIZE", Max: 10, Right: true},
		{Header: "MODIFIED", Max: 19},
	}, rows)
	fmt.Fprintln(out, "\nUse --tail for the latest, or --file <name> for a specific run. Add --json for raw NDJSON.")
	return nil
}

func printRun(store *runlog.Store, taskID, name string, raw bool, out io.Writer) error {
	path, err := store.Resolve(taskID, name)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("task %s: log file %q not found", taskID, name)
		}
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if raw {
		_, err := io.Copy(out, f)
		return err
	}
	return decodeEventStream(f, out)
}

// decodeEventStream renders the slim NDJSON transcript produced by
// runner.summaryWriter into a compact human-readable view. The transcript
// has one line per meaningful step (run_start / session_start / user /
// assistant / tool_call / tool_result / run_end), so decoding is mostly a
// straight field lookup — no filtering or content flattening needed.
//
// Unknown event types fall to a one-line "raw" entry so anything new added
// upstream isn't silently swallowed.
func decodeEventStream(r io.Reader, out io.Writer) error {
	dec := json.NewDecoder(r)
	for {
		var ev map[string]any
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			fmt.Fprintf(out, "  (decode error: %v)\n", err)
			return nil
		}
		kind, _ := ev["type"].(string)
		switch kind {
		case "run_start":
			fmt.Fprintf(out, "▶ run start      task=%v started=%v\n", ev["task_id"], ev["started_at"])
			if p, _ := ev["prompt"].(string); p != "" {
				fmt.Fprintln(out, "✎ prompt:")
				writeIndented(out, p)
			}
		case "session_start":
			fmt.Fprintf(out, "  session         model=%v id=%v\n", ev["model"], ev["session_id"])
		case "user":
			if t, _ := ev["text"].(string); t != "" {
				fmt.Fprintln(out, "✎ user:")
				writeIndented(out, t)
			}
		case "assistant":
			if t, _ := ev["text"].(string); t != "" {
				fmt.Fprintln(out, "✎ assistant:")
				writeIndented(out, t)
			}
		case "tool_call":
			fmt.Fprintf(out, "→ tool call      %s%s\n", ev["name"], formatArgs(ev["args"]))
		case "tool_result":
			marker := "ok"
			if ok, _ := ev["ok"].(bool); !ok {
				marker = "ERROR"
			}
			fmt.Fprintf(out, "← tool result    %s  %s\n", ev["name"], marker)
			if s, _ := ev["snippet"].(string); s != "" {
				writeIndented(out, s)
			}
		case "run_end":
			status, _ := ev["status"].(string)
			dur, _ := ev["duration_ms"].(float64)
			fmt.Fprintf(out, "■ run end        status=%s duration=%dms\n", status, int64(dur))
			if e, _ := ev["error"].(string); e != "" {
				fmt.Fprintln(out, "  error:")
				writeIndented(out, e)
			}
		default:
			fmt.Fprintf(out, "· %s\n", kind)
		}
	}
}

// writeIndented prefixes each line of text with 4 spaces so the user can
// tell quoted content apart from event markers at a glance.
func writeIndented(out io.Writer, text string) {
	if text == "" {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Fprintf(out, "    %s\n", line)
	}
}

// formatArgs renders tool args as `(key=value, ...)` truncated for one-line
// display. Empty args produce an empty string.
func formatArgs(args any) string {
	m, ok := args.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%s", k, truncate(fmt.Sprintf("%v", v), 60)))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func humanSize(n int64) string {
	const (
		KB = 1024
		MB = 1024 * 1024
	)
	switch {
	case n >= MB:
		return fmt.Sprintf("%.1fMB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1fKB", float64(n)/KB)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
