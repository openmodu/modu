package workflow

import (
	"fmt"
	"strings"
)

// RuntimeState exposes workflow progress to RuntimeState JSON and host UIs.
// Scripts stay behind /workflows commands; agent prompts are capped to match
// the /workflows agent detail view.
const workflowRuntimePromptLimit = 4000

func (e *Extension) RuntimeState() any {
	state := map[string]any{
		"enabled":        e != nil && !e.cfg.Disabled && !workflowDisabledByEnv(),
		"runningCount":   0,
		"stoppedCount":   0,
		"completedCount": 0,
		"failedCount":    0,
		"runs":           []map[string]any{},
	}
	if e == nil {
		return state
	}
	runs, _, err := e.workflowRuns()
	if err != nil {
		state["error"] = err.Error()
		return state
	}
	runStates := make([]map[string]any, 0, len(runs))
	var latestRunning map[string]any
	for _, run := range runs {
		status := run.Status
		if status == "" {
			status = workflowStatusCompleted
		}
		switch status {
		case workflowStatusRunning:
			state["runningCount"] = state["runningCount"].(int) + 1
		case workflowStatusStopped:
			state["stoppedCount"] = state["stoppedCount"].(int) + 1
		case workflowStatusFailed:
			state["failedCount"] = state["failedCount"].(int) + 1
		case workflowStatusCompleted:
			state["completedCount"] = state["completedCount"].(int) + 1
		}
		entry := map[string]any{
			"id":         run.ID,
			"status":     string(status),
			"scriptPath": run.ScriptPath,
			"runDir":     run.RunDir,
			"updatedAt":  run.UpdatedAt.UnixMilli(),
		}
		if run.SnapshotPath != "" {
			entry["snapshotPath"] = run.SnapshotPath
		}
		if run.Snapshot != nil {
			entry["name"] = run.Snapshot.Name
			entry["agentCount"] = run.Snapshot.AgentCount
			entry["doneCount"] = run.Snapshot.DoneCount
			entry["runningAgentCount"] = run.Snapshot.RunningCount
			entry["errorCount"] = run.Snapshot.ErrorCount
			entry["durationMs"] = run.Snapshot.DurationMs
			entry["currentPhase"] = run.Snapshot.CurrentPhase
			entry["phases"] = workflowRuntimePhaseStates(run.Snapshot.PhaseSummaries)
			entry["agents"] = workflowRuntimeAgentStates(run.Snapshot.Agents)
		}
		if run.Error != "" {
			entry["error"] = run.Error
		}
		if status == workflowStatusRunning && latestRunning == nil {
			latestRunning = entry
		}
		runStates = append(runStates, entry)
	}
	state["runs"] = runStates
	if len(runStates) > 0 {
		state["latestRunId"] = runStates[0]["id"]
	}
	if latestRunning != nil {
		state["indicator"] = workflowRuntimeIndicator(state["runningCount"].(int), latestRunning)
	}
	return state
}

func workflowRuntimePhaseStates(phases []phaseSummary) []map[string]any {
	out := make([]map[string]any, 0, len(phases))
	for _, phase := range phases {
		out = append(out, map[string]any{
			"title":           phase.Title,
			"agentCount":      phase.AgentCount,
			"doneCount":       phase.DoneCount,
			"runningCount":    phase.RunningCount,
			"errorCount":      phase.ErrorCount,
			"estimatedTokens": phase.EstimatedTokens,
			"cost":            phase.Cost,
			"durationMs":      phase.DurationMs,
		})
	}
	return out
}

func workflowRuntimeAgentStates(agents []agentSnapshot) []map[string]any {
	out := make([]map[string]any, 0, len(agents))
	for _, agent := range agents {
		entry := map[string]any{
			"id":              agent.ID,
			"label":           agent.Label,
			"phase":           agent.Phase,
			"status":          string(agent.Status),
			"prompt":          workflowRuntimePrompt(agent.Prompt),
			"promptPreview":   preview(agent.Prompt, 200),
			"estimatedTokens": agent.EstimatedTokens,
			"turnTokens":      agent.TurnTokens,
			"cost":            agent.Cost,
			"failedToolCalls": agent.FailedToolCalls,
			"durationMs":      agent.DurationMs,
		}
		if agent.ResultPreview != "" {
			entry["resultPreview"] = agent.ResultPreview
		}
		if agent.Error != "" {
			entry["error"] = agent.Error
		}
		if len(agent.RecentToolCalls) > 0 {
			entry["recentToolCalls"] = len(agent.RecentToolCalls)
			entry["recentToolCallPreviews"] = workflowRuntimeToolCallStates(agent.RecentToolCalls)
		}
		out = append(out, entry)
	}
	return out
}

func workflowRuntimePrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) <= workflowRuntimePromptLimit {
		return prompt
	}
	if workflowRuntimePromptLimit <= 4 {
		return prompt[:workflowRuntimePromptLimit]
	}
	return prompt[:workflowRuntimePromptLimit-4] + "\n..."
}

func workflowRuntimeToolCallStates(calls []workflowToolCallSnapshot) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		entry := map[string]any{
			"toolName": call.ToolName,
			"isError":  call.IsError,
		}
		if call.ArgsPreview != "" {
			entry["argsPreview"] = call.ArgsPreview
		}
		if call.ResultPreview != "" {
			entry["resultPreview"] = call.ResultPreview
		}
		out = append(out, entry)
	}
	return out
}

func workflowRuntimeIndicator(running int, run map[string]any) string {
	if running <= 0 || run == nil {
		return ""
	}
	name, _ := run["name"].(string)
	if strings.TrimSpace(name) == "" {
		name, _ = run["id"].(string)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "run"
	}
	done, _ := run["doneCount"].(int)
	total, _ := run["agentCount"].(int)
	phase, _ := run["currentPhase"].(string)
	if running == 1 {
		progress := ""
		if total > 0 {
			progress = fmt.Sprintf(" %d/%d", done, total)
		}
		if strings.TrimSpace(phase) != "" {
			return fmt.Sprintf("workflow %s%s running: %s", name, progress, strings.TrimSpace(phase))
		}
		return fmt.Sprintf("workflow %s%s running", name, progress)
	}
	return fmt.Sprintf("workflows %d running", running)
}
