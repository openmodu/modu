package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func moduTUIWorkflowPhaseRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	if strings.TrimSpace(run.ID) == "" {
		return nil
	}
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	rows := make([]modutui.PanelRow, 0, len(phases))
	for _, phase := range phases {
		label := "Phase: " + moduTUIWorkflowPhaseTitle(phase.Title)
		detail := fmt.Sprintf("%d/%d %s", phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase))
		rows = append(rows, modutui.PanelRow{
			Label:   label,
			Detail:  detail,
			Value:   phase.Title,
			Command: moduTUIWorkflowPanelPhasePrefix + run.ID + ":" + phase.Title,
		})
	}
	return rows
}

func moduTUIWorkflowPhasePanel(session *coding_agent.CodingSession, runID, phaseTitle string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowPhasePanelFromStates(nil, runID, phaseTitle)
	}
	return moduTUIWorkflowPhasePanelFromStates(session.ExtensionRuntimeStates(), runID, phaseTitle)
}

func moduTUIWorkflowPhasePanelFromStates(states map[string]any, runID, phaseTitle string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowPhasePanelID, "Workflow Phase", runID)
	}
	phase, ok := moduTUIWorkflowPhaseByTitle(run, phaseTitle)
	if !ok {
		phase = moduTUIWorkflowPhase{Title: phaseTitle}
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	title := moduTUIWorkflowPhaseTitle(phase.Title)
	status := moduTUIWorkflowPhaseStatus(phase)
	agents := moduTUIWorkflowAgentsForPhase(run.Agents, phase.Title)
	lines := []string{
		"summary",
		"  workflow: " + name,
		"  run: " + run.ID,
		"  phase: " + title,
		fmt.Sprintf("  progress: %d/%d %s", phase.DoneCount, phase.AgentCount, status),
	}
	if phase.RunningCount > 0 {
		lines = append(lines, fmt.Sprintf("  running: %d", phase.RunningCount))
	}
	if phase.ErrorCount > 0 {
		lines = append(lines, fmt.Sprintf("  errors: %d", phase.ErrorCount))
	}
	if phase.EstimatedTokens > 0 {
		lines = append(lines, fmt.Sprintf("  estimated tokens: %d", phase.EstimatedTokens))
	}
	if phase.DurationMs > 0 {
		lines = append(lines, "  duration: "+formatModuTUIActivityDuration(time.Duration(phase.DurationMs)*time.Millisecond))
	}
	if position := moduTUIWorkflowPhasePositionLines(run, phase.Title); len(position) > 0 {
		lines = append(lines, "", "position")
		lines = append(lines, position...)
	}
	lines = append(lines, "", "agents")
	if len(agents) == 0 {
		lines = append(lines, "  no agent snapshot available for this phase")
	}
	rows := make([]modutui.PanelRow, 0, len(agents)+6)
	for _, agent := range agents {
		lines = append(lines, "  "+moduTUIWorkflowAgentLine(agent))
		if agent.Error != "" {
			lines = append(lines, "    error: "+moduTUITruncate(agent.Error, 120))
		} else if agent.ResultPreview != "" {
			lines = append(lines, "    result: "+moduTUITruncate(agent.ResultPreview, 120))
		} else if agent.PromptPreview != "" {
			lines = append(lines, "    prompt: "+moduTUITruncate(agent.PromptPreview, 120))
		}
		if len(agent.ToolCalls) > 0 {
			lines = append(lines, "    tools: "+moduTUIWorkflowToolSummary(agent.ToolCalls))
		}
		label := agent.Label
		if label == "" {
			label = fmt.Sprintf("agent-%d", agent.ID)
		}
		detailParts := []string{agent.Status}
		if agent.TurnTokens > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d tokens", agent.TurnTokens))
		} else if agent.EstimatedTokens > 0 {
			detailParts = append(detailParts, fmt.Sprintf("est %d", agent.EstimatedTokens))
		}
		if agent.RecentToolCalls > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
		}
		rows = append(rows, modutui.PanelRow{
			Label:   fmt.Sprintf("#%d %s", agent.ID, label),
			Detail:  strings.Join(detailParts, " · "),
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, run.ID, agent.ID),
		})
	}
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Value:   phase.Title,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "All agents",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowPhasePanelID,
		Title:     "Workflow Phase",
		Subtitle:  fmt.Sprintf("%s / %s [%s]", name, title, status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowAgentSelectedRow(agents, rows),
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select agent  [enter] open  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowPhasePanelID, RunID: run.ID, Phase: phase.Title},
	}
}

func moduTUIWorkflowAgentsPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowAgentsPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowAgentsPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowAgentsPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowAgentsPanelID, "Workflow Agents", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{"agents"}
	if len(run.Agents) == 0 {
		lines = append(lines, "  no agent snapshot available")
	} else if lanes := moduTUIWorkflowRunLaneLines(run); len(lanes) > 0 {
		lines = append(lines, "phase lanes")
		lines = append(lines, lanes...)
		lines = append(lines, "", "agent list")
	}
	rows := make([]modutui.PanelRow, 0, len(run.Agents)+5)
	for _, agent := range run.Agents {
		label := agent.Label
		if label == "" {
			label = fmt.Sprintf("agent-%d", agent.ID)
		}
		label = fmt.Sprintf("#%d [%s] %s", agent.ID, agent.Status, label)
		detailParts := []string{}
		if agent.Phase != "" {
			detailParts = append(detailParts, agent.Phase)
		}
		if agent.TurnTokens > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d tokens", agent.TurnTokens))
		} else if agent.EstimatedTokens > 0 {
			detailParts = append(detailParts, fmt.Sprintf("est %d", agent.EstimatedTokens))
		}
		if agent.RecentToolCalls > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
		}
		if agent.FailedToolCalls > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
		}
		rows = append(rows, modutui.PanelRow{
			Label:   label,
			Detail:  strings.Join(detailParts, " · "),
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, run.ID, agent.ID),
		})
	}
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowAgentsPanelID,
		Title:     "Workflow Agents",
		Subtitle:  fmt.Sprintf("%s [%s] %d agent(s)", name, run.Status, len(run.Agents)),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowAgentSelectedRow(run.Agents, rows),
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select agent  [enter] detail  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowAgentsPanelID, RunID: run.ID},
	}
}

func moduTUIWorkflowAgentPanel(session *coding_agent.CodingSession, runID string, agentID int) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowAgentPanelFromStates(nil, runID, agentID)
	}
	return moduTUIWorkflowAgentPanelFromStates(session.ExtensionRuntimeStates(), runID, agentID)
}

func moduTUIWorkflowAgentPanelFromStates(states map[string]any, runID string, agentID int) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowAgentPanelID, "Workflow Agent", runID)
	}
	agent, ok := moduTUIWorkflowAgentByID(run.Agents, agentID)
	if !ok {
		return modutui.Panel{
			ID:       moduTUIWorkflowAgentPanelID,
			Title:    "Workflow Agent",
			Subtitle: fmt.Sprintf("agent %d not found in %s", agentID, run.ID),
			Lines:    []string{"Agent not found in workflow runtime state."},
			Rows: []modutui.PanelRow{{
				Label:   "Back to agents",
				Detail:  run.ID,
				Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
			}},
			Footer: "[enter] back  [esc/q] close",
			Meta:   moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowAgentPanelID, RunID: run.ID, AgentID: agentID},
		}
	}
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	var lines []string
	lines = append(lines, "summary")
	lines = append(lines, fmt.Sprintf("  id: %d", agent.ID))
	lines = append(lines, "  label: "+label)
	lines = append(lines, "  status: "+agent.Status)
	if agent.Phase != "" {
		lines = append(lines, "  phase: "+agent.Phase)
	}
	if agent.TurnTokens > 0 {
		lines = append(lines, fmt.Sprintf("  tokens: %d", agent.TurnTokens))
	} else if agent.EstimatedTokens > 0 {
		lines = append(lines, fmt.Sprintf("  estimated tokens: %d", agent.EstimatedTokens))
	}
	if agent.FailedToolCalls > 0 {
		lines = append(lines, fmt.Sprintf("  failed tools: %d", agent.FailedToolCalls))
	}
	if contextLines := moduTUIWorkflowAgentContextLines(run, agent); len(contextLines) > 0 {
		lines = append(lines, "", "context")
		lines = append(lines, contextLines...)
	}
	if agent.Error != "" {
		lines = append(lines, "", "error")
		lines = append(lines, moduTUIWorkflowTextLines(agent.Error)...)
	}
	if agent.ResultPreview != "" {
		lines = append(lines, "", "result preview")
		lines = append(lines, moduTUIWorkflowTextLines(agent.ResultPreview)...)
	}
	if agent.PromptPreview != "" {
		lines = append(lines, "", "prompt preview")
		lines = append(lines, moduTUIWorkflowTextLines(agent.PromptPreview)...)
	}
	if len(agent.ToolCalls) > 0 {
		lines = append(lines, "", "recent tool calls")
		for _, call := range agent.ToolCalls {
			name := call.ToolName
			if name == "" {
				name = "tool"
			}
			status := "ok"
			if call.IsError {
				status = "error"
			}
			lines = append(lines, fmt.Sprintf("  - %s [%s]", name, status))
			if call.ArgsPreview != "" {
				lines = append(lines, "    args: "+moduTUITruncate(call.ArgsPreview, 160))
			}
			if call.ResultPreview != "" {
				lines = append(lines, "    result: "+moduTUITruncate(call.ResultPreview, 160))
			}
		}
	}
	controlRows := moduTUIWorkflowAgentControlRows(run.ID, agent)
	rows := controlRows
	rows = append(rows, modutui.PanelRow{
		Label:   "Transcript",
		Detail:  "full child transcript",
		Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelTranscriptPrefix, run.ID, agent.ID),
	})
	if row, ok := moduTUIWorkflowParentPhaseRow(run.ID, agent.Phase); ok {
		rows = append(rows, row)
	}
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to agents",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowAgentShortcuts(run.ID, agent),
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowAgentPanelID,
		Title:     "Workflow Agent",
		Subtitle:  fmt.Sprintf("%s #%d [%s]", run.ID, agent.ID, agent.Status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  len(controlRows),
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select  [enter] open  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowAgentPanelID, RunID: run.ID, AgentID: agent.ID},
	}
}

