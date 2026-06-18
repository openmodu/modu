package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
)

func workflowPanelContent(session *coding_agent.CodingSession) string {
	if session == nil {
		return ""
	}
	return workflowPanelContentFromStates(session.ExtensionRuntimeStates())
}

func workflowPanelContentFromStates(states map[string]any) string {
	state, ok := workflowPanelState(states)
	if !ok {
		return ""
	}
	var lines []string
	lines = append(lines, "status")
	lines = append(lines, fmt.Sprintf("  running: %d", runtimeStateNumber(state["runningCount"])))
	lines = append(lines, fmt.Sprintf("  stopped: %d", runtimeStateNumber(state["stoppedCount"])))
	lines = append(lines, fmt.Sprintf("  completed: %d", runtimeStateNumber(state["completedCount"])))
	lines = append(lines, fmt.Sprintf("  failed: %d", runtimeStateNumber(state["failedCount"])))
	if indicator, _ := state["indicator"].(string); strings.TrimSpace(indicator) != "" {
		lines = append(lines, "  "+strings.TrimSpace(indicator))
	}
	runs := workflowPanelRuns(state["runs"])
	if len(runs) == 0 {
		lines = append(lines, "", "runs", "  no live workflow runs in this session")
	} else {
		lines = append(lines, "", fmt.Sprintf("runs (%d)", len(runs)))
		for i, run := range runs {
			if i >= 8 {
				lines = append(lines, fmt.Sprintf("  ... +%d more", len(runs)-i))
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
			line := fmt.Sprintf("  %s [%s]%s %s", run.ShortID(), run.Status, progress, name)
			if run.CurrentPhase != "" {
				line += " phase=" + run.CurrentPhase
			}
			if run.ErrorCount > 0 {
				line += fmt.Sprintf(" errors=%d", run.ErrorCount)
			}
			lines = append(lines, line)
		}
		if len(runs[0].Phases) > 0 {
			lines = append(lines, "", "latest run phases")
			for i, phase := range runs[0].Phases {
				if i >= 6 {
					lines = append(lines, fmt.Sprintf("  ... +%d more", len(runs[0].Phases)-i))
					break
				}
				title := phase.Title
				if title == "" {
					title = "(no phase)"
				}
				line := fmt.Sprintf("  %s: %d agent(s), %d done, %d running, %d errors", title, phase.AgentCount, phase.DoneCount, phase.RunningCount, phase.ErrorCount)
				if phase.EstimatedTokens > 0 {
					line += fmt.Sprintf(" estimated=%d", phase.EstimatedTokens)
				}
				if phase.Cost > 0 {
					line += " cost=" + workflowPanelCost(phase.Cost)
				}
				if phase.DurationMs > 0 {
					line += fmt.Sprintf(" durationMs=%d", phase.DurationMs)
				}
				lines = append(lines, line)
			}
		}
		if len(runs[0].Agents) > 0 {
			lines = append(lines, "", "latest run agents")
			for i, agent := range runs[0].Agents {
				if i >= 6 {
					lines = append(lines, fmt.Sprintf("  ... +%d more", len(runs[0].Agents)-i))
					break
				}
				label := agent.Label
				if label == "" {
					label = fmt.Sprintf("agent-%d", agent.ID)
				}
				line := fmt.Sprintf("  #%d [%s] %s", agent.ID, agent.Status, label)
				if agent.Phase != "" {
					line += " phase=" + agent.Phase
				}
				if agent.TurnTokens > 0 {
					line += fmt.Sprintf(" tokens=%d", agent.TurnTokens)
				} else if agent.EstimatedTokens > 0 {
					line += fmt.Sprintf(" estimated=%d", agent.EstimatedTokens)
				}
				if agent.Cost > 0 {
					line += " cost=" + workflowPanelCost(agent.Cost)
				}
				if agent.FailedToolCalls > 0 {
					line += fmt.Sprintf(" failedTools=%d", agent.FailedToolCalls)
				}
				if agent.RecentToolCalls > 0 {
					line += fmt.Sprintf(" recentTools=%d", agent.RecentToolCalls)
				}
				lines = append(lines, line)
				if agent.PromptPreview != "" {
					lines = append(lines, "     prompt: "+truncateRunes(agent.PromptPreview, 100))
				}
				if agent.Error != "" {
					lines = append(lines, "     error: "+truncateRunes(agent.Error, 100))
				} else if agent.ResultPreview != "" {
					lines = append(lines, "     result: "+truncateRunes(agent.ResultPreview, 100))
				}
				if len(agent.ToolCalls) > 0 {
					lines = append(lines, "     tools: "+workflowPanelToolCallSummary(agent.ToolCalls))
				}
			}
		}
	}
	lines = append(lines, "", "commands")
	lines = append(lines, "  /workflows show <run-id|latest>")
	lines = append(lines, "  /workflows agent <run-id|latest> <agent-id>")
	lines = append(lines, "  /workflows pause|stop|resume|restart <run-id|latest>")
	lines = append(lines, "  /workflows agent-stop|agent-restart <run-id|latest> <agent-id>")
	return strings.Join(lines, "\n")
}

type workflowPanelLevel int

const (
	workflowPanelLevelRuns workflowPanelLevel = iota
	workflowPanelLevelPhases
	workflowPanelLevelAgents
	workflowPanelLevelAgentDetail
	workflowPanelLevelSave
)

func workflowPanelState(states map[string]any) (map[string]any, bool) {
	raw, ok := states["workflow"]
	if !ok {
		return nil, false
	}
	state, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	return state, true
}

type workflowPanelRun struct {
	ID           string
	Name         string
	Status       string
	AgentCount   int
	DoneCount    int
	ErrorCount   int
	CurrentPhase string
	UpdatedAt    int64
	Phases       []workflowPanelPhase
	Agents       []workflowPanelAgent
}

func (r workflowPanelRun) ShortID() string {
	id := strings.TrimSpace(r.ID)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func (r workflowPanelRun) DisplayName() string {
	name := strings.TrimSpace(r.Name)
	if name != "" {
		return name
	}
	return strings.TrimSpace(r.ID)
}

func workflowPanelRuns(value any) []workflowPanelRun {
	var out []workflowPanelRun
	switch runs := value.(type) {
	case []map[string]any:
		for _, item := range runs {
			out = append(out, workflowPanelRunFromMap(item))
		}
	case []any:
		for _, raw := range runs {
			if item, ok := raw.(map[string]any); ok {
				out = append(out, workflowPanelRunFromMap(item))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func (b *bubbleTUI) openWorkflowPanel() bool {
	if b.session == nil {
		return false
	}
	state, ok := workflowPanelState(b.session.ExtensionRuntimeStates())
	if !ok {
		return false
	}
	b.workflowPanelState = state
	b.workflowRuns = workflowPanelRuns(state["runs"])
	b.workflowPanelLevel = workflowPanelLevelRuns
	b.workflowSelectIdx = workflowPanelMoveSelection(b.workflowSelectIdx, len(b.workflowRuns), 0)
	b.workflowPhaseIdx = 0
	b.workflowPhaseScroll = 0
	b.workflowAgentIdx = 0
	b.workflowAgentScroll = 0
	b.workflowDetailScroll = 0
	b.workflowSaveReturn = workflowPanelLevelRuns
	b.workflowSaveName = ""
	b.workflowSaveScope = "project"
	b.adjustWorkflowScroll()
	b.model.state = uiStateWorkflowPanel
	b.model.statusMsg = ""
	b.slashMatches = nil
	return true
}

func (b *bubbleTUI) closeWorkflowPanel(status string) {
	b.model.state = uiStateInput
	b.workflowPanelState = nil
	b.workflowRuns = nil
	b.workflowPanelLevel = workflowPanelLevelRuns
	b.workflowSelectIdx = 0
	b.workflowScroll = 0
	b.workflowPhaseIdx = 0
	b.workflowPhaseScroll = 0
	b.workflowAgentIdx = 0
	b.workflowAgentScroll = 0
	b.workflowDetailScroll = 0
	b.workflowSaveReturn = workflowPanelLevelRuns
	b.workflowSaveName = ""
	b.workflowSaveScope = ""
	b.model.statusMsg = status
}

func (b *bubbleTUI) updateWorkflowPanelKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if b.workflowPanelLevel == workflowPanelLevelSave {
		return b, b.updateWorkflowSaveKey(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		b.closeWorkflowPanel("workflows closed")
	case "esc", "left":
		switch b.workflowPanelLevel {
		case workflowPanelLevelAgentDetail:
			b.workflowPanelLevel = workflowPanelLevelAgents
			b.model.statusMsg = "workflow agents"
			return b, nil
		case workflowPanelLevelAgents:
			b.workflowPanelLevel = workflowPanelLevelPhases
			b.model.statusMsg = "workflow phases"
			return b, nil
		case workflowPanelLevelPhases:
			b.workflowPanelLevel = workflowPanelLevelRuns
			b.model.statusMsg = "workflow runs"
			return b, nil
		}
		b.closeWorkflowPanel("workflows closed")
	case "up":
		b.moveWorkflowPanelSelection(-1)
	case "down":
		b.moveWorkflowPanelSelection(1)
	case "home":
		b.jumpWorkflowPanelSelection(0)
	case "end":
		b.jumpWorkflowPanelSelection(b.workflowPanelItemCount() - 1)
	case "pgup":
		b.jumpWorkflowPanelSelection(b.workflowPanelSelectedIndex() - modelSelectVisibleRows)
	case "pgdown":
		b.jumpWorkflowPanelSelection(b.workflowPanelSelectedIndex() + modelSelectVisibleRows)
	case "enter", "ctrl+j", "right":
		return b, b.confirmWorkflowSelection()
	default:
		runes := []rune(msg.Text)
		if len(runes) != 1 {
			return b, nil
		}
		switch runes[0] {
		case 'j':
			b.moveWorkflowPanelSelection(1)
		case 'k':
			b.moveWorkflowPanelSelection(-1)
		case 'q', 'Q':
			b.closeWorkflowPanel("workflows closed")
		case 'p', 'P':
			return b, b.workflowPanelControl("p")
		case 'x', 'X':
			return b, b.workflowPanelControl("x")
		case 'r', 'R':
			return b, b.workflowPanelControl("r")
		case 's', 'S':
			b.openWorkflowSave()
			return b, nil
		case 'h':
			switch b.workflowPanelLevel {
			case workflowPanelLevelAgentDetail:
				b.workflowPanelLevel = workflowPanelLevelAgents
				b.model.statusMsg = "workflow agents"
			case workflowPanelLevelAgents:
				b.workflowPanelLevel = workflowPanelLevelPhases
				b.model.statusMsg = "workflow phases"
			case workflowPanelLevelPhases:
				b.workflowPanelLevel = workflowPanelLevelRuns
				b.model.statusMsg = "workflow runs"
			}
		case 'l':
			return b, b.confirmWorkflowSelection()
		}
	}
	return b, nil
}

func (b *bubbleTUI) openWorkflowSave() {
	if _, ok := b.currentWorkflowRun(); !ok {
		b.model.statusMsg = "no workflow run selected"
		return
	}
	b.workflowSaveReturn = b.workflowPanelLevel
	b.workflowPanelLevel = workflowPanelLevelSave
	b.workflowSaveName = ""
	b.workflowSaveScope = "project"
	b.model.statusMsg = "workflow save"
}

func (b *bubbleTUI) updateWorkflowSaveKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		b.closeWorkflowPanel("workflows closed")
	case "esc", "left":
		b.workflowPanelLevel = b.workflowSaveReturn
		b.workflowSaveName = ""
		b.workflowSaveScope = ""
		b.model.statusMsg = "workflow save cancelled"
	case "tab":
		b.toggleWorkflowSaveScope()
	case "enter", "ctrl+j":
		return b.confirmWorkflowSave()
	case "backspace", "ctrl+h":
		rs := []rune(b.workflowSaveName)
		if len(rs) > 0 {
			b.workflowSaveName = string(rs[:len(rs)-1])
		}
	default:
		for _, r := range []rune(msg.Text) {
			if workflowSaveNameRuneOK(r) && len([]rune(b.workflowSaveName)) < 64 {
				b.workflowSaveName += string(r)
			}
		}
	}
	return nil
}

func (b *bubbleTUI) toggleWorkflowSaveScope() {
	if b.workflowSaveScope == "user" {
		b.workflowSaveScope = "project"
		return
	}
	b.workflowSaveScope = "user"
}

func (b *bubbleTUI) confirmWorkflowSave() tea.Cmd {
	run, ok := b.currentWorkflowRun()
	if !ok {
		b.model.statusMsg = "no workflow run selected"
		return nil
	}
	cmdLine, status := workflowPanelSaveCommand(run, b.workflowSaveName, b.workflowSaveScope)
	if cmdLine == "" {
		b.model.statusMsg = status
		return nil
	}
	b.closeWorkflowPanel("saving workflow")
	return b.runSlash(cmdLine)
}

func workflowPanelSaveCommand(run workflowPanelRun, name, scope string) (cmdLine, status string) {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return "", "no workflow run selected"
	}
	name = strings.TrimSpace(name)
	if !workflowSaveNameValid(name) {
		return "", "workflow name must start with a letter or digit and use letters, digits, '.', '_' or '-'"
	}
	if scope != "user" {
		scope = "project"
	}
	return fmt.Sprintf("/workflows save %s %s %s", runID, name, scope), "saving workflow"
}

func workflowSaveNameRuneOK(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '.' || r == '_' || r == '-'
}

func workflowSaveNameValid(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if !workflowSaveNameRuneOK(r) {
			return false
		}
		if i == 0 && !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func (b *bubbleTUI) workflowPanelControl(key string) tea.Cmd {
	cmdLine, status := b.workflowPanelControlCommand(key)
	if cmdLine == "" {
		b.model.statusMsg = status
		return nil
	}
	b.closeWorkflowPanel(status)
	return b.runSlash(cmdLine)
}

func (b *bubbleTUI) workflowPanelControlCommand(key string) (cmdLine, status string) {
	run, ok := b.currentWorkflowRun()
	if !ok || strings.TrimSpace(run.ID) == "" {
		return "", "no workflow run selected"
	}
	runID := strings.TrimSpace(run.ID)
	switch strings.ToLower(key) {
	case "p":
		if strings.EqualFold(run.Status, "stopped") {
			return "/workflows resume " + runID, "resuming workflow"
		}
		return "/workflows pause " + runID, "pausing workflow"
	case "x":
		if agent, ok := b.currentWorkflowAgentForControl(); ok {
			return fmt.Sprintf("/workflows agent-stop %s %d", runID, agent.ID), "stopping workflow agent"
		}
		return "/workflows stop " + runID, "stopping workflow"
	case "r":
		if agent, ok := b.currentWorkflowAgentForControl(); ok {
			return fmt.Sprintf("/workflows agent-restart %s %d", runID, agent.ID), "restarting workflow agent"
		}
		return "", "select an agent to restart"
	}
	return "", ""
}

func workflowPanelMoveSelection(idx, total, delta int) int {
	if total <= 0 {
		return 0
	}
	next := (idx + delta) % total
	if next < 0 {
		next += total
	}
	return next
}

func (b *bubbleTUI) workflowPanelItemCount() int {
	switch b.workflowPanelLevel {
	case workflowPanelLevelPhases:
		if run, ok := b.currentWorkflowRun(); ok {
			return len(run.Phases)
		}
		return 0
	case workflowPanelLevelAgents:
		return len(b.currentWorkflowAgents())
	case workflowPanelLevelAgentDetail:
		agent, ok := b.currentWorkflowAgent()
		if !ok {
			return 0
		}
		return len(workflowPanelAgentDetailLines(agent))
	case workflowPanelLevelSave:
		return 1
	}
	return len(b.workflowRuns)
}

func (b *bubbleTUI) workflowPanelSelectedIndex() int {
	switch b.workflowPanelLevel {
	case workflowPanelLevelPhases:
		return b.workflowPhaseIdx
	case workflowPanelLevelAgents:
		return b.workflowAgentIdx
	case workflowPanelLevelAgentDetail:
		return b.workflowDetailScroll
	case workflowPanelLevelSave:
		return 0
	}
	return b.workflowSelectIdx
}

func (b *bubbleTUI) moveWorkflowPanelSelection(delta int) {
	switch b.workflowPanelLevel {
	case workflowPanelLevelPhases:
		b.workflowPhaseIdx = workflowPanelMoveSelection(b.workflowPhaseIdx, b.workflowPanelItemCount(), delta)
		b.adjustWorkflowPhaseScroll()
		return
	case workflowPanelLevelAgents:
		b.workflowAgentIdx = workflowPanelMoveSelection(b.workflowAgentIdx, b.workflowPanelItemCount(), delta)
		b.adjustWorkflowAgentScroll()
		return
	case workflowPanelLevelAgentDetail:
		b.moveWorkflowDetailScroll(delta)
		return
	case workflowPanelLevelSave:
		return
	}
	b.workflowSelectIdx = workflowPanelMoveSelection(b.workflowSelectIdx, len(b.workflowRuns), delta)
	b.adjustWorkflowScroll()
}

func (b *bubbleTUI) jumpWorkflowPanelSelection(idx int) {
	total := b.workflowPanelItemCount()
	if total == 0 {
		switch b.workflowPanelLevel {
		case workflowPanelLevelPhases:
			b.workflowPhaseIdx = 0
		case workflowPanelLevelAgents:
			b.workflowAgentIdx = 0
		case workflowPanelLevelAgentDetail:
			b.workflowDetailScroll = 0
		case workflowPanelLevelSave:
			b.workflowSaveName = ""
		default:
			b.workflowSelectIdx = 0
		}
		return
	}
	switch b.workflowPanelLevel {
	case workflowPanelLevelPhases:
		b.workflowPhaseIdx = clampInt(idx, 0, total-1)
		b.adjustWorkflowPhaseScroll()
		return
	case workflowPanelLevelAgents:
		b.workflowAgentIdx = clampInt(idx, 0, total-1)
		b.adjustWorkflowAgentScroll()
		return
	case workflowPanelLevelAgentDetail:
		b.workflowDetailScroll = clampInt(idx, 0, max(0, total-modelSelectVisibleRows))
		return
	case workflowPanelLevelSave:
		return
	}
	b.workflowSelectIdx = clampInt(idx, 0, total-1)
	b.adjustWorkflowScroll()
}

func (b *bubbleTUI) adjustWorkflowScroll() {
	total := len(b.workflowRuns)
	if total <= modelSelectVisibleRows {
		b.workflowScroll = 0
		return
	}
	if b.workflowSelectIdx < b.workflowScroll {
		b.workflowScroll = b.workflowSelectIdx
	} else if b.workflowSelectIdx >= b.workflowScroll+modelSelectVisibleRows {
		b.workflowScroll = b.workflowSelectIdx - modelSelectVisibleRows + 1
	}
	if maxOffset := total - modelSelectVisibleRows; b.workflowScroll > maxOffset {
		b.workflowScroll = maxOffset
	}
	if b.workflowScroll < 0 {
		b.workflowScroll = 0
	}
}

func (b *bubbleTUI) adjustWorkflowPhaseScroll() {
	total := b.workflowPanelItemCount()
	if total <= modelSelectVisibleRows {
		b.workflowPhaseScroll = 0
		return
	}
	if b.workflowPhaseIdx < b.workflowPhaseScroll {
		b.workflowPhaseScroll = b.workflowPhaseIdx
	} else if b.workflowPhaseIdx >= b.workflowPhaseScroll+modelSelectVisibleRows {
		b.workflowPhaseScroll = b.workflowPhaseIdx - modelSelectVisibleRows + 1
	}
	if maxOffset := total - modelSelectVisibleRows; b.workflowPhaseScroll > maxOffset {
		b.workflowPhaseScroll = maxOffset
	}
	if b.workflowPhaseScroll < 0 {
		b.workflowPhaseScroll = 0
	}
}

func (b *bubbleTUI) adjustWorkflowAgentScroll() {
	total := b.workflowPanelItemCount()
	if total <= modelSelectVisibleRows {
		b.workflowAgentScroll = 0
		return
	}
	if b.workflowAgentIdx < b.workflowAgentScroll {
		b.workflowAgentScroll = b.workflowAgentIdx
	} else if b.workflowAgentIdx >= b.workflowAgentScroll+modelSelectVisibleRows {
		b.workflowAgentScroll = b.workflowAgentIdx - modelSelectVisibleRows + 1
	}
	if maxOffset := total - modelSelectVisibleRows; b.workflowAgentScroll > maxOffset {
		b.workflowAgentScroll = maxOffset
	}
	if b.workflowAgentScroll < 0 {
		b.workflowAgentScroll = 0
	}
}

func (b *bubbleTUI) moveWorkflowDetailScroll(delta int) {
	total := b.workflowPanelItemCount()
	maxOffset := max(0, total-modelSelectVisibleRows)
	b.workflowDetailScroll = clampInt(b.workflowDetailScroll+delta, 0, maxOffset)
}

func (b *bubbleTUI) confirmWorkflowSelection() tea.Cmd {
	if b.workflowPanelLevel == workflowPanelLevelPhases {
		agents := b.currentWorkflowAgents()
		if len(agents) == 0 {
			b.model.statusMsg = "no agents in selected phase"
			return nil
		}
		b.workflowPanelLevel = workflowPanelLevelAgents
		b.workflowAgentIdx = 0
		b.workflowAgentScroll = 0
		b.workflowDetailScroll = 0
		b.model.statusMsg = "workflow agents"
		return nil
	}
	if b.workflowPanelLevel == workflowPanelLevelAgents {
		if _, ok := b.currentWorkflowAgent(); !ok {
			b.model.statusMsg = "no workflow agent selected"
			return nil
		}
		b.workflowPanelLevel = workflowPanelLevelAgentDetail
		b.workflowDetailScroll = 0
		b.model.statusMsg = "workflow agent detail"
		return nil
	}
	if b.workflowPanelLevel == workflowPanelLevelAgentDetail {
		return nil
	}
	if b.workflowPanelLevel == workflowPanelLevelRuns {
		if run, ok := b.currentWorkflowRun(); ok && len(run.Phases) > 0 {
			b.workflowPanelLevel = workflowPanelLevelPhases
			b.workflowPhaseIdx = 0
			b.workflowPhaseScroll = 0
			b.workflowAgentIdx = 0
			b.workflowAgentScroll = 0
			b.workflowDetailScroll = 0
			b.model.statusMsg = "workflow phases"
			return nil
		}
	}
	cmdLine := workflowPanelSelectedRunCommand(b.workflowRuns, b.workflowSelectIdx)
	if cmdLine == "" {
		b.closeWorkflowPanel("no workflow run selected")
		return nil
	}
	b.closeWorkflowPanel("opening workflow details")
	return b.runSlash(cmdLine)
}

func workflowPanelSelectedRunCommand(runs []workflowPanelRun, idx int) string {
	if len(runs) == 0 {
		return ""
	}
	idx = clampInt(idx, 0, len(runs)-1)
	id := strings.TrimSpace(runs[idx].ID)
	if id == "" {
		return ""
	}
	return "/workflows show " + id
}

func (b *bubbleTUI) currentWorkflowRun() (workflowPanelRun, bool) {
	if len(b.workflowRuns) == 0 {
		return workflowPanelRun{}, false
	}
	idx := clampInt(b.workflowSelectIdx, 0, len(b.workflowRuns)-1)
	return b.workflowRuns[idx], true
}

func (b *bubbleTUI) currentWorkflowPhase() (workflowPanelPhase, bool) {
	run, ok := b.currentWorkflowRun()
	if !ok || len(run.Phases) == 0 {
		return workflowPanelPhase{}, false
	}
	idx := clampInt(b.workflowPhaseIdx, 0, len(run.Phases)-1)
	return run.Phases[idx], true
}

func (b *bubbleTUI) currentWorkflowAgents() []workflowPanelAgent {
	run, ok := b.currentWorkflowRun()
	if !ok {
		return nil
	}
	phase, ok := b.currentWorkflowPhase()
	if !ok {
		return append([]workflowPanelAgent(nil), run.Agents...)
	}
	title := strings.TrimSpace(phase.Title)
	var out []workflowPanelAgent
	for _, agent := range run.Agents {
		if strings.TrimSpace(agent.Phase) == title {
			out = append(out, agent)
		}
	}
	return out
}

func (b *bubbleTUI) currentWorkflowAgent() (workflowPanelAgent, bool) {
	agents := b.currentWorkflowAgents()
	if len(agents) == 0 {
		return workflowPanelAgent{}, false
	}
	idx := clampInt(b.workflowAgentIdx, 0, len(agents)-1)
	return agents[idx], true
}

func (b *bubbleTUI) currentWorkflowAgentForControl() (workflowPanelAgent, bool) {
	switch b.workflowPanelLevel {
	case workflowPanelLevelAgents, workflowPanelLevelAgentDetail:
		return b.currentWorkflowAgent()
	default:
		return workflowPanelAgent{}, false
	}
}

func (b *bubbleTUI) renderWorkflowPanel() string {
	switch b.workflowPanelLevel {
	case workflowPanelLevelPhases:
		run, _ := b.currentWorkflowRun()
		return workflowPanelPhaseContent(run, b.workflowPhaseIdx, b.workflowPhaseScroll)
	case workflowPanelLevelAgents:
		run, _ := b.currentWorkflowRun()
		phase, _ := b.currentWorkflowPhase()
		return workflowPanelAgentsContent(run, phase, b.currentWorkflowAgents(), b.workflowAgentIdx, b.workflowAgentScroll)
	case workflowPanelLevelAgentDetail:
		run, _ := b.currentWorkflowRun()
		phase, _ := b.currentWorkflowPhase()
		agent, _ := b.currentWorkflowAgent()
		return workflowPanelAgentDetailContent(run, phase, agent, b.workflowDetailScroll)
	case workflowPanelLevelSave:
		run, _ := b.currentWorkflowRun()
		return workflowPanelSaveContent(run, b.workflowSaveName, b.workflowSaveScope)
	}
	return workflowPanelSelectableContent(b.workflowPanelState, b.workflowRuns, b.workflowSelectIdx, b.workflowScroll)
}

func workflowPanelSelectableContent(state map[string]any, runs []workflowPanelRun, selected, scroll int) string {
	total := len(runs)
	selected = workflowPanelMoveSelection(selected, total, 0)
	visible := min(total, modelSelectVisibleRows)
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Workflows",
			Selected: selected,
			Visible:  visible,
			Total:    total,
			Mode:     workflowPanelStatusText(state),
		})),
	}
	indicator, _ := state["indicator"].(string)
	if strings.TrimSpace(indicator) != "" {
		lines = append(lines, uiDimText.Render("  "+strings.TrimSpace(indicator)))
	}
	if total == 0 {
		lines = append(lines, uiDimText.Render("  no live workflow runs in this session"))
		lines = append(lines, uiDimText.Render(workflowPanelHint()))
		return strings.Join(lines, "\n")
	}
	start, end := bubbleWindowRange(selected, total, modelSelectVisibleRows)
	if scroll >= 0 && total > modelSelectVisibleRows {
		start = clampInt(scroll, 0, total-modelSelectVisibleRows)
		end = start + modelSelectVisibleRows
	}
	for i := start; i < end; i++ {
		line := workflowPanelRunLine(runs[i], i == selected)
		if i == start && start > 0 {
			line += "  ^"
		} else if i == end-1 && end < total {
			line += "  v"
		}
		lines = append(lines, line)
	}
	lines = append(lines, uiDimText.Render(workflowPanelHint()))
	return strings.Join(lines, "\n")
}

