package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

func moduTUIWorkflowArgs(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "/workflows" {
		return "", true
	}
	if rest, ok := strings.CutPrefix(line, "/workflows "); ok {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func moduTUIWorkflowPanelFromSlash(session *coding_agent.CodingSession, line string) (modutui.Panel, bool) {
	if session == nil {
		return moduTUIWorkflowPanelFromSlashStates(nil, line)
	}
	return moduTUIWorkflowPanelFromSlashStates(session.ExtensionRuntimeStates(), line)
}

func moduTUIWorkflowPanelFromNotify(session *coding_agent.CodingSession, ev coding_agent.SessionEvent) (modutui.Panel, string, bool) {
	if session == nil {
		return moduTUIWorkflowPanelFromNotifyStates(nil, ev)
	}
	return moduTUIWorkflowPanelFromNotifyStates(session.ExtensionRuntimeStates(), ev)
}

func moduTUIWorkflowPanelFromToolEvent(session *coding_agent.CodingSession, ev types.Event) (modutui.Panel, string, bool) {
	if session == nil {
		return moduTUIWorkflowPanelFromToolEventStates(nil, ev)
	}
	return moduTUIWorkflowPanelFromToolEventStates(session.ExtensionRuntimeStates(), ev)
}

func moduTUIWorkflowPanelFromToolEventStates(states map[string]any, ev types.Event) (modutui.Panel, string, bool) {
	if !strings.EqualFold(ev.ToolName, "workflow") {
		return modutui.Panel{}, "", false
	}
	if ev.Type == types.EventTypeToolExecutionStart {
		return moduTUIWorkflowCockpitPanelFromStates(states), "workflow started", true
	}
	if ev.Type == types.EventTypeToolExecutionUpdate {
		if runID := moduTUIWorkflowRunIDFromToolResult(ev.Result); runID != "" {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, runID); ok {
				return moduTUIWorkflowRunFollowPanelFromStates(states, run), "workflow updated: " + run.ID, true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", runID), "workflow updated: " + runID, true
		}
		return modutui.Panel{}, "", false
	}
	if ev.Type != types.EventTypeToolExecutionEnd || ev.IsError {
		return modutui.Panel{}, "", false
	}
	if runID := moduTUIWorkflowRunIDFromToolResult(ev.Result); runID != "" {
		if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, runID); ok {
			return moduTUIWorkflowRunFollowPanelFromStates(states, run), "workflow started: " + run.ID, true
		}
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", runID), "workflow started: " + runID, true
	}
	output := toolOutputFromResult(ev.ToolName, ev.IsError, ev.Result)
	if strings.Contains(output, " completed with ") {
		if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, "latest"); ok {
			return moduTUIWorkflowFeedPanelFromStates(states, run.ID), moduTUIWorkflowNotifyStatus(output), true
		}
		return modutui.Panel{}, moduTUIWorkflowNotifyStatus(output), true
	}
	return modutui.Panel{}, "", false
}

func moduTUIWorkflowRunIDFromToolResult(result any) string {
	switch r := result.(type) {
	case types.ToolResult:
		return moduTUIWorkflowRunIDFromToolDetails(r.Details)
	case *types.ToolResult:
		if r == nil {
			return ""
		}
		return moduTUIWorkflowRunIDFromToolDetails(r.Details)
	default:
		return ""
	}
}

func moduTUIWorkflowRunIDFromToolDetails(details any) string {
	m, ok := moduTUIWorkflowToolDetailsMap(details)
	if !ok {
		return ""
	}
	for _, key := range []string{"runID", "runId", "id"} {
		if runID := strings.TrimSpace(moduTUIRuntimeStateString(m[key])); runID != "" {
			return runID
		}
	}
	for _, key := range []string{"runDir", "snapshotPath", "scriptPath"} {
		if runID := moduTUIWorkflowRunIDFromArtifactPath(moduTUIRuntimeStateString(m[key])); runID != "" {
			return runID
		}
	}
	return ""
}

func moduTUIWorkflowToolDetailsMap(details any) (map[string]any, bool) {
	switch d := details.(type) {
	case map[string]any:
		return d, true
	case map[string]string:
		m := make(map[string]any, len(d))
		for key, value := range d {
			m[key] = value
		}
		return m, true
	case nil:
		return nil, false
	default:
		data, err := json.Marshal(d)
		if err != nil || len(data) == 0 || string(data) == "null" {
			return nil, false
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil || len(m) == 0 {
			return nil, false
		}
		return m, true
	}
}

func moduTUIWorkflowRunIDFromArtifactPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	base := filepath.Base(cleaned)
	switch base {
	case ".", string(filepath.Separator):
		return ""
	case "script.js", "snapshot.json", "status.json":
		base = filepath.Base(filepath.Dir(cleaned))
	}
	return strings.TrimSpace(base)
}