func moduTUIWorkflowAgentControlRows(runID string, agent moduTUIWorkflowAgent) []modutui.PanelRow {
	runID = strings.TrimSpace(runID)
	if runID == "" || agent.ID <= 0 || strings.ToLower(strings.TrimSpace(agent.Status)) != "running" {
		return nil
	}
	control := func(label, detail, verb string) modutui.PanelRow {
		return modutui.PanelRow{
			Label:   label,
			Detail:  detail,
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%s:%d", moduTUIWorkflowPanelAgentControlPrefix, verb, runID, agent.ID),
		}
	}
	return []modutui.PanelRow{
		control("Stop agent", "request stop", "stop"),
		control("Restart agent", "retry this agent", "restart"),
	}
}

func moduTUIWorkflowAgentShortcuts(runID string, agent moduTUIWorkflowAgent) []modutui.PanelShortcut {
	runID = strings.TrimSpace(runID)
	if runID == "" || agent.ID <= 0 || strings.ToLower(strings.TrimSpace(agent.Status)) != "running" {
		return nil
	}
	shortcut := func(key, label, verb string) modutui.PanelShortcut {
		return modutui.PanelShortcut{
			Key:     key,
			Label:   label,
			Command: fmt.Sprintf("%s%s:%s:%d", moduTUIWorkflowPanelAgentControlPrefix, verb, runID, agent.ID),
		}
	}
	return []modutui.PanelShortcut{
		shortcut("x", "Stop agent", "stop"),
		shortcut("r", "Restart agent", "restart"),
	}
}

func moduTUIWorkflowTranscriptPanel(session *coding_agent.CodingSession, runID string, agentID int) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowTranscriptPanelFromStates(nil, runID, agentID)
	}
	return moduTUIWorkflowTranscriptPanelFromStates(session.ExtensionRuntimeStates(), runID, agentID)
}

func moduTUIWorkflowTranscriptPanelFromStates(states map[string]any, runID string, agentID int) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowTranscriptPanelID, "Workflow Transcript", runID)
	}
	agent, _ := moduTUIWorkflowAgentByID(run.Agents, agentID)
	lines, err := moduTUIWorkflowTranscriptLines(run.SnapshotPath, agentID)
	if err != nil {
		lines = []string{"transcript", "  error: " + err.Error()}
	}
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agentID)
	}
	rows := []modutui.PanelRow{{
		Label:   "Back to agent",
		Detail:  fmt.Sprintf("#%d", agentID),
		Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, run.ID, agentID),
	}}
	if row, ok := moduTUIWorkflowParentPhaseRow(run.ID, agent.Phase); ok {
		rows = append(rows, row)
	}
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to agents",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowTranscriptPanelID,
		Title:     "Workflow Transcript",
		Subtitle:  fmt.Sprintf("%s #%d %s", run.ID, agentID, label),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Footer:    moduTUIWorkflowPanelFooter("[enter] back  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowTranscriptPanelID, RunID: run.ID, AgentID: agentID},
	}
}

