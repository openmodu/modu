package slash

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

	case "clear":
		if err := session.ClearConversation(); err != nil {
			r.PrintError(fmt.Errorf("clear session: %w", err))
		} else {
			r.PrintInfo("session cleared")
		}
		if clearer, ok := r.(Clearer); ok {
			clearer.ClearScreen()
		} else {
			fmt.Print("\033[2J\033[H")
		}
		return true, false

	case "model":
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		handleModel(arg, session, r, model)
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

	case "doctor":
		handleDoctor(ctx, session, r)
		return true, false

	case "retry":
		r.PrintInfo("retry is available in the interactive TUI after a failed prompt")
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
			if session.IsPlanMode() {
				r.PrintInfo("plan mode: on")
			} else {
				r.PrintInfo("plan mode: off")
			}
		case "on":
			session.EnterPlanMode()
			r.PrintInfo("plan mode enabled")
		case "off":
			session.ExitPlanMode("manually exited from /plan off")
			r.PrintInfo("plan mode disabled")
		default:
			r.PrintInfo("usage: /plan [status|on|off]")
		}
		return true, false

	case "worktree":
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		switch arg {
		case "", "status":
			if path := session.ActiveWorktree(); path != "" {
				r.PrintInfo("active worktree: " + path)
			} else {
				r.PrintInfo("active worktree: none")
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
		default:
			r.PrintInfo("usage: /worktree [status|on|off]")
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

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
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
		"/doctor             — show runtime diagnostics",
		"/retry              — retry last failed prompt in interactive TUI",
		"/tools              — list active tools",
		"/allow <tool>       — clear always-deny so the tool is asked again",
		"/agents             — list discovered subagents",
		"/todos              — show current todo list",
		"/tasks              — show background subagent tasks",
		"/plan [on|off]      — inspect or toggle plan mode",
		"/worktree [on|off]  — inspect or toggle isolated worktree mode",
		"/skills             — list available skills",
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
