package slash

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

// Printer is implemented by the caller (TUI or stdout) to surface command output.
type Printer interface {
	PrintInfo(string)
	PrintError(error)
	PrintSection(string, []string)
}

// Clearer is optionally implemented to clear the screen.
type Clearer interface {
	ClearScreen()
}

type CommandDefinition struct {
	Name        string
	Aliases     []string
	Description string
	operation   commandOperation
}

type commandOperation uint8

const (
	commandQuit commandOperation = iota + 1
	commandClear
	commandModel
	commandCompact
	commandTokens
	commandContext
	commandSession
	commandName
	commandSessions
	commandResume
	commandForkSession
	commandTree
	commandFork
	commandClone
	commandBranchSession
	commandExport
	commandCopy
	commandChangelog
	commandDoctor
	commandReload
	commandTools
	commandAllow
	commandAgents
	commandTodos
	commandTasks
	commandPlan
	commandWorktree
	commandSkills
	commandPrompts
)

// CommandDefinitions is the canonical metadata for built-in slash commands.
// Hosts use it to build their own command registry, help, and completion UI.
func CommandDefinitions() []CommandDefinition {
	return []CommandDefinition{
		{Name: "/quit", Aliases: []string{"/exit", "/q"}, Description: "Exit modu_code", operation: commandQuit},
		{Name: "/clear", Aliases: []string{"/new"}, Description: "Clear the current session", operation: commandClear},
		{Name: "/model", Description: "Show, list, or switch the active model", operation: commandModel},
		{Name: "/compact", Description: "Manually trigger context compaction", operation: commandCompact},
		{Name: "/tokens", Description: "Show token usage", operation: commandTokens},
		{Name: "/context", Description: "Show loaded context", operation: commandContext},
		{Name: "/session", Description: "Show, name, or delete a session", operation: commandSession},
		{Name: "/name", Description: "Set the current session name", operation: commandName},
		{Name: "/sessions", Description: "List or delete saved sessions", operation: commandSessions},
		{Name: "/resume", Description: "Switch to a saved session", operation: commandResume},
		{Name: "/fork-session", Description: "Copy a saved session into this cwd", operation: commandForkSession},
		{Name: "/tree", Description: "Show conversation branches", operation: commandTree},
		{Name: "/fork", Description: "Move the session leaf to an entry", operation: commandFork},
		{Name: "/clone", Description: "Clone the current session", operation: commandClone},
		{Name: "/branch-session", Description: "Create a branched session from an entry", operation: commandBranchSession},
		{Name: "/export", Description: "Export the session to HTML", operation: commandExport},
		{Name: "/copy", Description: "Copy the last assistant message", operation: commandCopy},
		{Name: "/changelog", Description: "Show recent git commits", operation: commandChangelog},
		{Name: "/doctor", Description: "Show runtime diagnostics", operation: commandDoctor},
		{Name: "/reload", Description: "Reload skills, prompts, and other resources", operation: commandReload},
		{Name: "/tools", Description: "List active tools", operation: commandTools},
		{Name: "/allow", Description: "Clear a stored deny decision for a tool", operation: commandAllow},
		{Name: "/agents", Description: "List discovered subagents", operation: commandAgents},
		{Name: "/todos", Description: "Show the current todo list", operation: commandTodos},
		{Name: "/tasks", Description: "Show background subagent tasks", operation: commandTasks},
		{Name: "/plan", Description: "Inspect or update plan mode", operation: commandPlan},
		{Name: "/worktree", Description: "Inspect or manage the current worktree", operation: commandWorktree},
		{Name: "/skills", Description: "List available skills", operation: commandSkills},
		{Name: "/prompts", Description: "List available prompt templates", operation: commandPrompts},
	}
}

// Execute runs this definition after a host registry has resolved it.
// invokedName preserves alias-specific behavior such as /new versus /clear.
func (d CommandDefinition) Execute(ctx context.Context, invokedName, args string, session *coding_agent.CodingSession, r Printer, model *types.Model) bool {
	invokedName = strings.ToLower(strings.TrimSpace(invokedName))
	parts := []string{strings.TrimPrefix(d.Name, "/")}
	if args = strings.TrimSpace(args); args != "" {
		parts = append(parts, args)
	}
	if d.operation == 0 {
		r.PrintError(fmt.Errorf("command %s has no operation", d.Name))
		return false
	}
	return executeCommand(ctx, d.operation, invokedName, parts, session, r, model)
}

