package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func moduTUIWorkflowCockpitPanel(session *coding_agent.CodingSession) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowCockpitPanelFromStates(nil)
	}
	return moduTUIWorkflowCockpitPanelFromStates(session.ExtensionRuntimeStates())
}

func moduTUIWorkflowCockpitPanelFromStates(states map[string]any) modutui.Panel {
	snapshot, ok := decodeModuTUIWorkflowSnapshot(states)
	return moduTUIWorkflowCockpitPanelFromSnapshot(snapshot, ok, moduTUIWorkflowCockpitTextFromSnapshot(snapshot, ok))
}

func moduTUIWorkflowCockpitPanelFromSnapshot(snapshot moduTUIWorkflowSnapshot, available bool, text string) modutui.Panel {
	lines := strings.Split(text, "\n")
	title := "Workflow Cockpit"
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == title {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if recent := moduTUIWorkflowCockpitRecentRunLinesFromSnapshot(snapshot); len(recent) > 0 {
		lines = append(recent, append([]string{""}, lines...)...)
	}
	if preview := moduTUIWorkflowCockpitPreviewLinesFromSnapshot(snapshot); len(preview) > 0 {
		lines = append(preview, append([]string{""}, lines...)...)
	}
	subtitle := moduTUIWorkflowCockpitSubtitleFromSnapshot(snapshot, available)
	rows := moduTUIWorkflowCockpitRowsFromSnapshot(snapshot)
	shortcuts := moduTUIWorkflowCockpitShortcutsFromSnapshot(snapshot)
	return modutui.Panel{
		ID:        moduTUIWorkflowCockpitPanelID,
		Title:     title,
		Subtitle:  subtitle,
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowCockpitSelectedRowFromSnapshot(snapshot, rows),
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select run  [enter] open  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowCockpitPanelID},
	}
}

func moduTUIWorkflowRunDetailPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowRunDetailPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowRunDetailPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowRunDetailPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return modutui.Panel{
			ID:       moduTUIWorkflowRunDetailPanelID,
			Title:    "Workflow Run",
			Subtitle: "run not found: " + strings.TrimSpace(runID),
			Lines: []string{
				"Run not found in workflow runtime state.",
				"Use /workflows list to refresh persisted runs.",
			},
			Rows: []modutui.PanelRow{{
				Label:   "Back to workflow runs",
				Command: moduTUIWorkflowPanelBackCommand,
			}},
			Footer: "[enter] back  [esc/q] close",
			Meta:   moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowRunDetailPanelID, RunID: runID},
		}
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	progress := "no agent progress"
	if run.AgentCount > 0 {
		progress = fmt.Sprintf("%d/%d done, %d running, %d errors", run.DoneCount, run.AgentCount, run.RunningAgentCount, run.ErrorCount)
	}
	var lines []string
	lines = append(lines, "summary")
	lines = append(lines, "  id: "+run.ID)
	lines = append(lines, "  status: "+run.Status)
	lines = append(lines, "  progress: "+progress)
	if run.CurrentPhase != "" {
		lines = append(lines, "  current phase: "+run.CurrentPhase)
	}
	if run.DurationMs > 0 {
		lines = append(lines, "  duration: "+formatModuTUIActivityDuration(time.Duration(run.DurationMs)*time.Millisecond))
	}
	if run.ErrorCount > 0 {
		lines = append(lines, fmt.Sprintf("  errors: %d", run.ErrorCount))
	}
	if board := moduTUIWorkflowRunBoardLines(run); len(board) > 0 {
		lines = append(lines, "", "board")
		lines = append(lines, board...)
	}
	lines = append(lines, "", "flow")
	lines = append(lines, moduTUIWorkflowRunFlowLines(run)...)
	if updates := moduTUIWorkflowRunUpdateLines(run); len(updates) > 0 {
		lines = append(lines, "", "updates")
		lines = append(lines, updates...)
	}
	if timeline := moduTUIWorkflowRunTimelineLines(run); len(timeline) > 0 {
		lines = append(lines, "", "timeline")
		lines = append(lines, timeline...)
	}
	lines = append(lines, "", "actions")
	if controlRows := moduTUIWorkflowControlRows(run); len(controlRows) > 0 {
		controlLabels := make([]string, 0, len(controlRows))
		for _, row := range controlRows {
			controlLabels = append(controlLabels, row.Label)
		}
		lines = append(lines, "  "+strings.Join(controlLabels, ", "))
	}
	lines = append(lines, "  Enter Guide to understand the run views")
	lines = append(lines, "  Enter Map to inspect the full phase and agent tree")
	lines = append(lines, "  Enter Agents to inspect per-agent work")
	lines = append(lines, "  Enter Phase rows to inspect one orchestration stage")
	lines = append(lines, "  Enter Result to inspect final output")
	lines = append(lines, "  Enter Script to inspect workflow definition")
	lines = append(lines, "  Enter Back to workflow runs")
	lines = append(lines, "  /workflows agent "+run.ID+" <agent-id>")
	lines = append(lines, "  /workflows transcript "+run.ID+" <agent-id>")

	rows := moduTUIWorkflowRunQuickRows(run)
	rows = append(rows, moduTUIWorkflowControlRows(run)...)
	rows = append(rows, moduTUIWorkflowPhaseRows(run)...)
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Agents",
		Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Result",
		Detail:  "final output",
		Command: moduTUIWorkflowPanelResultPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Script",
		Detail:  "workflow definition",
		Command: moduTUIWorkflowPanelScriptPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowRunShortcuts(run),
		moduTUIWorkflowAttentionShortcut(run),
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowRunDetailPanelID,
		Title:     "Workflow Run",
		Subtitle:  fmt.Sprintf("%s [%s] %s", name, run.Status, progress),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowRunDetailSelectedRow(run, rows),
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select  [enter] open  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowRunDetailPanelID, RunID: run.ID},
	}
}

