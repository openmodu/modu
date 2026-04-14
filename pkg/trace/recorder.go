package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

const defaultPreviewLimit = 240

type Options struct {
	SessionID       string
	Cwd             string
	Provider        string
	ModelID         string
	EventsFile      string
	SummaryFile     string
	PreviewLimit    int
	MaxFileSizeBytes int64
	MaxRotatedFiles  int
}

type ModelRef struct {
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
}

type Counts struct {
	Events            int `json:"events"`
	Turns             int `json:"turns"`
	Messages          int `json:"messages"`
	AssistantMessages int `json:"assistantMessages"`
	ToolCalls         int `json:"toolCalls"`
	SessionEvents     int `json:"sessionEvents"`
	Errors            int `json:"errors"`
	Interrupts        int `json:"interrupts"`
}

type Totals struct {
	Turns             int     `json:"turns"`
	Messages          int     `json:"messages"`
	AssistantMessages int     `json:"assistantMessages"`
	ToolCalls         int     `json:"toolCalls"`
	Errors            int     `json:"errors"`
	Interrupts        int     `json:"interrupts"`
	Input             int     `json:"input"`
	Output            int     `json:"output"`
	CacheRead         int     `json:"cacheRead"`
	CacheWrite        int     `json:"cacheWrite"`
	TotalTokens       int     `json:"totalTokens"`
	CostTotal         float64 `json:"costTotal"`
}

type Event struct {
	Seq        int64            `json:"seq"`
	Time       int64            `json:"time"`
	Source     string           `json:"source"`
	Type       string           `json:"type"`
	Turn       int              `json:"turn,omitempty"`
	Role       string           `json:"role,omitempty"`
	ToolName   string           `json:"toolName,omitempty"`
	ToolCallID string           `json:"toolCallId,omitempty"`
	Parallel   bool             `json:"parallel,omitempty"`
	IsError    bool             `json:"isError,omitempty"`
	StopReason string           `json:"stopReason,omitempty"`
	Preview    string           `json:"preview,omitempty"`
	DurationMs int64            `json:"durationMs,omitempty"`
	Usage      types.AgentUsage `json:"usage,omitempty"`
	Args       map[string]any   `json:"args,omitempty"`
	Details    any              `json:"details,omitempty"`
	Meta       map[string]any   `json:"meta,omitempty"`
	Totals     Totals           `json:"totals"`
}

type Summary struct {
	StartedAt  int64    `json:"startedAt"`
	UpdatedAt  int64    `json:"updatedAt"`
	DurationMs int64    `json:"durationMs"`
	SessionID  string   `json:"sessionId,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	Model      ModelRef `json:"model"`
	Counts     Counts   `json:"counts"`
	Tokens     struct {
		Input       int `json:"input"`
		Output      int `json:"output"`
		CacheRead   int `json:"cacheRead"`
		CacheWrite  int `json:"cacheWrite"`
		TotalTokens int `json:"totalTokens"`
	} `json:"tokens"`
	Cost struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cacheRead"`
		CacheWrite float64 `json:"cacheWrite"`
		Total      float64 `json:"total"`
	} `json:"cost"`
	LastEvent *Event `json:"lastEvent,omitempty"`
}

type Recorder struct {
	mu             sync.Mutex
	opts           Options
	summary        Summary
	seq            int64
	currentTurn    int
	turnStartTime  int64
	toolStartTimes map[string]int64
	closed         bool
}

func NewRecorder(opts Options) (*Recorder, error) {
	if opts.PreviewLimit <= 0 {
		opts.PreviewLimit = defaultPreviewLimit
	}
	now := time.Now().UnixMilli()
	r := &Recorder{
		opts: opts,
		summary: Summary{
			StartedAt: now,
			UpdatedAt: now,
			SessionID: opts.SessionID,
			Cwd:       opts.Cwd,
			Model: ModelRef{
				Provider: opts.Provider,
				ID:       opts.ModelID,
			},
		},
		toolStartTimes: make(map[string]int64),
	}
	if err := ensureParentDir(opts.EventsFile); err != nil {
		return nil, err
	}
	if err := ensureParentDir(opts.SummaryFile); err != nil {
		return nil, err
	}
	if err := r.flushSummaryLocked(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Recorder) Summary() Summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSummary(r.summary)
}

