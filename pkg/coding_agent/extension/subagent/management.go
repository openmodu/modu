package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/extension"
	csubagent "github.com/openmodu/modu/pkg/coding_agent/subagent"
)

func runAction(ctx context.Context, ext *Extension, action string, args map[string]any) (string, error) {
	switch action {
	case "list":
		scope, err := decodeAgentScope(args)
		if err != nil {
			return "", err
		}
		return formatAgentList(ext, scope), nil
	case "get":
		return handleGet(ext, args)
	case "create":
		return handleCreate(ext, args)
	case "update":
		return handleUpdate(ext, args)
	case "delete":
		return handleDelete(ext, args)
	case "status":
		id, _ := args["id"].(string)
		return formatStatus(ext, id), nil
	case "resume":
		return resumeTask(ctx, ext, args)
	case "interrupt":
		return interruptTask(ext, args)
	case "doctor":
		return formatDoctor(ext), nil
	case "intercom":
		return runIntercomAction(ext, args)
	default:
		return "", fmt.Errorf("unknown action %q (expected list|get|create|update|delete|status|resume|interrupt|doctor|intercom)", action)
	}
}

func sortedDefinitions(ext *Extension) []*csubagent.SubagentDefinition {
	if ext == nil || ext.loader == nil {
		return nil
	}
	defs := ext.loader.List()
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// decodeAgentScope normalises the agentScope argument to "user", "project",
// "both", or "" (treated as both). Unknown values are rejected so a typo
// surfaces immediately instead of silently returning everything.
func decodeAgentScope(args map[string]any) (string, error) {
	raw, ok := args["agentScope"]
	if !ok || raw == nil {
		return "both", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("agentScope must be a string, got %T", raw)
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "both":
		return "both", nil
	case "user":
		return "user", nil
	case "project":
		return "project", nil
	default:
		return "", fmt.Errorf("agentScope must be 'user', 'project', or 'both', got %q", s)
	}
}

// filterDefinitionsByScope keeps only defs matching scope ("user"/"project");
// "both" or "" returns the slice unchanged.
func filterDefinitionsByScope(defs []*csubagent.SubagentDefinition, scope string) []*csubagent.SubagentDefinition {
	if scope == "" || scope == "both" {
		return defs
	}
	out := make([]*csubagent.SubagentDefinition, 0, len(defs))
	for _, def := range defs {
		if def.Source == scope {
			out = append(out, def)
		}
	}
	return out
}

func formatAgentList(ext *Extension, scope string) string {
	defs := filterDefinitionsByScope(sortedDefinitions(ext), scope)
	if len(defs) == 0 {
		if scope != "" && scope != "both" {
			return fmt.Sprintf("No subagent profiles found in scope %q.", scope)
		}
		return "No subagent profiles found.\nSearched the configured agents_dir, or the host defaults when agents_dir is empty."
	}
	var b strings.Builder
	if scope != "" && scope != "both" {
		fmt.Fprintf(&b, "Available subagents (%d) [scope: %s]:", len(defs), scope)
	} else {
		fmt.Fprintf(&b, "Available subagents (%d):", len(defs))
	}
	for _, def := range defs {
		desc := strings.TrimSpace(def.Description)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "\n- %s: %s", def.Name, desc)
		if def.Source != "" {
			fmt.Fprintf(&b, " [%s]", def.Source)
		}
	}
	return b.String()
}