func moduTUIWorkflowCockpitRecentRunLinesFromSnapshot(snapshot moduTUIWorkflowSnapshot) []string {
	runs := snapshot.Runs
	if len(runs) <= 1 {
		return nil
	}
	lines := []string{"recent runs"}
	for i, run := range runs {
		if i >= 6 {
			lines = append(lines, fmt.Sprintf("  ... +%d more run(s)", len(runs)-i))
			break
		}
		lines = append(lines, moduTUIWorkflowCockpitRecentRunLine(i+1, run))
	}
	return lines
}

func moduTUIWorkflowCockpitRecentRunLine(index int, run moduTUIWorkflowRun) string {
	name := strings.TrimSpace(run.Name)
	if name == "" {
		name = strings.TrimSpace(run.ID)
	}
	if name == "" {
		name = "workflow"
	}
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "unknown"
	}
	parts := []string{fmt.Sprintf("%d. %s [%s]", index, name, status)}
	if run.AgentCount > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d", run.DoneCount, run.AgentCount))
	}
	if run.CurrentPhase != "" {
		parts = append(parts, "@"+run.CurrentPhase)
	}
	if run.DurationMs > 0 {
		parts = append(parts, formatModuTUIActivityDuration(time.Duration(run.DurationMs)*time.Millisecond))
	}
	if run.ErrorCount > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", run.ErrorCount))
	}
	return "  " + strings.Join(parts, " | ")
}

func moduTUIWorkflowCockpitPreviewLinesFromSnapshot(snapshot moduTUIWorkflowSnapshot) []string {
	runs := snapshot.Runs
	if len(runs) == 0 {
		return nil
	}
	lines := []string{"latest run preview"}
	lines = append(lines, moduTUIWorkflowRunCardLines(runs[0])...)
	return lines
}

func moduTUIWorkflowFeedPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowFeedPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowFeedPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowFeedPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := moduTUIWorkflowRunCardLines(run)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	if board := moduTUIWorkflowRunBoardLines(run); len(board) > 0 {
		lines = append(lines, "board")
		lines = append(lines, board...)
		lines = append(lines, "")
	}
	if lanes := moduTUIWorkflowRunLaneLines(run); len(lanes) > 0 {
		lines = append(lines, "lanes")
		lines = append(lines, lanes...)
		lines = append(lines, "  legend: run active | done complete | err attention | wait queued")
		lines = append(lines, "")
	}
	lines = append(lines, "flow")
	lines = append(lines, moduTUIWorkflowRunFlowLines(run)...)
	if updates := moduTUIWorkflowRunUpdateLines(run); len(updates) > 0 {
		lines = append(lines, "", "updates")
		lines = append(lines, updates...)
	}
	if timeline := moduTUIWorkflowRunTimelineLines(run); len(timeline) > 0 {
		lines = append(lines, "", "timeline")
		lines = append(lines, timeline...)
	}
	if len(lines) == 1 {
		lines = append(lines, "  no workflow progress snapshot yet")
	}
	rows := moduTUIWorkflowRunFocusRows(run)
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "All agents",
		Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	})
	if moduTUIWorkflowStatusIsTerminal(run.Status) {
		rows = append(rows, modutui.PanelRow{
			Label:   "Result",
			Detail:  "final output",
			Command: moduTUIWorkflowPanelResultPrefix + run.ID,
		}, modutui.PanelRow{
			Label:   "Script",
			Detail:  "workflow definition",
			Command: moduTUIWorkflowPanelScriptPrefix + run.ID,
		})
	}
	rows = append(rows, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	views := []string{"detail", "map", "agents"}
	if moduTUIWorkflowStatusIsTerminal(run.Status) {
		views = append(views, "result", "script")
	}
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowRunShortcuts(run),
		moduTUIWorkflowAttentionShortcut(run),
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, views...),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowFeedPanelID,
		Title:     "Workflow Feed",
		Subtitle:  fmt.Sprintf("%s [%s]", name, run.Status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowFeedSelectedRow(run, rows),
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select  [enter] open  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowFeedPanelID, RunID: run.ID},
	}
}

func moduTUIWorkflowGuidePanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowGuidePanelFromStates(nil, runID)
	}
	return moduTUIWorkflowGuidePanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowGuidePanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowGuidePanelID, "Workflow Guide", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{
		"workflow guide",
		"  Feed: live Status/Plan/Metrics cards, board, lanes, updates, timeline",
		"  Map: topology, phase path edges, agent lanes, detailed tree",
		"  Phase: one orchestration stage with position and neighbors",
		"  Agent: phase context, peer lanes, status, tools, result/error, transcript",
		"  Result: final workflow output with run and plan context",
		"  Script: generated or resumed workflow script with run context",
		"",
		"current route",
		"  /workflows -> running run -> Feed",
		"  Feed cards -> current phase, attention agent, active agent",
		"  Feed -> Phase/Agent for active work",
		"  Map topology -> Phase/Agent for structure",
		"  Result/Script -> Feed/Map to return to execution context",
	}
	if phase, ok := moduTUIWorkflowCurrentOrRunningPhase(run); ok {
		lines = append(lines, "", "current phase")
		lines = append(lines, fmt.Sprintf("  %s %d/%d %s",
			moduTUIWorkflowPhaseTitle(phase.Title),
			phase.DoneCount,
			phase.AgentCount,
			moduTUIWorkflowPhaseStatus(phase),
		))
	}
	if agent, ok := moduTUIWorkflowFirstAttentionAgent(run.Agents); ok {
		lines = append(lines, "", "attention")
		lines = append(lines, "  "+moduTUIWorkflowAgentPulse(agent))
		if agent.Error != "" {
			lines = append(lines, "  error: "+moduTUITruncate(agent.Error, 120))
		}
	}
	if agent, ok := moduTUIWorkflowFirstRunningAgent(run.Agents); ok {
		lines = append(lines, "", "active")
		lines = append(lines, "  "+moduTUIWorkflowAgentPulse(agent))
		if agent.PromptPreview != "" {
			lines = append(lines, "  prompt: "+moduTUITruncate(agent.PromptPreview, 120))
		}
	}
	rows := moduTUIWorkflowRunFocusRows(run)
	rows = append(rows, modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Run detail",
		Detail:  "metadata, result, script",
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Result",
		Detail:  "final workflow output",
		Command: moduTUIWorkflowPanelResultPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Script",
		Detail:  "generated workflow script",
		Command: moduTUIWorkflowPanelScriptPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "All agents",
		Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowAttentionShortcut(run),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents", "result", "script"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowGuidePanelID,
		Title:     "Workflow Guide",
		Subtitle:  fmt.Sprintf("%s [%s]", name, run.Status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowRunFocusSelectedRow(rows),
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select  [enter] open  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowGuidePanelID, RunID: run.ID},
	}
}

func moduTUIWorkflowRunCardLines(run moduTUIWorkflowRun) []string {
	name := strings.TrimSpace(run.Name)
	if name == "" {
		name = strings.TrimSpace(run.ID)
	}
	if name == "" {
		name = "workflow"
	}
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "unknown"
	}
	progress := "no agent progress"
	if run.AgentCount > 0 {
		progress = fmt.Sprintf("%d/%d done | %d running | %s",
			run.DoneCount,
			run.AgentCount,
			run.RunningAgentCount,
			moduTUIWorkflowCountText(run.ErrorCount, "error"),
		)
	}
	statusLines := []string{name + " [" + status + "]", "progress: " + progress}
	if run.CurrentPhase != "" {
		statusLines = append(statusLines, "current: "+run.CurrentPhase)
	}
	if run.DurationMs > 0 {
		statusLines = append(statusLines, "duration: "+formatModuTUIActivityDuration(time.Duration(run.DurationMs)*time.Millisecond))
	}
	lines := []string{"cards"}
	lines = append(lines, moduTUIWorkflowCardLines("Status", statusLines)...)
	lines = append(lines, moduTUIWorkflowCardLines("Plan", moduTUIWorkflowRunPlanLines(run))...)
	lines = append(lines, moduTUIWorkflowCardLines("Metrics", moduTUIWorkflowRunMetricLines(run))...)
	lines = append(lines, moduTUIWorkflowCardLines("Path", moduTUIWorkflowRunPathLines(run))...)
	if outcome := moduTUIWorkflowRunOutcomeLines(run); len(outcome) > 0 {
		lines = append(lines, moduTUIWorkflowCardLines("Outcome", outcome)...)
	}
	if agent, ok := moduTUIWorkflowFirstAttentionAgent(run.Agents); ok {
		cardLines := []string{moduTUIWorkflowAgentPulse(agent)}
		if agent.Error != "" {
			cardLines = append(cardLines, "error: "+moduTUITruncate(agent.Error, 120))
		}
		lines = append(lines, moduTUIWorkflowCardLines("Attention", cardLines)...)
	}
	if agent, ok := moduTUIWorkflowFirstRunningAgent(run.Agents); ok {
		cardLines := []string{moduTUIWorkflowAgentPulse(agent)}
		if agent.PromptPreview != "" {
			cardLines = append(cardLines, "prompt: "+moduTUITruncate(agent.PromptPreview, 120))
		}
		if len(agent.ToolCalls) > 0 {
			cardLines = append(cardLines, "tools: "+moduTUIWorkflowToolSummary(agent.ToolCalls))
		}
		lines = append(lines, moduTUIWorkflowCardLines("Active", cardLines)...)
	}
	if next := moduTUIWorkflowNextPhaseTitle(run); next != "" {
		lines = append(lines, moduTUIWorkflowCardLines("Next", []string{"phase: " + next})...)
	}
	return lines
}