func moduTUIWorkflowPanelFromNotifyStates(states map[string]any, ev coding_agent.SessionEvent) (modutui.Panel, string, bool) {
	if ev.Type != coding_agent.SessionEventExtensionNotify || ev.ExtensionName != "workflow" {
		return modutui.Panel{}, "", false
	}
	text := strings.TrimSpace(ev.Message)
	if text == "" {
		return modutui.Panel{}, "", false
	}
	if runID := moduTUIWorkflowRunIDFromNotify(text); runID != "" {
		if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, runID); ok {
			return moduTUIWorkflowRunFollowPanelFromStates(states, run), moduTUIWorkflowNotifyStatus(text), true
		}
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", runID), moduTUIWorkflowNotifyStatus(text), true
	}
	if strings.Contains(text, " completed with ") {
		if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, "latest"); ok {
			return moduTUIWorkflowFeedPanelFromStates(states, run.ID), moduTUIWorkflowNotifyStatus(text), true
		}
		return modutui.Panel{}, moduTUIWorkflowNotifyStatus(text), true
	}
	if strings.HasPrefix(text, "Stop requested for workflow") ||
		strings.HasPrefix(text, "Pause requested for workflow") ||
		strings.HasPrefix(text, "Restart requested for workflow agent") ||
		strings.HasPrefix(text, "Stop requested for workflow agent") ||
		strings.Contains(text, " status persistence failed") ||
		strings.Contains(text, " stopped:") {
		return modutui.Panel{}, moduTUIWorkflowNotifyStatus(text), true
	}
	return modutui.Panel{}, "", false
}

func moduTUIWorkflowRunIDFromNotify(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"Run: ", "New run: "} {
			if runID, ok := strings.CutPrefix(line, prefix); ok {
				return strings.Fields(runID)[0]
			}
		}
	}
	return ""
}

func moduTUIWorkflowRunFollowPanelFromStates(states map[string]any, run moduTUIWorkflowRun) modutui.Panel {
	return moduTUIWorkflowFeedPanelFromStates(states, run.ID)
}

func moduTUIWorkflowNotifyStatus(text string) string {
	line := strings.TrimSpace(text)
	if first, _, ok := strings.Cut(line, "\n"); ok {
		line = strings.TrimSpace(first)
	}
	line = strings.TrimPrefix(line, "Workflow ")
	if line == "" {
		return "workflow updated"
	}
	return moduTUITruncate("workflow "+line, 96)
}

func moduTUIWorkflowPanelFromSlashStates(states map[string]any, line string) (modutui.Panel, bool) {
	args, ok := moduTUIWorkflowArgs(line)
	if !ok {
		return modutui.Panel{}, false
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return moduTUIWorkflowCockpitPanelFromStates(states), true
	}
	switch fields[0] {
	case "list":
		if len(fields) == 1 {
			return moduTUIWorkflowCockpitPanelFromStates(states), true
		}
	case "show":
		if len(fields) == 2 {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1]); ok {
				return moduTUIWorkflowRunDetailPanelFromStates(states, run.ID), true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowRunDetailPanelID, "Workflow Run", fields[1]), true
		}
	case "feed":
		if len(fields) == 2 {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1]); ok {
				return moduTUIWorkflowFeedPanelFromStates(states, run.ID), true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", fields[1]), true
		}
	case "guide":
		if len(fields) == 2 {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1]); ok {
				return moduTUIWorkflowGuidePanelFromStates(states, run.ID), true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowGuidePanelID, "Workflow Guide", fields[1]), true
		}
	case "map":
		if len(fields) == 2 {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1]); ok {
				return moduTUIWorkflowMapPanelFromStates(states, run.ID), true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowMapPanelID, "Workflow Map", fields[1]), true
		}
	case "agent":
		if len(fields) == 3 {
			run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1])
			if !ok {
				return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowAgentPanelID, "Workflow Agent", fields[1]), true
			}
			agentID, err := strconv.Atoi(fields[2])
			if err != nil || agentID <= 0 {
				return moduTUIWorkflowInvalidAgentPanel(run.ID, fields[2]), true
			}
			return moduTUIWorkflowAgentPanelFromStates(states, run.ID, agentID), true
		}
	case "transcript":
		if len(fields) == 3 {
			run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1])
			if !ok {
				return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowTranscriptPanelID, "Workflow Transcript", fields[1]), true
			}
			agentID, err := strconv.Atoi(fields[2])
			if err != nil || agentID <= 0 {
				return moduTUIWorkflowInvalidAgentPanel(run.ID, fields[2]), true
			}
			return moduTUIWorkflowTranscriptPanelFromStates(states, run.ID, agentID), true
		}
	}
	return modutui.Panel{}, false
}

