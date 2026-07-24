package main

import (
	"fmt"
	"strings"
	"time"
)

func moduTUIWorkflowRunBoardLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return nil
	}
	lines := make([]string, 0, len(phases)*3)
	for i, phase := range phases {
		if i >= 8 {
			lines = append(lines, fmt.Sprintf("  ... +%d more phase(s)", len(phases)-i))
			break
		}
		agents := moduTUIWorkflowAgentsForPhase(run.Agents, phase.Title)
		summary := moduTUIWorkflowBoardPhaseSummary(phases, i)
		lines = append(lines, fmt.Sprintf("  %d. [%s] %s %d/%d %s",
			i+1,
			moduTUIWorkflowTimelinePhaseStatus(phase),
			moduTUIWorkflowPhaseTitle(phase.Title),
			phase.DoneCount,
			phase.AgentCount,
			summary,
		))
		lines = append(lines, moduTUIWorkflowBoardAgentLines(agents)...)
	}
	return lines
}

func moduTUIWorkflowBoardPhaseSummary(phases []moduTUIWorkflowPhase, index int) string {
	if index < 0 || index >= len(phases) {
		return ""
	}
	phase := phases[index]
	switch moduTUIWorkflowTimelinePhaseStatus(phase) {
	case "error":
		return "needs attention"
	case "running":
		return "running now"
	case "done":
		return "complete"
	case "working":
		return "partially complete"
	}
	if index > 0 && !moduTUIWorkflowPhaseIsComplete(phases[index-1]) {
		return "waits for " + moduTUIWorkflowPhaseTitle(phases[index-1].Title)
	}
	return "waiting"
}

func moduTUIWorkflowPhaseIsComplete(phase moduTUIWorkflowPhase) bool {
	return phase.AgentCount > 0 && phase.DoneCount >= phase.AgentCount && phase.ErrorCount == 0 && phase.RunningCount == 0
}

func moduTUIWorkflowBoardAgentLines(agents []moduTUIWorkflowAgent) []string {
	if len(agents) == 0 {
		return nil
	}
	lines := make([]string, 0, 3)
	add := func(prefix string, agent moduTUIWorkflowAgent) {
		if len(lines) >= 3 {
			return
		}
		lines = append(lines, "     "+prefix+" "+moduTUIWorkflowBoardAgentLine(agent))
	}
	for _, agent := range agents {
		if strings.TrimSpace(agent.Error) != "" || strings.EqualFold(strings.TrimSpace(agent.Status), "error") || strings.EqualFold(strings.TrimSpace(agent.Status), "failed") {
			add("!", agent)
		}
	}
	for _, agent := range agents {
		if moduTUIWorkflowStatusIsRunning(agent.Status) {
			add(">", agent)
		}
	}
	if len(lines) == 0 {
		for _, agent := range agents {
			add("-", agent)
			if len(lines) >= 1 {
				break
			}
		}
	}
	if len(lines) < len(agents) {
		lines = append(lines, fmt.Sprintf("     ... +%d more agent(s)", len(agents)-len(lines)))
	}
	return lines
}

func moduTUIWorkflowBoardAgentLine(agent moduTUIWorkflowAgent) string {
	label := strings.TrimSpace(agent.Label)
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	status := strings.TrimSpace(agent.Status)
	if status == "" {
		status = "unknown"
	}
	parts := []string{fmt.Sprintf("#%d %s %s", agent.ID, label, status)}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
	}
	line := strings.Join(parts, " · ")
	if agent.Error != "" {
		line += ": " + moduTUITruncate(agent.Error, 100)
	} else if agent.ResultPreview != "" {
		line += ": " + moduTUITruncate(agent.ResultPreview, 100)
	} else if agent.PromptPreview != "" {
		line += ": " + moduTUITruncate(agent.PromptPreview, 100)
	}
	return line
}

func moduTUIWorkflowRunLaneLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 || len(run.Agents) == 0 {
		return nil
	}
	lines := make([]string, 0, len(phases))
	for i, phase := range phases {
		if i >= 8 {
			lines = append(lines, fmt.Sprintf("  ... +%d more phase(s)", len(phases)-i))
			break
		}
		agents := moduTUIWorkflowAgentsForPhase(run.Agents, phase.Title)
		if len(agents) == 0 {
			lines = append(lines, "  "+moduTUIWorkflowPhaseTitle(phase.Title)+": no agent snapshot")
			continue
		}
		parts := make([]string, 0, min(len(agents), 4))
		for j, agent := range agents {
			if j >= 4 {
				parts = append(parts, fmt.Sprintf("+%d more", len(agents)-j))
				break
			}
			parts = append(parts, moduTUIWorkflowLaneAgent(agent))
		}
		lines = append(lines, "  "+moduTUIWorkflowPhaseTitle(phase.Title)+": "+strings.Join(parts, " | "))
	}
	return lines
}

func moduTUIWorkflowLaneAgent(agent moduTUIWorkflowAgent) string {
	label := strings.TrimSpace(agent.Label)
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	parts := []string{moduTUIWorkflowLaneStatus(agent), fmt.Sprintf("#%d", agent.ID), label}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
	}
	return strings.Join(parts, " ")
}

