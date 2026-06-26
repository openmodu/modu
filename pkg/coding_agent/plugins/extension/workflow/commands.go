package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const workflowsUsage = "Usage: /workflows [list|show <run-id|latest>|agent <run-id|latest> <agent-id>|transcript <run-id|latest> <agent-id>|agent-stop <run-id|latest> <agent-id>|agent-restart <run-id|latest> <agent-id>|pause <run-id>|stop <run-id>|resume <run-id|latest>|restart <run-id|latest>|save <run-id|latest> <name> [project|user]]"

type workflowRunSummary struct {
	ID           string
	RunDir       string
	ScriptPath   string
	SnapshotPath string
	Status       workflowRunStatus
	Snapshot     *workflowSnapshot
	Error        string
	UpdatedAt    time.Time
}

func (e *Extension) cmdWorkflows(args string) error {
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "list" {
		return e.cmdWorkflowsList()
	}
	if fields[0] == "show" {
		if len(fields) != 2 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsShow(fields[1])
	}
	if fields[0] == "agent" {
		if len(fields) != 3 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsAgent(fields[1], fields[2])
	}
	if fields[0] == "transcript" {
		if len(fields) != 3 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsTranscript(fields[1], fields[2])
	}
	if fields[0] == "agent-stop" {
		if len(fields) != 3 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsAgentControl(fields[1], fields[2], workflowAgentActionStop)
	}
	if fields[0] == "agent-restart" {
		if len(fields) != 3 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsAgentControl(fields[1], fields[2], workflowAgentActionRestart)
	}
	if fields[0] == "stop" {
		if len(fields) != 2 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsStop(fields[1], "stop")
	}
	if fields[0] == "pause" {
		if len(fields) != 2 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsStop(fields[1], "pause")
	}
	if fields[0] == "resume" {
		if len(fields) != 2 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsResume(fields[1])
	}
	if fields[0] == "restart" {
		if len(fields) != 2 {
			e.tell(workflowsUsage)
			return nil
		}
		return e.cmdWorkflowsRestart(fields[1])
	}
	if fields[0] == "save" {
		if len(fields) < 3 || len(fields) > 4 {
			e.tell(workflowsUsage)
			return nil
		}
		scope := "project"
		if len(fields) == 4 {
			scope = fields[3]
		}
		return e.cmdWorkflowsSave(fields[1], fields[2], scope)
	}
	e.tell(workflowsUsage)
	return nil
}

func (e *Extension) cmdWorkflowsList() error {
	runs, dir, err := e.workflowRuns()
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		e.tell("No workflow runs found in " + dir)
		return nil
	}
	var b strings.Builder
	b.WriteString("Workflow runs:\n")
	for i, run := range runs {
		if i >= 20 {
			fmt.Fprintf(&b, "- ... %d more\n", len(runs)-i)
			break
		}
		status := run.Status
		if status == "" {
			status = workflowStatusCompleted
		}
		fmt.Fprintf(&b, "- %s  %s  %s", run.ID, status, run.UpdatedAt.Format(time.RFC3339))
		if run.Snapshot != nil {
			fmt.Fprintf(&b, "  %s (%d agent(s), %d error(s))",
				run.Snapshot.Name, run.Snapshot.AgentCount, run.Snapshot.ErrorCount)
		} else if run.Error != "" {
			fmt.Fprintf(&b, "  %s", run.Error)
		}
		fmt.Fprintf(&b, "\n  %s\n", run.ScriptPath)
	}
	b.WriteString("\nUse /workflows show <run-id|latest> to inspect script and metadata, /workflows agent <run-id|latest> <agent-id> to inspect one agent, /workflows agent-stop <run-id|latest> <agent-id> to stop one running agent, /workflows agent-restart <run-id|latest> <agent-id> to restart one running agent, /workflows pause <run-id> to pause a running workflow, /workflows stop <run-id> to stop a running workflow, /workflows resume <run-id|latest> to resume a stopped run in this session, /workflows restart <run-id|latest> to relaunch a script, or /workflows save <run-id|latest> <name> [project|user] to save it for reuse.")
	e.tell(b.String())
	return nil
}