func executeCommand(ctx context.Context, operation commandOperation, invokedName string, parts []string, session *coding_agent.CodingSession, r Printer, model *types.Model) bool {
	switch operation {
	case commandQuit:
		r.PrintInfo("bye!")
		return true

	case commandClear:
		if err := session.ClearConversation(); err != nil {
			r.PrintError(fmt.Errorf("clear session: %w", err))
		} else {
			if invokedName == "/new" {
				r.PrintInfo("new session")
			} else {
				r.PrintInfo("session cleared")
			}
		}
		if invokedName != "/new" {
			if clearer, ok := r.(Clearer); ok {
				clearer.ClearScreen()
			} else {
				fmt.Print("\033[2J\033[H")
			}
		}
		return false

	case commandModel:
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		handleModel(arg, session, r, model)
		return false

	case commandCompact:
		r.PrintInfo("compacting context…")
		changed, err := session.CompactIfNeeded(ctx)
		if err != nil {
			r.PrintError(err)
		} else if !changed {
			r.PrintInfo("context unchanged: not enough messages to compact")
		} else {
			r.PrintInfo("context compacted")
		}
		return false

	case commandTokens:
		stats := session.GetSessionStats()
		r.PrintInfo(fmt.Sprintf("tokens used this session: %d", stats.TotalTokens))
		return false

	case commandContext:
		handleContext(session, r)
		return false

	case commandSession:
		handleSession(parts, session, r)
		return false

	case commandName:
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		session.SetSessionName(arg)
		if arg == "" {
			r.PrintInfo("session name cleared")
		} else {
			r.PrintInfo("session name: " + arg)
		}
		return false

	case commandSessions:
		handleSessions(parts, session, r)
		return false

	case commandResume:
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /resume <session-file | session-id-prefix>")
			return false
		}
		target := strings.TrimSpace(parts[1])
		// Accept both a session file path and a session id (or unique
		// prefix), matching the `modu_code --resume <id>` startup flag.
		// The stat check decides the route up front: SwitchSession quietly
		// tolerates missing files (it would "resume" onto an empty session
		// at a bogus path), so only hand it real files.
		if fi, statErr := os.Stat(target); statErr == nil && !fi.IsDir() {
			if err := session.ResumeSession(target); err != nil {
				r.PrintError(err)
				return false
			}
		} else if err := session.ResumeByID(target); err != nil {
			r.PrintError(fmt.Errorf("resume %q: %v", target, err))
			return false
		}
		r.PrintInfo("resumed session: " + session.GetSessionFile())
		return false

	case commandForkSession:
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /fork-session <session-file>")
			return false
		}
		path := strings.TrimSpace(parts[1])
		if err := session.ForkFromSession(path); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("forked session: " + session.GetSessionFile())
		}
		return false

	case commandTree:
		handleTree(session, r)
		return false

	case commandFork:
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /fork <entry-id>")
			return false
		}
		entryID := strings.TrimSpace(parts[1])
		if err := session.Fork(entryID); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("forked from entry: " + entryID)
		}
		return false

	case commandClone:
		leafID := session.GetSessionLeafID()
		if leafID == "" {
			r.PrintInfo("nothing to clone")
			return false
		}
		path, err := session.CreateBranchedSession(leafID)
		if err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("cloned session: " + path)
		}
		return false

	case commandBranchSession:
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /branch-session <entry-id>")
			return false
		}
		path, err := session.CreateBranchedSession(strings.TrimSpace(parts[1]))
		if err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("created branched session: " + path)
		}
		return false

	case commandExport:
		path := ""
		if len(parts) > 1 {
			path = strings.TrimSpace(parts[1])
		}
		path = resolveExportPath(session, path)
		if err := session.ExportHTML(path); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("exported session: " + path)
		}
		return false

	case commandCopy:
		text := strings.TrimSpace(session.GetLastAssistantText())
		if text == "" {
			r.PrintInfo("no assistant message to copy")
			return false
		}
		if err := copyTextToClipboard(text); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("copied last assistant message")
		}
		return false

	case commandChangelog:
		handleChangelog(session, r)
		return false

	case commandDoctor:
		handleDoctor(ctx, session, r)
		return false

	case commandReload:
		session.ReloadResources()
		r.PrintInfo("reloaded resources")
		return false

	case commandTools:
		names := session.GetActiveToolNames()
		r.PrintInfo("active tools: " + strings.Join(names, ", "))
		return false

	case commandAllow:
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /allow <tool>  — clear deny decision so the tool is asked again")
			return false
		}
		toolName := strings.TrimSpace(parts[1])
		session.ClearToolDecision(toolName)
		r.PrintInfo(fmt.Sprintf("cleared decision for %q — will ask again on next call", toolName))
		return false

	case commandAgents:
		subagents := session.GetSubagents()
		sort.Slice(subagents, func(i, j int) bool { return subagents[i].Name < subagents[j].Name })
		if len(subagents) == 0 {
			r.PrintInfo("no subagents found")
		} else {
			r.PrintInfo(fmt.Sprintf("available subagents (%d):", len(subagents)))
			for _, sg := range subagents {
				line := "  " + sg.Name
				if sg.Description != "" {
					line += " — " + sg.Description
				}
				line += " [" + sg.Source + "]"
				r.PrintInfo(line)
			}
		}
		return false

	case commandTodos:
		todos := session.GetTodos()
		if len(todos) == 0 {
			r.PrintInfo("no todos")
			return false
		}
		r.PrintInfo(fmt.Sprintf("todos (%d):", len(todos)))
		for i, todo := range todos {
			r.PrintInfo(fmt.Sprintf("  %d. [%s] %s", i+1, todo.Status, todo.Content))
		}
		return false

	case commandTasks:
		tasks := session.GetBackgroundTasks()
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt < tasks[j].CreatedAt })
		if len(tasks) == 0 {
			r.PrintInfo("no background tasks")
			return false
		}
		r.PrintInfo(fmt.Sprintf("background tasks (%d):", len(tasks)))
		for _, task := range tasks {
			l := fmt.Sprintf("  %s [%s] %s", task.ID, task.Status, task.Summary)
			if task.Error != "" {
				l += " error=" + task.Error
			}
			r.PrintInfo(l)
		}
		return false

	case commandPlan:
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		switch arg {
		case "", "status":
			status := session.PlanStatus()
			lines := []string{
				"active: " + yesNo(status.Active),
				"latest plan: " + yesNo(status.PlanExists),
				fmt.Sprintf("revisions: %d", status.RevisionCount),
				fmt.Sprintf("todos: total=%d pending=%d in_progress=%d completed=%d", status.TodoTotal, status.TodoPending, status.TodoInProgress, status.TodoCompleted),
			}
			r.PrintSection("Plan", lines)
		case "on":
			session.EnterPlanMode()
			r.PrintInfo("plan mode enabled")
		case "off":
			session.ExitPlanMode("manually exited from /plan off", nil)
			r.PrintInfo("plan mode disabled")
		case "show":
			status := session.PlanStatus()
			if !status.PlanExists {
				r.PrintInfo("no latest plan")
				return false
			}
			content := strings.TrimSpace(status.LatestPlan)
			if content == "" {
				content = "(empty plan)"
			}
			r.PrintSection("Plan", []string{content})
		case "clear":
			if err := session.ClearPlan(); err != nil {
				r.PrintError(err)
			} else {
				r.PrintInfo("cleared latest plan and todos")
			}
		case "history":
			revisions := session.ListPlanRevisions()
			if len(revisions) == 0 {
				r.PrintInfo("no plan revisions")
				return false
			}
			lines := make([]string, 0, len(revisions))
			for i, revision := range revisions {
				if i >= 10 {
					break
				}
				lines = append(lines, fmt.Sprintf("%s  %s", revision.ModTime.Format(time.RFC3339), revision.Name))
			}
			r.PrintSection("Plan history", lines)
		default:
			r.PrintInfo("usage: /plan [status|show|history|clear|on|off]")
		}
		return false

	case commandWorktree:
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		switch arg {
		case "", "status":
			status := session.WorktreeStatus()
			if status.Active {
				lines := []string{
					"active: yes",
					"path: " + status.Path,
					"cwd: " + status.Cwd,
					"original cwd: " + status.OriginalCwd,
					"exists: " + yesNo(status.Exists),
				}
				r.PrintSection("Worktree", lines)
			} else {
				r.PrintSection("Worktree", []string{
					"active: no",
					"cwd: " + status.Cwd,
				})
			}
		case "on":
			path, err := session.EnterWorktree()
			if err != nil {
				r.PrintError(err)
			} else {
				r.PrintInfo("entered worktree: " + path)
			}
		case "off":
			if err := session.ExitWorktree(); err != nil {
				r.PrintError(err)
			} else {
				r.PrintInfo("exited worktree")
			}
		case "list":
			worktrees := session.ListManagedWorktrees()
			if len(worktrees) == 0 {
				r.PrintInfo("no managed worktrees")
				return false
			}
			lines := make([]string, 0, len(worktrees))
			for _, wt := range worktrees {
				state := "idle"
				if wt.Active {
					state = "active"
				}
				lines = append(lines, fmt.Sprintf("%s exists=%s %s", state, yesNo(wt.Exists), wt.Path))
			}
			r.PrintSection("Worktrees", lines)
		case "cleanup":
			removed, err := session.CleanupManagedWorktrees()
			if err != nil {
				r.PrintError(err)
				return false
			}
			if len(removed) == 0 {
				r.PrintInfo("no inactive managed worktrees to cleanup")
				return false
			}
			lines := make([]string, 0, len(removed))
			for _, wt := range removed {
				lines = append(lines, "removed "+wt.Path)
			}
			r.PrintSection("Worktree cleanup", lines)
		case "diff":
			diff, err := session.ActiveWorktreeDiff()
			if err != nil {
				r.PrintError(err)
				return false
			}
			lines := []string{"path: " + diff.Path}
			if diff.Stat == "" && diff.NameStatus == "" && diff.Patch == "" {
				lines = append(lines, "no changes")
				r.PrintSection("Worktree diff", lines)
				return false
			}
			if diff.Stat != "" {
				lines = append(lines, "stat:", diff.Stat)
			}
			if diff.NameStatus != "" {
				lines = append(lines, "files:", diff.NameStatus)
			}
			if diff.Patch != "" {
				lines = append(lines, "patch:", diff.Patch)
			}
			r.PrintSection("Worktree diff", lines)
		default:
			r.PrintInfo("usage: /worktree [status|list|diff|cleanup|on|off]")
		}
		return false

	case commandSkills:
		skills := session.GetSkills()
		if len(skills) == 0 {
			r.PrintInfo("no skills found")
			return false
		}
		r.PrintInfo(fmt.Sprintf("available skills (%d):", len(skills)))
		for _, s := range skills {
			l := "  /" + s.Name
			if s.Description != "" {
				l += " — " + s.Description
			}
			if s.Source != "" {
				l += " [" + s.Source + "]"
			}
			r.PrintInfo(l)
		}
		return false

	case commandPrompts:
		prompts := session.GetPromptTemplates()
		if len(prompts) == 0 {
			printNoPromptTemplatesFound(r)
			return false
		}
		r.PrintInfo(fmt.Sprintf("available prompt templates (%d):", len(prompts)))
		for _, p := range prompts {
			l := "  /" + p.Name
			if p.Description != "" {
				l += " — " + p.Description
			}
			if p.Source != "" {
				l += " [" + p.Source + "]"
			}
			r.PrintInfo(l)
		}
		return false

	default:
		r.PrintError(fmt.Errorf("unknown built-in operation: %d", operation))
		return false
	}
}