func moduTUIWorkflowRunOutcomeLines(run moduTUIWorkflowRun) []string {
	if !moduTUIWorkflowStatusIsTerminal(run.Status) {
		return nil
	}
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "unknown"
	}
	lines := []string{"status: " + status}
	if run.SnapshotPath != "" {
		lines = append(lines, "result: open Result view")
		lines = append(lines, "snapshot: "+run.SnapshotPath)
	} else {
		lines = append(lines, "result: no snapshot artifact yet")
	}
	if run.ScriptPath != "" {
		lines = append(lines, "script: open Script view")
	}
	if run.ErrorCount > 0 {
		lines = append(lines, fmt.Sprintf("attention: %d error agent(s)", run.ErrorCount))
	}
	lines = append(lines, "next: Result, Script, or Restart")
	return lines
}

func moduTUIWorkflowRunPathLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return []string{"no phase snapshot yet"}
	}
	lines := []string{"path: " + moduTUIWorkflowPhaseFlowLine(phases)}
	if current := strings.TrimSpace(moduTUIWorkflowCurrentPhaseLine(run, phases)); current != "" {
		lines = append(lines, current)
	}
	if next := strings.TrimSpace(moduTUIWorkflowNextPhaseLine(phases)); next != "" {
		lines = append(lines, next)
	}
	return lines
}

func moduTUIWorkflowRunPlanLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return []string{"waiting for phase plan"}
	}
	lines := []string{"route: " + moduTUIWorkflowPhasePlanRoute(phases)}
	currentIndex := moduTUIWorkflowCurrentPhaseIndex(run, phases)
	if strings.EqualFold(strings.TrimSpace(run.Status), "completed") {
		currentIndex = -1
	}
	nextIndex := moduTUIWorkflowNextPhaseIndex(phases)
	if currentIndex >= 0 {
		lines = append(lines, fmt.Sprintf("now: %d/%d %s %s",
			currentIndex+1,
			len(phases),
			moduTUIWorkflowPhaseTitle(phases[currentIndex].Title),
			moduTUIWorkflowPlanPhaseDetail(phases[currentIndex]),
		))
	} else {
		status := strings.TrimSpace(run.Status)
		if status == "" {
			status = "unknown"
		}
		lines = append(lines, "now: workflow "+status)
	}
	if nextIndex >= 0 {
		lines = append(lines, fmt.Sprintf("next: %d/%d %s %s",
			nextIndex+1,
			len(phases),
			moduTUIWorkflowPhaseTitle(phases[nextIndex].Title),
			moduTUIWorkflowBoardPhaseSummary(phases, nextIndex),
		))
	} else if moduTUIWorkflowStatusIsTerminal(run.Status) {
		lines = append(lines, "next: inspect outcome")
	}
	for i, phase := range phases {
		if i >= 4 {
			lines = append(lines, fmt.Sprintf("stage: +%d more phase(s)", len(phases)-i))
			break
		}
		label := moduTUIWorkflowPlanPhaseLabel(i, currentIndex, nextIndex, phase)
		lines = append(lines, fmt.Sprintf("stage %d: %s %s %d/%d",
			i+1,
			label,
			moduTUIWorkflowPhaseTitle(phase.Title),
			phase.DoneCount,
			phase.AgentCount,
		))
	}
	return lines
}