func formatStatus(ext *Extension, id string) string {
	if ext == nil || ext.api == nil {
		return "subagent status unavailable: extension is not initialized"
	}
	tasks := overlayStaleStatus(ext, filterSubagentTasks(ext.api.BackgroundTasks()))
	if ext.batchTasks != nil {
		tasks = mergeBatchTasks(tasks, ext.batchTasks.snapshots())
	}
	if strings.TrimSpace(id) != "" {
		task, ok, ambiguous := resolveTask(tasks, id)
		if ambiguous {
			return fmt.Sprintf("Multiple background tasks match %q; use the full task id.", id)
		}
		if !ok {
			return fmt.Sprintf("Background task %q not found.", id)
		}
		return formatTask(task, true)
	}
	if len(tasks) == 0 {
		return "No background subagent tasks in the project runtime."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Background subagent tasks (%d):", len(tasks))
	writeTaskTree(&b, tasks)
	return b.String()
}

// overlayStaleStatus returns a copy of tasks where any ID in
// ext.staleTaskIDs gets its Status and Error rewritten to reflect the
// reconciled state. The host's in-memory snapshot keeps the original
// "running" until next process startup, so we apply the overlay here.
func overlayStaleStatus(ext *Extension, tasks []extension.TaskSnapshot) []extension.TaskSnapshot {
	if ext == nil || len(ext.staleTaskIDs) == 0 {
		return tasks
	}
	out := make([]extension.TaskSnapshot, len(tasks))
	for i, task := range tasks {
		if ext.staleTaskIDs[task.ID] {
			task.Status = staleStatus
			if strings.TrimSpace(task.Error) == "" {
				task.Error = staleReason
			}
		}
		out[i] = task
	}
	return out
}

func writeTaskTree(b *strings.Builder, tasks []extension.TaskSnapshot) {
	byID := make(map[string]extension.TaskSnapshot, len(tasks))
	children := make(map[string][]extension.TaskSnapshot, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
		if strings.TrimSpace(task.ParentID) != "" {
			children[task.ParentID] = append(children[task.ParentID], task)
		}
	}
	var roots []extension.TaskSnapshot
	for _, task := range tasks {
		if strings.TrimSpace(task.ParentID) == "" {
			roots = append(roots, task)
			continue
		}
		if _, ok := byID[task.ParentID]; !ok {
			roots = append(roots, task)
		}
	}
	sortTaskSnapshots(roots)
	for parentID := range children {
		sortTaskSnapshots(children[parentID])
	}
	visited := map[string]bool{}
	for _, root := range roots {
		writeTaskTreeNode(b, root, children, 0, visited)
	}
	for _, task := range tasks {
		if !visited[task.ID] {
			writeTaskTreeNode(b, task, children, 0, visited)
		}
	}
}

func writeTaskTreeNode(b *strings.Builder, task extension.TaskSnapshot, children map[string][]extension.TaskSnapshot, depth int, visited map[string]bool) {
	if visited[task.ID] {
		return
	}
	visited[task.ID] = true
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "\n%s- %s [%s] %s", indent, task.ID, task.Status, task.Summary)
	for _, child := range children[task.ID] {
		writeTaskTreeNode(b, child, children, depth+1, visited)
	}
}

func sortTaskSnapshots(tasks []extension.TaskSnapshot) {
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
}

func filterSubagentTasks(tasks []extension.TaskSnapshot) []extension.TaskSnapshot {
	out := make([]extension.TaskSnapshot, 0, len(tasks))
	for _, task := range tasks {
		if task.Kind == "subagent" {
			out = append(out, task)
		}
	}
	return out
}

func resolveTask(tasks []extension.TaskSnapshot, id string) (extension.TaskSnapshot, bool, bool) {
	id = strings.TrimSpace(id)
	var match extension.TaskSnapshot
	found := false
	for _, task := range tasks {
		if task.ID == id {
			return task, true, false
		}
		if strings.HasPrefix(task.ID, id) {
			if found {
				return extension.TaskSnapshot{}, false, true
			}
			match = task
			found = true
		}
	}
	return match, found, false
}

func formatTask(task extension.TaskSnapshot, includeOutput bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s\nkind: %s\nstatus: %s\nsummary: %s", task.ID, task.Kind, task.Status, task.Summary)
	if task.Agent != "" {
		fmt.Fprintf(&b, "\nagent: %s", task.Agent)
	}
	if task.ParentID != "" {
		fmt.Fprintf(&b, "\nparent: %s", task.ParentID)
	}
	if task.RunDir != "" {
		fmt.Fprintf(&b, "\nrun_dir: %s", task.RunDir)
	}
	if task.StatusFile != "" {
		fmt.Fprintf(&b, "\nstatus_file: %s", task.StatusFile)
	}
	if task.SessionFile != "" {
		fmt.Fprintf(&b, "\nsession_file: %s", task.SessionFile)
	}
	if task.OutputFile != "" {
		fmt.Fprintf(&b, "\noutput_file: %s", task.OutputFile)
	}
	if task.Error != "" {
		fmt.Fprintf(&b, "\n\nerror:\n%s", task.Error)
	}
	if includeOutput && task.Output != "" {
		fmt.Fprintf(&b, "\n\noutput:\n%s", task.Output)
	}
	return b.String()
}

func resumeTask(ctx context.Context, ext *Extension, args map[string]any) (string, error) {
	task, err := taskFromArgs(ext, args)
	if err != nil {
		return "", err
	}
	if task.Status == "running" {
		return "", fmt.Errorf("task %s is still running; live resume is not available yet", task.ID)
	}
	if strings.TrimSpace(task.Agent) == "" || strings.TrimSpace(task.Task) == "" {
		return "", fmt.Errorf("task %s cannot be resumed because it lacks persisted agent/task metadata", task.ID)
	}
	message, _ := args["message"].(string)
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf(`resume requires "message"`)
	}
	followUp := buildResumeTaskPrompt(task, message)
	background := true
	return forkOne(ctx, ext, task.Agent, followUp, callOptions{background: &background, parentID: task.ID})
}

func interruptTask(ext *Extension, args map[string]any) (string, error) {
	task, err := taskFromArgs(ext, args)
	if err != nil {
		return "", err
	}
	reason, _ := args["message"].(string)
	if strings.TrimSpace(reason) == "" {
		reason = "interrupted by subagent action"
	}
	updated, ok := ext.api.InterruptBackgroundTask(task.ID, reason)
	if !ok {
		return "", fmt.Errorf("task %s is not interruptible in this process (status: %s)", task.ID, task.Status)
	}
	return formatTask(updated, false), nil
}

