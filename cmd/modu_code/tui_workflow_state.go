package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type moduTUIWorkflowSnapshot struct {
	Raw            map[string]any
	Runs           []moduTUIWorkflowRun
	RunningCount   int
	StoppedCount   int
	CompletedCount int
	FailedCount    int
	Indicator      string
}

func decodeModuTUIWorkflowSnapshot(states map[string]any) (moduTUIWorkflowSnapshot, bool) {
	if states == nil {
		return moduTUIWorkflowSnapshot{}, false
	}
	raw, ok := states["workflow"]
	if !ok {
		return moduTUIWorkflowSnapshot{}, false
	}
	state, ok := raw.(map[string]any)
	if !ok {
		return moduTUIWorkflowSnapshot{}, false
	}
	return moduTUIWorkflowSnapshot{
		Raw:            state,
		Runs:           moduTUIWorkflowRuns(state["runs"]),
		RunningCount:   moduTUIRuntimeStateNumber(state["runningCount"]),
		StoppedCount:   moduTUIRuntimeStateNumber(state["stoppedCount"]),
		CompletedCount: moduTUIRuntimeStateNumber(state["completedCount"]),
		FailedCount:    moduTUIRuntimeStateNumber(state["failedCount"]),
		Indicator:      strings.TrimSpace(moduTUIRuntimeStateString(state["indicator"])),
	}, true
}

type moduTUIWorkflowRun struct {
	ID                string
	Name              string
	Status            string
	ScriptPath        string
	SnapshotPath      string
	AgentCount        int
	DoneCount         int
	RunningAgentCount int
	ErrorCount        int
	CurrentPhase      string
	UpdatedAt         int64
	DurationMs        int64
	Cost              float64
	Logs              []string
	Phases            []moduTUIWorkflowPhase
	Agents            []moduTUIWorkflowAgent
}

type moduTUIWorkflowPhase struct {
	Title           string
	AgentCount      int
	DoneCount       int
	RunningCount    int
	ErrorCount      int
	EstimatedTokens int
	DurationMs      int64
	Cost            float64
}

type moduTUIWorkflowAgent struct {
	ID              int
	Label           string
	Phase           string
	Status          string
	PromptPreview   string
	ResultPreview   string
	Error           string
	EstimatedTokens int
	TurnTokens      int
	Cost            float64
	RecentToolCalls int
	FailedToolCalls int
	ToolCalls       []moduTUIWorkflowToolCall
}

type moduTUIWorkflowToolCall struct {
	ToolName      string
	ArgsPreview   string
	ResultPreview string
	IsError       bool
}

func moduTUIWorkflowRuns(value any) []moduTUIWorkflowRun {
	items, ok := value.([]map[string]any)
	if !ok {
		rawItems, ok := value.([]any)
		if !ok {
			return nil
		}
		items = make([]map[string]any, 0, len(rawItems))
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if ok {
				items = append(items, item)
			}
		}
	}
	runs := make([]moduTUIWorkflowRun, 0, len(items))
	for _, item := range items {
		run := moduTUIWorkflowRun{
			ID:                moduTUIRuntimeStateString(item["id"]),
			Name:              moduTUIRuntimeStateString(item["name"]),
			Status:            moduTUIRuntimeStateString(item["status"]),
			ScriptPath:        moduTUIRuntimeStateString(item["scriptPath"]),
			SnapshotPath:      moduTUIRuntimeStateString(item["snapshotPath"]),
			AgentCount:        moduTUIRuntimeStateNumber(item["agentCount"]),
			DoneCount:         moduTUIRuntimeStateNumber(item["doneCount"]),
			RunningAgentCount: moduTUIRuntimeStateNumber(item["runningAgentCount"]),
			ErrorCount:        moduTUIRuntimeStateNumber(item["errorCount"]),
			CurrentPhase:      moduTUIRuntimeStateString(item["currentPhase"]),
			UpdatedAt:         int64(moduTUIRuntimeStateNumber(item["updatedAt"])),
			DurationMs:        int64(moduTUIRuntimeStateNumber(item["durationMs"])),
			Cost:              moduTUIRuntimeStateFloat(item["cost"]),
			Logs:              moduTUIRuntimeStateStrings(item["logs"]),
			Phases:            moduTUIWorkflowPhases(item["phases"]),
			Agents:            moduTUIWorkflowAgents(item["agents"]),
		}
		if run.Status == "" {
			run.Status = "unknown"
		}
		if run.ID == "" {
			run.ID = "latest"
		}
		if run.AgentCount == 0 && len(run.Agents) > 0 {
			run.AgentCount = len(run.Agents)
		}
		runs = append(runs, run)
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].UpdatedAt > runs[j].UpdatedAt })
	return runs
}