const (
	moduTUIWorkflowCockpitPanelID          = "workflow-cockpit"
	moduTUIWorkflowRunDetailPanelID        = "workflow-run-detail"
	moduTUIWorkflowFeedPanelID             = "workflow-feed"
	moduTUIWorkflowGuidePanelID            = "workflow-guide"
	moduTUIWorkflowMapPanelID              = "workflow-map"
	moduTUIWorkflowPhasePanelID            = "workflow-phase"
	moduTUIWorkflowResultPanelID           = "workflow-result"
	moduTUIWorkflowScriptPanelID           = "workflow-script"
	moduTUIWorkflowAgentsPanelID           = "workflow-agents"
	moduTUIWorkflowAgentPanelID            = "workflow-agent"
	moduTUIWorkflowTranscriptPanelID       = "workflow-transcript"
	moduTUIWorkflowPanelBackCommand        = "workflow-panel:back"
	moduTUIWorkflowPanelDetailPrefix       = "workflow-panel:detail:"
	moduTUIWorkflowPanelFeedPrefix         = "workflow-panel:feed:"
	moduTUIWorkflowPanelGuidePrefix        = "workflow-panel:guide:"
	moduTUIWorkflowPanelMapPrefix          = "workflow-panel:map:"
	moduTUIWorkflowPanelResultPrefix       = "workflow-panel:result:"
	moduTUIWorkflowPanelScriptPrefix       = "workflow-panel:script:"
	moduTUIWorkflowPanelAgentsPrefix       = "workflow-panel:agents:"
	moduTUIWorkflowPanelAgentPrefix        = "workflow-panel:agent:"
	moduTUIWorkflowPanelPhasePrefix        = "workflow-panel:phase:"
	moduTUIWorkflowPanelTranscriptPrefix   = "workflow-panel:transcript:"
	moduTUIWorkflowPanelControlPrefix      = "workflow-panel:control:"
	moduTUIWorkflowPanelAgentControlPrefix = "workflow-panel:agent-control:"
	moduTUIWorkflowArtifactLineLimit       = 200
	moduTUIWorkflowNavigateActionID        = "workflow.navigate"
	moduTUIWorkflowControlActionID         = "workflow.control"
	moduTUIWorkflowAgentControlActionID    = "workflow.agent.control"
)

type moduTUIWorkflowActionPayload struct {
	View    string
	Verb    string
	RunID   string
	Phase   string
	AgentID int
}

func moduTUIWorkflowActionCommand(action modutui.PanelAction) string {
	payload, ok := action.Action.Payload.(moduTUIWorkflowActionPayload)
	if ok {
		runID := strings.TrimSpace(payload.RunID)
		switch strings.TrimSpace(action.Action.ID) {
		case moduTUIWorkflowControlActionID:
			if verb := strings.TrimSpace(payload.Verb); verb != "" && runID != "" {
				return moduTUIWorkflowPanelControlPrefix + verb + ":" + runID
			}
		case moduTUIWorkflowAgentControlActionID:
			if verb := strings.TrimSpace(payload.Verb); verb != "" && runID != "" && payload.AgentID > 0 {
				return fmt.Sprintf("%s%s:%s:%d", moduTUIWorkflowPanelAgentControlPrefix, verb, runID, payload.AgentID)
			}
		case moduTUIWorkflowNavigateActionID:
			switch strings.TrimSpace(payload.View) {
			case "back":
				return moduTUIWorkflowPanelBackCommand
			case "detail":
				return moduTUIWorkflowPanelDetailPrefix + runID
			case "feed":
				return moduTUIWorkflowPanelFeedPrefix + runID
			case "guide":
				return moduTUIWorkflowPanelGuidePrefix + runID
			case "map":
				return moduTUIWorkflowPanelMapPrefix + runID
			case "result":
				return moduTUIWorkflowPanelResultPrefix + runID
			case "script":
				return moduTUIWorkflowPanelScriptPrefix + runID
			case "agents":
				return moduTUIWorkflowPanelAgentsPrefix + runID
			case "agent":
				if payload.AgentID > 0 {
					return fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, runID, payload.AgentID)
				}
			case "phase":
				if phase := strings.TrimSpace(payload.Phase); phase != "" {
					return moduTUIWorkflowPanelPhasePrefix + runID + ":" + phase
				}
			case "transcript":
				if payload.AgentID > 0 {
					return fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelTranscriptPrefix, runID, payload.AgentID)
				}
			}
		}
	}
	return strings.TrimSpace(action.Command)
}

