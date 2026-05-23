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
	fmt.Fprintf(out, "%-32s %10s  %s\n", "FILE", "SIZE", "MODIFIED")
	for _, e := range entries {
		fmt.Fprintf(out, "%-32s %10s  %s\n", e.Name, humanSize(e.Size), e.ModTime.Format("2006-01-02 15:04:05"))
	}
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

// decodeEventStream renders the NDJSON event stream into a compact,
// human-readable transcript. Unknown event types are emitted as a one-line
// "raw" entry so nothing is silently swallowed.
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
		case "session_start":
			fmt.Fprintf(out, "▶ session start  model=%v session=%v\n", ev["model"], ev["sessionId"])
		case "session_end":
			fmt.Fprintln(out, "■ session end")
		case "tool_call_start":
			fmt.Fprintf(out, "→ tool call      %s%s\n", ev["toolName"], formatArgs(ev["args"]))
		case "tool_call_end":
			marker := "ok"
			if isErr, _ := ev["isError"].(bool); isErr {
				marker = "ERROR"
			}
			fmt.Fprintf(out, "← tool result    %s  %s\n", ev["toolName"], marker)
		case "message_end":
			text := extractAssistantText(ev["message"])
			if text != "" {
				fmt.Fprintln(out, "✎ assistant:")
				for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
					fmt.Fprintf(out, "    %s\n", line)
				}
			}
		case "", "message_update":
			// message_update is per-token noise; skip in decoded view.
			continue
		default:
			fmt.Fprintf(out, "· %s\n", kind)
		}
	}
}

// extractAssistantText pulls a flat text from an AssistantMessage encoded as
// JSON. The shape is `{"content": [{"text": "..."}, ...]}` for the text
// blocks we care about.
func extractAssistantText(message any) string {
	msg, ok := message.(map[string]any)
	if !ok {
		return ""
	}
	content, ok := msg["content"].([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, c := range content {
		blk, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := blk["text"].(string); ok && t != "" {
			b.WriteString(t)
		}
	}
	return b.String()
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