func moduTUIWorkflowLaneStatus(agent moduTUIWorkflowAgent) string {
	if strings.TrimSpace(agent.Error) != "" {
		return "err"
	}
	switch strings.ToLower(strings.TrimSpace(agent.Status)) {
	case "done", "completed":
		return "done"
	case "running", "in_progress", "in-progress":
		return "run"
	case "error", "failed":
		return "err"
	case "queued", "pending", "waiting":
		return "wait"
	default:
		if strings.TrimSpace(agent.Status) == "" {
			return "wait"
		}
		return strings.ToLower(strings.TrimSpace(agent.Status))
	}
}

func moduTUIWorkflowRunFlowLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return moduTUIWorkflowRunAgentPulseLines(run)
	}
	lines := []string{"  phases: " + moduTUIWorkflowPhaseFlowLine(phases)}
	if current := moduTUIWorkflowCurrentPhaseLine(run, phases); current != "" {
		lines = append(lines, current)
	}
	lines = append(lines, moduTUIWorkflowRunAgentPulseLines(run)...)
	if next := moduTUIWorkflowNextPhaseLine(phases); next != "" {
		lines = append(lines, next)
	}
	return lines
}

func moduTUIWorkflowPhaseFlowLine(phases []moduTUIWorkflowPhase) string {
	parts := make([]string, 0, min(len(phases), 6))
	for i, phase := range phases {
		if i >= 6 {
			parts = append(parts, fmt.Sprintf("+%d", len(phases)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%s:%s", moduTUIWorkflowPhaseTitle(phase.Title), moduTUIWorkflowPhaseShortStatus(phase)))
	}
	if len(parts) == 0 {
		return "no phases"
	}
	return strings.Join(parts, " -> ")
}

func moduTUIWorkflowPhaseShortStatus(phase moduTUIWorkflowPhase) string {
	switch {
	case phase.ErrorCount > 0:
		return "error"
	case phase.RunningCount > 0:
		return "run"
	case phase.AgentCount > 0 && phase.DoneCount >= phase.AgentCount:
		return "done"
	case phase.DoneCount > 0:
		return "work"
	default:
		return "wait"
	}
}

func moduTUIWorkflowCurrentPhaseLine(run moduTUIWorkflowRun, phases []moduTUIWorkflowPhase) string {
	current := strings.TrimSpace(run.CurrentPhase)
	if current != "" {
		if phase, ok := moduTUIWorkflowPhaseByTitle(moduTUIWorkflowRun{Phases: phases}, current); ok {
			return fmt.Sprintf("  now: %s %d/%d %s", moduTUIWorkflowPhaseTitle(phase.Title), phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase))
		}
		return "  now: " + current
	}
	for _, phase := range phases {
		if phase.RunningCount > 0 {
			return fmt.Sprintf("  now: %s %d/%d %s", moduTUIWorkflowPhaseTitle(phase.Title), phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase))
		}
	}
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "unknown"
	}
	return "  now: workflow " + status
}

func moduTUIWorkflowRunAgentPulseLines(run moduTUIWorkflowRun) []string {
	var lines []string
	running := moduTUIWorkflowAgentsWithStatus(run.Agents, true)
	for i, agent := range running {
		if i >= 3 {
			lines = append(lines, fmt.Sprintf("  active: +%d more running agent(s)", len(running)-i))
			break
		}
		lines = append(lines, "  active: "+moduTUIWorkflowAgentPulse(agent))
		if agent.PromptPreview != "" {
			lines = append(lines, "    prompt: "+moduTUITruncate(agent.PromptPreview, 120))
		}
		if len(agent.ToolCalls) > 0 {
			lines = append(lines, "    tools: "+moduTUIWorkflowToolSummary(agent.ToolCalls))
		}
	}
	errors := moduTUIWorkflowAgentsWithError(run.Agents)
	for i, agent := range errors {
		if i >= 2 {
			lines = append(lines, fmt.Sprintf("  attention: +%d more error agent(s)", len(errors)-i))
			break
		}
		lines = append(lines, "  attention: "+moduTUIWorkflowAgentPulse(agent))
		if agent.Error != "" {
			lines = append(lines, "    error: "+moduTUITruncate(agent.Error, 120))
		}
	}
	if len(lines) == 0 && len(run.Agents) == 0 {
		status := strings.TrimSpace(run.Status)
		if status == "" {
			status = "waiting"
		}
		lines = append(lines, "  active: no agent snapshot yet ("+status+")")
	}
	return lines
}

func moduTUIWorkflowAgentPulse(agent moduTUIWorkflowAgent) string {
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	parts := []string{fmt.Sprintf("#%d %s [%s]", agent.ID, label, agent.Status)}
	if strings.TrimSpace(agent.Phase) != "" {
		parts = append(parts, "@"+agent.Phase)
	}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
	}
	return strings.Join(parts, " ")
}