func workflowPanelStatusText(state map[string]any) string {
	if state == nil {
		return ""
	}
	var parts []string
	if n := runtimeStateNumber(state["runningCount"]); n > 0 {
		parts = append(parts, fmt.Sprintf("running=%d", n))
	}
	if n := runtimeStateNumber(state["stoppedCount"]); n > 0 {
		parts = append(parts, fmt.Sprintf("stopped=%d", n))
	}
	if n := runtimeStateNumber(state["completedCount"]); n > 0 {
		parts = append(parts, fmt.Sprintf("completed=%d", n))
	}
	if n := runtimeStateNumber(state["failedCount"]); n > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", n))
	}
	return strings.Join(parts, " ")
}

func workflowPanelRunLine(run workflowPanelRun, selected bool) string {
	marker := "  "
	style := uiDimText
	if selected {
		marker = uiPrimaryText.Render("> ")
		style = uiPrimaryText
	}
	progress := ""
	if run.AgentCount > 0 {
		progress = fmt.Sprintf(" %d/%d", run.DoneCount, run.AgentCount)
	}
	line := fmt.Sprintf("%s%s [%s]%s %s", marker, run.ShortID(), run.Status, progress, run.DisplayName())
	if run.CurrentPhase != "" {
		line += " phase=" + run.CurrentPhase
	}
	if run.ErrorCount > 0 {
		line += fmt.Sprintf(" errors=%d", run.ErrorCount)
	}
	return style.Render(line)
}