func moduTUIWorkflowTranscriptLines(snapshotPath string, agentID int) ([]string, error) {
	snapshotPath = strings.TrimSpace(snapshotPath)
	if snapshotPath == "" {
		return []string{"transcript", "  no snapshot path available"}, nil
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	agent, ok := moduTUIWorkflowSnapshotAgent(snapshot, agentID)
	if !ok {
		return []string{"transcript", fmt.Sprintf("  agent %d not found in snapshot", agentID)}, nil
	}
	transcript := moduTUIRuntimeStateMaps(agent["transcript"])
	if len(transcript) == 0 {
		return []string{"transcript", "  no child transcript captured for this agent"}, nil
	}
	lines := []string{"transcript"}
	for i, entry := range transcript {
		if i > 0 {
			lines = append(lines, "")
		}
		role := strings.ToUpper(moduTUIRuntimeStateString(entry["role"]))
		if role == "" {
			role = "UNKNOWN"
		}
		header := fmt.Sprintf("## %d. %s", i+1, role)
		if toolName := moduTUIRuntimeStateString(entry["toolName"]); toolName != "" {
			header += " " + toolName
		}
		if moduTUIRuntimeStateBool(entry["isError"]) {
			header += " [error]"
		}
		lines = append(lines, header)
		if text := moduTUIRuntimeStateString(entry["text"]); text != "" {
			lines = append(lines, moduTUIWorkflowTextLines(text)...)
		}
		for _, call := range moduTUIRuntimeStateMaps(entry["toolCalls"]) {
			name := moduTUIRuntimeStateString(call["name"])
			if name == "" {
				name = "tool"
			}
			callLine := "  ToolCall: " + name
			if id := moduTUIRuntimeStateString(call["id"]); id != "" {
				callLine += " (" + id + ")"
			}
			lines = append(lines, callLine)
			if args := moduTUIRuntimeStateString(call["args"]); args != "" {
				lines = append(lines, "  Args: "+args)
			}
		}
		if usage, ok := entry["usage"].(map[string]any); ok {
			input := moduTUIRuntimeStateNumber(usage["input"])
			output := moduTUIRuntimeStateNumber(usage["output"])
			total := moduTUIRuntimeStateNumber(usage["totalTokens"])
			if input > 0 || output > 0 || total > 0 {
				lines = append(lines, fmt.Sprintf("  Usage: input=%d output=%d total=%d", input, output, total))
			}
		}
	}
	return lines, nil
}

func moduTUIWorkflowSnapshotAgent(snapshot map[string]any, agentID int) (map[string]any, bool) {
	for _, agent := range moduTUIRuntimeStateMaps(snapshot["agents"]) {
		if moduTUIRuntimeStateNumber(agent["id"]) == agentID {
			return agent, true
		}
	}
	return nil, false
}

func moduTUIWorkflowAgentByID(agents []moduTUIWorkflowAgent, agentID int) (moduTUIWorkflowAgent, bool) {
	for _, agent := range agents {
		if agent.ID == agentID {
			return agent, true
		}
	}
	return moduTUIWorkflowAgent{}, false
}

func moduTUIWorkflowResultPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowResultPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowResultPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowResultPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowResultPanelID, "Workflow Result", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{"result"}
	if context := moduTUIWorkflowArtifactContextLines(run); len(context) > 0 {
		lines = append(lines, "context")
		lines = append(lines, context...)
		lines = append(lines, "")
	}
	if run.SnapshotPath != "" {
		lines = append(lines, "  snapshot: "+run.SnapshotPath)
	}
	result, err := moduTUIWorkflowResultLines(run.SnapshotPath)
	if err != nil {
		lines = append(lines, "  error: "+err.Error())
	} else {
		lines = append(lines, moduTUIWorkflowArtifactPreviewLines(result, run.SnapshotPath)...)
	}
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowResultPanelID,
		Title:     "Workflow Result",
		Subtitle:  name + " [" + run.Status + "]",
		Lines:     lines,
		Rows:      moduTUIWorkflowArtifactNavigationRows(run),
		Shortcuts: shortcuts,
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select  [enter] open  [esc/q] close", shortcuts),
		Markdown:  true,
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowResultPanelID, RunID: run.ID},
	}
}

func moduTUIWorkflowScriptPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowScriptPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowScriptPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowScriptPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowScriptPanelID, "Workflow Script", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{"script"}
	if context := moduTUIWorkflowArtifactContextLines(run); len(context) > 0 {
		lines = append(lines, "context")
		lines = append(lines, context...)
		lines = append(lines, "")
	}
	if run.ScriptPath != "" {
		lines = append(lines, "  path: "+run.ScriptPath)
	}
	script, err := moduTUIWorkflowFileLines(run.ScriptPath)
	if err != nil {
		lines = append(lines, "  error: "+err.Error())
	} else {
		lines = append(lines, moduTUIWorkflowArtifactPreviewLines(script, run.ScriptPath)...)
	}
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowScriptPanelID,
		Title:     "Workflow Script",
		Subtitle:  name + " [" + run.Status + "]",
		Lines:     lines,
		Rows:      moduTUIWorkflowArtifactNavigationRows(run),
		Shortcuts: shortcuts,
		Footer:    moduTUIWorkflowPanelFooter("[up/down] select  [enter] open  [esc/q] close", shortcuts),
		Meta:      moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowScriptPanelID, RunID: run.ID},
	}
}

