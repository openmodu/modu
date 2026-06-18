package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

const maxWorkflowRecentToolCalls = 8

type workflowToolCallSnapshot struct {
	ToolName      string `json:"toolName"`
	ArgsPreview   string `json:"argsPreview,omitempty"`
	ResultPreview string `json:"resultPreview,omitempty"`
	IsError       bool   `json:"isError,omitempty"`
	Timestamp     int64  `json:"timestamp,omitempty"`
}

type workflowTranscriptToolCall struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

type workflowTranscriptEntry struct {
	Role       string                       `json:"role"`
	Text       string                       `json:"text,omitempty"`
	ToolCalls  []workflowTranscriptToolCall `json:"toolCalls,omitempty"`
	ToolCallID string                       `json:"toolCallId,omitempty"`
	ToolName   string                       `json:"toolName,omitempty"`
	IsError    bool                         `json:"isError,omitempty"`
	Usage      types.AgentUsage             `json:"usage,omitempty"`
	Timestamp  int64                        `json:"timestamp,omitempty"`
}

type workflowAgentActivity struct {
	TurnTokens      int
	UsageTokens     int
	TurnCost        float64
	UsageCost       float64
	FailedToolCalls int
	RecentToolCalls []workflowToolCallSnapshot
	Transcript      []workflowTranscriptEntry
}

type workflowActivityRegistry struct {
	mu       sync.Mutex
	agents   map[string]int
	activity map[string]workflowAgentActivity
}

func newWorkflowActivityRegistry() *workflowActivityRegistry {
	return &workflowActivityRegistry{
		agents:   map[string]int{},
		activity: map[string]workflowAgentActivity{},
	}
}

func workflowBubbleID(runDir string, agentID int) string {
	return fmt.Sprintf("workflow:%s:%d", workflowRunID(runDir), agentID)
}

func workflowRunIDFromBubbleID(value string) string {
	parts := strings.Split(value, ":")
	if len(parts) != 3 || parts[0] != "workflow" {
		return ""
	}
	return parts[1]
}

func (r *workflowActivityRegistry) register(bubbleID string, agentID int) {
	if r == nil || bubbleID == "" || agentID <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents == nil {
		r.agents = map[string]int{}
	}
	if r.activity == nil {
		r.activity = map[string]workflowAgentActivity{}
	}
	r.agents[bubbleID] = agentID
}

func (r *workflowActivityRegistry) unregister(bubbleID string) {
	if r == nil || bubbleID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, bubbleID)
	delete(r.activity, bubbleID)
}