func moduTUIWorkflowPhasePlanRoute(phases []moduTUIWorkflowPhase) string {
	parts := make([]string, 0, min(len(phases), 6))
	for i, phase := range phases {
		if i >= 6 {
			parts = append(parts, fmt.Sprintf("+%d", len(phases)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%d %s", i+1, moduTUIWorkflowPhaseTitle(phase.Title)))
	}
	return strings.Join(parts, " -> ")
}

func moduTUIWorkflowCurrentPhaseIndex(run moduTUIWorkflowRun, phases []moduTUIWorkflowPhase) int {
	current := strings.TrimSpace(run.CurrentPhase)
	if current != "" {
		for i, phase := range phases {
			if strings.TrimSpace(phase.Title) == current {
				return i
			}
		}
	}
	for i, phase := range phases {
		if phase.RunningCount > 0 || phase.ErrorCount > 0 {
			return i
		}
	}
	for i, phase := range phases {
		if phase.DoneCount > 0 && !moduTUIWorkflowPhaseIsComplete(phase) {
			return i
		}
	}
	return -1
}

func moduTUIWorkflowNextPhaseIndex(phases []moduTUIWorkflowPhase) int {
	for i, phase := range phases {
		if phase.AgentCount == 0 || phase.DoneCount < phase.AgentCount {
			if phase.RunningCount > 0 || phase.ErrorCount > 0 {
				continue
			}
			return i
		}
	}
	return -1
}

func moduTUIWorkflowPlanPhaseDetail(phase moduTUIWorkflowPhase) string {
	parts := []string{fmt.Sprintf("%d/%d", phase.DoneCount, phase.AgentCount)}
	if phase.RunningCount > 0 {
		parts = append(parts, fmt.Sprintf("%d running", phase.RunningCount))
	}
	if phase.ErrorCount > 0 {
		parts = append(parts, fmt.Sprintf("%d error", phase.ErrorCount))
	}
	if phase.ErrorCount > 0 {
		parts = append(parts, "needs attention")
	} else if moduTUIWorkflowPhaseIsComplete(phase) {
		parts = append(parts, "complete")
	} else if phase.RunningCount > 0 {
		parts = append(parts, "running now")
	}
	return strings.Join(parts, " | ")
}

func moduTUIWorkflowPlanPhaseLabel(index, currentIndex, nextIndex int, phase moduTUIWorkflowPhase) string {
	switch {
	case index == currentIndex && phase.ErrorCount > 0:
		return "attention"
	case index == currentIndex:
		return "now"
	case index == nextIndex:
		return "next"
	case moduTUIWorkflowPhaseIsComplete(phase):
		return "done"
	case phase.ErrorCount > 0:
		return "attention"
	case phase.RunningCount > 0:
		return "running"
	default:
		return "wait"
	}
}

func moduTUIWorkflowRunMetricLines(run moduTUIWorkflowRun) []string {
	lines := []string{}
	if run.AgentCount > 0 {
		lines = append(lines, fmt.Sprintf("agents: %d total | %d done | %d running | %s",
			run.AgentCount,
			run.DoneCount,
			run.RunningAgentCount,
			moduTUIWorkflowCountText(run.ErrorCount, "error"),
		))
	}
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) > 0 {
		lines = append(lines, "phases: "+moduTUIWorkflowPhaseMetricText(phases))
	}
	if tokens := moduTUIWorkflowEstimatedTokens(run, phases); tokens > 0 {
		lines = append(lines, fmt.Sprintf("estimated tokens: %d", tokens))
	}
	if cost := moduTUIWorkflowCost(run, phases); cost > 0 {
		lines = append(lines, "cost: "+moduTUIWorkflowCostText(cost))
	}
	if run.DurationMs > 0 {
		lines = append(lines, "elapsed: "+formatModuTUIActivityDuration(time.Duration(run.DurationMs)*time.Millisecond))
	}
	if len(lines) == 0 {
		return []string{"waiting for first snapshot"}
	}
	return lines
}

func moduTUIWorkflowPhaseMetricText(phases []moduTUIWorkflowPhase) string {
	total := len(phases)
	done := 0
	active := 0
	attention := 0
	waiting := 0
	for _, phase := range phases {
		switch moduTUIWorkflowPhaseShortStatus(phase) {
		case "done":
			done++
		case "run", "work":
			active++
		case "error":
			attention++
		default:
			waiting++
		}
	}
	parts := []string{fmt.Sprintf("%d total", total)}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", done))
	}
	if active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", active))
	}
	if attention > 0 {
		parts = append(parts, fmt.Sprintf("%d attention", attention))
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", waiting))
	}
	return strings.Join(parts, " | ")
}

func moduTUIWorkflowEstimatedTokens(run moduTUIWorkflowRun, phases []moduTUIWorkflowPhase) int {
	total := 0
	for _, phase := range phases {
		total += phase.EstimatedTokens
	}
	if total > 0 {
		return total
	}
	for _, agent := range run.Agents {
		total += agent.EstimatedTokens
	}
	return total
}

func moduTUIWorkflowCost(run moduTUIWorkflowRun, phases []moduTUIWorkflowPhase) float64 {
	if run.Cost > 0 {
		return run.Cost
	}
	total := 0.0
	for _, phase := range phases {
		total += phase.Cost
	}
	if total > 0 {
		return total
	}
	for _, agent := range run.Agents {
		total += agent.Cost
	}
	return total
}

func moduTUIWorkflowCostText(cost float64) string {
	if cost <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.6f", cost)
}

func moduTUIWorkflowCardLines(title string, body []string) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Card"
	}
	lines := []string{"  +-- " + title}
	for _, line := range body {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, "  | "+line)
	}
	return lines
}