func resolveExportPath(session *coding_agent.CodingSession, path string) string {
	cwd := session.GetContextInfo().Cwd
	if strings.TrimSpace(path) == "" {
		path = "modu_code-session.html"
	}
	if filepath.IsAbs(path) || cwd == "" {
		return filepath.Clean(path)
	}
	return filepath.Join(cwd, path)
}

func printNoPromptTemplatesFound(r Printer) {
	lines := []string{
		"no prompt templates found",
		"",
		"Create a project prompt template:",
		"  mkdir -p .coding_agent/prompts",
		"  $EDITOR .coding_agent/prompts/review.md",
		"",
		"Example .coding_agent/prompts/review.md:",
		"  ---",
		"  description: Review code changes",
		"  ---",
		"  Review the following target and point out bugs, regressions, and missing tests:",
		"",
		"  $ARGUMENTS",
		"",
		"Then run:",
		"  /reload",
		"  /prompts",
		"  /review cmd/modu_code",
		"",
		"User-wide templates also work under ~/.modu/prompts/.",
	}
	for _, line := range lines {
		r.PrintInfo(line)
	}
}

var copyTextToClipboard = func(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy to clipboard: %w", err)
	}
	return nil
}

func handleChangelog(session *coding_agent.CodingSession, r Printer) {
	cwd := session.GetContextInfo().Cwd
	if cwd == "" {
		r.PrintInfo("changelog unavailable: no cwd")
		return
	}
	cmd := exec.Command("git", "log", "--oneline", "--decorate", "-n", "8")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		r.PrintError(fmt.Errorf("changelog: %w", err))
		return
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		r.PrintInfo("changelog: no commits found")
		return
	}
	r.PrintSection("Changelog", strings.Split(text, "\n"))
}