func moduTUIWorkflowNavigationAction(view, runID string) modutui.Action {
	return modutui.Action{
		ID: moduTUIWorkflowNavigateActionID,
		Payload: moduTUIWorkflowActionPayload{
			View:  strings.TrimSpace(view),
			RunID: strings.TrimSpace(runID),
		},
	}
}

func moduTUIWorkflowControlActionValue(verb, runID string) modutui.Action {
	return modutui.Action{
		ID: moduTUIWorkflowControlActionID,
		Payload: moduTUIWorkflowActionPayload{
			Verb:  strings.TrimSpace(verb),
			RunID: strings.TrimSpace(runID),
		},
	}
}

type moduTUIWorkflowPanelRef struct {
	PanelID string
	RunID   string
	Phase   string
	AgentID int
}

func (ref moduTUIWorkflowPanelRef) MatchesRun(runID string) bool {
	runID = strings.TrimSpace(runID)
	if strings.TrimSpace(ref.PanelID) == moduTUIWorkflowCockpitPanelID {
		return true
	}
	return runID != "" && strings.TrimSpace(ref.RunID) == runID
}

func (ref moduTUIWorkflowPanelRef) Panel(session *coding_agent.CodingSession) (modutui.Panel, bool) {
	switch ref.PanelID {
	case moduTUIWorkflowCockpitPanelID:
		return moduTUIWorkflowCockpitPanel(session), true
	case moduTUIWorkflowRunDetailPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowRunDetailPanel(session, ref.RunID), true
	case moduTUIWorkflowFeedPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowFeedPanel(session, ref.RunID), true
	case moduTUIWorkflowGuidePanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowGuidePanel(session, ref.RunID), true
	case moduTUIWorkflowMapPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowMapPanel(session, ref.RunID), true
	case moduTUIWorkflowAgentsPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowAgentsPanel(session, ref.RunID), true
	case moduTUIWorkflowPhasePanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowPhasePanel(session, ref.RunID, ref.Phase), true
	case moduTUIWorkflowAgentPanelID:
		if ref.RunID == "" || ref.AgentID <= 0 {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowAgentPanel(session, ref.RunID, ref.AgentID), true
	case moduTUIWorkflowTranscriptPanelID:
		if ref.RunID == "" || ref.AgentID <= 0 {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowTranscriptPanel(session, ref.RunID, ref.AgentID), true
	case moduTUIWorkflowResultPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowResultPanel(session, ref.RunID), true
	case moduTUIWorkflowScriptPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowScriptPanel(session, ref.RunID), true
	default:
		return modutui.Panel{}, false
	}
}

// moduTUIWorkflowPanelRefFromPanel recovers which run/phase/agent a panel is
// showing from the Meta every workflow panel builder attaches to itself. Panel
// content (Rows/Lines) is rendered UI, not a reliable source of truth for
// this: a layout change that drops or reorders a row would silently break
// live-refresh tracking. Meta round-trips through standard panel updates
// unchanged, so this always reflects exactly what the builder constructed.
func moduTUIWorkflowPanelRefFromPanel(panel modutui.Panel) (moduTUIWorkflowPanelRef, bool) {
	ref, ok := panel.Meta.(moduTUIWorkflowPanelRef)
	if !ok || ref.PanelID == "" {
		return moduTUIWorkflowPanelRef{}, false
	}
	return ref, true
}

func moduTUIWorkflowRunIDFromPanel(panel modutui.Panel) string {
	ref, ok := moduTUIWorkflowPanelRefFromPanel(panel)
	if !ok {
		return ""
	}
	return strings.TrimSpace(ref.RunID)
}

func moduTUIWorkflowRuntimeFingerprint(session *coding_agent.CodingSession) string {
	if session == nil {
		return ""
	}
	snapshot, ok := decodeModuTUIWorkflowSnapshot(session.ExtensionRuntimeStates())
	if !ok {
		return ""
	}
	data, err := json.Marshal(snapshot.Raw)
	if err != nil {
		return fmt.Sprint(snapshot.Raw)
	}
	return string(data)
}

func moduTUIWorkflowControlAction(action modutui.PanelAction) (command, runID, status string, ok bool) {
	rest, ok := strings.CutPrefix(moduTUIWorkflowActionCommand(action), moduTUIWorkflowPanelControlPrefix)
	if !ok {
		return "", "", "", false
	}
	verb, runID, ok := strings.Cut(rest, ":")
	if !ok {
		return "", "", "", false
	}
	verb = strings.TrimSpace(verb)
	runID = strings.TrimSpace(runID)
	if verb == "" || runID == "" {
		return "", "", "", false
	}
	switch verb {
	case "pause", "stop", "resume", "restart":
		return "/workflows " + verb + " " + runID, runID, "workflow " + verb + " requested", true
	default:
		return "", "", "", false
	}
}

func moduTUIWorkflowAgentControlAction(action modutui.PanelAction) (command, runID string, agentID int, status string, ok bool) {
	rest, ok := strings.CutPrefix(moduTUIWorkflowActionCommand(action), moduTUIWorkflowPanelAgentControlPrefix)
	if !ok {
		return "", "", 0, "", false
	}
	verb, tail, ok := strings.Cut(rest, ":")
	if !ok {
		return "", "", 0, "", false
	}
	runID, agentIDText, ok := strings.Cut(tail, ":")
	if !ok {
		return "", "", 0, "", false
	}
	verb = strings.TrimSpace(verb)
	runID = strings.TrimSpace(runID)
	agentID, err := strconv.Atoi(strings.TrimSpace(agentIDText))
	if verb == "" || runID == "" || err != nil || agentID <= 0 {
		return "", "", 0, "", false
	}
	switch verb {
	case "stop":
		return "/workflows agent-stop " + runID + " " + strconv.Itoa(agentID), runID, agentID, "workflow agent stop requested", true
	case "restart":
		return "/workflows agent-restart " + runID + " " + strconv.Itoa(agentID), runID, agentID, "workflow agent restart requested", true
	default:
		return "", "", 0, "", false
	}
}

func moduTUIWorkflowPanelAction(session *coding_agent.CodingSession, action modutui.PanelAction) (modutui.Panel, bool) {
	switch action.PanelID {
	case moduTUIWorkflowCockpitPanelID:
		command := moduTUIWorkflowActionCommand(action)
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelResultPrefix); ok {
			return moduTUIWorkflowResultPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelScriptPrefix); ok {
			return moduTUIWorkflowScriptPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		runID := strings.TrimSpace(action.Row.Value)
		if runID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowRunDetailPanel(session, runID), true
	case moduTUIWorkflowRunDetailPanelID:
		if moduTUIWorkflowActionCommand(action) == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
		command := moduTUIWorkflowActionCommand(action)
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelResultPrefix); ok {
			return moduTUIWorkflowResultPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelScriptPrefix); ok {
			return moduTUIWorkflowScriptPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix); ok {
			runID, phase, hasPhase := strings.Cut(rest, ":")
			if hasPhase {
				return moduTUIWorkflowPhasePanel(session, runID, phase), true
			}
		}
	case moduTUIWorkflowAgentsPanelID:
		command := moduTUIWorkflowActionCommand(action)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if command == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
	case moduTUIWorkflowPhasePanelID:
		command := moduTUIWorkflowActionCommand(action)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if command == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
	case moduTUIWorkflowFeedPanelID:
		command := moduTUIWorkflowActionCommand(action)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix); ok {
			runID, phase, hasPhase := strings.Cut(rest, ":")
			if hasPhase {
				return moduTUIWorkflowPhasePanel(session, runID, phase), true
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelResultPrefix); ok {
			return moduTUIWorkflowResultPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelScriptPrefix); ok {
			return moduTUIWorkflowScriptPanel(session, runID), true
		}
		if command == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
	case moduTUIWorkflowResultPanelID, moduTUIWorkflowScriptPanelID, moduTUIWorkflowAgentPanelID, moduTUIWorkflowMapPanelID, moduTUIWorkflowGuidePanelID:
		command := moduTUIWorkflowActionCommand(action)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix); ok {
			runID, phase, hasPhase := strings.Cut(rest, ":")
			if hasPhase {
				return moduTUIWorkflowPhasePanel(session, runID, phase), true
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelTranscriptPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowTranscriptPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelResultPrefix); ok {
			return moduTUIWorkflowResultPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelScriptPrefix); ok {
			return moduTUIWorkflowScriptPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if command == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
	case moduTUIWorkflowTranscriptPanelID:
		command := moduTUIWorkflowActionCommand(action)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix); ok {
			runID, phase, hasPhase := strings.Cut(rest, ":")
			if hasPhase {
				return moduTUIWorkflowPhasePanel(session, runID, phase), true
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
	}
	return modutui.Panel{}, false
}