func workflowPanelHint() string {
	return "  up/down or j/k select  enter/right phases  p pause/resume  x stop  esc/q close"
}

func (b *bubbleTUI) currentWorkflowPanelHint() string {
	switch b.workflowPanelLevel {
	case workflowPanelLevelPhases:
		return workflowPanelPhaseHint()
	case workflowPanelLevelAgents:
		return workflowPanelAgentsHint()
	case workflowPanelLevelAgentDetail:
		return workflowPanelAgentDetailHint()
	case workflowPanelLevelSave:
		return workflowPanelSaveHint()
	}
	return workflowPanelHint()
}

func workflowPanelPhaseContent(run workflowPanelRun, selected, scroll int) string {
	total := len(run.Phases)
	selected = workflowPanelMoveSelection(selected, total, 0)
	visible := min(total, modelSelectVisibleRows)
	mode := strings.TrimSpace(run.ShortID() + " " + run.Status)
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Workflow phases",
			Selected: selected,
			Visible:  visible,
			Total:    total,
			Mode:     mode,
		})),
		uiDimText.Render("  " + workflowPanelRunSummary(run)),
	}
	if total == 0 {
		lines = append(lines, uiDimText.Render("  no phase summary for this workflow run"))
		lines = append(lines, uiDimText.Render(workflowPanelPhaseHint()))
		return strings.Join(lines, "\n")
	}
	start, end := bubbleWindowRange(selected, total, modelSelectVisibleRows)
	if scroll >= 0 && total > modelSelectVisibleRows {
		start = clampInt(scroll, 0, total-modelSelectVisibleRows)
		end = start + modelSelectVisibleRows
	}
	for i := start; i < end; i++ {
		line := workflowPanelPhaseLine(run.Phases[i], i == selected)
		if i == start && start > 0 {
			line += "  ^"
		} else if i == end-1 && end < total {
			line += "  v"
		}
		lines = append(lines, line)
	}
	lines = append(lines, uiDimText.Render(workflowPanelPhaseHint()))
	return strings.Join(lines, "\n")
}