func moduTUIWorkflowArtifactContextLines(run moduTUIWorkflowRun) []string {
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
	lines := []string{
		"  workflow: " + name,
		"  run: " + strings.TrimSpace(run.ID),
		"  status: " + status,
	}
	if run.AgentCount > 0 {
		lines = append(lines, fmt.Sprintf("  progress: %d/%d done, %d running, %d errors",
			run.DoneCount, run.AgentCount, run.RunningAgentCount, run.ErrorCount))
	}
	if current := strings.TrimSpace(run.CurrentPhase); current != "" {
		lines = append(lines, "  current phase: "+current)
	}
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) > 0 {
		lines = append(lines, "  plan: "+moduTUIWorkflowPhasePlanRoute(phases))
	}
	return lines
}

func moduTUIWorkflowMissingRunPanel(id, title, runID string) modutui.Panel {
	return modutui.Panel{
		ID:       id,
		Title:    title,
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
		Meta:   moduTUIWorkflowPanelRef{PanelID: id, RunID: strings.TrimSpace(runID)},
	}
}

func moduTUIWorkflowInvalidAgentPanel(runID, agentIDText string) modutui.Panel {
	runID = strings.TrimSpace(runID)
	return modutui.Panel{
		ID:       moduTUIWorkflowAgentPanelID,
		Title:    "Workflow Agent",
		Subtitle: "invalid agent id: " + strings.TrimSpace(agentIDText),
		Lines: []string{
			"Agent id must be a positive integer.",
			"Use the Agents or Phase panel to choose an agent row.",
		},
		Rows: []modutui.PanelRow{{
			Label:   "Back to agents",
			Detail:  runID,
			Command: moduTUIWorkflowPanelAgentsPrefix + runID,
		}, {
			Label:   "Back to run detail",
			Detail:  runID,
			Command: moduTUIWorkflowPanelDetailPrefix + runID,
		}},
		Footer: "[enter] back  [esc/q] close",
	}
}

func moduTUIWorkflowResultLines(snapshotPath string) ([]string, error) {
	snapshotPath = strings.TrimSpace(snapshotPath)
	if snapshotPath == "" {
		return []string{"  no snapshot path available"}, nil
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	result, ok := snapshot["result"]
	if !ok || result == nil {
		return []string{"  no result in snapshot"}, nil
	}
	return moduTUIWorkflowValueLines(result), nil
}

func moduTUIWorkflowFileLines(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return []string{"  no file path available"}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return moduTUIWorkflowTextLines(string(data)), nil
}

func moduTUIWorkflowArtifactPreviewLines(lines []string, path string) []string {
	if len(lines) <= moduTUIWorkflowArtifactLineLimit {
		return lines
	}
	preview := append([]string{}, lines[:moduTUIWorkflowArtifactLineLimit]...)
	hidden := len(lines) - moduTUIWorkflowArtifactLineLimit
	truncated := fmt.Sprintf("  ... +%d more line(s) truncated", hidden)
	if strings.TrimSpace(path) != "" {
		truncated += "; full artifact: " + strings.TrimSpace(path)
	}
	preview = append(preview, truncated)
	return preview
}

func moduTUIWorkflowValueLines(value any) []string {
	if text, ok := value.(string); ok {
		return moduTUIWorkflowTextLines(text)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return moduTUIWorkflowTextLines(fmt.Sprint(value))
	}
	return moduTUIWorkflowTextLines(string(data))
}

func moduTUIWorkflowTextLines(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{"  (empty)"}
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return lines
}

func moduTUIWorkflowRunBySelectorFromStates(states map[string]any, selector string) (moduTUIWorkflowRun, bool) {
	snapshot, ok := decodeModuTUIWorkflowSnapshot(states)
	if !ok {
		return moduTUIWorkflowRun{}, false
	}
	runs := snapshot.Runs
	if len(runs) == 0 {
		return moduTUIWorkflowRun{}, false
	}
	selector = strings.TrimSpace(selector)
	if selector == "" || selector == "latest" {
		return runs[0], true
	}
	for _, run := range runs {
		if run.ID == selector {
			return run, true
		}
	}
	return moduTUIWorkflowRun{}, false
}

func moduTUIWorkflowRunByIDFromStates(states map[string]any, runID string) (moduTUIWorkflowRun, bool) {
	snapshot, ok := decodeModuTUIWorkflowSnapshot(states)
	if !ok {
		return moduTUIWorkflowRun{}, false
	}
	runID = strings.TrimSpace(runID)
	for _, run := range snapshot.Runs {
		if run.ID == runID {
			return run, true
		}
	}
	return moduTUIWorkflowRun{}, false
}

func moduTUIWorkflowCockpitRowsFromSnapshot(snapshot moduTUIWorkflowSnapshot) []modutui.PanelRow {
	runs := snapshot.Runs
	rows := make([]modutui.PanelRow, 0, min(len(runs), 12))
	for i, run := range runs {
		if i >= 12 {
			break
		}
		name := run.Name
		if name == "" {
			name = run.ID
		}
		progress := ""
		if run.AgentCount > 0 {
			progress = fmt.Sprintf(" %d/%d", run.DoneCount, run.AgentCount)
		}
		label := fmt.Sprintf("%s [%s]%s", name, run.Status, progress)
		detailParts := []string{}
		if run.CurrentPhase != "" {
			detailParts = append(detailParts, run.CurrentPhase)
		}
		if run.DurationMs > 0 {
			detailParts = append(detailParts, formatModuTUIActivityDuration(time.Duration(run.DurationMs)*time.Millisecond))
		}
		if run.ErrorCount > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d errors", run.ErrorCount))
		}
		rows = append(rows, modutui.PanelRow{
			Label:   label,
			Detail:  strings.Join(detailParts, " · "),
			Value:   run.ID,
			Command: moduTUIWorkflowCockpitRunCommand(run),
		})
	}
	return rows
}