func (e *Extension) cmdWorkflowsShow(selector string) error {
	runs, _, err := e.workflowRuns()
	if err != nil {
		return err
	}
	run, ok, err := selectWorkflowRun(runs, selector)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if !ok {
		e.tell("Workflow run not found: " + selector)
		return nil
	}
	var b strings.Builder
	status := run.Status
	if status == "" {
		status = workflowStatusCompleted
	}
	fmt.Fprintf(&b, "Workflow run %s\nStatus: %s\nScript: %s\nUpdated: %s\n", run.ID, status, run.ScriptPath, run.UpdatedAt.Format(time.RFC3339))
	if run.Snapshot != nil {
		fmt.Fprintf(&b, "Workflow: %s\nAgents: %d done, %d running, %d errors\nDurationMs: %d\n",
			run.Snapshot.Name, run.Snapshot.DoneCount, run.Snapshot.RunningCount, run.Snapshot.ErrorCount, run.Snapshot.DurationMs)
		if run.Snapshot.Cost > 0 {
			fmt.Fprintf(&b, "Cost: %s\n", formatWorkflowCost(run.Snapshot.Cost))
		}
		if len(run.Snapshot.PhaseSummaries) > 0 {
			b.WriteString("Phases:\n")
			for _, phase := range run.Snapshot.PhaseSummaries {
				title := phase.Title
				if strings.TrimSpace(title) == "" {
					title = "(no phase)"
				}
				cost := ""
				if phase.Cost > 0 {
					cost = ", cost=" + formatWorkflowCost(phase.Cost)
				}
				fmt.Fprintf(&b, "- %s: %d agent(s), %d done, %d running, %d errors, estimatedTokens=%d%s, durationMs=%d\n",
					title, phase.AgentCount, phase.DoneCount, phase.RunningCount, phase.ErrorCount, phase.EstimatedTokens, cost, phase.DurationMs)
			}
		}
		for _, agent := range run.Snapshot.Agents {
			cached := ""
			if agent.Cached {
				cached = " cached"
			}
			cost := ""
			if agent.Cost > 0 {
				cost = " cost=" + formatWorkflowCost(agent.Cost)
			}
			fmt.Fprintf(&b, "- Agent %d [%s%s] %s estimatedTokens=%d%s durationMs=%d\n", agent.ID, agent.Status, cached, agent.Label, agent.EstimatedTokens, cost, agent.DurationMs)
		}
		if run.Snapshot.Result != nil {
			fmt.Fprintf(&b, "Result: %s\n", preview(run.Snapshot.Result, 600))
		}
	}
	if run.Error != "" {
		fmt.Fprintf(&b, "Error: %s\n", run.Error)
	}
	if run.ScriptPath != "" {
		data, err := os.ReadFile(run.ScriptPath)
		if err != nil {
			return fmt.Errorf("read workflow script %s: %w", run.ScriptPath, err)
		}
		script := strings.TrimSpace(string(data))
		if len(script) > 4000 {
			script = script[:4000] + "\n..."
		}
		fmt.Fprintf(&b, "\n```js\n%s\n```", script)
	}
	e.tell(b.String())
	return nil
}

func (e *Extension) cmdWorkflowsAgent(selector, agentIDText string) error {
	runs, _, err := e.workflowRuns()
	if err != nil {
		return err
	}
	run, ok, err := selectWorkflowRun(runs, selector)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if !ok {
		e.tell("Workflow run not found: " + selector)
		return nil
	}
	if run.Snapshot == nil {
		e.tell("Workflow run has no agent metadata: " + run.ID)
		return nil
	}
	agentID, err := parseAgentID(agentIDText)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	for _, agent := range run.Snapshot.Agents {
		if agent.ID == agentID {
			e.tell(formatWorkflowAgentDetail(run, agent))
			return nil
		}
	}
	e.tell(fmt.Sprintf("Workflow agent %d not found in run %s", agentID, run.ID))
	return nil
}

func (e *Extension) cmdWorkflowsTranscript(selector, agentIDText string) error {
	runs, _, err := e.workflowRuns()
	if err != nil {
		return err
	}
	run, ok, err := selectWorkflowRun(runs, selector)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if !ok {
		e.tell("Workflow run not found: " + selector)
		return nil
	}
	if run.Snapshot == nil {
		e.tell("Workflow run has no agent metadata: " + run.ID)
		return nil
	}
	agentID, err := parseAgentID(agentIDText)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	for _, agent := range run.Snapshot.Agents {
		if agent.ID == agentID {
			e.tell(formatWorkflowAgentTranscript(run, agent))
			return nil
		}
	}
	e.tell(fmt.Sprintf("Workflow agent %d not found in run %s", agentID, run.ID))
	return nil
}