func moduTUIWorkflowCountText(count int, singular string) string {
	singular = strings.TrimSpace(singular)
	if singular == "" {
		singular = "item"
	}
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

func moduTUIWorkflowMapPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowMapPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowMapPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowMapPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowMapPanelID, "Workflow Map", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{"orchestration map"}
	if topology := moduTUIWorkflowTopologyLines(run); len(topology) > 0 {
		lines = append(lines, "topology")
		lines = append(lines, topology...)
		lines = append(lines, "")
	}
	lines = append(lines, "tree")
	lines = append(lines, moduTUIWorkflowOrchestrationLines(run)...)
	rows := moduTUIWorkflowRunFocusRows(run)
	rows = moduTUIWorkflowAppendRowsUnique(rows, moduTUIWorkflowPhaseRows(run)...)
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "flow, updates, timeline",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "All agents",
		Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowAttentionShortcut(run),
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowMapPanelID,
		Title:     "Workflow Map",
		Subtitle:  fmt.Sprintf("%s [%s]", name, run.Status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowRunFocusSelectedRow(rows),
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select  [enter] open  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowMapPanelID, RunID: run.ID},
	}
}

func moduTUIWorkflowControlRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	control := func(label, detail, verb string) modutui.PanelRow {
		return modutui.PanelRow{
			Label:   label,
			Detail:  detail,
			Command: moduTUIWorkflowPanelControlPrefix + verb + ":" + runID,
			Action:  moduTUIWorkflowControlActionValue(verb, runID),
		}
	}
	switch strings.ToLower(strings.TrimSpace(run.Status)) {
	case "running":
		return []modutui.PanelRow{
			control("Pause", "request cooperative pause", "pause"),
			control("Stop", "request stop", "stop"),
		}
	case "stopped", "paused":
		return []modutui.PanelRow{
			control("Resume", "continue run", "resume"),
			control("Restart", "start from script", "restart"),
		}
	case "completed", "failed", "error", "cancelled", "canceled":
		return []modutui.PanelRow{
			control("Restart", "start from script", "restart"),
		}
	default:
		return nil
	}
}

func moduTUIWorkflowGuideRow(runID string) modutui.PanelRow {
	return modutui.PanelRow{
		Label:   "Guide",
		Detail:  "view map and navigation",
		Command: moduTUIWorkflowPanelGuidePrefix + strings.TrimSpace(runID),
		Action:  moduTUIWorkflowNavigationAction("guide", runID),
	}
}

func moduTUIWorkflowParentPhaseRow(runID, phase string) (modutui.PanelRow, bool) {
	runID = strings.TrimSpace(runID)
	phase = strings.TrimSpace(phase)
	if runID == "" || phase == "" {
		return modutui.PanelRow{}, false
	}
	return modutui.PanelRow{
		Label:   "Parent phase: " + moduTUIWorkflowPhaseTitle(phase),
		Detail:  "return to orchestration stage",
		Value:   phase,
		Command: moduTUIWorkflowPanelPhasePrefix + runID + ":" + phase,
		Action: modutui.Action{
			ID: moduTUIWorkflowNavigateActionID,
			Payload: moduTUIWorkflowActionPayload{
				View:  "phase",
				RunID: runID,
				Phase: phase,
			},
		},
	}, true
}

func moduTUIWorkflowArtifactNavigationRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	return []modutui.PanelRow{
		moduTUIWorkflowGuideRow(run.ID),
		{
			Label:   "Execution feed",
			Detail:  "flow, updates, timeline",
			Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
			Action:  moduTUIWorkflowNavigationAction("feed", run.ID),
		},
		{
			Label:   "Map",
			Detail:  "phase and agent tree",
			Command: moduTUIWorkflowPanelMapPrefix + run.ID,
			Action:  moduTUIWorkflowNavigationAction("map", run.ID),
		},
		{
			Label:   "All agents",
			Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
			Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
			Action:  moduTUIWorkflowNavigationAction("agents", run.ID),
		},
		{
			Label:   "Back to run detail",
			Detail:  run.ID,
			Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
			Action:  moduTUIWorkflowNavigationAction("detail", run.ID),
		},
		{
			Label:   "Back to workflow runs",
			Detail:  "return",
			Command: moduTUIWorkflowPanelBackCommand,
			Action:  moduTUIWorkflowNavigationAction("back", ""),
		},
	}
}

func moduTUIWorkflowRunShortcuts(run moduTUIWorkflowRun) []modutui.PanelShortcut {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	shortcut := func(key, label, verb string) modutui.PanelShortcut {
		return modutui.PanelShortcut{
			Key:     key,
			Label:   label,
			Command: moduTUIWorkflowPanelControlPrefix + verb + ":" + runID,
			Action:  moduTUIWorkflowControlActionValue(verb, runID),
		}
	}
	switch strings.ToLower(strings.TrimSpace(run.Status)) {
	case "running":
		return []modutui.PanelShortcut{
			shortcut("p", "Pause", "pause"),
			shortcut("x", "Stop", "stop"),
		}
	case "stopped", "paused":
		return []modutui.PanelShortcut{
			shortcut("p", "Resume", "resume"),
			shortcut("r", "Restart", "restart"),
		}
	case "completed", "failed", "error", "cancelled", "canceled":
		return []modutui.PanelShortcut{
			shortcut("r", "Restart", "restart"),
		}
	default:
		return nil
	}
}