func moduTUIWorkflowCockpitShortcutsFromSnapshot(snapshot moduTUIWorkflowSnapshot) []modutui.PanelShortcut {
	runs := snapshot.Runs
	if len(runs) == 0 {
		return nil
	}
	views := []string{"feed", "map", "detail"}
	if moduTUIWorkflowStatusIsTerminal(runs[0].Status) {
		views = append(views, "result", "script")
	}
	return moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(runs[0].ID),
		moduTUIWorkflowNavigationShortcuts(runs[0].ID, views...),
	)
}

func moduTUIWorkflowCockpitRunCommand(run moduTUIWorkflowRun) string {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return moduTUIWorkflowPanelBackCommand
	}
	return moduTUIWorkflowPanelFeedPrefix + runID
}

func moduTUIWorkflowCockpitSubtitleFromSnapshot(snapshot moduTUIWorkflowSnapshot, available bool) string {
	if !available {
		return "workflow runtime state unavailable"
	}
	runs := snapshot.Runs
	latest := "no runs"
	if len(runs) > 0 {
		name := runs[0].Name
		if name == "" {
			name = runs[0].ID
		}
		latest = fmt.Sprintf("latest %s [%s]", name, runs[0].Status)
	}
	return fmt.Sprintf("running %d  stopped %d  completed %d  failed %d  %s",
		snapshot.RunningCount,
		snapshot.StoppedCount,
		snapshot.CompletedCount,
		snapshot.FailedCount,
		latest,
	)
}

func moduTUIWorkflowCockpitTextFromSnapshot(snapshot moduTUIWorkflowSnapshot, available bool) string {
	if !available {
		return strings.Join([]string{
			"Workflow Cockpit",
			"",
			"overview",
			"  workflow runtime state is not available",
			"",
			"next actions",
			"  enable dynamic workflows in /config",
			"  start a workflow, then rerun /workflows",
		}, "\n")
	}
	runs := snapshot.Runs
	var lines []string
	lines = append(lines, "Workflow Cockpit", "")
	lines = append(lines, "overview")
	lines = append(lines, fmt.Sprintf("  running %d  stopped %d  completed %d  failed %d",
		snapshot.RunningCount,
		snapshot.StoppedCount,
		snapshot.CompletedCount,
		snapshot.FailedCount,
	))
	if indicator := snapshot.Indicator; indicator != "" {
		lines = append(lines, "  "+indicator)
	}
	if len(runs) == 0 {
		lines = append(lines, "  no workflow runs in this session", "", "next actions")
		lines = append(lines, "  start a workflow, then rerun /workflows")
		lines = append(lines, "  /workflows list")
		return strings.Join(lines, "\n")
	}

	latest := runs[0]
	name := latest.Name
	if strings.TrimSpace(name) == "" {
		name = latest.ID
	}
	progress := ""
	if latest.AgentCount > 0 {
		progress = fmt.Sprintf(" %d/%d", latest.DoneCount, latest.AgentCount)
	}
	current := ""
	if latest.CurrentPhase != "" {
		current = " current=" + latest.CurrentPhase
	}
	lines = append(lines, fmt.Sprintf("  latest %s [%s]%s%s", name, latest.Status, progress, current))
	if latest.ErrorCount > 0 {
		lines = append(lines, fmt.Sprintf("  errors %d", latest.ErrorCount))
	}

	if board := moduTUIWorkflowRunBoardLines(latest); len(board) > 0 {
		lines = append(lines, "", "board")
		lines = append(lines, board...)
	}
	lines = append(lines, "", "flow")
	lines = append(lines, moduTUIWorkflowRunFlowLines(latest)...)
	if updates := moduTUIWorkflowRunUpdateLines(latest); len(updates) > 0 {
		lines = append(lines, "", "updates")
		lines = append(lines, updates...)
	}
	if timeline := moduTUIWorkflowRunTimelineLines(latest); len(timeline) > 0 {
		lines = append(lines, "", "timeline")
		lines = append(lines, timeline...)
	}

	lines = append(lines, "", "latest run")
	lines = append(lines, "  id: "+latest.ID)
	if latest.Name != "" {
		lines = append(lines, "  name: "+latest.Name)
	}
	lines = append(lines, "  status: "+latest.Status)
	if latest.CurrentPhase != "" {
		lines = append(lines, "  current phase: "+latest.CurrentPhase)
	}
	lines = append(lines, fmt.Sprintf("  progress: %d/%d done, %d running, %d errors",
		latest.DoneCount, latest.AgentCount, latest.RunningAgentCount, latest.ErrorCount))
	lines = append(lines, "", "next actions")
	lines = append(lines, "  /workflows guide latest")
	lines = append(lines, "  /workflows feed latest")
	lines = append(lines, "  /workflows map latest")
	lines = append(lines, "  /workflows show latest")
	lines = append(lines, "  rerun /workflows to refresh")
	return strings.Join(lines, "\n")
}

func moduTUIWorkflowOrchestrationLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return []string{"  no phase or agent snapshot yet"}
	}
	var lines []string
	for _, phase := range phases {
		title := phase.Title
		if title == "" {
			title = "(no phase)"
		}
		lines = append(lines, fmt.Sprintf("  [%s] %d/%d %s",
			title, phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase)))
		agents := moduTUIWorkflowAgentsForPhase(run.Agents, phase.Title)
		if len(agents) == 0 {
			continue
		}
		for i, agent := range agents {
			if i >= 8 {
				lines = append(lines, fmt.Sprintf("    ... +%d more agents", len(agents)-i))
				break
			}
			lines = append(lines, "    "+moduTUIWorkflowAgentLine(agent))
			if agent.Error != "" {
				lines = append(lines, "      error: "+moduTUITruncate(agent.Error, 120))
			} else if agent.ResultPreview != "" {
				lines = append(lines, "      result: "+moduTUITruncate(agent.ResultPreview, 120))
			} else if agent.PromptPreview != "" {
				lines = append(lines, "      prompt: "+moduTUITruncate(agent.PromptPreview, 120))
			}
			if len(agent.ToolCalls) > 0 {
				lines = append(lines, "      tools: "+moduTUIWorkflowToolSummary(agent.ToolCalls))
			}
		}
	}
	return lines
}

func moduTUIWorkflowTopologyLines(run moduTUIWorkflowRun) []string {
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
		title := moduTUIWorkflowPhaseTitle(phase.Title)
		lines = append(lines, fmt.Sprintf("  %02d %s [%s] %d/%d",
			i+1,
			title,
			moduTUIWorkflowTimelinePhaseStatus(phase),
			phase.DoneCount,
			phase.AgentCount,
		))
		lines = append(lines, "     path: "+moduTUIWorkflowTopologyPath(phases, i))
		if agents := moduTUIWorkflowTopologyAgentLine(run.Agents, phase.Title); agents != "" {
			lines = append(lines, "     agents: "+agents)
		}
	}
	return lines
}

func moduTUIWorkflowTopologyPath(phases []moduTUIWorkflowPhase, index int) string {
	current := "phase"
	if index >= 0 && index < len(phases) {
		current = moduTUIWorkflowPhaseTitle(phases[index].Title)
	}
	prev := "start"
	if index > 0 && index-1 < len(phases) {
		prev = moduTUIWorkflowPhaseTitle(phases[index-1].Title)
	}
	next := "finish"
	if index >= 0 && index+1 < len(phases) {
		next = moduTUIWorkflowPhaseTitle(phases[index+1].Title)
	}
	return prev + " -> " + current + " -> " + next
}

func moduTUIWorkflowTopologyAgentLine(agents []moduTUIWorkflowAgent, phase string) string {
	phaseAgents := moduTUIWorkflowAgentsForPhase(agents, phase)
	if len(phaseAgents) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(phaseAgents), 4))
	for i, agent := range phaseAgents {
		if i >= 4 {
			parts = append(parts, fmt.Sprintf("+%d more", len(phaseAgents)-i))
			break
		}
		parts = append(parts, moduTUIWorkflowLaneAgent(agent))
	}
	return strings.Join(parts, " | ")
}