func (r *Recorder) RecordSessionEvent(eventType string, meta map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if provider, _ := meta["provider"].(string); provider != "" {
		r.summary.Model.Provider = provider
	}
	if modelID, _ := meta["modelId"].(string); modelID != "" {
		r.summary.Model.ID = modelID
	}
	if sessionID, _ := meta["sessionId"].(string); sessionID != "" {
		r.summary.SessionID = sessionID
	}
	if cwd, _ := meta["cwd"].(string); cwd != "" {
		r.summary.Cwd = cwd
	}

	event := Event{
		Source: "session",
		Type:   eventType,
		Meta:   cloneMap(meta),
	}
	r.summary.Counts.SessionEvents++
	return r.appendEventLocked(event)
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.summary.DurationMs = time.Now().UnixMilli() - r.summary.StartedAt
	return r.flushSummaryLocked()
}

func (r *Recorder) RecordAgentEvent(event agent.AgentEvent) error {
	built, ok := r.buildAgentEvent(event)
	if !ok {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appendEventLocked(built)
}

func (r *Recorder) buildAgentEvent(event agent.AgentEvent) (Event, bool) {
	switch event.Type {
	case agent.EventTypeAgentStart:
		return Event{
			Source: "agent",
			Type:   string(event.Type),
		}, true
	case agent.EventTypeAgentEnd:
		return Event{
			Source:  "agent",
			Type:    string(event.Type),
			IsError: event.IsError,
		}, true
	case agent.EventTypeTurnStart:
		return Event{
			Source: "agent",
			Type:   string(event.Type),
		}, true
	case agent.EventTypeTurnEnd:
		return Event{
			Source:     "agent",
			Type:       string(event.Type),
			ToolName:   event.ToolName,
			ToolCallID: event.ToolCallID,
		}, true
	case agent.EventTypeInterrupt:
		return Event{
			Source:     "agent",
			Type:       string(event.Type),
			ToolName:   event.ToolName,
			ToolCallID: event.ToolCallID,
			Parallel:   event.Parallel,
			Meta:       interruptMeta(event),
		}, true
	case agent.EventTypeMessageStart:
		role, preview, _, _ := summarizeMessage(event.Message, r.opts.PreviewLimit)
		return Event{
			Source:  "agent",
			Type:    string(event.Type),
			Role:    role,
			Preview: preview,
		}, true
	case agent.EventTypeMessageUpdate:
		// Streaming updates are high-frequency; record type only, no preview.
		return Event{
			Source: "agent",
			Type:   string(event.Type),
		}, true
	case agent.EventTypeMessageEnd:
		role, preview, usage, stopReason := summarizeMessage(event.Message, r.opts.PreviewLimit)
		return Event{
			Source:     "agent",
			Type:       string(event.Type),
			Role:       role,
			Preview:    preview,
			StopReason: stopReason,
			Usage:      usage,
		}, true
	case agent.EventTypeToolExecutionStart:
		return Event{
			Source:     "agent",
			Type:       string(event.Type),
			ToolName:   event.ToolName,
			ToolCallID: event.ToolCallID,
			Parallel:   event.Parallel,
			Args:       cloneAnyMap(event.Args),
		}, true
	case agent.EventTypeToolExecutionUpdate:
		return Event{
			Source:     "agent",
			Type:       string(event.Type),
			ToolName:   event.ToolName,
			ToolCallID: event.ToolCallID,
			Parallel:   event.Parallel,
		}, true
	case agent.EventTypeToolExecutionEnd:
		preview, details := summarizeToolResult(event.Result, r.opts.PreviewLimit)
		return Event{
			Source:     "agent",
			Type:       string(event.Type),
			ToolName:   event.ToolName,
			ToolCallID: event.ToolCallID,
			Parallel:   event.Parallel,
			IsError:    event.IsError,
			Preview:    preview,
			Args:       cloneAnyMap(event.Args),
			Details:    details,
		}, true
	default:
		return Event{}, false
	}
}

func (r *Recorder) appendEventLocked(event Event) error {
	now := time.Now().UnixMilli()
	event.Seq = r.seq + 1
	event.Time = now

	switch event.Type {
	case string(agent.EventTypeTurnStart):
		r.currentTurn++
		r.summary.Counts.Turns++
		r.turnStartTime = now
	case string(agent.EventTypeTurnEnd):
		if r.turnStartTime > 0 {
			event.DurationMs = now - r.turnStartTime
			r.turnStartTime = 0
		}
	case string(agent.EventTypeMessageEnd):
		r.summary.Counts.Messages++
		if event.Role == string(agent.RoleAssistant) {
			r.summary.Counts.AssistantMessages++
		}
		addUsage(&r.summary, event.Usage)
	case string(agent.EventTypeToolExecutionStart):
		r.toolStartTimes[event.ToolCallID] = now
	case string(agent.EventTypeToolExecutionEnd):
		r.summary.Counts.ToolCalls++
		if event.IsError {
			r.summary.Counts.Errors++
		}
		if startTime, ok := r.toolStartTimes[event.ToolCallID]; ok {
			event.DurationMs = now - startTime
			delete(r.toolStartTimes, event.ToolCallID)
		}
	case string(agent.EventTypeInterrupt):
		r.summary.Counts.Interrupts++
	case string(agent.EventTypeAgentEnd):
		if event.IsError {
			r.summary.Counts.Errors++
		}
		r.summary.DurationMs = now - r.summary.StartedAt
	}

	// Attach turn number to all events except agent_start.
	if event.Type != string(agent.EventTypeAgentStart) {
		event.Turn = r.currentTurn
	}

	r.seq = event.Seq
	r.summary.Counts.Events++
	r.summary.UpdatedAt = now
	event.Totals = Totals{
		Turns:             r.summary.Counts.Turns,
		Messages:          r.summary.Counts.Messages,
		AssistantMessages: r.summary.Counts.AssistantMessages,
		ToolCalls:         r.summary.Counts.ToolCalls,
		Errors:            r.summary.Counts.Errors,
		Interrupts:        r.summary.Counts.Interrupts,
		Input:             r.summary.Tokens.Input,
		Output:            r.summary.Tokens.Output,
		CacheRead:         r.summary.Tokens.CacheRead,
		CacheWrite:        r.summary.Tokens.CacheWrite,
		TotalTokens:       r.summary.Tokens.TotalTokens,
		CostTotal:         r.summary.Cost.Total,
	}
	clone := event
	clone.Meta = cloneMap(event.Meta)
	clone.Args = cloneMap(event.Args)
	r.summary.LastEvent = &clone

	if err := r.appendEventFileLocked(event); err != nil {
		return err
	}
	return r.flushSummaryLocked()
}

func (r *Recorder) appendEventFileLocked(event Event) error {
	if strings.TrimSpace(r.opts.EventsFile) == "" {
		return nil
	}
	// Check rotation before writing.
	if r.opts.MaxFileSizeBytes > 0 {
		if info, err := os.Stat(r.opts.EventsFile); err == nil && info.Size() >= r.opts.MaxFileSizeBytes {
			r.rotateEventsFileLocked()
		}
	}
	file, err := os.OpenFile(r.opts.EventsFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open trace events file: %w", err)
	}
	defer file.Close()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal trace event: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write trace event: %w", err)
	}
	return nil
}

