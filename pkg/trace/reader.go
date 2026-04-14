package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// EventFilter controls which events are returned by ReadEvents.
type EventFilter struct {
	// Types limits results to these event types (e.g. "tool_execution_end").
	Types []string
	// ToolNames limits results to events matching these tool names.
	ToolNames []string
	// Source limits results to events from this source ("agent" or "session").
	Source string
	// ErrorsOnly returns only events where IsError is true.
	ErrorsOnly bool
	// MinSeq returns events with Seq >= MinSeq.
	MinSeq int64
	// After returns events with Time >= After (unix millis).
	After int64
	// Before returns events with Time <= Before (unix millis).
	Before int64
	// Limit caps the number of returned events. 0 means no limit.
	Limit int
}

// ReadEvents reads trace events from a JSONL file, applying the given filter.
func ReadEvents(path string, filter EventFilter) ([]Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open trace events: %w", err)
	}
	defer file.Close()

	var events []Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // skip malformed lines
		}
		if !matchesFilter(event, filter) {
			continue
		}
		events = append(events, event)
		if filter.Limit > 0 && len(events) >= filter.Limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("scan trace events: %w", err)
	}
	return events, nil
}

// ReadSummary reads the trace summary file.
func ReadSummary(path string) (Summary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, fmt.Errorf("read trace summary: %w", err)
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		return Summary{}, fmt.Errorf("parse trace summary: %w", err)
	}
	return summary, nil
}

// TailEvents returns the last n events from the JSONL file.
func TailEvents(path string, n int) ([]Event, error) {
	if n <= 0 {
		return nil, nil
	}
	all, err := ReadEvents(path, EventFilter{})
	if err != nil {
		return nil, err
	}
	if len(all) <= n {
		return all, nil
	}
	return all[len(all)-n:], nil
}

// EventStats computes aggregate statistics from a list of events.
type EventStats struct {
	TotalEvents    int            `json:"totalEvents"`
	ByType         map[string]int `json:"byType"`
	ByTool         map[string]int `json:"byTool"`
	ErrorCount     int            `json:"errorCount"`
	TotalDurationMs int64         `json:"totalDurationMs"`
	ToolDurations  map[string]int64 `json:"toolDurations"`
	TimeSpanMs     int64          `json:"timeSpanMs"`
}

// ComputeStats computes aggregate statistics from a list of events.
func ComputeStats(events []Event) EventStats {
	stats := EventStats{
		TotalEvents:   len(events),
		ByType:        make(map[string]int),
		ByTool:        make(map[string]int),
		ToolDurations: make(map[string]int64),
	}
	var minTime, maxTime int64
	for _, e := range events {
		stats.ByType[e.Type]++
		if e.ToolName != "" {
			stats.ByTool[e.ToolName]++
		}
		if e.IsError {
			stats.ErrorCount++
		}
		if e.DurationMs > 0 {
			stats.TotalDurationMs += e.DurationMs
			if e.ToolName != "" {
				stats.ToolDurations[e.ToolName] += e.DurationMs
			}
		}
		if e.Time > 0 {
			if minTime == 0 || e.Time < minTime {
				minTime = e.Time
			}
			if e.Time > maxTime {
				maxTime = e.Time
			}
		}
	}
	if maxTime > minTime {
		stats.TimeSpanMs = maxTime - minTime
	}
	return stats
}

// FormatEvent returns a single-line human-readable representation of an event.
func FormatEvent(e Event) string {
	ts := time.UnixMilli(e.Time).Format("15:04:05.000")
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] #%d ", ts, e.Seq)
	if e.Turn > 0 {
		fmt.Fprintf(&b, "T%d ", e.Turn)
	}
	b.WriteString(e.Type)
	if e.ToolName != "" {
		fmt.Fprintf(&b, " tool=%s", e.ToolName)
	}
	if e.Role != "" {
		fmt.Fprintf(&b, " role=%s", e.Role)
	}
	if e.IsError {
		b.WriteString(" ERROR")
	}
	if e.DurationMs > 0 {
		fmt.Fprintf(&b, " %dms", e.DurationMs)
	}
	if e.Preview != "" {
		preview := e.Preview
		if len(preview) > 80 {
			preview = preview[:77] + "..."
		}
		fmt.Fprintf(&b, " | %s", preview)
	}
	return b.String()
}

// FormatSummary returns a multi-line human-readable summary string.
func FormatSummary(s Summary) string {
	var b strings.Builder
	started := time.UnixMilli(s.StartedAt).Format("2006-01-02 15:04:05")
	fmt.Fprintf(&b, "Session: %s\n", s.SessionID)
	fmt.Fprintf(&b, "Started: %s\n", started)
	if s.DurationMs > 0 {
		fmt.Fprintf(&b, "Duration: %s\n", formatDuration(s.DurationMs))
	}
	fmt.Fprintf(&b, "Model: %s/%s\n", s.Model.Provider, s.Model.ID)
	fmt.Fprintf(&b, "Events: %d  Turns: %d  Messages: %d  Tools: %d  Errors: %d  Interrupts: %d\n",
		s.Counts.Events, s.Counts.Turns, s.Counts.Messages,
		s.Counts.ToolCalls, s.Counts.Errors, s.Counts.Interrupts)
	fmt.Fprintf(&b, "Tokens: input=%d output=%d cache_read=%d cache_write=%d total=%d\n",
		s.Tokens.Input, s.Tokens.Output, s.Tokens.CacheRead,
		s.Tokens.CacheWrite, s.Tokens.TotalTokens)
	if s.Cost.Total > 0 {
		fmt.Fprintf(&b, "Cost: $%.4f (input=$%.4f output=$%.4f)\n",
			s.Cost.Total, s.Cost.Input, s.Cost.Output)
	}
	return b.String()
}

func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}

func matchesFilter(event Event, f EventFilter) bool {
	if len(f.Types) > 0 && !containsStr(f.Types, event.Type) {
		return false
	}
	if len(f.ToolNames) > 0 && !containsStr(f.ToolNames, event.ToolName) {
		return false
	}
	if f.Source != "" && event.Source != f.Source {
		return false
	}
	if f.ErrorsOnly && !event.IsError {
		return false
	}
	if f.MinSeq > 0 && event.Seq < f.MinSeq {
		return false
	}
	if f.After > 0 && event.Time < f.After {
		return false
	}
	if f.Before > 0 && event.Time > f.Before {
		return false
	}
	return true
}

func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