func taskFromArgs(ext *Extension, args map[string]any) (extension.TaskSnapshot, error) {
	if ext == nil || ext.api == nil {
		return extension.TaskSnapshot{}, fmt.Errorf("subagent task control unavailable: extension is not initialized")
	}
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return extension.TaskSnapshot{}, fmt.Errorf(`task control requires "id"`)
	}
	task, ok, ambiguous := resolveTask(filterSubagentTasks(ext.api.BackgroundTasks()), id)
	if ambiguous {
		return extension.TaskSnapshot{}, fmt.Errorf("multiple background tasks match %q; use the full task id", id)
	}
	if !ok {
		return extension.TaskSnapshot{}, fmt.Errorf("background task %q not found", id)
	}
	return task, nil
}

func buildResumeTaskPrompt(task extension.TaskSnapshot, message string) string {
	var b strings.Builder
	b.WriteString("Continue this delegated subagent task.\n\nOriginal task:\n")
	b.WriteString(task.Task)
	if task.Output != "" {
		b.WriteString("\n\nPrevious output:\n")
		b.WriteString(task.Output)
	}
	if task.Error != "" {
		b.WriteString("\n\nPrevious error/status detail:\n")
		b.WriteString(task.Error)
	}
	b.WriteString("\n\nFollow-up instruction:\n")
	b.WriteString(strings.TrimSpace(message))
	return b.String()
}

func formatDoctor(ext *Extension) string {
	status := "ok"
	var lines []string
	lines = append(lines, "Subagent doctor")
	if ext == nil {
		return "Subagent doctor\nstatus: error\n- extension is nil"
	}
	if ext.api == nil {
		status = "error"
		lines = append(lines, "- extension API is not initialized")
	}
	defs := sortedDefinitions(ext)
	if len(defs) == 0 {
		if status == "ok" {
			status = "warning"
		}
		lines = append(lines, "- no subagent profiles discovered")
	} else {
		lines = append(lines, fmt.Sprintf("- profiles discovered: %d", len(defs)))
		lines = append(lines, "- profile sources: "+profileSourceBreakdown(defs))
	}
	if ext.cfg.AgentsDir != "" {
		lines = append(lines, "- agents_dir: "+ext.cfg.AgentsDir)
	} else if ext.api != nil {
		lines = append(lines, "- agents_dir: host defaults")
		lines = append(lines, "- host agent_dir: "+ext.api.AgentDir())
		lines = append(lines, "- host cwd: "+ext.api.Cwd())
	}
	if ext.api != nil {
		runtimeDir := subagentRuntimeDir(ext)
		lines = append(lines, "- subagents runtime dir: "+runtimeDir+" "+dirStatus(runtimeDir))
		tasks := filterSubagentTasks(ext.api.BackgroundTasks())
		lines = append(lines, fmt.Sprintf("- background subagent tasks: %d", len(tasks)))
		if stale := len(ext.staleTaskIDs); stale > 0 {
			if status == "ok" {
				status = "warning"
			}
			lines = append(lines, fmt.Sprintf("- stale tasks reconciled at init: %d", stale))
		}
	}
	lines = append(lines, fmt.Sprintf("- default_model: %s", valueOrUnset(ext.cfg.DefaultModel)))
	lines = append(lines, fmt.Sprintf("- max_depth: %d", ext.cfg.MaxDepth))
	lines = append(lines, fmt.Sprintf("- timeout_seconds: %d", ext.cfg.TimeoutSeconds))
	lines = append(lines, fmt.Sprintf("- force_top_level_async: %t", ext.cfg.ForceTopLevelAsync))
	return "status: " + status + "\n" + strings.Join(lines, "\n")
}

func profileSourceBreakdown(defs []*csubagent.SubagentDefinition) string {
	counts := map[string]int{}
	order := []string{}
	for _, def := range defs {
		source := def.Source
		if source == "" {
			source = "unknown"
		}
		if _, seen := counts[source]; !seen {
			order = append(order, source)
		}
		counts[source]++
	}
	sort.Strings(order)
	parts := make([]string, 0, len(order))
	for _, source := range order {
		parts = append(parts, fmt.Sprintf("%s %d", source, counts[source]))
	}
	return strings.Join(parts, ", ")
}

func subagentRuntimeDir(ext *Extension) string {
	if ext == nil || ext.api == nil {
		return "(unavailable)"
	}
	return filepath.Join(ext.api.AgentDir(), "tool-results", projectKey(ext.api.Cwd()), "subagents")
}

func dirStatus(path string) string {
	if strings.TrimSpace(path) == "" {
		return "(unset)"
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "(missing, created on first use)"
		}
		return "(unreadable: " + err.Error() + ")"
	}
	if !info.IsDir() {
		return "(not a directory)"
	}
	return "(ok)"
}

func valueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(inherit)"
	}
	return value
}
