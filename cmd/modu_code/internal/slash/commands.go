package slash

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_code/internal/mailboxrt"
	"github.com/openmodu/modu/cmd/modu_code/internal/tgbot"
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
func Handle(ctx context.Context, line string, session *coding_agent.CodingSession, r Printer, model *types.Model, rt *mailboxrt.Runtime) (bool, bool) {
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
		if err := session.ClearSavedMessages(); err != nil {
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
		r.PrintInfo(fmt.Sprintf("current model: %s (%s / %s)", model.Name, model.ProviderID, model.ID))
		r.PrintInfo("restart with a different env var to switch models")
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
		if rt != nil && len(rt.AgentIDs()) > 0 {
			r.PrintInfo("mailbox workers: " + strings.Join(rt.AgentIDs(), ", "))
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

	case "hints":
		hints := session.GetPendingHarnessHints()
		if len(hints) == 0 {
			r.PrintInfo("no harness hints")
			return true, false
		}
		r.PrintInfo(fmt.Sprintf("harness hints (%d):", len(hints)))
		for _, hint := range hints {
			r.PrintInfo(fmt.Sprintf("  v%d %s=%s [%s]", hint.Version, hint.Type, hint.Value, hint.SourceTool))
		}
		return true, false

	case "runtime":
		paths := session.RuntimePaths()
		r.PrintSection("Runtime Paths", []string{
			"root: " + paths.Root,
			"sessions: " + paths.SessionsDir,
			"plans: " + paths.PlansDir,
			"plan_file: " + paths.PlanFile,
			"worktrees: " + paths.WorktreesDir,
			"tool_results: " + paths.ToolResultsDir,
			"global_memory: " + paths.GlobalMemoryDir,
			"project_memory: " + paths.ProjectMemoryDir,
		})
		return true, false

	case "state":
		r.PrintSection("Runtime State", strings.Split(strings.TrimSpace(session.RuntimeStateJSON()), "\n"))
		return true, false

	case "dashboard":
		PrintDashboard(r, session)
		return true, false

	case "config":
		r.PrintSection("Effective Config", strings.Split(strings.TrimSpace(session.EffectiveConfigJSON()), "\n"))
		return true, false

	case "config-template":
		r.PrintSection("Default Config Template", strings.Split(strings.TrimSpace(coding_agent.DefaultConfigTemplate()), "\n"))
		return true, false

	case "logs":
		printHarnessTargets(r, "harness log files", session, map[string]string{
			"tool_use":   session.GetConfig().Harness.LogFiles.ToolUse,
			"compact":    session.GetConfig().Harness.LogFiles.Compact,
			"subagent":   session.GetConfig().Harness.LogFiles.Subagent,
			"session":    session.GetConfig().Harness.LogFiles.Session,
			"permission": session.GetConfig().Harness.LogFiles.Permission,
		}, false)
		return true, false

	case "artifacts":
		printHarnessTargets(r, "harness artifact files", session, map[string]string{
			"tool_use":   session.GetConfig().Harness.ArtifactFiles.ToolUse,
			"compact":    session.GetConfig().Harness.ArtifactFiles.Compact,
			"subagent":   session.GetConfig().Harness.ArtifactFiles.Subagent,
			"session":    session.GetConfig().Harness.ArtifactFiles.Session,
			"permission": session.GetConfig().Harness.ArtifactFiles.Permission,
		}, false)
		return true, false

	case "bridge":
		printHarnessTargets(r, "harness bridge directories", session, map[string]string{
			"tool_use":   session.GetConfig().Harness.BridgeDirs.ToolUse,
			"compact":    session.GetConfig().Harness.BridgeDirs.Compact,
			"subagent":   session.GetConfig().Harness.BridgeDirs.Subagent,
			"session":    session.GetConfig().Harness.BridgeDirs.Session,
			"permission": session.GetConfig().Harness.BridgeDirs.Permission,
		}, true)
		return true, false

	case "actions":
		printHarnessActions(r, session)
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
		r.PrintInfo("Run with --telegram to start the bot.")
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
	r.PrintInfo("  start with: modu_code --telegram")
}

func PrintHelp(r Printer) {
	lines := []string{
		"/help, /h           — show this help",
		"/quit, /exit        — exit",
		"/clear              — clear the screen",
		"/model              — show current model",
		"/compact            — compact the conversation context",
		"/tokens             — show total token usage",
		"/tools              — list active tools",
		"/agents             — list discovered subagents and mailbox workers",
		"/todos              — show current todo list",
		"/tasks              — show background subagent tasks",
		"/hints              — show pending harness-only hints",
		"/runtime            — show harness runtime paths",
		"/dashboard          — show runtime summary and latest events",
		"/state              — show unified runtime state snapshot",
		"/config             — show effective merged config",
		"/config-template    — show the default config template",
		"/logs               — show configured harness JSONL logs",
		"/artifacts          — show configured harness latest snapshots",
		"/bridge             — show configured harness event bridge dirs",
		"/actions            — show latest harness action statuses",
		"/plan [on|off]      — inspect or toggle plan mode",
		"/worktree [on|off]  — inspect or toggle isolated worktree mode",
		"/skills             — list available skills",
		"/telegram           — show Telegram bot config",
		"/telegram token <t> — set Telegram bot token",
		"",
		"vim modes",
		"esc            — enter Normal mode (viewport navigation + yank)",
		"i / a          — return to Insert mode",
		"",
		"Normal mode keys",
		"j / k          — scroll down / up",
		"ctrl+d / u     — half page down / up",
		"ctrl+f / b     — full page down / up",
		"G / gg         — bottom / top",
		"yy             — yank last AI response to clipboard",
		"yG             — yank entire conversation to clipboard",
		"",
		"tool approval",
		"y              — allow once",
		"a              — always allow this tool",
		"n / ESC        — deny once",
		"d              — always deny this tool",
	}
	r.PrintSection("Help", lines)
}

func PrintDashboard(r Printer, session *coding_agent.CodingSession) {
	state := session.RuntimeState()
	lines := []string{
		fmt.Sprintf("session: %s", state.SessionID),
		fmt.Sprintf("cwd: %s", state.Cwd),
		fmt.Sprintf("model: %s/%s", state.Model["provider"], state.Model["id"]),
		fmt.Sprintf("thinking: %s", state.Thinking),
		fmt.Sprintf("modes: plan=%v worktree=%v streaming=%v", state.Modes["plan"], state.Modes["worktree"], state.Modes["streaming"]),
		fmt.Sprintf("counts: messages=%d todos=%d tasks=%d tools=%d", state.Counts["messages"], state.Counts["todos"], state.Counts["tasks"], state.Counts["tools"]),
	}
	if gitInfo, ok := state.Git["inGitRepository"].(bool); ok && gitInfo {
		lines = append(lines, fmt.Sprintf("git: staged=%d unstaged=%d untracked=%d",
			len(asSlice(state.Git["stagedFiles"])), len(asSlice(state.Git["unstagedFiles"])), len(asSlice(state.Git["untrackedFiles"]))))
	} else {
		lines = append(lines, "git: not a repository")
	}
	if data, err := os.ReadFile(session.RuntimePaths().RuntimeIndexFile); err == nil {
		var payload map[string]any
		if json.Unmarshal(data, &payload) == nil {
			lines = append(lines, "", "latest events:")
			if last, ok := payload["last_events"].(map[string]any); ok {
				keys := make([]string, 0, len(last))
				for key := range last {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					raw, _ := json.Marshal(last[key])
					l := string(raw)
					if len(l) > 180 {
						l = l[:180] + "..."
					}
					lines = append(lines, "  "+key+": "+l)
				}
			}
		}
	}
	r.PrintSection("Runtime Dashboard", lines)
	printHarnessActions(r, session)
}

func printHarnessTargets(r Printer, title string, session *coding_agent.CodingSession, targets map[string]string, dirMode bool) {
	lines := make([]string, 0, len(targets)*3)
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	agentDir := session.RuntimePaths().Root
	seenAny := false
	for _, key := range keys {
		target := strings.TrimSpace(targets[key])
		if target == "" {
			continue
		}
		seenAny = true
		abs := target
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(agentDir, abs)
		}
		lines = append(lines, key+": "+abs)
		if dirMode {
			lines = append(lines, formatRecentDirEntries(abs)...)
		} else {
			lines = append(lines, formatFilePreview(abs)...)
		}
	}
	if !seenAny {
		lines = append(lines, "(not configured)")
	}
	r.PrintSection(title, lines)
}

func printHarnessActions(r Printer, session *coding_agent.CodingSession) {
	base := filepath.Join(session.RuntimePaths().RuntimeDir, "actions")
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			r.PrintInfo("no harness action status files")
			return
		}
		r.PrintError(err)
		return
	}
	dirs := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry)
		}
	}
	if len(dirs) == 0 {
		r.PrintInfo("no harness action status files")
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	lines := make([]string, 0, len(dirs)*2)
	for _, entry := range dirs {
		path := filepath.Join(base, entry.Name(), "latest.json")
		lines = append(lines, entry.Name()+": "+path)
		lines = append(lines, formatFilePreview(path)...)
	}
	r.PrintSection("Harness Action Status Files", lines)
}

func asSlice(v any) []any {
	items, _ := v.([]any)
	return items
}

func formatRecentDirEntries(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"  status: " + err.Error()}
	}
	if len(entries) == 0 {
		return []string{"  status: empty"}
	}
	count := min(5, len(entries))
	lines := make([]string, 0, count)
	for _, entry := range entries[len(entries)-count:] {
		lines = append(lines, "  - "+entry.Name())
	}
	return lines
}

func formatFilePreview(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{"  status: " + err.Error()}
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return []string{"  status: empty"}
	}
	if len(text) > 160 {
		text = text[:160] + "..."
	}
	return []string{"  preview: " + text}
}