func parseAgentID(text string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("workflow agent id must be a positive integer")
	}
	return id, nil
}

func formatWorkflowAgentDetail(run workflowRunSummary, agent agentSnapshot) string {
	var b strings.Builder
	status := run.Status
	if status == "" {
		status = workflowStatusCompleted
	}
	cached := ""
	if agent.Cached {
		cached = " cached"
	}
	fmt.Fprintf(&b, "Workflow agent %d in run %s\nWorkflow: %s\nRun status: %s\nAgent status: %s%s\nLabel: %s\n",
		agent.ID, run.ID, snapshotName(run.Snapshot), status, agent.Status, cached, agent.Label)
	if strings.TrimSpace(agent.Phase) != "" {
		fmt.Fprintf(&b, "Phase: %s\n", agent.Phase)
	}
	fmt.Fprintf(&b, "EstimatedTokens: %d\nTurnTokens: %d\nFailedToolCalls: %d\nDurationMs: %d\n", agent.EstimatedTokens, agent.TurnTokens, agent.FailedToolCalls, agent.DurationMs)
	if agent.Cost > 0 {
		fmt.Fprintf(&b, "Cost: %s\n", formatWorkflowCost(agent.Cost))
	}
	if !agent.StartedAt.IsZero() {
		fmt.Fprintf(&b, "StartedAt: %s\n", agent.StartedAt.Format(time.RFC3339))
	}
	if !agent.EndedAt.IsZero() {
		fmt.Fprintf(&b, "EndedAt: %s\n", agent.EndedAt.Format(time.RFC3339))
	}
	if strings.TrimSpace(agent.Error) != "" {
		fmt.Fprintf(&b, "Error: %s\n", agent.Error)
	}
	if strings.TrimSpace(agent.ResultPreview) != "" {
		fmt.Fprintf(&b, "ResultPreview: %s\n", agent.ResultPreview)
	}
	if len(agent.RecentToolCalls) > 0 {
		b.WriteString("RecentToolCalls:\n")
		for _, call := range agent.RecentToolCalls {
			status := "ok"
			if call.IsError {
				status = "error"
			}
			fmt.Fprintf(&b, "- %s [%s]\n", call.ToolName, status)
			if strings.TrimSpace(call.ArgsPreview) != "" {
				fmt.Fprintf(&b, "  Args: %s\n", call.ArgsPreview)
			}
			if strings.TrimSpace(call.ResultPreview) != "" {
				fmt.Fprintf(&b, "  Result: %s\n", call.ResultPreview)
			}
		}
	}
	if strings.TrimSpace(agent.Prompt) != "" {
		prompt := agent.Prompt
		if len(prompt) > 4000 {
			prompt = prompt[:4000] + "\n..."
		}
		fmt.Fprintf(&b, "\nPrompt:\n```text\n%s\n```", prompt)
	}
	return b.String()
}

func formatWorkflowCost(cost float64) string {
	if cost <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.6f", cost)
}