func (r *workflowActivityRegistry) agentID(bubbleID string) (int, bool) {
	if r == nil || bubbleID == "" {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.agents[bubbleID]
	return id, ok
}

func (r *workflowActivityRegistry) add(bubbleID string, activity workflowAgentActivity) {
	if r == nil || bubbleID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.activity[bubbleID]
	mergeWorkflowAgentActivity(&current, activity)
	if r.activity == nil {
		r.activity = map[string]workflowAgentActivity{}
	}
	r.activity[bubbleID] = current
}

func (r *workflowActivityRegistry) snapshot(bubbleID string) workflowAgentActivity {
	if r == nil || bubbleID == "" {
		return workflowAgentActivity{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneWorkflowAgentActivity(r.activity[bubbleID])
}

func workflowActivityFromEvent(ev types.Event) workflowAgentActivity {
	out := workflowAgentActivity{}
	switch ev.Reason {
	case string(types.EventTypeTurnEnd):
		out.TurnTokens = workflowTurnTokens(ev.Message)
		out.TurnCost = workflowTurnCost(ev.Message)
	case string(types.EventTypeToolExecutionEnd):
		if ev.ToolName != "" {
			out.RecentToolCalls = append(out.RecentToolCalls, workflowToolCallSnapshot{
				ToolName:      ev.ToolName,
				ArgsPreview:   preview(ev.Args, 240),
				ResultPreview: preview(ev.Result, 240),
				IsError:       ev.IsError,
				Timestamp:     time.Now().UnixMilli(),
			})
		}
		if ev.IsError {
			out.FailedToolCalls = 1
		}
	}
	return out
}

func workflowActivityFromUsage(ev types.Event) workflowAgentActivity {
	return workflowAgentActivity{
		UsageTokens: workflowMessagesTokens(ev.Messages),
		UsageCost:   workflowMessagesCost(ev.Messages),
		Transcript:  workflowTranscriptFromMessages(ev.Messages),
	}
}

func mergeWorkflowAgentActivity(dst *workflowAgentActivity, src workflowAgentActivity) {
	if dst == nil {
		return
	}
	dst.TurnTokens += src.TurnTokens
	if src.UsageTokens > 0 {
		dst.UsageTokens = src.UsageTokens
	}
	dst.TurnCost += src.TurnCost
	if src.UsageCost > 0 {
		dst.UsageCost = src.UsageCost
	}
	dst.FailedToolCalls += src.FailedToolCalls
	if len(src.RecentToolCalls) > 0 {
		dst.RecentToolCalls = append(dst.RecentToolCalls, src.RecentToolCalls...)
		if len(dst.RecentToolCalls) > maxWorkflowRecentToolCalls {
			dst.RecentToolCalls = append([]workflowToolCallSnapshot(nil), dst.RecentToolCalls[len(dst.RecentToolCalls)-maxWorkflowRecentToolCalls:]...)
		}
	}
	if len(src.Transcript) > 0 {
		dst.Transcript = append([]workflowTranscriptEntry(nil), src.Transcript...)
	}
}

func cloneWorkflowAgentActivity(activity workflowAgentActivity) workflowAgentActivity {
	if len(activity.RecentToolCalls) > 0 {
		activity.RecentToolCalls = append([]workflowToolCallSnapshot(nil), activity.RecentToolCalls...)
	}
	if len(activity.Transcript) > 0 {
		activity.Transcript = append([]workflowTranscriptEntry(nil), activity.Transcript...)
		for i := range activity.Transcript {
			if len(activity.Transcript[i].ToolCalls) > 0 {
				activity.Transcript[i].ToolCalls = append([]workflowTranscriptToolCall(nil), activity.Transcript[i].ToolCalls...)
			}
		}
	}
	return activity
}

func workflowTurnTokens(msg types.AgentMessage) int {
	switch m := msg.(type) {
	case types.AssistantMessage:
		return workflowUsageTokens(m.Usage)
	case *types.AssistantMessage:
		if m != nil {
			return workflowUsageTokens(m.Usage)
		}
	}
	return 0
}

func workflowMessagesTokens(messages []types.AgentMessage) int {
	total := 0
	for _, msg := range messages {
		total += workflowTurnTokens(msg)
	}
	return total
}

func workflowTurnCost(msg types.AgentMessage) float64 {
	switch m := msg.(type) {
	case types.AssistantMessage:
		return workflowUsageCost(m.Usage)
	case *types.AssistantMessage:
		if m != nil {
			return workflowUsageCost(m.Usage)
		}
	}
	return 0
}

func workflowMessagesCost(messages []types.AgentMessage) float64 {
	var total float64
	for _, msg := range messages {
		total += workflowTurnCost(msg)
	}
	return total
}

func workflowUsageTokens(usage types.AgentUsage) int {
	total := usage.Input + usage.Output
	if total <= 0 {
		total = usage.TotalTokens
	}
	if total < 0 {
		return 0
	}
	return total
}

func workflowUsageCost(usage types.AgentUsage) float64 {
	if usage.Cost.Total <= 0 {
		return 0
	}
	return usage.Cost.Total
}

func workflowTranscriptFromMessages(messages []types.AgentMessage) []workflowTranscriptEntry {
	if len(messages) == 0 {
		return nil
	}
	out := make([]workflowTranscriptEntry, 0, len(messages))
	for _, msg := range messages {
		switch m := msg.(type) {
		case types.UserMessage:
			out = append(out, workflowTranscriptUser(m))
		case *types.UserMessage:
			if m != nil {
				out = append(out, workflowTranscriptUser(*m))
			}
		case types.AssistantMessage:
			out = append(out, workflowTranscriptAssistant(m))
		case *types.AssistantMessage:
			if m != nil {
				out = append(out, workflowTranscriptAssistant(*m))
			}
		case types.ToolResultMessage:
			out = append(out, workflowTranscriptToolResult(m))
		case *types.ToolResultMessage:
			if m != nil {
				out = append(out, workflowTranscriptToolResult(*m))
			}
		default:
			out = append(out, workflowTranscriptEntry{
				Role: "unknown",
				Text: workflowJSONText(m),
			})
		}
	}
	return out
}

func workflowTranscriptUser(msg types.UserMessage) workflowTranscriptEntry {
	return workflowTranscriptEntry{
		Role:      types.RoleUser,
		Text:      workflowAnyText(msg.Content),
		Timestamp: msg.Timestamp,
	}
}

func workflowTranscriptAssistant(msg types.AssistantMessage) workflowTranscriptEntry {
	entry := workflowTranscriptEntry{
		Role:      types.RoleAssistant,
		Text:      workflowContentBlocksText(msg.Content),
		Usage:     msg.Usage,
		Timestamp: msg.Timestamp,
	}
	for _, block := range msg.Content {
		if call, ok := block.(*types.ToolCallContent); ok && call != nil {
			entry.ToolCalls = append(entry.ToolCalls, workflowTranscriptToolCall{
				ID:   call.ID,
				Name: call.Name,
				Args: workflowJSONText(call.Arguments),
			})
		}
	}
	return entry
}

func workflowTranscriptToolResult(msg types.ToolResultMessage) workflowTranscriptEntry {
	return workflowTranscriptEntry{
		Role:       types.RoleToolResult,
		Text:       workflowContentBlocksText(msg.Content),
		ToolCallID: msg.ToolCallID,
		ToolName:   msg.ToolName,
		IsError:    msg.IsError,
		Timestamp:  msg.Timestamp,
	}
}

func workflowContentBlocksText(blocks []types.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		switch b := block.(type) {
		case *types.TextContent:
			if b != nil && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		case *types.ThinkingContent:
			if b != nil && strings.TrimSpace(b.Thinking) != "" {
				parts = append(parts, "[thinking omitted]")
			}
		case *types.ImageContent:
			if b != nil {
				parts = append(parts, fmt.Sprintf("[image %s]", b.MimeType))
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func workflowAnyText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return workflowJSONText(value)
}

func workflowJSONText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