func moduTUIWorkflowNavigationShortcuts(runID string, views ...string) []modutui.PanelShortcut {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	shortcuts := make([]modutui.PanelShortcut, 0, len(views))
	for _, view := range views {
		switch strings.TrimSpace(view) {
		case "feed":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "f", Label: "Feed", Command: moduTUIWorkflowPanelFeedPrefix + runID, Action: moduTUIWorkflowNavigationAction("feed", runID)})
		case "guide":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "?", Label: "Guide", Command: moduTUIWorkflowPanelGuidePrefix + runID, Action: moduTUIWorkflowNavigationAction("guide", runID)})
		case "detail":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "d", Label: "Detail", Command: moduTUIWorkflowPanelDetailPrefix + runID, Action: moduTUIWorkflowNavigationAction("detail", runID)})
		case "map":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "m", Label: "Map", Command: moduTUIWorkflowPanelMapPrefix + runID, Action: moduTUIWorkflowNavigationAction("map", runID)})
		case "agents":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "a", Label: "Agents", Command: moduTUIWorkflowPanelAgentsPrefix + runID, Action: moduTUIWorkflowNavigationAction("agents", runID)})
		case "result":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "o", Label: "Result", Command: moduTUIWorkflowPanelResultPrefix + runID, Action: moduTUIWorkflowNavigationAction("result", runID)})
		case "script":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "s", Label: "Script", Command: moduTUIWorkflowPanelScriptPrefix + runID, Action: moduTUIWorkflowNavigationAction("script", runID)})
		}
	}
	return shortcuts
}

func moduTUIWorkflowGuideShortcut(runID string) []modutui.PanelShortcut {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	return moduTUIWorkflowNavigationShortcuts(runID, "guide")
}

func moduTUIWorkflowAttentionShortcut(run moduTUIWorkflowRun) []modutui.PanelShortcut {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	agent, ok := moduTUIWorkflowFirstAttentionAgent(run.Agents)
	if !ok {
		return nil
	}
	return []modutui.PanelShortcut{{
		Key:     "!",
		Label:   "Attention",
		Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, runID, agent.ID),
		Action: modutui.Action{
			ID: moduTUIWorkflowNavigateActionID,
			Payload: moduTUIWorkflowActionPayload{
				View:    "agent",
				RunID:   runID,
				AgentID: agent.ID,
			},
		},
	}}
}

func moduTUIWorkflowAppendShortcuts(groups ...[]modutui.PanelShortcut) []modutui.PanelShortcut {
	var out []modutui.PanelShortcut
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func moduTUIWorkflowAppendRowsUnique(rows []modutui.PanelRow, additions ...modutui.PanelRow) []modutui.PanelRow {
	seen := make(map[string]struct{}, len(rows)+len(additions))
	for _, row := range rows {
		command := strings.TrimSpace(row.Command)
		if command != "" {
			seen[command] = struct{}{}
		}
	}
	for _, row := range additions {
		command := strings.TrimSpace(row.Command)
		if command != "" {
			if _, ok := seen[command]; ok {
				continue
			}
			seen[command] = struct{}{}
		}
		rows = append(rows, row)
	}
	return rows
}

func moduTUIWorkflowPanelFooter(base string, shortcuts []modutui.PanelShortcut) string {
	base = strings.TrimSpace(base)
	if len(shortcuts) == 0 {
		return base
	}
	parts := make([]string, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		key := strings.TrimSpace(shortcut.Key)
		label := strings.TrimSpace(shortcut.Label)
		if key == "" || label == "" {
			continue
		}
		parts = append(parts, "["+key+"] "+label)
	}
	if len(parts) == 0 {
		return base
	}
	if base == "" {
		return strings.Join(parts, "  ")
	}
	return base + "  " + strings.Join(parts, "  ")
}

func moduTUIWorkflowRunQuickRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	rows := []modutui.PanelRow{{
		Label:   "Execution feed",
		Detail:  "flow, updates, timeline",
		Command: moduTUIWorkflowPanelFeedPrefix + runID,
	}}
	rows = append(rows, moduTUIWorkflowRunFocusRows(run)...)
	return rows
}

func moduTUIWorkflowRunFocusRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	var rows []modutui.PanelRow
	if phase, ok := moduTUIWorkflowCurrentOrRunningPhase(run); ok {
		rows = append(rows, modutui.PanelRow{
			Label:   "Current phase: " + moduTUIWorkflowPhaseTitle(phase.Title),
			Detail:  fmt.Sprintf("%d/%d %s", phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase)),
			Value:   phase.Title,
			Command: moduTUIWorkflowPanelPhasePrefix + runID + ":" + phase.Title,
		})
	}
	if agent, ok := moduTUIWorkflowFirstAttentionAgent(run.Agents); ok {
		rows = append(rows, modutui.PanelRow{
			Label:   "Attention agent: " + moduTUIWorkflowAgentName(agent),
			Detail:  moduTUIWorkflowAgentRowDetail(agent),
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, runID, agent.ID),
		})
	}
	if agent, ok := moduTUIWorkflowFirstRunningAgent(run.Agents); ok {
		rows = append(rows, modutui.PanelRow{
			Label:   "Active agent: " + moduTUIWorkflowAgentName(agent),
			Detail:  moduTUIWorkflowAgentRowDetail(agent),
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, runID, agent.ID),
		})
	}
	return rows
}