func formatWorkflowAgentTranscript(run workflowRunSummary, agent agentSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Workflow agent %d transcript in run %s\nWorkflow: %s\nLabel: %s\n",
		agent.ID, run.ID, snapshotName(run.Snapshot), agent.Label)
	if len(agent.Transcript) == 0 {
		b.WriteString("No child transcript captured for this agent.")
		return b.String()
	}
	for i, entry := range agent.Transcript {
		if i > 0 {
			b.WriteString("\n")
		}
		role := strings.ToUpper(strings.TrimSpace(entry.Role))
		if role == "" {
			role = "UNKNOWN"
		}
		fmt.Fprintf(&b, "## %d. %s", i+1, role)
		if entry.ToolName != "" {
			fmt.Fprintf(&b, " %s", entry.ToolName)
		}
		if entry.IsError {
			b.WriteString(" [error]")
		}
		b.WriteString("\n")
		if strings.TrimSpace(entry.Text) != "" {
			b.WriteString(entry.Text)
			b.WriteString("\n")
		}
		if len(entry.ToolCalls) > 0 {
			for _, call := range entry.ToolCalls {
				fmt.Fprintf(&b, "ToolCall: %s", call.Name)
				if call.ID != "" {
					fmt.Fprintf(&b, " (%s)", call.ID)
				}
				b.WriteString("\n")
				if strings.TrimSpace(call.Args) != "" {
					fmt.Fprintf(&b, "Args: %s\n", call.Args)
				}
			}
		}
		if entry.Usage.Input > 0 || entry.Usage.Output > 0 || entry.Usage.TotalTokens > 0 {
			fmt.Fprintf(&b, "Usage: input=%d output=%d total=%d\n", entry.Usage.Input, entry.Usage.Output, entry.Usage.TotalTokens)
		}
	}
	return strings.TrimSpace(b.String())
}

func (e *Extension) cmdWorkflowsAgentControl(selector, agentIDText string, action workflowAgentControlAction) error {
	runs, _, err := e.workflowRuns()
	if err != nil {
		return err
	}
	run, ok, err := selectWorkflowRun(runs, selector)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if !ok {
		e.tell("Workflow run not found: " + selector)
		return nil
	}
	agentID, err := parseAgentID(agentIDText)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if e.registry == nil || !e.registry.requestAgentAction(run.ID, agentID, action) {
		e.tell(fmt.Sprintf("Workflow agent %d is not running in run %s", agentID, run.ID))
		return nil
	}
	switch action {
	case workflowAgentActionRestart:
		e.tell(fmt.Sprintf("Restart requested for workflow agent %d in run %s", agentID, run.ID))
	default:
		e.tell(fmt.Sprintf("Stop requested for workflow agent %d in run %s", agentID, run.ID))
	}
	return nil
}

func snapshotName(snapshot *workflowSnapshot) string {
	if snapshot == nil || strings.TrimSpace(snapshot.Name) == "" {
		return "(unknown)"
	}
	return snapshot.Name
}