// rotateEventsFileLocked renames events.jsonl -> events.1.jsonl, etc.
// Keeps at most MaxRotatedFiles rotated files.
func (r *Recorder) rotateEventsFileLocked() {
	base := r.opts.EventsFile
	maxKeep := r.opts.MaxRotatedFiles
	if maxKeep <= 0 {
		maxKeep = 3
	}
	// Remove the oldest rotated file.
	oldest := fmt.Sprintf("%s.%d", base, maxKeep)
	_ = os.Remove(oldest)
	// Shift existing rotated files: N-1 -> N, N-2 -> N-1, etc.
	for i := maxKeep - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", base, i)
		to := fmt.Sprintf("%s.%d", base, i+1)
		_ = os.Rename(from, to)
	}
	// Rotate current file to .1
	_ = os.Rename(base, fmt.Sprintf("%s.1", base))
}

func (r *Recorder) flushSummaryLocked() error {
	if strings.TrimSpace(r.opts.SummaryFile) == "" {
		return nil
	}
	data, err := json.MarshalIndent(r.summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal trace summary: %w", err)
	}
	if err := os.WriteFile(r.opts.SummaryFile, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write trace summary: %w", err)
	}
	return nil
}

func interruptMeta(event agent.AgentEvent) map[string]any {
	if event.Interrupt == nil {
		return nil
	}
	meta := map[string]any{
		"reason":    event.Interrupt.Reason,
		"stepCount": event.Interrupt.StepCount,
	}
	if event.Interrupt.ToolCallID != "" {
		meta["toolCallId"] = event.Interrupt.ToolCallID
	}
	if event.Interrupt.ToolName != "" {
		meta["toolName"] = event.Interrupt.ToolName
	}
	if len(event.Interrupt.ToolArgs) > 0 {
		meta["toolArgs"] = cloneMap(event.Interrupt.ToolArgs)
	}
	return meta
}