func handleTree(session *coding_agent.CodingSession, r Printer) {
	branches := session.GetSessionBranches()
	if len(branches) == 0 {
		forkMessages := session.GetForkMessages()
		if len(forkMessages) == 0 {
			r.PrintInfo("session tree: empty")
			return
		}
		lines := make([]string, 0, len(forkMessages))
		for _, msg := range forkMessages {
			content := strings.ReplaceAll(strings.TrimSpace(msg.Content), "\n", " ")
			if len(content) > 80 {
				content = content[:77] + "..."
			}
			lines = append(lines, fmt.Sprintf("%s  %s", msg.EntryID, content))
		}
		r.PrintSection("Forkable Messages", lines)
		return
	}
	lines := make([]string, 0, len(branches))
	for _, branch := range branches {
		line := fmt.Sprintf("%s parent=%s entries=%d", branch.ID, branch.ParentID, branch.EntryCount)
		if branch.Label != "" {
			line += " label=" + branch.Label
		}
		lines = append(lines, line)
	}
	r.PrintSection("Session Tree", lines)
}

func handleSession(parts []string, session *coding_agent.CodingSession, r Printer) {
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	if strings.HasPrefix(arg, "name ") || arg == "name" {
		name := strings.TrimSpace(strings.TrimPrefix(arg, "name"))
		session.SetSessionName(name)
		if name == "" {
			r.PrintInfo("session name cleared")
		} else {
			r.PrintInfo("session name: " + name)
		}
		return
	}
	if strings.HasPrefix(arg, "delete ") || arg == "delete" {
		path := strings.TrimSpace(strings.TrimPrefix(arg, "delete"))
		if path == "" {
			r.PrintInfo("usage: /session delete <session-file>")
			return
		}
		if err := session.DeleteSession(path); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("deleted session: " + path)
		}
		return
	}
	name := session.GetSessionName()
	if name == "" {
		name = "(unnamed)"
	}
	stats := session.GetSessionStats()
	info := session.GetContextInfo()
	model := info.ModelName
	if model == "" {
		model = info.ModelID
	}
	if model == "" {
		model = "(unknown)"
	}
	modelLine := model
	if info.ModelProvider != "" || info.ModelID != "" {
		modelLine += fmt.Sprintf(" (%s / %s)", info.ModelProvider, info.ModelID)
	}
	planMode := "off"
	if info.PlanMode {
		planMode = "on"
	}
	worktree := "none"
	if info.ActiveWorktree != "" {
		worktree = info.ActiveWorktree
	}
	lines := []string{
		"id: " + session.GetSessionID(),
		"name: " + name,
		"file: " + session.GetSessionFile(),
		"cwd: " + info.Cwd,
		"model: " + modelLine,
		fmt.Sprintf("messages: %d", stats.MessageCount),
		fmt.Sprintf("tokens: %d", stats.TotalTokens),
		"duration: " + formatSessionDuration(stats.DurationMs),
		"plan mode: " + planMode,
		"worktree: " + worktree,
		fmt.Sprintf("context files: %d", len(info.ContextFiles)),
		fmt.Sprintf("skills: %d", len(info.Skills)),
		fmt.Sprintf("prompt templates: %d", len(info.PromptTemplates)),
	}
	r.PrintSection("Session", lines)
}