func (e *Extension) cmdWorkflowsStop(selector, action string) error {
	runs, _, err := e.workflowRuns()
	if err != nil {
		return err
	}
	run, ok, err := selectWorkflowRun(runs, selector)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if !ok {
		e.tell("Workflow run not found: " + selector)
		return nil
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "pause" {
		action = "stop"
	}
	reason := action + " requested"
	if e.registry == nil || !e.registry.stop(run.ID, reason) {
		e.tell("Workflow run is not running: " + run.ID)
		return nil
	}
	if err := persistWorkflowRunStatus(run.RunDir, workflowStatusStopped, reason); err != nil {
		e.tell(fmt.Sprintf("Workflow %s status persistence failed: %v", run.ID, err))
	}
	if action == "pause" {
		e.tell("Pause requested for workflow run " + run.ID + "\nUse /workflows resume " + run.ID + " to continue it in this session.")
		return nil
	}
	e.tell("Stop requested for workflow run " + run.ID)
	return nil
}

func (e *Extension) cmdWorkflowsResume(selector string) error {
	runs, _, err := e.workflowRuns()
	if err != nil {
		return err
	}
	run, ok, err := selectWorkflowRun(runs, selector)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if !ok {
		e.tell("Workflow run not found: " + selector)
		return nil
	}
	if e.registry == nil {
		e.tell("Workflow run is not available for resume in this session: " + run.ID)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	exec, ok, msg := e.registry.resume(run.ID, cancel)
	if !ok {
		cancel()
		e.tell(msg)
		return nil
	}
	if err := persistWorkflowRunStatus(exec.RunDir, workflowStatusRunning, ""); err != nil {
		e.tell(fmt.Sprintf("Workflow %s status persistence failed: %v", run.ID, err))
	}
	go e.runBackgroundWorkflow(run.ID, ctx, exec)
	text := fmt.Sprintf("Workflow run %s resumed in background.", run.ID)
	if exec.ScriptPath != "" {
		text += "\nScript: " + exec.ScriptPath
	}
	e.tell(text)
	return nil
}

func (e *Extension) cmdWorkflowsRestart(selector string) error {
	runs, _, err := e.workflowRuns()
	if err != nil {
		return err
	}
	run, ok, err := selectWorkflowRun(runs, selector)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if !ok {
		e.tell("Workflow run not found: " + selector)
		return nil
	}
	if strings.TrimSpace(run.ScriptPath) == "" {
		e.tell("Workflow run has no persisted script: " + run.ID)
		return nil
	}
	data, err := os.ReadFile(run.ScriptPath)
	if err != nil {
		return fmt.Errorf("read workflow script %s: %w", run.ScriptPath, err)
	}
	script := normalizeScript(string(data))
	if script == "" {
		e.tell("Workflow run script is empty: " + run.ID)
		return nil
	}
	scriptPath, runDir, err := persistWorkflowScript(e.api.SessionDir(), script)
	if err != nil {
		return err
	}
	exec := workflowExecution{
		Script:      script,
		Concurrency: e.cfg.Concurrency,
		MaxAgents:   e.cfg.MaxAgents,
		ScriptPath:  scriptPath,
		RunDir:      runDir,
	}
	if !e.approveWorkflowRun(exec, "/workflows restart "+run.ID) {
		e.tell("Workflow run " + run.ID + " restart cancelled before start.")
		return nil
	}
	runID := e.startBackgroundWorkflow(exec)
	text := fmt.Sprintf("Workflow run %s restarted in background.\nNew run: %s", run.ID, runID)
	if scriptPath != "" {
		text += "\nScript: " + scriptPath
	}
	e.tell(text)
	return nil
}

func (e *Extension) cmdWorkflowsSave(selector, name, scope string) error {
	runs, _, err := e.workflowRuns()
	if err != nil {
		return err
	}
	run, ok, err := selectWorkflowRun(runs, selector)
	if err != nil {
		e.tell(err.Error())
		return nil
	}
	if !ok {
		e.tell("Workflow run not found: " + selector)
		return nil
	}
	path, err := e.saveWorkflowRunScript(run, name, scope)
	if err != nil {
		e.tell(fmt.Sprintf("Workflow save failed: %v", err))
		return nil
	}
	e.tell(fmt.Sprintf("Workflow run %s saved as /%s (also /workflow:%s)\nPath: %s\nThe saved workflow is available in future sessions.", run.ID, name, name, path))
	return nil
}

func (e *Extension) saveWorkflowRunScript(run workflowRunSummary, name, scope string) (string, error) {
	name = strings.TrimSpace(name)
	if filepath.Ext(name) == ".js" {
		name = strings.TrimSuffix(name, ".js")
	}
	if !savedWorkflowCommandNameRE.MatchString(name) {
		return "", fmt.Errorf("workflow name must match %s", savedWorkflowCommandNameRE.String())
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	var root string
	var err error
	switch scope {
	case "", "project":
		cwd := ""
		if e.api != nil {
			cwd = e.api.Cwd()
		}
		root, err = projectWorkflowSaveDir(cwd)
		if err != nil {
			return "", err
		}
	case "user", "personal":
		if e.api == nil || strings.TrimSpace(e.api.AgentDir()) == "" {
			return "", fmt.Errorf("user workflow save requires an agent directory")
		}
		root = filepath.Join(filepath.Dir(filepath.Clean(e.api.AgentDir())), ".claude", "workflows")
	default:
		return "", fmt.Errorf("scope must be project or user")
	}
	if strings.TrimSpace(run.ScriptPath) == "" {
		return "", fmt.Errorf("workflow run %s has no persisted script to save", run.ID)
	}
	data, err := os.ReadFile(run.ScriptPath)
	if err != nil {
		return "", fmt.Errorf("read workflow script %s: %w", run.ScriptPath, err)
	}
	script := normalizeScript(string(data))
	if script == "" {
		return "", fmt.Errorf("workflow run %s script is empty", run.ID)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create workflow directory %s: %w", root, err)
	}
	path := filepath.Join(root, name+".js")
	if _, err := os.Stat(path); err == nil {
		if e.api == nil || !e.api.Confirm("Overwrite saved workflow?", fmt.Sprintf("%s already exists. Overwrite it?", path), false) {
			return "", fmt.Errorf("saved workflow already exists: %s", path)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("check saved workflow %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(script+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write saved workflow %s: %w", path, err)
	}
	return path, nil
}

func (e *Extension) workflowRuns() ([]workflowRunSummary, string, error) {
	runs, dir, err := e.persistedWorkflowRuns()
	if err != nil {
		return nil, dir, err
	}
	byID := map[string]int{}
	for i, run := range runs {
		byID[run.ID] = i
	}
	var liveRuns []liveWorkflowRun
	if e.registry != nil {
		liveRuns = e.registry.list()
	}
	for _, live := range liveRuns {
		summary := workflowRunSummary{
			ID:         live.ID,
			RunDir:     live.RunDir,
			ScriptPath: live.ScriptPath,
			Status:     live.Status,
			Snapshot:   live.Snapshot,
			Error:      live.Error,
			UpdatedAt:  live.UpdatedAt,
		}
		if summary.UpdatedAt.IsZero() {
			summary.UpdatedAt = live.StartedAt
		}
		if live.RunDir != "" {
			summary.SnapshotPath = filepath.Join(live.RunDir, "snapshot.json")
		}
		if idx, ok := byID[live.ID]; ok {
			runs[idx] = summary
			continue
		}
		byID[live.ID] = len(runs)
		runs = append(runs, summary)
	}
	sortWorkflowRuns(runs)
	return runs, dir, nil
}

func (e *Extension) persistedWorkflowRuns() ([]workflowRunSummary, string, error) {
	sessionDir := ""
	if e.api != nil {
		sessionDir = strings.TrimSpace(e.api.SessionDir())
	}
	if sessionDir == "" {
		return nil, "(memory)", nil
	}
	dir := filepath.Join(sessionDir, "extensions", "workflow", "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, dir, nil
		}
		return nil, dir, fmt.Errorf("list workflow runs: %w", err)
	}
	var runs []workflowRunSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "script.js")
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		runDir := filepath.Join(dir, entry.Name())
		snapshotPath := filepath.Join(runDir, "snapshot.json")
		snapshot, snapshotTime := readWorkflowRunSnapshot(snapshotPath)
		statusPath := filepath.Join(runDir, "status.json")
		statusFile, statusTime, _ := readWorkflowRunStatus(statusPath)
		updatedAt := info.ModTime()
		if snapshotTime.After(updatedAt) {
			updatedAt = snapshotTime
		}
		status := workflowStatusCompleted
		errText := ""
		if statusFile != nil {
			status = statusFile.Status
			errText = statusFile.Error
			if statusTime.After(updatedAt) {
				updatedAt = statusTime
			}
		}
		runs = append(runs, workflowRunSummary{
			ID:           entry.Name(),
			RunDir:       runDir,
			ScriptPath:   path,
			SnapshotPath: snapshotPath,
			Status:       status,
			Snapshot:     snapshot,
			Error:        errText,
			UpdatedAt:    updatedAt,
		})
	}
	sortWorkflowRuns(runs)
	return runs, dir, nil
}

func sortWorkflowRuns(runs []workflowRunSummary) {
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].UpdatedAt.Equal(runs[j].UpdatedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].UpdatedAt.After(runs[j].UpdatedAt)
	})
}

func readWorkflowRunSnapshot(path string) (*workflowSnapshot, time.Time) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, time.Time{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, info.ModTime()
	}
	var snapshot workflowSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, info.ModTime()
	}
	return &snapshot, info.ModTime()
}

func selectWorkflowRun(runs []workflowRunSummary, selector string) (workflowRunSummary, bool, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return workflowRunSummary{}, false, nil
	}
	if selector == "latest" {
		if len(runs) == 0 {
			return workflowRunSummary{}, false, nil
		}
		return runs[0], true, nil
	}
	var matches []workflowRunSummary
	for _, run := range runs {
		if run.ID == selector {
			return run, true, nil
		}
		if strings.HasPrefix(run.ID, selector) {
			matches = append(matches, run)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, nil
	}
	if len(matches) > 1 {
		return workflowRunSummary{}, false, fmt.Errorf("Workflow run prefix %q is ambiguous", selector)
	}
	return workflowRunSummary{}, false, nil
}

func (e *Extension) tell(text string) {
	if e.api != nil {
		e.api.Notify(e.Name(), text)
	}
}