func moduTUIWorkflowPhasePositionLines(run moduTUIWorkflowRun, phaseTitle string) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return nil
	}
	index := -1
	phaseTitle = strings.TrimSpace(phaseTitle)
	for i, phase := range phases {
		if strings.TrimSpace(phase.Title) == phaseTitle {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}
	lines := []string{
		fmt.Sprintf("  stage: %d/%d", index+1, len(phases)),
		"  path: " + moduTUIWorkflowTopologyPath(phases, index),
	}
	if index > 0 {
		lines = append(lines, "  previous: "+moduTUIWorkflowPhaseTitle(phases[index-1].Title))
	}
	if index+1 < len(phases) {
		lines = append(lines, "  next: "+moduTUIWorkflowPhaseTitle(phases[index+1].Title))
	}
	return lines
}

func moduTUIWorkflowAgentContextLines(run moduTUIWorkflowRun, agent moduTUIWorkflowAgent) []string {
	phase := strings.TrimSpace(agent.Phase)
	if phase == "" {
		return nil
	}
	lines := moduTUIWorkflowPhasePositionLines(run, phase)
	phaseAgents := moduTUIWorkflowAgentsForPhase(run.Agents, phase)
	if len(phaseAgents) == 0 {
		return lines
	}
	for i, peer := range phaseAgents {
		if peer.ID == agent.ID {
			lines = append(lines, fmt.Sprintf("  agent: %d/%d in %s", i+1, len(phaseAgents), moduTUIWorkflowPhaseTitle(phase)))
			break
		}
	}
	if peers := moduTUIWorkflowTopologyAgentLine(run.Agents, phase); peers != "" {
		lines = append(lines, "  peers: "+peers)
	}
	return lines
}

func moduTUIWorkflowDerivedPhases(agents []moduTUIWorkflowAgent) []moduTUIWorkflowPhase {
	index := map[string]int{}
	var phases []moduTUIWorkflowPhase
	for _, agent := range agents {
		title := agent.Phase
		if _, ok := index[title]; !ok {
			index[title] = len(phases)
			phases = append(phases, moduTUIWorkflowPhase{Title: title})
		}
		phase := &phases[index[title]]
		phase.AgentCount++
		switch agent.Status {
		case "done", "completed":
			phase.DoneCount++
		case "running":
			phase.RunningCount++
		case "error", "failed":
			phase.ErrorCount++
		}
	}
	return phases
}

func moduTUIWorkflowAgentsForPhase(agents []moduTUIWorkflowAgent, phase string) []moduTUIWorkflowAgent {
	var out []moduTUIWorkflowAgent
	for _, agent := range agents {
		if agent.Phase == phase {
			out = append(out, agent)
		}
	}
	return out
}

func moduTUIWorkflowPhaseByTitle(run moduTUIWorkflowRun, title string) (moduTUIWorkflowPhase, bool) {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	for _, phase := range phases {
		if phase.Title == title {
			return phase, true
		}
	}
	return moduTUIWorkflowPhase{}, false
}

func moduTUIWorkflowPhaseTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return "(no phase)"
	}
	return title
}

func moduTUIWorkflowPhaseStatus(phase moduTUIWorkflowPhase) string {
	switch {
	case phase.ErrorCount > 0:
		return fmt.Sprintf("errors=%d", phase.ErrorCount)
	case phase.RunningCount > 0:
		return fmt.Sprintf("running=%d", phase.RunningCount)
	case phase.AgentCount > 0 && phase.DoneCount >= phase.AgentCount:
		return "done"
	case phase.DoneCount > 0:
		return "in-progress"
	default:
		return "waiting"
	}
}

func moduTUIWorkflowAgentLine(agent moduTUIWorkflowAgent) string {
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	parts := []string{fmt.Sprintf("#%d [%s] %s", agent.ID, agent.Status, label)}
	if agent.TurnTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens=%d", agent.TurnTokens))
	} else if agent.EstimatedTokens > 0 {
		parts = append(parts, fmt.Sprintf("estimated=%d", agent.EstimatedTokens))
	}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("tools=%d", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("failedTools=%d", agent.FailedToolCalls))
	}
	return strings.Join(parts, " ")
}

func moduTUIWorkflowToolSummary(calls []moduTUIWorkflowToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(calls), 3))
	for i, call := range calls {
		if i >= 3 {
			parts = append(parts, fmt.Sprintf("+%d more", len(calls)-i))
			break
		}
		name := call.ToolName
		if name == "" {
			name = "tool"
		}
		item := name
		if call.IsError {
			item += " error"
		}
		if call.ResultPreview != "" {
			item += " -> " + moduTUITruncate(call.ResultPreview, 60)
		} else if call.ArgsPreview != "" {
			item += " " + moduTUITruncate(call.ArgsPreview, 60)
		}
		parts = append(parts, item)
	}
	return strings.Join(parts, "; ")
}