func moduTUIWorkflowPhases(value any) []moduTUIWorkflowPhase {
	items := moduTUIRuntimeStateMaps(value)
	phases := make([]moduTUIWorkflowPhase, 0, len(items))
	for _, item := range items {
		phases = append(phases, moduTUIWorkflowPhase{
			Title:           moduTUIRuntimeStateString(item["title"]),
			AgentCount:      moduTUIRuntimeStateNumber(item["agentCount"]),
			DoneCount:       moduTUIRuntimeStateNumber(item["doneCount"]),
			RunningCount:    moduTUIRuntimeStateNumber(item["runningCount"]),
			ErrorCount:      moduTUIRuntimeStateNumber(item["errorCount"]),
			EstimatedTokens: moduTUIRuntimeStateNumber(item["estimatedTokens"]),
			DurationMs:      int64(moduTUIRuntimeStateNumber(item["durationMs"])),
			Cost:            moduTUIRuntimeStateFloat(item["cost"]),
		})
	}
	return phases
}

func moduTUIWorkflowAgents(value any) []moduTUIWorkflowAgent {
	items := moduTUIRuntimeStateMaps(value)
	agents := make([]moduTUIWorkflowAgent, 0, len(items))
	for _, item := range items {
		agents = append(agents, moduTUIWorkflowAgent{
			ID:              moduTUIRuntimeStateNumber(item["id"]),
			Label:           moduTUIRuntimeStateString(item["label"]),
			Phase:           moduTUIRuntimeStateString(item["phase"]),
			Status:          moduTUIRuntimeStateString(item["status"]),
			PromptPreview:   moduTUIRuntimeStateString(item["promptPreview"]),
			ResultPreview:   moduTUIRuntimeStateString(item["resultPreview"]),
			Error:           moduTUIRuntimeStateString(item["error"]),
			EstimatedTokens: moduTUIRuntimeStateNumber(item["estimatedTokens"]),
			TurnTokens:      moduTUIRuntimeStateNumber(item["turnTokens"]),
			Cost:            moduTUIRuntimeStateFloat(item["cost"]),
			RecentToolCalls: moduTUIRuntimeStateNumber(item["recentToolCalls"]),
			FailedToolCalls: moduTUIRuntimeStateNumber(item["failedToolCalls"]),
			ToolCalls:       moduTUIWorkflowToolCalls(item["recentToolCallPreviews"]),
		})
	}
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents
}

func moduTUIWorkflowToolCalls(value any) []moduTUIWorkflowToolCall {
	items := moduTUIRuntimeStateMaps(value)
	calls := make([]moduTUIWorkflowToolCall, 0, len(items))
	for _, item := range items {
		calls = append(calls, moduTUIWorkflowToolCall{
			ToolName:      moduTUIRuntimeStateString(item["toolName"]),
			ArgsPreview:   moduTUIRuntimeStateString(item["argsPreview"]),
			ResultPreview: moduTUIRuntimeStateString(item["resultPreview"]),
			IsError:       moduTUIRuntimeStateBool(item["isError"]),
		})
	}
	return calls
}

func moduTUIRuntimeStateMaps(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, raw := range items {
			if item, ok := raw.(map[string]any); ok {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func moduTUIRuntimeStateStrings(value any) []string {
	switch items := value.(type) {
	case []string:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(item) != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			text := moduTUIRuntimeStateString(item)
			if strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func moduTUIRuntimeStateString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func moduTUIRuntimeStateNumber(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func moduTUIRuntimeStateFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	case json.Number:
		n, _ := v.Float64()
		return n
	case string:
		n, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return n
	default:
		return 0
	}
}

func moduTUIRuntimeStateBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func moduTUITruncate(text string, limit int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}