func workflowPanelRunSummary(run workflowPanelRun) string {
	name := run.DisplayName()
	progress := ""
	if run.AgentCount > 0 {
		progress = fmt.Sprintf(" %d/%d", run.DoneCount, run.AgentCount)
	}
	line := fmt.Sprintf("%s [%s]%s", name, run.Status, progress)
	if run.CurrentPhase != "" {
		line += " phase=" + run.CurrentPhase
	}
	if run.ErrorCount > 0 {
		line += fmt.Sprintf(" errors=%d", run.ErrorCount)
	}
	return strings.TrimSpace(line)
}

func workflowPanelPhaseLine(phase workflowPanelPhase, selected bool) string {
	marker := "  "
	style := uiDimText
	if selected {
		marker = uiPrimaryText.Render("> ")
		style = uiPrimaryText
	}
	title := phase.Title
	if title == "" {
		title = "(no phase)"
	}
	line := fmt.Sprintf("%s%s: %d agent(s), %d done, %d running, %d errors", marker, title, phase.AgentCount, phase.DoneCount, phase.RunningCount, phase.ErrorCount)
	if phase.EstimatedTokens > 0 {
		line += fmt.Sprintf(" estimated=%d", phase.EstimatedTokens)
	}
	if phase.Cost > 0 {
		line += " cost=" + workflowPanelCost(phase.Cost)
	}
	if phase.DurationMs > 0 {
		line += fmt.Sprintf(" durationMs=%d", phase.DurationMs)
	}
	return style.Render(line)
}