func summarizeMessage(msg agent.AgentMessage, limit int) (string, string, types.AgentUsage, string) {
	switch m := msg.(type) {
	case types.UserMessage:
		return string(agent.RoleUser), previewFromAny(m.Content, limit), types.AgentUsage{}, ""
	case *types.UserMessage:
		return string(agent.RoleUser), previewFromAny(m.Content, limit), types.AgentUsage{}, ""
	case types.AssistantMessage:
		return string(agent.RoleAssistant), previewAssistantContent(m.Content, limit), m.Usage, m.StopReason
	case *types.AssistantMessage:
		return string(agent.RoleAssistant), previewAssistantContent(m.Content, limit), m.Usage, m.StopReason
	case types.ToolResultMessage:
		return string(agent.RoleToolResult), previewContentBlocks(m.Content, limit), types.AgentUsage{}, ""
	case *types.ToolResultMessage:
		return string(agent.RoleToolResult), previewContentBlocks(m.Content, limit), types.AgentUsage{}, ""
	default:
		return "", previewFromAny(msg, limit), types.AgentUsage{}, ""
	}
}

func summarizeToolResult(result any, limit int) (string, any) {
	switch v := result.(type) {
	case agent.AgentToolResult:
		return previewContentBlocks(v.Content, limit), sanitizeAny(v.Details)
	case *agent.AgentToolResult:
		if v == nil {
			return "", nil
		}
		return previewContentBlocks(v.Content, limit), sanitizeAny(v.Details)
	default:
		return previewFromAny(result, limit), nil
	}
}

func previewAssistantContent(content []types.ContentBlock, limit int) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch v := block.(type) {
		case *types.TextContent:
			if text := strings.TrimSpace(v.Text); text != "" {
				parts = append(parts, "text: "+text)
			}
		case *types.ThinkingContent:
			if thinking := strings.TrimSpace(v.Thinking); thinking != "" {
				parts = append(parts, "thinking: "+thinking)
			}
		case *types.ToolCallContent:
			if v.Name != "" {
				parts = append(parts, "tool_call: "+v.Name)
			}
		case *types.ImageContent:
			parts = append(parts, "image")
		}
	}
	return truncatePreview(strings.Join(parts, "\n"), limit)
}

func previewContentBlocks(content []types.ContentBlock, limit int) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch v := block.(type) {
		case *types.TextContent:
			if text := strings.TrimSpace(v.Text); text != "" {
				parts = append(parts, text)
			}
		case *types.ThinkingContent:
			if thinking := strings.TrimSpace(v.Thinking); thinking != "" {
				parts = append(parts, thinking)
			}
		case *types.ToolCallContent:
			if v.Name != "" {
				parts = append(parts, "tool_call: "+v.Name)
			}
		case *types.ImageContent:
			parts = append(parts, "image")
		}
	}
	return truncatePreview(strings.Join(parts, "\n"), limit)
}

func previewFromAny(value any, limit int) string {
	switch v := value.(type) {
	case string:
		return truncatePreview(v, limit)
	case fmt.Stringer:
		return truncatePreview(v.String(), limit)
	case []types.ContentBlock:
		return previewContentBlocks(v, limit)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := strings.TrimSpace(previewFromAny(item, limit)); text != "" {
				parts = append(parts, text)
			}
		}
		return truncatePreview(strings.Join(parts, "\n"), limit)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return truncatePreview(string(data), limit)
	}
}

func truncatePreview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func addUsage(summary *Summary, usage types.AgentUsage) {
	summary.Tokens.Input += usage.Input
	summary.Tokens.Output += usage.Output
	summary.Tokens.CacheRead += usage.CacheRead
	summary.Tokens.CacheWrite += usage.CacheWrite
	total := usage.TotalTokens
	if total == 0 {
		total = usage.Input + usage.Output
	}
	summary.Tokens.TotalTokens += total
	summary.Cost.Input += usage.Cost.Input
	summary.Cost.Output += usage.Cost.Output
	summary.Cost.CacheRead += usage.Cost.CacheRead
	summary.Cost.CacheWrite += usage.Cost.CacheWrite
	summary.Cost.Total += usage.Cost.Total
}

func sanitizeAny(value any) any {
	switch v := value.(type) {
	case nil, string, bool, float64, float32, int, int32, int64, uint, uint32, uint64:
		return v
	case []string:
		out := make([]string, len(v))
		copy(out, v)
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, sanitizeAny(item))
		}
		return out
	case map[string]any:
		return cloneMap(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil
		}
		return decoded
	}
}

func cloneSummary(summary Summary) Summary {
	out := summary
	if summary.LastEvent != nil {
		last := *summary.LastEvent
		last.Meta = cloneMap(last.Meta)
		last.Args = cloneMap(last.Args)
		out.LastEvent = &last
	}
	return out
}

func cloneAnyMap(value any) map[string]any {
	if v, ok := value.(map[string]any); ok {
		return cloneMap(v)
	}
	return nil
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = sanitizeAny(value)
	}
	return out
}

func ensureParentDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create trace directory: %w", err)
	}
	return nil
}