func moduTUIWorkflowAgentsWithStatus(agents []moduTUIWorkflowAgent, running bool) []moduTUIWorkflowAgent {
	var filtered []moduTUIWorkflowAgent
	for _, agent := range agents {
		if moduTUIWorkflowStatusIsRunning(agent.Status) == running {
			filtered = append(filtered, agent)
		}
	}
	return filtered
}

func moduTUIWorkflowAgentsWithError(agents []moduTUIWorkflowAgent) []moduTUIWorkflowAgent {
	var filtered []moduTUIWorkflowAgent
	for _, agent := range agents {
		if strings.TrimSpace(agent.Error) != "" || strings.EqualFold(strings.TrimSpace(agent.Status), "error") || strings.EqualFold(strings.TrimSpace(agent.Status), "failed") {
			filtered = append(filtered, agent)
		}
	}
	return filtered
}

func moduTUIWorkflowNextPhaseLine(phases []moduTUIWorkflowPhase) string {
	for _, phase := range phases {
		if phase.AgentCount == 0 || phase.DoneCount < phase.AgentCount {
			if phase.RunningCount > 0 || phase.ErrorCount > 0 {
				continue
			}
			return "  next: " + moduTUIWorkflowPhaseTitle(phase.Title)
		}
	}
	return ""
}

func moduTUIWorkflowNextPhaseTitle(run moduTUIWorkflowRun) string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	for _, phase := range phases {
		if phase.AgentCount == 0 || phase.DoneCount < phase.AgentCount {
			if phase.RunningCount > 0 || phase.ErrorCount > 0 {
				continue
			}
			return moduTUIWorkflowPhaseTitle(phase.Title)
		}
	}
	return ""
}

func moduTUIWorkflowRunUpdateLines(run moduTUIWorkflowRun) []string {
	if len(run.Logs) == 0 {
		return nil
	}
	start := 0
	if len(run.Logs) > 5 {
		start = len(run.Logs) - 5
	}
	lines := make([]string, 0, len(run.Logs)-start)
	for _, log := range run.Logs[start:] {
		log = strings.TrimSpace(log)
		if log == "" {
			continue
		}
		lines = append(lines, "  - "+moduTUITruncate(log, 120))
	}
	return lines
}

func moduTUIWorkflowRunTimelineLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return nil
	}
	lines := make([]string, 0, len(phases)*2)
	for i, phase := range phases {
		if i >= 8 {
			lines = append(lines, fmt.Sprintf("  ... +%d more phase(s)", len(phases)-i))
			break
		}
		lines = append(lines, moduTUIWorkflowTimelinePhaseLine(phase))
		lines = append(lines, moduTUIWorkflowTimelineAgentLines(run.Agents, phase.Title)...)
	}
	return lines
}

func moduTUIWorkflowTimelinePhaseLine(phase moduTUIWorkflowPhase) string {
	parts := []string{fmt.Sprintf("%d/%d", phase.DoneCount, phase.AgentCount)}
	if phase.RunningCount > 0 {
		parts = append(parts, fmt.Sprintf("%d running", phase.RunningCount))
	}
	if phase.ErrorCount > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", phase.ErrorCount))
	}
	if phase.EstimatedTokens > 0 {
		parts = append(parts, fmt.Sprintf("est %d", phase.EstimatedTokens))
	}
	if phase.DurationMs > 0 {
		parts = append(parts, formatModuTUIActivityDuration(time.Duration(phase.DurationMs)*time.Millisecond))
	}
	return fmt.Sprintf("  [%s] %s %s", moduTUIWorkflowTimelinePhaseStatus(phase), moduTUIWorkflowPhaseTitle(phase.Title), strings.Join(parts, " · "))
}

func moduTUIWorkflowTimelinePhaseStatus(phase moduTUIWorkflowPhase) string {
	switch {
	case phase.ErrorCount > 0:
		return "error"
	case phase.RunningCount > 0:
		return "running"
	case phase.AgentCount > 0 && phase.DoneCount >= phase.AgentCount:
		return "done"
	case phase.DoneCount > 0:
		return "working"
	default:
		return "waiting"
	}
}

func moduTUIWorkflowTimelineAgentLines(agents []moduTUIWorkflowAgent, phase string) []string {
	phaseAgents := moduTUIWorkflowAgentsForPhase(agents, phase)
	lines := make([]string, 0, 2)
	added := 0
	for _, agent := range phaseAgents {
		if strings.TrimSpace(agent.Error) == "" && !strings.EqualFold(strings.TrimSpace(agent.Status), "error") && !strings.EqualFold(strings.TrimSpace(agent.Status), "failed") {
			continue
		}
		lines = append(lines, "    attention: "+moduTUIWorkflowAgentPulse(agent))
		added++
		if added >= 2 {
			return lines
		}
	}
	for _, agent := range phaseAgents {
		if !moduTUIWorkflowStatusIsRunning(agent.Status) {
			continue
		}
		lines = append(lines, "    active: "+moduTUIWorkflowAgentPulse(agent))
		added++
		if added >= 2 {
			return lines
		}
	}
	return lines
}