func workflowPanelPhaseHint() string {
	return "  up/down or j/k select  enter/right agents  p pause/resume  x stop run  esc/left runs  q close"
}

func workflowPanelAgentsContent(run workflowPanelRun, phase workflowPanelPhase, agents []workflowPanelAgent, selected, scroll int) string {
	total := len(agents)
	selected = workflowPanelMoveSelection(selected, total, 0)
	visible := min(total, modelSelectVisibleRows)
	mode := strings.TrimSpace(run.ShortID() + " " + run.Status)
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Workflow agents",
			Selected: selected,
			Visible:  visible,
			Total:    total,
			Mode:     mode,
		})),
		uiDimText.Render("  " + workflowPanelPhaseSummary(phase)),
	}
	if total == 0 {
		lines = append(lines, uiDimText.Render("  no agents in this phase"))
		lines = append(lines, uiDimText.Render(workflowPanelAgentsHint()))
		return strings.Join(lines, "\n")
	}
	start, end := bubbleWindowRange(selected, total, modelSelectVisibleRows)
	if scroll >= 0 && total > modelSelectVisibleRows {
		start = clampInt(scroll, 0, total-modelSelectVisibleRows)
		end = start + modelSelectVisibleRows
	}
	for i := start; i < end; i++ {
		line := workflowPanelAgentLine(agents[i], i == selected)
		if i == start && start > 0 {
			line += "  ^"
		} else if i == end-1 && end < total {
			line += "  v"
		}
		lines = append(lines, line)
	}
	lines = append(lines, uiDimText.Render(workflowPanelAgentsHint()))
	return strings.Join(lines, "\n")
}

