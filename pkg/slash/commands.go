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

	"github.com/openmodu/modu/pkg/tgbot"
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

// Handle processes built-in /commands. Returns (handled, shouldExit).
func Handle(ctx context.Context, line string, session *coding_agent.CodingSession, r Printer, model *types.Model) (bool, bool) {
	if !strings.HasPrefix(line, "/") {
		return false, false
	}

	parts := strings.SplitN(line[1:], " ", 2)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "quit", "exit", "q":
		r.PrintInfo("bye!")
		return true, true

	case "help", "h":
		PrintHelp(r)
		return true, false

	case "clear", "new":
		if err := session.ClearConversation(); err != nil {
			r.PrintError(fmt.Errorf("clear session: %w", err))
		} else {
			if cmd == "new" {
				r.PrintInfo("new session")
			} else {
				r.PrintInfo("session cleared")
			}
		}
		if cmd == "clear" {
			if clearer, ok := r.(Clearer); ok {
				clearer.ClearScreen()
			} else {
				fmt.Print("\033[2J\033[H")
			}
		}
		return true, false

	case "model":
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		handleModel(arg, session, r, model)
		return true, false

	case "settings":
		r.PrintInfo("settings are available in the interactive TUI via /settings")
		return true, false

	case "scoped-models":
		r.PrintInfo("model scope editing is available in the interactive TUI via /scoped-models")
		return true, false

	case "compact":
		r.PrintInfo("compacting context…")
		if err := session.Compact(ctx); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("context compacted")
		}
		return true, false

	case "tokens":
		stats := session.GetSessionStats()
		r.PrintInfo(fmt.Sprintf("tokens used this session: %d", stats.TotalTokens))
		return true, false

	case "context":
		handleContext(session, r)
		return true, false

	case "session":
		handleSession(parts, session, r)
		return true, false

	case "name":
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
		return true, false

	case "sessions":
		handleSessions(parts, session, r)
		return true, false

	case "resume":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /resume <session-file>")
			return true, false
		}
		path := strings.TrimSpace(parts[1])
		if err := session.SwitchSession(path); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("resumed session: " + path)
		}
		return true, false

	case "fork-session":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /fork-session <session-file>")
			return true, false
		}
		path := strings.TrimSpace(parts[1])
		if err := session.ForkFromSession(path); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("forked session: " + session.GetSessionFile())
		}
		return true, false

	case "tree":
		handleTree(session, r)
		return true, false

	case "fork":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /fork <entry-id>")
			return true, false
		}
		entryID := strings.TrimSpace(parts[1])
		if err := session.Fork(entryID); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("forked from entry: " + entryID)
		}
		return true, false

	case "clone":
		leafID := session.GetSessionLeafID()
		if leafID == "" {
			r.PrintInfo("nothing to clone")
			return true, false
		}
		path, err := session.CreateBranchedSession(leafID)
		if err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("cloned session: " + path)
		}
		return true, false

	case "branch-session":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /branch-session <entry-id>")
			return true, false
		}
		path, err := session.CreateBranchedSession(strings.TrimSpace(parts[1]))
		if err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("created branched session: " + path)
		}
		return true, false

	case "export":
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
		return true, false

	case "copy":
		text := strings.TrimSpace(session.GetLastAssistantText())
		if text == "" {
			r.PrintInfo("no assistant message to copy")
			return true, false
		}
		if err := copyTextToClipboard(text); err != nil {
			r.PrintError(err)
		} else {
			r.PrintInfo("copied last assistant message")
		}
		return true, false

	case "changelog":
		handleChangelog(session, r)
		return true, false

	case "doctor":
		handleDoctor(ctx, session, r)
		return true, false

	case "retry":
		r.PrintInfo("retry is available in the interactive TUI after a failed prompt")
		return true, false

	case "hotkeys":
		r.PrintSection("Hotkeys", []string{
			"Ctrl+C interrupt/exit",
			"Ctrl+L clear screen",
			"Ctrl+O expand/collapse tool output",
			"Ctrl+P/Ctrl+N cycle models",
			"Shift+Tab toggle plan mode",
			"/settings, /model, /sessions, /tree, /fork",
		})
		return true, false

	case "reload":
		session.ReloadResources()
		r.PrintInfo("reloaded resources")
		return true, false

	case "tools":
		names := session.GetActiveToolNames()
		r.PrintInfo("active tools: " + strings.Join(names, ", "))
		return true, false

	case "allow":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			r.PrintInfo("usage: /allow <tool>  — clear deny decision so the tool is asked again")
			return true, false
		}
		toolName := strings.TrimSpace(parts[1])
		session.ClearToolDecision(toolName)
		r.PrintInfo(fmt.Sprintf("cleared decision for %q — will ask again on next call", toolName))
		return true, false

	case "agents":
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
		return true, false

	case "todos":
		todos := session.GetTodos()
		if len(todos) == 0 {
			r.PrintInfo("no todos")
			return true, false
		}
		r.PrintInfo(fmt.Sprintf("todos (%d):", len(todos)))
		for i, todo := range todos {
			r.PrintInfo(fmt.Sprintf("  %d. [%s] %s", i+1, todo.Status, todo.Content))
		}
		return true, false

	case "tasks":
		tasks := session.GetBackgroundTasks()
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt < tasks[j].CreatedAt })
		if len(tasks) == 0 {
			r.PrintInfo("no background tasks")
			return true, false
		}
		r.PrintInfo(fmt.Sprintf("background tasks (%d):", len(tasks)))
		for _, task := range tasks {
			l := fmt.Sprintf("  %s [%s] %s", task.ID, task.Status, task.Summary)
			if task.Error != "" {
				l += " error=" + task.Error
			}
			r.PrintInfo(l)
		}
		return true, false

	case "plan":
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		switch arg {
		case "", "status":
			status := session.PlanStatus()
			lines := []string{
				"active: " + yesNo(status.Active),
				"latest plan: " + status.PlanFile,
				"latest plan exists: " + yesNo(status.PlanExists),
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
				return true, false
			}
			data, err := os.ReadFile(status.PlanFile)
			if err != nil {
				r.PrintError(err)
				return true, false
			}
			content := strings.TrimSpace(string(data))
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
				return true, false
			}
			lines := make([]string, 0, len(revisions))
			for i, revision := range revisions {
				if i >= 10 {
					break
				}
				lines = append(lines, fmt.Sprintf("%s  %s", revision.ModTime.Format(time.RFC3339), revision.Path))
			}
			r.PrintSection("Plan history", lines)
		default:
			r.PrintInfo("usage: /plan [status|show|history|clear|on|off]")
		}
		return true, false

	case "worktree":
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
				return true, false
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
				return true, false
			}
			if len(removed) == 0 {
				r.PrintInfo("no inactive managed worktrees to cleanup")
				return true, false
			}
			lines := make([]string, 0, len(removed))
			for _, wt := range removed {
				lines = append(lines, "removed "+wt.Path)
			}
			r.PrintSection("Worktree cleanup", lines)
		default:
			r.PrintInfo("usage: /worktree [status|list|cleanup|on|off]")
		}
		return true, false

	case "skills":
		skills := session.GetSkills()
		if len(skills) == 0 {
			r.PrintInfo("no skills found")
			return true, false
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
		return true, false

	case "prompts":
		prompts := session.GetPromptTemplates()
		if len(prompts) == 0 {
			r.PrintInfo("no prompt templates found")
			return true, false
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
		return true, false

	case "telegram":
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		handleTelegram(arg, r)
		return true, false

	default:
		return false, false
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
		fmt.Sprintf("memory: %s", bytePresence(info.MemoryBytes)),
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

func handleTelegram(arg string, r Printer) {
	configPath := tgbot.ConfigPath()

	if strings.HasPrefix(arg, "token ") {
		token := strings.TrimSpace(strings.TrimPrefix(arg, "token "))
		if token == "" {
			r.PrintInfo("usage: /telegram token <bot_token>")
			return
		}
		cfg, err := tgbot.LoadConfig()
		if err != nil {
			cfg = &tgbot.Config{}
		}
		cfg.Token = token
		if err := tgbot.SaveConfig(cfg); err != nil {
			r.PrintError(fmt.Errorf("save telegram config: %w", err))
			return
		}
		r.PrintInfo("Telegram token saved to " + configPath)
		r.PrintInfo("Restart modu_code to start the bot.")
		return
	}

	cfg, err := tgbot.LoadConfig()
	if err != nil {
		r.PrintInfo("telegram config: " + configPath)
		r.PrintError(fmt.Errorf("read config: %w", err))
		return
	}

	r.PrintInfo("telegram config: " + configPath)
	if cfg.Token != "" {
		masked := cfg.Token
		if len(masked) > 8 {
			masked = masked[:4] + strings.Repeat("*", len(masked)-8) + masked[len(masked)-4:]
		}
		r.PrintInfo("  token: " + masked + "  (set)")
	} else {
		r.PrintInfo("  token: (not set)")
		r.PrintInfo("  set with: /telegram token <bot_token>")
	}
	r.PrintInfo("  start: automatic on modu_code startup when a token is set")
}

func PrintHelp(r Printer) {
	lines := []string{
		"/help, /h           — show this help",
		"/quit, /exit        — exit",
		"/clear              — clear the screen",
		"/model              — show or switch model",
		"/model list         — list configured models",
		"/compact            — compact the conversation context",
		"/tokens             — show total token usage",
		"/context            — show current prompt/context sources",
		"/session            — show or name the current session",
		"/sessions [all]     — list saved sessions",
		"/session delete <f> — delete a saved session file",
		"/resume <file>      — switch to a saved session",
		"/fork-session <file> — copy a saved session into this cwd",
		"/tree               — show forkable messages or branch points",
		"/fork <entry-id>    — move the session leaf to an entry",
		"/export [file]      — export the session to HTML",
		"/copy               — copy the last assistant message",
		"/changelog          — show recent git commits",
		"/doctor             — show runtime diagnostics",
		"/retry              — retry last failed prompt in interactive TUI",
		"/tools              — list active tools",
		"/allow <tool>       — clear always-deny so the tool is asked again",
		"/agents             — list discovered subagents",
		"/todos              — show current todo list",
		"/tasks              — show background subagent tasks",
		"/plan [status|show|history|clear|on|off] — inspect, show, clear, or toggle plan mode",
		"/worktree [status|list|cleanup|on|off] — inspect, clean, or toggle isolated worktree mode",
		"/skills             — list available skills",
		"/prompts            — list available prompt templates",
		"/telegram           — show Telegram bot config",
		"/telegram token <t> — set Telegram bot token",
		"",
		"keys",
		"ctrl+j         — insert newline",
		"ctrl+l         — clear conversation buffer",
		"ctrl+o         — toggle expanded tool output",
		"ctrl+c         — interrupt running query / exit when idle",
		"esc            — interrupt running query / dismiss suggestions",
		"tab            — autocomplete slash command",
		"↑ / ↓          — history (or navigate slash suggestions)",
		"",
		"tool approval",
		"y              — allow once",
		"a              — always allow this tool",
		"n / ESC        — deny once",
		"d              — always deny this tool",
	}
	r.PrintSection("Help", lines)
}