func formatSessionDuration(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func handleSessions(parts []string, session *coding_agent.CodingSession, r Printer) {
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	if strings.HasPrefix(arg, "delete ") || arg == "delete" {
		path := strings.TrimSpace(strings.TrimPrefix(arg, "delete"))
		if path == "" {
			r.PrintInfo("usage: /sessions delete <session-file>")
			return
		}
		if err := session.DeleteSession(path); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("deleted session: " + path)
		}
		return
	}
	all := arg == "all"
	var sessions []coding_agent.SessionInfo
	var err error
	if all {
		sessions, err = session.ListAllSessionInfos()
	} else {
		sessions, err = session.ListSessionInfos()
	}
	if err != nil {
		r.PrintError(err)
		return
	}
	if len(sessions) == 0 {
		r.PrintInfo("no sessions found")
		return
	}
	title := fmt.Sprintf("Sessions (%d)", len(sessions))
	if all {
		title += " all"
	}
	lines := make([]string, 0, len(sessions))
	for i, info := range sessions {
		if i >= 20 {
			lines = append(lines, fmt.Sprintf("... %d more", len(sessions)-i))
			break
		}
		label := info.Name
		if label == "" {
			label = info.FirstMessage
		}
		lines = append(lines, fmt.Sprintf("%s  messages=%d  %s", info.Path, info.MessageCount, label))
	}
	r.PrintSection(title, lines)
}