func workflowPanelPhaseSummary(phase workflowPanelPhase) string {
	title := phase.Title
	if title == "" {
		title = "(no phase)"
	}
	line := fmt.Sprintf("%s: %d agent(s), %d done, %d running, %d errors", title, phase.AgentCount, phase.DoneCount, phase.RunningCount, phase.ErrorCount)
	if phase.EstimatedTokens > 0 {
		line += fmt.Sprintf(" estimated=%d", phase.EstimatedTokens)
	}
	if phase.Cost > 0 {
		line += " cost=" + workflowPanelCost(phase.Cost)
	}
	if phase.DurationMs > 0 {
		line += fmt.Sprintf(" durationMs=%d", phase.DurationMs)
	}
	return strings.TrimSpace(line)
}

func workflowPanelAgentLine(agent workflowPanelAgent, selected bool) string {
	marker := "  "
	style := uiDimText
	if selected {
		marker = uiPrimaryText.Render("> ")
		style = uiPrimaryText
	}
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	line := fmt.Sprintf("%s#%d [%s] %s", marker, agent.ID, agent.Status, label)
	if agent.TurnTokens > 0 {
		line += fmt.Sprintf(" tokens=%d", agent.TurnTokens)
	} else if agent.EstimatedTokens > 0 {
		line += fmt.Sprintf(" estimated=%d", agent.EstimatedTokens)
	}
	if agent.Cost > 0 {
		line += " cost=" + workflowPanelCost(agent.Cost)
	}
	if agent.FailedToolCalls > 0 {
		line += fmt.Sprintf(" failedTools=%d", agent.FailedToolCalls)
	}
	if agent.RecentToolCalls > 0 {
		line += fmt.Sprintf(" recentTools=%d", agent.RecentToolCalls)
	}
	return style.Render(line)
}