func moduTUIWorkflowFirstAttentionAgent(agents []moduTUIWorkflowAgent) (moduTUIWorkflowAgent, bool) {
	for _, agent := range agents {
		if strings.TrimSpace(agent.Error) != "" || strings.EqualFold(strings.TrimSpace(agent.Status), "error") || strings.EqualFold(strings.TrimSpace(agent.Status), "failed") {
			return agent, true
		}
	}
	return moduTUIWorkflowAgent{}, false
}

func moduTUIWorkflowCurrentOrRunningPhase(run moduTUIWorkflowRun) (moduTUIWorkflowPhase, bool) {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	current := strings.TrimSpace(run.CurrentPhase)
	if current != "" {
		for _, phase := range phases {
			if strings.TrimSpace(phase.Title) == current {
				return phase, true
			}
		}
		return moduTUIWorkflowPhase{Title: current}, true
	}
	for _, phase := range phases {
		if phase.RunningCount > 0 {
			return phase, true
		}
	}
	return moduTUIWorkflowPhase{}, false
}

func moduTUIWorkflowFirstRunningAgent(agents []moduTUIWorkflowAgent) (moduTUIWorkflowAgent, bool) {
	for _, agent := range agents {
		if moduTUIWorkflowStatusIsRunning(agent.Status) {
			return agent, true
		}
	}
	return moduTUIWorkflowAgent{}, false
}

func moduTUIWorkflowAgentName(agent moduTUIWorkflowAgent) string {
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	return fmt.Sprintf("#%d %s", agent.ID, label)
}

func moduTUIWorkflowAgentRowDetail(agent moduTUIWorkflowAgent) string {
	parts := []string{agent.Status}
	if agent.Phase != "" {
		parts = append(parts, agent.Phase)
	}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
	}
	return strings.Join(parts, " · ")
}

func moduTUIWorkflowRunFocusSelectedRow(rows []modutui.PanelRow) int {
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelPhasePrefix) ||
			strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentPrefix) {
			return i
		}
	}
	return 0
}

func moduTUIWorkflowFeedSelectedRow(run moduTUIWorkflowRun, rows []modutui.PanelRow) int {
	if moduTUIWorkflowStatusIsTerminal(run.Status) {
		for i, row := range rows {
			if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelResultPrefix) {
				return i
			}
		}
		for i, row := range rows {
			if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelScriptPrefix) {
				return i
			}
		}
	}
	return moduTUIWorkflowRunFocusSelectedRow(rows)
}

func moduTUIWorkflowCockpitSelectedRowFromSnapshot(snapshot moduTUIWorkflowSnapshot, rows []modutui.PanelRow) int {
	if len(rows) == 0 {
		return 0
	}
	runs := snapshot.Runs
	for i, run := range runs {
		if i >= len(rows) {
			break
		}
		if moduTUIWorkflowStatusIsRunning(run.Status) {
			return i
		}
	}
	return 0
}

func moduTUIWorkflowRunDetailSelectedRow(run moduTUIWorkflowRun, rows []modutui.PanelRow) int {
	if len(rows) == 0 {
		return 0
	}
	if current := strings.TrimSpace(run.CurrentPhase); current != "" {
		if index, ok := moduTUIWorkflowPhaseRowIndex(rows, current); ok {
			return index
		}
	}
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	for _, phase := range phases {
		if phase.RunningCount > 0 {
			if index, ok := moduTUIWorkflowPhaseRowIndex(rows, phase.Title); ok {
				return index
			}
		}
	}
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelPhasePrefix) {
			return i
		}
	}
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentPrefix) {
			return i
		}
	}
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentsPrefix) {
			return i
		}
	}
	return 0
}

func moduTUIWorkflowPhaseRowIndex(rows []modutui.PanelRow, phaseTitle string) (int, bool) {
	phaseTitle = strings.TrimSpace(phaseTitle)
	for i, row := range rows {
		if !strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelPhasePrefix) {
			continue
		}
		if strings.TrimSpace(row.Value) == phaseTitle {
			return i, true
		}
	}
	return 0, false
}

func moduTUIWorkflowAgentSelectedRow(agents []moduTUIWorkflowAgent, rows []modutui.PanelRow) int {
	if len(rows) == 0 {
		return 0
	}
	for _, agent := range agents {
		if !moduTUIWorkflowStatusIsRunning(agent.Status) {
			continue
		}
		target := strconv.Itoa(agent.ID)
		for i, row := range rows {
			if strings.TrimSpace(row.Value) == target && strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentPrefix) {
				return i
			}
		}
	}
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentPrefix) {
			return i
		}
	}
	return 0
}

func moduTUIWorkflowStatusIsRunning(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "in_progress", "in-progress":
		return true
	default:
		return false
	}
}

func moduTUIWorkflowStatusIsTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "error", "stopped", "paused", "cancelled", "canceled":
		return true
	default:
		return false
	}
}