func handleModel(arg string, session *coding_agent.CodingSession, r Printer, fallback *types.Model) {
	current := session.GetModel()
	if current == nil {
		current = fallback
	}
	if arg == "" || arg == "status" {
		if current == nil {
			r.PrintInfo("current model: none")
			return
		}
		r.PrintInfo(fmt.Sprintf("current model: %s (%s / %s)", current.Name, current.ProviderID, current.ID))
		r.PrintInfo("usage: /model list | /model <name> | /model <provider> <modelId>")
		return
	}
	if arg == "list" || arg == "ls" {
		models := session.GetAvailableModels()
		sort.Slice(models, func(i, j int) bool {
			if models[i].ProviderID == models[j].ProviderID {
				return models[i].ID < models[j].ID
			}
			return models[i].ProviderID < models[j].ProviderID
		})
		if len(models) == 0 {
			r.PrintInfo("no models configured")
			return
		}
		r.PrintInfo(fmt.Sprintf("available models (%d):", len(models)))
		for _, m := range models {
			prefix := "  "
			if current != nil && current.ProviderID == m.ProviderID && current.ID == m.ID {
				prefix = "* "
			}
			r.PrintInfo(fmt.Sprintf("%s%s (%s / %s)", prefix, m.Name, m.ProviderID, m.ID))
		}
		return
	}
	fields := strings.Fields(arg)
	var err error
	before := session.GetModel()
	if len(fields) == 2 {
		err = session.SetModelByID(fields[0], fields[1])
	} else {
		err = session.SetModelByName(arg)
	}
	if err != nil {
		r.PrintError(err)
		return
	}
	current = session.GetModel()
	r.PrintInfo(fmt.Sprintf("switched model: %s (%s / %s)", current.Name, current.ProviderID, current.ID))
	r.PrintInfo("active entry: " + modelDisplayName(current))
	if sameModel(before, current) {
		r.PrintInfo("conversation context unchanged")
	} else {
		r.PrintInfo("conversation context cleared")
	}
}

func sameModel(a, b *types.Model) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ProviderID == b.ProviderID && a.ID == b.ID
}

func modelDisplayName(model *types.Model) string {
	if model == nil {
		return "none"
	}
	if strings.TrimSpace(model.Name) != "" {
		return model.Name
	}
	if model.ProviderID != "" {
		return model.ProviderID + "/" + model.ID
	}
	return model.ID
}