func workflowPanelAgentsHint() string {
	return "  up/down or j/k select  enter/right detail  p pause/resume  x stop  r restart  esc/left phases  q close"
}

func workflowPanelAgentDetailContent(run workflowPanelRun, phase workflowPanelPhase, agent workflowPanelAgent, scroll int) string {
	lines := workflowPanelAgentDetailLines(agent)
	total := len(lines)
	if total == 0 {
		lines = []string{"no agent detail available"}
		total = 1
	}
	scroll = clampInt(scroll, 0, max(0, total-modelSelectVisibleRows))
	end := min(total, scroll+modelSelectVisibleRows)
	mode := strings.TrimSpace(run.ShortID() + " " + run.Status)
	out := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Workflow agent",
			Selected: scroll,
			Visible:  end - scroll,
			Total:    total,
			Mode:     mode,
		})),
		uiDimText.Render("  " + workflowPanelPhaseSummary(phase)),
	}
	for i := scroll; i < end; i++ {
		line := "  " + lines[i]
		if i == scroll && scroll > 0 {
			line += "  ^"
		} else if i == end-1 && end < total {
			line += "  v"
		}
		out = append(out, uiDimText.Render(line))
	}
	out = append(out, uiDimText.Render(workflowPanelAgentDetailHint()))
	return strings.Join(out, "\n")
}

func workflowPanelAgentDetailLines(agent workflowPanelAgent) []string {
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	lines := []string{
		fmt.Sprintf("#%d [%s] %s", agent.ID, agent.Status, label),
	}
	if agent.Phase != "" {
		lines = append(lines, "phase: "+agent.Phase)
	}
	if agent.TurnTokens > 0 {
		lines = append(lines, fmt.Sprintf("tokens: %d", agent.TurnTokens))
	} else if agent.EstimatedTokens > 0 {
		lines = append(lines, fmt.Sprintf("estimated tokens: %d", agent.EstimatedTokens))
	}
	if agent.Cost > 0 {
		lines = append(lines, "cost: "+workflowPanelCost(agent.Cost))
	}
	if agent.FailedToolCalls > 0 || agent.RecentToolCalls > 0 {
		lines = append(lines, fmt.Sprintf("tool calls: %d recent, %d failed", agent.RecentToolCalls, agent.FailedToolCalls))
	}
	if agent.Prompt != "" {
		lines = append(lines, "prompt:")
		lines = append(lines, workflowPanelMultilineLines(agent.Prompt)...)
	} else if agent.PromptPreview != "" {
		lines = append(lines, "prompt: "+truncateRunes(agent.PromptPreview, 140))
	}
	if agent.Error != "" {
		lines = append(lines, "error: "+truncateRunes(agent.Error, 140))
	} else if agent.ResultPreview != "" {
		lines = append(lines, "result: "+truncateRunes(agent.ResultPreview, 140))
	}
	if len(agent.ToolCalls) > 0 {
		lines = append(lines, "recent tools:")
		for _, call := range agent.ToolCalls {
			lines = append(lines, "  "+workflowPanelToolCallSummary([]workflowPanelToolCall{call}))
		}
	}
	return lines
}

func workflowPanelMultilineLines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			out = append(out, "  ")
			continue
		}
		out = append(out, "  "+line)
	}
	return out
}

func workflowPanelAgentDetailHint() string {
	return "  up/down or j/k scroll  p pause/resume  x stop  r restart  esc/left agents  q close"
}

func workflowPanelSaveContent(run workflowPanelRun, name, scope string) string {
	if scope != "user" {
		scope = "project"
	}
	displayName := name
	if displayName == "" {
		displayName = "type workflow name"
	}
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title: "Save workflow",
			Mode:  run.ShortID(),
		})),
		uiDimText.Render("  " + workflowPanelRunSummary(run)),
		uiDimText.Render("  name: " + displayName),
		uiDimText.Render("  scope: " + scope),
		uiDimText.Render(workflowPanelSaveHint()),
	}
	return strings.Join(lines, "\n")
}

func workflowPanelSaveHint() string {
	return "  type name  tab scope  enter save  esc cancel"
}