func handleContext(session *coding_agent.CodingSession, r Printer) {
	info := session.GetContextInfo()
	model := "none"
	if info.ModelID != "" {
		model = fmt.Sprintf("%s (%s / %s)", info.ModelName, info.ModelProvider, info.ModelID)
	}

	lines := []string{
		"model: " + model,
		"cwd: " + info.Cwd,
		"agent dir: " + info.AgentDir,
		fmt.Sprintf("messages: %d", info.MessageCount),
		fmt.Sprintf("system prompt: %d bytes", info.PromptByteCount),
		fmt.Sprintf("memory: %s", memoryPresence(info.MemoryEnabled, info.MemorySummaryActive, info.MemoryBytes)),
		"context remaining: " + contextRemainingPresence(info.TokensUntilCompaction, info.TokensUntilCompactionAvailable),
		fmt.Sprintf("plan mode: %s", onOff(info.PlanMode)),
	}
	if info.ActiveWorktree != "" {
		lines = append(lines, "worktree: "+info.ActiveWorktree)
	} else {
		lines = append(lines, "worktree: none")
	}

	if len(info.ContextFiles) == 0 {
		lines = append(lines, "context files: none")
	} else {
		lines = append(lines, fmt.Sprintf("context files (%d):", len(info.ContextFiles)))
		for _, file := range info.ContextFiles {
			lines = append(lines, fmt.Sprintf("  %s - %s (%d bytes)", file.Name, file.Path, file.Bytes))
		}
	}

	if len(info.Skills) == 0 {
		lines = append(lines, "skills: none")
	} else {
		lines = append(lines, fmt.Sprintf("skills (%d):", len(info.Skills)))
		for _, skill := range info.Skills {
			line := "  " + skill.Name
			if skill.Source != "" {
				line += " [" + skill.Source + "]"
			}
			lines = append(lines, line)
		}
	}
	if len(info.PromptTemplates) == 0 {
		lines = append(lines, "prompt templates: none")
	} else {
		lines = append(lines, fmt.Sprintf("prompt templates (%d):", len(info.PromptTemplates)))
		for _, tmpl := range info.PromptTemplates {
			line := "  " + tmpl.Name
			if tmpl.Source != "" {
				line += " [" + tmpl.Source + "]"
			}
			lines = append(lines, line)
		}
	}
	if len(info.Packages) == 0 {
		lines = append(lines, "resource packages: none")
	} else {
		lines = append(lines, fmt.Sprintf("resource packages (%d):", len(info.Packages)))
		for _, pkg := range info.Packages {
			status := "disabled"
			if pkg.Enabled {
				status = "enabled"
			}
			lines = append(lines, fmt.Sprintf("  %s [%s/%s] skills=%d prompts=%d - %s", pkg.Name, pkg.Source, status, pkg.Skills, pkg.Prompts, pkg.Path))
		}
	}

	r.PrintSection("Context", lines)
}

func bytePresence(n int) string {
	if n <= 0 {
		return "empty"
	}
	return fmt.Sprintf("present (%d bytes)", n)
}

func memoryPresence(enabled, summaryActive bool, bytes int) string {
	if !enabled {
		return "disabled"
	}
	if summaryActive {
		if bytes <= 0 {
			return "summary"
		}
		return fmt.Sprintf("summary (%d bytes)", bytes)
	}
	return bytePresence(bytes)
}

func contextRemainingPresence(tokens int, available bool) string {
	if !available {
		return "unknown"
	}
	return fmt.Sprintf("%d tokens until compaction", tokens)
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func handleDoctor(ctx context.Context, session *coding_agent.CodingSession, r Printer) {
	info := session.GetDoctorInfo(ctx)
	model := "none"
	if info.ModelID != "" {
		model = fmt.Sprintf("%s (%s / %s)", info.ModelName, info.ModelProvider, info.ModelID)
	}
	configPath := info.ModelConfigPath
	if configPath == "" {
		configPath = "(not provided)"
	}
	baseURL := info.ModelBaseURL
	if baseURL == "" {
		baseURL = "(empty)"
	}

	lines := []string{
		"cwd: " + info.Cwd,
		"agent dir: " + info.AgentDir,
		"model config: " + configPath,
		"model: " + model,
		"baseUrl: " + baseURL,
		"baseUrl status: " + info.BaseURLStatus,
		"provider registered: " + yesNo(info.ProviderRegistered),
		"api key: " + info.APIKeyStatus,
		fmt.Sprintf("context files: %d", info.ContextFileCount),
		fmt.Sprintf("MCP: %d server(s), %d tool(s)", info.MCPServerCount, info.MCPToolCount),
	}
	if len(info.Problems) == 0 {
		lines = append(lines, "problems: none")
	} else {
		lines = append(lines, fmt.Sprintf("problems (%d):", len(info.Problems)))
		for _, problem := range info.Problems {
			lines = append(lines, "  "+problem)
		}
	}
	r.PrintSection("Doctor", lines)
}