func workflowPanelRunFromMap(item map[string]any) workflowPanelRun {
	id, _ := item["id"].(string)
	name, _ := item["name"].(string)
	status, _ := item["status"].(string)
	phase, _ := item["currentPhase"].(string)
	return workflowPanelRun{
		ID:           strings.TrimSpace(id),
		Name:         strings.TrimSpace(name),
		Status:       strings.TrimSpace(status),
		AgentCount:   runtimeStateNumber(item["agentCount"]),
		DoneCount:    runtimeStateNumber(item["doneCount"]),
		ErrorCount:   runtimeStateNumber(item["errorCount"]),
		CurrentPhase: strings.TrimSpace(phase),
		UpdatedAt:    int64(runtimeStateNumber(item["updatedAt"])),
		Phases:       workflowPanelPhases(item["phases"]),
		Agents:       workflowPanelAgents(item["agents"]),
	}
}

type workflowPanelPhase struct {
	Title           string
	AgentCount      int
	DoneCount       int
	RunningCount    int
	ErrorCount      int
	EstimatedTokens int
	Cost            float64
	DurationMs      int
}

func workflowPanelPhases(value any) []workflowPanelPhase {
	var out []workflowPanelPhase
	switch phases := value.(type) {
	case []map[string]any:
		for _, item := range phases {
			out = append(out, workflowPanelPhaseFromMap(item))
		}
	case []any:
		for _, raw := range phases {
			if item, ok := raw.(map[string]any); ok {
				out = append(out, workflowPanelPhaseFromMap(item))
			}
		}
	}
	return out
}

func workflowPanelPhaseFromMap(item map[string]any) workflowPanelPhase {
	title, _ := item["title"].(string)
	return workflowPanelPhase{
		Title:           strings.TrimSpace(title),
		AgentCount:      runtimeStateNumber(item["agentCount"]),
		DoneCount:       runtimeStateNumber(item["doneCount"]),
		RunningCount:    runtimeStateNumber(item["runningCount"]),
		ErrorCount:      runtimeStateNumber(item["errorCount"]),
		EstimatedTokens: runtimeStateNumber(item["estimatedTokens"]),
		Cost:            runtimeStateFloat(item["cost"]),
		DurationMs:      runtimeStateNumber(item["durationMs"]),
	}
}

type workflowPanelAgent struct {
	ID              int
	Label           string
	Phase           string
	Status          string
	EstimatedTokens int
	TurnTokens      int
	Cost            float64
	FailedToolCalls int
	RecentToolCalls int
	Prompt          string
	PromptPreview   string
	ResultPreview   string
	Error           string
	ToolCalls       []workflowPanelToolCall
}

func workflowPanelAgents(value any) []workflowPanelAgent {
	var out []workflowPanelAgent
	switch agents := value.(type) {
	case []map[string]any:
		for _, item := range agents {
			out = append(out, workflowPanelAgentFromMap(item))
		}
	case []any:
		for _, raw := range agents {
			if item, ok := raw.(map[string]any); ok {
				out = append(out, workflowPanelAgentFromMap(item))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func workflowPanelAgentFromMap(item map[string]any) workflowPanelAgent {
	label, _ := item["label"].(string)
	phase, _ := item["phase"].(string)
	status, _ := item["status"].(string)
	return workflowPanelAgent{
		ID:              runtimeStateNumber(item["id"]),
		Label:           strings.TrimSpace(label),
		Phase:           strings.TrimSpace(phase),
		Status:          strings.TrimSpace(status),
		EstimatedTokens: runtimeStateNumber(item["estimatedTokens"]),
		TurnTokens:      runtimeStateNumber(item["turnTokens"]),
		Cost:            runtimeStateFloat(item["cost"]),
		FailedToolCalls: runtimeStateNumber(item["failedToolCalls"]),
		RecentToolCalls: runtimeStateNumber(item["recentToolCalls"]),
		Prompt:          strings.TrimSpace(stringField(item, "prompt")),
		PromptPreview:   strings.TrimSpace(stringField(item, "promptPreview")),
		ResultPreview:   strings.TrimSpace(stringField(item, "resultPreview")),
		Error:           strings.TrimSpace(stringField(item, "error")),
		ToolCalls:       workflowPanelToolCalls(item["recentToolCallPreviews"]),
	}
}

type workflowPanelToolCall struct {
	ToolName      string
	IsError       bool
	ArgsPreview   string
	ResultPreview string
}

func workflowPanelToolCalls(value any) []workflowPanelToolCall {
	var out []workflowPanelToolCall
	switch calls := value.(type) {
	case []map[string]any:
		for _, item := range calls {
			out = append(out, workflowPanelToolCallFromMap(item))
		}
	case []any:
		for _, raw := range calls {
			if item, ok := raw.(map[string]any); ok {
				out = append(out, workflowPanelToolCallFromMap(item))
			}
		}
	}
	return out
}

func workflowPanelToolCallFromMap(item map[string]any) workflowPanelToolCall {
	return workflowPanelToolCall{
		ToolName:      strings.TrimSpace(stringField(item, "toolName")),
		IsError:       boolField(item, "isError"),
		ArgsPreview:   strings.TrimSpace(stringField(item, "argsPreview")),
		ResultPreview: strings.TrimSpace(stringField(item, "resultPreview")),
	}
}

func workflowPanelToolCallSummary(calls []workflowPanelToolCall) string {
	parts := make([]string, 0, len(calls))
	for i, call := range calls {
		if i >= 3 {
			parts = append(parts, fmt.Sprintf("+%d more", len(calls)-i))
			break
		}
		name := call.ToolName
		if name == "" {
			name = "tool"
		}
		status := "ok"
		if call.IsError {
			status = "error"
		}
		part := fmt.Sprintf("%s[%s]", name, status)
		if call.ArgsPreview != "" {
			part += " args=" + truncateRunes(call.ArgsPreview, 60)
		}
		if call.ResultPreview != "" {
			part += " result=" + truncateRunes(call.ResultPreview, 60)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func workflowPanelCost(cost float64) string {
	return fmt.Sprintf("%.6f", cost)
}

func stringField(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return value
}

func boolField(item map[string]any, key string) bool {
	value, _ := item[key].(bool)
	return value
}
