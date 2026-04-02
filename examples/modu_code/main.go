// modu_code is a Claude Code-style interactive coding assistant built on
// modu's CodingAgent and pkg/tui. It provides a REPL where the AI can read,
// write, and search files in the current working directory.
//
// Provider selection (first matching wins):
//
//	ANTHROPIC_API_KEY  → Anthropic Claude via OpenAI-compat endpoint
//	OPENAI_API_KEY     → OpenAI (model: $OPENAI_MODEL or gpt-4o)
//	DEEPSEEK_API_KEY   → DeepSeek (model: $DEEPSEEK_MODEL or deepseek-chat)
//	OLLAMA_HOST        → Ollama (model: $OLLAMA_MODEL, required)
//
// Additional env vars:
//
//	OPENAI_BASE_URL    → custom base URL for an OpenAI-compat provider
//	THINKING_LEVEL     → off | low | medium | high (default: off)
//	MOMS_TG_TOKEN      → Telegram bot token (required for --telegram mode)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/coding_agent/modes/rpc"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"
)

func main() {
	var (
		printPrompt = flag.String("p", "", "run in print mode: send prompt and output result to stdout")
		printJSON   = flag.Bool("json", false, "with -p: output NDJSON event stream instead of plain text")
		rpcMode     = flag.Bool("rpc", false, "run in RPC mode: JSON-line protocol over stdin/stdout")
		noApprove   = flag.Bool("no-approve", false, "skip user approval for tool executions (auto-allow all)")
	)
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get cwd: %v\n", err)
		os.Exit(1)
	}

	model, getAPIKey := resolveProvider()
	if model == nil {
		fmt.Fprintln(os.Stderr, "no provider configured")
		fmt.Fprintln(os.Stderr, "set one of: ANTHROPIC_API_KEY, OPENAI_API_KEY, DEEPSEEK_API_KEY, OLLAMA_HOST+OLLAMA_MODEL")
		os.Exit(1)
	}

	thinkingLevel := resolveThinkingLevel()
	exampleDir := locateExampleDir()
	agentDir := coding_agent.DefaultAgentDir()
	exampleAgentsDir := filepath.Join(exampleDir, "agents")
	mailboxRuntime, mailboxErr := startMailboxRuntime(agentDir, exampleAgentsDir, cwd, model, getAPIKey)
	if mailboxErr != nil {
		fmt.Fprintf(os.Stderr, "[mailbox] failed to start local runtime: %v\n", mailboxErr)
	}
	if mailboxRuntime != nil {
		defer mailboxRuntime.Close()
	}

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:           cwd,
		AgentDir:      agentDir,
		Model:         model,
		ThinkingLevel: thinkingLevel,
		GetAPIKey:     getAPIKey,
		MailboxClient: mailboxRuntime.Client(),
		ExtraSubagentDirs: []string{
			exampleAgentsDir,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}
	defer session.Close("prompt_input_exit")

	// Print mode: non-interactive, output result then exit.
	if *printPrompt != "" {
		printMode := modes.PrintModeText
		if *printJSON {
			printMode = modes.PrintModeJSON
		}
		if err := modes.RunPrint(context.Background(), modes.PrintOptions{
			Mode:     printMode,
			Messages: []string{*printPrompt},
			Session:  session,
			Output:   os.Stdout,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// RPC mode: long-lived process, JSON-line protocol over stdin/stdout.
	if *rpcMode {
		ctx, cancel := context.WithCancel(context.Background())
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			cancel()
		}()
		if err := rpc.NewRpcMode(session).Run(ctx); err != nil && err != context.Canceled {
			fmt.Fprintf(os.Stderr, "rpc error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	renderer := tui.NewRenderer(os.Stdout)
	input := tui.NewInput(os.Stdin, os.Stdout)
	input.OnCtrlR = renderer.ExpandLastTool
	input.OnPromptChange = renderer.SetActivePrompt

	// Load persisted input history for this project.
	histFile := session.InputHistoryFile()
	if err := input.LoadHistoryFile(histFile); err != nil {
		renderer.PrintInfo(fmt.Sprintf("(warning: failed to load input history: %v)", err))
	}

	// Wire tool approval (default on; disabled with --no-approve).
	var tuiApprovalCh chan tui.ApprovalRequest
	if !*noApprove {
		tuiApprovalCh = make(chan tui.ApprovalRequest, 1)
		input.ApprovalRequests = tuiApprovalCh
		session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
			respCh := make(chan string, 1)
			tuiApprovalCh <- tui.ApprovalRequest{
				ToolName:   toolName,
				ToolCallID: toolCallID,
				Args:       args,
				Response:   respCh,
			}
			return agent.ToolApprovalDecision(<-respCh), nil
		})
	}

	// Wire agent events to the renderer.
	unsub := session.Subscribe(func(ev agent.AgentEvent) {
		renderer.HandleEvent(ev)
	})
	defer unsub()

	// promptMu serializes session.Prompt() calls between the TUI and the
	// Telegram background goroutine so they never run concurrently.
	var promptMu sync.Mutex

	// Wire session events (compaction, model changes, etc.) to the renderer.
	unsubSession := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		switch ev.Type {
		case coding_agent.SessionEventCompactionStart:
			renderer.PrintInfo("compacting context…")
		case coding_agent.SessionEventCompactionDone:
			renderer.PrintInfo("context compacted")
		}
	})
	defer unsubSession()

	// SIGINT: abort the current streaming operation.
	// Ctrl+C during input is handled via ErrInterrupt from ReadLine below.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() {
		for range sigCh {
			if session.IsStreaming() {
				session.Abort()
				renderer.PrintInfo("[interrupted]")
			}
		}
	}()

	// SIGWINCH: terminal resize — notify the input loop to redraw itself.
	resizeCh := make(chan struct{}, 1)
	input.ResizeCh = resizeCh
	sigwinchCh := make(chan os.Signal, 1)
	signal.Notify(sigwinchCh, syscall.SIGWINCH)
	go func() {
		for range sigwinchCh {
			select {
			case resizeCh <- struct{}{}:
			default: // drop if already pending
			}
		}
	}()

	ctx, cancelCtx := context.WithCancel(context.Background())
	exit := func() { cancelCtx(); os.Exit(0) }

	// Auto-start Telegram bot in background if a token is configured.
	// Resolve the bot username before printing the banner so it appears inline.
	var tgUsername string
	{
		token := os.Getenv("MOMS_TG_TOKEN")
		if tgCfg, err := loadTelegramConfig(); err == nil && tgCfg.Token != "" {
			token = tgCfg.Token
		}
		if token != "" {
			attachDir := os.TempDir() + "/modu_code_tg"
			if username, err := startTelegramBackground(ctx, token, attachDir, session, renderer, &promptMu, tuiApprovalCh); err != nil {
				fmt.Fprintf(os.Stderr, "[telegram] failed to start: %v\n", err)
			} else {
				tgUsername = username
			}
		}
	}

	// Restore previous session for this working directory (like Claude Code).
	if n, err := session.RestoreMessages(); err != nil {
		renderer.PrintInfo(fmt.Sprintf("(failed to restore session: %v)", err))
	} else if n > 0 {
		renderer.PrintInfo(fmt.Sprintf("(restored previous session — %d messages)", n))
	}

	renderer.PrintBanner(model.Name, cwd, tgUsername)

	// REPL loop.
	for {
		line, err := input.ReadLine("❯ ")
		if err == tui.ErrInterrupt {
			// Ctrl+C while idle → exit.
			exit()
		}
		if err != nil {
			// EOF (Ctrl+D) — exit cleanly.
			exit()
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Built-in slash commands handled before forwarding to the session.
		if handled, exit := handleSlash(ctx, line, session, renderer, model, mailboxRuntime); handled {
			if exit {
				break
			}
			continue
		}

		renderer.PrintUser(line)

		// Run prompt in a goroutine so the main goroutine can handle
		// scroll events (mouse wheel, Page Up/Down) during AI streaming.
		// Use a cancellable context so Ctrl+C aborts the HTTP request.
		promptCtx, promptCancel := context.WithCancel(ctx)
		promptDone := make(chan struct{})
		var promptErr error
		go func() {
			defer promptCancel()
			// TryLock: if the session is busy (e.g. a Telegram prompt is
			// running), reject immediately instead of deadlocking the TUI.
			if !promptMu.TryLock() {
				renderer.PrintInfo("[busy] session is processing a Telegram message, please wait…")
				close(promptDone)
				return
			}
			defer promptMu.Unlock()
			promptErr = session.Prompt(promptCtx, line)
			close(promptDone)
		}()

		input.RunScrollLoop(promptDone, func() {
			promptCancel()
			session.Abort()
			renderer.PrintInfo("[interrupted]")
		})

		<-promptDone // wait for the goroutine to finish before reading promptErr

		if promptErr != nil {
			renderer.PrintError(promptErr)
		}
		session.WaitForIdle()

		// Persist the conversation so the next startup can resume.
		if err := session.SaveMessages(); err != nil {
			renderer.PrintInfo(fmt.Sprintf("(warning: failed to save session: %v)", err))
		}
		// Persist input history so up-arrow works across restarts.
		if err := input.SaveHistoryFile(histFile); err != nil {
			renderer.PrintInfo(fmt.Sprintf("(warning: failed to save input history: %v)", err))
		}

		stats := session.GetSessionStats()
		renderer.PrintUsage(stats.TotalTokens)
		renderer.PrintSeparator()
	}
}

// handleSlash processes built-in /commands. Returns (handled, shouldExit).
func handleSlash(ctx context.Context, line string, session *coding_agent.CodingSession, r *tui.Renderer, model *types.Model, mailboxRuntime *moduCodeMailboxRuntime) (bool, bool) {
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
		printHelp(r)
		return true, false

	case "clear":
		// Clear the screen AND wipe the saved session so next startup is fresh.
		if err := session.ClearSavedMessages(); err != nil {
			r.PrintError(fmt.Errorf("clear session: %w", err))
		} else {
			r.PrintInfo("session cleared")
		}
		fmt.Print("\033[2J\033[H")
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
		if mailboxRuntime != nil && len(mailboxRuntime.AgentIDs()) > 0 {
			r.PrintInfo("mailbox workers: " + strings.Join(mailboxRuntime.AgentIDs(), ", "))
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
			line := fmt.Sprintf("  %s [%s] %s", task.ID, task.Status, task.Summary)
			if task.Error != "" {
				line += " error=" + task.Error
			}
			r.PrintInfo(line)
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
		r.PrintInfo("runtime paths:")
		r.PrintInfo("  root: " + paths.Root)
		r.PrintInfo("  sessions: " + paths.SessionsDir)
		r.PrintInfo("  plans: " + paths.PlansDir)
		r.PrintInfo("  plan_file: " + paths.PlanFile)
		r.PrintInfo("  worktrees: " + paths.WorktreesDir)
		r.PrintInfo("  tool_results: " + paths.ToolResultsDir)
		r.PrintInfo("  global_memory: " + paths.GlobalMemoryDir)
		r.PrintInfo("  project_memory: " + paths.ProjectMemoryDir)
		return true, false

	case "state":
		r.PrintInfo("runtime state:")
		for _, line := range strings.Split(strings.TrimSpace(session.RuntimeStateJSON()), "\n") {
			r.PrintInfo(line)
		}
		return true, false

	case "git":
		if gitState, ok := session.RuntimeState().Git["available"].(bool); ok && !gitState {
			r.PrintInfo("git: not a repository")
			return true, false
		}
		r.PrintInfo("git preflight:")
		gitData := session.RuntimeState().Git
		raw, _ := json.MarshalIndent(gitData, "", "  ")
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			r.PrintInfo(line)
		}
		return true, false

	case "dashboard":
		printDashboard(r, session)
		return true, false

	case "config":
		r.PrintInfo("effective config:")
		for _, line := range strings.Split(strings.TrimSpace(session.EffectiveConfigJSON()), "\n") {
			r.PrintInfo(line)
		}
		return true, false

	case "config-template":
		r.PrintInfo("default config template:")
		for _, line := range strings.Split(strings.TrimSpace(coding_agent.DefaultConfigTemplate()), "\n") {
			r.PrintInfo(line)
		}
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
			line := "  /" + s.Name
			if s.Description != "" {
				line += " — " + s.Description
			}
			if s.Source != "" {
				line += " [" + s.Source + "]"
			}
			r.PrintInfo(line)
		}
		return true, false

	case "telegram":
		arg := ""
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
		handleTelegramCommand(arg, r)
		return true, false

	default:
		// Let the session handle unknown slash commands (skills, etc.).
		return false, false
	}
}

// handleTelegramCommand processes /telegram [subcommand].
func handleTelegramCommand(arg string, r *tui.Renderer) {
	configPath := telegramConfigPath()

	// /telegram token <token>  — set bot token
	if strings.HasPrefix(arg, "token ") {
		token := strings.TrimSpace(strings.TrimPrefix(arg, "token "))
		if token == "" {
			r.PrintInfo("usage: /telegram token <bot_token>")
			return
		}
		cfg, err := loadTelegramConfig()
		if err != nil {
			cfg = &TelegramConfig{}
		}
		cfg.Token = token
		if err := saveTelegramConfig(cfg); err != nil {
			r.PrintError(fmt.Errorf("save telegram config: %w", err))
			return
		}
		r.PrintInfo("Telegram token saved to " + configPath)
		r.PrintInfo("Run with --telegram to start the bot.")
		return
	}

	// /telegram  — show status
	cfg, err := loadTelegramConfig()
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

func printHelp(r *tui.Renderer) {
	lines := []string{
		"built-in commands:",
		"  /help, /h           — show this help",
		"  /quit, /exit        — exit",
		"  /clear              — clear the screen",
		"  /model              — show current model",
		"  /compact            — compact the conversation context",
		"  /tokens             — show total token usage",
		"  /tools              — list active tools",
		"  /agents             — list discovered subagents and mailbox workers",
		"  /todos              — show current todo list",
		"  /tasks              — show background subagent tasks",
		"  /hints              — show pending harness-only hints",
		"  /runtime            — show harness runtime paths",
		"  /git                — show structured git preflight state",
		"  /dashboard          — show runtime summary and latest events",
		"  /state              — show unified runtime state snapshot",
		"  /config             — show effective merged config",
		"  /config-template    — show the default config template",
		"  /logs               — show configured harness JSONL logs",
		"  /artifacts          — show configured harness latest snapshots",
		"  /bridge             — show configured harness event bridge dirs",
		"  /actions            — show latest harness action statuses",
		"  /plan [on|off]      — inspect or toggle plan mode",
		"  /worktree [on|off]  — inspect or toggle isolated worktree mode",
		"  /skills             — list available skills",
		"  /telegram           — show Telegram bot config",
		"  /telegram token <t> — set Telegram bot token",
		"",
		"keyboard:",
		"  Enter          — send message",
		"  Ctrl+R         — expand last tool call output",
		"  Ctrl+C         — abort current operation (or exit when idle)",
		"  Ctrl+D         — exit",
		"",
		"tool approval (when prompted):",
		"  y              — allow once",
		"  a              — always allow this tool",
		"  n / ESC        — deny once",
		"  d              — always deny this tool",
	}
	for _, l := range lines {
		r.PrintInfo(l)
	}
}

func printHarnessTargets(r *tui.Renderer, title string, session *coding_agent.CodingSession, targets map[string]string, dirMode bool) {
	r.PrintInfo(title + ":")
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
		r.PrintInfo("  " + key + ": " + abs)
		if dirMode {
			printRecentDirEntries(r, abs)
		} else {
			printFilePreview(r, abs)
		}
	}
	if !seenAny {
		r.PrintInfo("  (not configured)")
	}
}

func printHarnessActions(r *tui.Renderer, session *coding_agent.CodingSession) {
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
	r.PrintInfo("harness action status files:")
	for _, entry := range dirs {
		path := filepath.Join(base, entry.Name(), "latest.json")
		r.PrintInfo("  " + entry.Name() + ": " + path)
		printFilePreview(r, path)
	}
}

func printDashboard(r *tui.Renderer, session *coding_agent.CodingSession) {
	r.PrintInfo("runtime dashboard:")
	state := session.RuntimeState()
	r.PrintInfo(fmt.Sprintf("  session: %s", state.SessionID))
	r.PrintInfo(fmt.Sprintf("  cwd: %s", state.Cwd))
	r.PrintInfo(fmt.Sprintf("  model: %s/%s", state.Model["provider"], state.Model["id"]))
	r.PrintInfo(fmt.Sprintf("  thinking: %s", state.Thinking))
	r.PrintInfo(fmt.Sprintf("  modes: plan=%v worktree=%v streaming=%v", state.Modes["plan"], state.Modes["worktree"], state.Modes["streaming"]))
	r.PrintInfo(fmt.Sprintf("  counts: messages=%d todos=%d tasks=%d tools=%d", state.Counts["messages"], state.Counts["todos"], state.Counts["tasks"], state.Counts["tools"]))
	if gitInfo, ok := state.Git["inGitRepository"].(bool); ok && gitInfo {
		r.PrintInfo(fmt.Sprintf("  git: staged=%d unstaged=%d untracked=%d", len(asSlice(state.Git["stagedFiles"])), len(asSlice(state.Git["unstagedFiles"])), len(asSlice(state.Git["untrackedFiles"]))))
	} else {
		r.PrintInfo("  git: not a repository")
	}
	if data, err := os.ReadFile(session.RuntimePaths().RuntimeIndexFile); err == nil {
		var payload map[string]any
		if json.Unmarshal(data, &payload) == nil {
			r.PrintInfo("  latest events:")
			if last, ok := payload["last_events"].(map[string]any); ok {
				keys := make([]string, 0, len(last))
				for key := range last {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					raw, _ := json.Marshal(last[key])
					line := string(raw)
					if len(line) > 180 {
						line = line[:180] + "..."
					}
					r.PrintInfo("    " + key + ": " + line)
				}
			}
		}
	}
	printHarnessActions(r, session)
}

func asSlice(v any) []any {
	items, _ := v.([]any)
	return items
}

func printRecentDirEntries(r *tui.Renderer, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		r.PrintInfo("    status: " + err.Error())
		return
	}
	if len(entries) == 0 {
		r.PrintInfo("    status: empty")
		return
	}
	count := len(entries)
	if count > 5 {
		count = 5
	}
	start := len(entries) - count
	for _, entry := range entries[start:] {
		r.PrintInfo("    - " + entry.Name())
	}
}

func printFilePreview(r *tui.Renderer, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		r.PrintInfo("    status: " + err.Error())
		return
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		r.PrintInfo("    status: empty")
		return
	}
	if len(text) > 160 {
		text = text[:160] + "..."
	}
	r.PrintInfo("    preview: " + text)
}

func locateExampleDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Dir(file)
}

type moduCodeConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
}

// resolveProvider returns the model and GetAPIKey function based on env vars.
func resolveProvider() (*types.Model, func(string) (string, error)) {
	// 1. Anthropic Claude via OpenAI-compat endpoint.
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		modelID := os.Getenv("ANTHROPIC_MODEL")
		if modelID == "" {
			modelID = "claude-sonnet-4-6"
		}
		providers.Register(openai.New(
			"anthropic",
			openai.WithBaseURL("https://api.anthropic.com/v1"),
			openai.WithAPIKey(key),
			openai.WithHeaders(map[string]string{
				"anthropic-version": "2023-06-01",
			}),
		))
		model := &types.Model{
			ID:         modelID,
			Name:       "Claude " + modelID,
			ProviderID: "anthropic",
		}
		return model, func(provider string) (string, error) {
			if provider == "anthropic" {
				return key, nil
			}
			return "", fmt.Errorf("no key for %s", provider)
		}
	}

	// 2. OpenAI (or any OpenAI-compat base URL).
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		modelID := os.Getenv("OPENAI_MODEL")
		if modelID == "" {
			modelID = "gpt-4o"
		}
		baseURL := os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		providers.Register(openai.New(
			"openai",
			openai.WithBaseURL(baseURL),
			openai.WithAPIKey(key),
		))
		model := &types.Model{
			ID:         modelID,
			Name:       "OpenAI " + modelID,
			ProviderID: "openai",
		}
		return model, func(provider string) (string, error) {
			if provider == "openai" {
				return key, nil
			}
			return "", fmt.Errorf("no key for %s", provider)
		}
	}

	// 3. DeepSeek.
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		modelID := os.Getenv("DEEPSEEK_MODEL")
		if modelID == "" {
			modelID = "deepseek-chat"
		}
		providers.Register(openai.New(
			"deepseek",
			openai.WithBaseURL("https://api.deepseek.com/v1"),
			openai.WithAPIKey(key),
		))
		model := &types.Model{
			ID:         modelID,
			Name:       "DeepSeek " + modelID,
			ProviderID: "deepseek",
		}
		return model, func(provider string) (string, error) {
			if provider == "deepseek" {
				return key, nil
			}
			return "", fmt.Errorf("no key for %s", provider)
		}
	}

	// 4. Ollama (local).
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		modelID := os.Getenv("OLLAMA_MODEL")
		if modelID == "" {
			fmt.Fprintln(os.Stderr, "OLLAMA_HOST set but OLLAMA_MODEL is empty")
			return nil, nil
		}
		providers.Register(openai.New(
			"ollama",
			openai.WithBaseURL(strings.TrimRight(host, "/")+"/v1"),
		))
		model := &types.Model{
			ID:         modelID,
			Name:       modelID + " (Ollama)",
			ProviderID: "ollama",
		}
		return model, func(provider string) (string, error) { return "", nil }
	}

	// 5. LM Studio local server (opt-in: requires LMSTUDIO_MODEL or LMSTUDIO_BASE_URL).
	if lmModel, lmURL := os.Getenv("LMSTUDIO_MODEL"), os.Getenv("LMSTUDIO_BASE_URL"); lmModel != "" || lmURL != "" {
		modelName := lmModel
		if modelName == "" {
			modelName = "qwen/qwen3.5-35b-a3b"
		}
		baseURL := lmURL
		if baseURL == "" {
			baseURL = "http://localhost:1234/v1"
		}
		providers.Register(openai.New(
			"lmstudio",
			openai.WithBaseURL(baseURL),
		))
		model := &types.Model{
			ID:         modelName,
			Name:       modelName + " (LM Studio)",
			ProviderID: "lmstudio",
		}
		return model, func(provider string) (string, error) { return "", nil }
	}

	// 6. ~/.coding_agent/config.json
	if cfg, ok := loadModuCodeConfig(); ok {
		return registerConfiguredProvider(cfg)
	}

	// 7. Built-in local default for this environment.
	return registerConfiguredProvider(moduCodeConfig{
		Provider: "lmstudio",
		Model:    "qwen/qwen3.5-35b-a3b",
		BaseURL:  "http://192.168.5.149:1234/v1",
		APIKey:   "lm-studio",
	})
}

func loadModuCodeConfig() (moduCodeConfig, bool) {
	path := filepath.Join(coding_agent.DefaultAgentDir(), "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return moduCodeConfig{}, false
	}
	var cfg moduCodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return moduCodeConfig{}, false
	}
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.Provider == "" || cfg.Model == "" || cfg.BaseURL == "" {
		return moduCodeConfig{}, false
	}
	return cfg, true
}

func registerConfiguredProvider(cfg moduCodeConfig) (*types.Model, func(string) (string, error)) {
	providerID := cfg.Provider
	baseURL := cfg.BaseURL
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "lm-studio"
	}

	providers.Register(openai.New(
		providerID,
		openai.WithBaseURL(baseURL),
		openai.WithAPIKey(apiKey),
	))

	model := &types.Model{
		ID:         cfg.Model,
		Name:       cfg.Model + " (" + providerID + ")",
		ProviderID: providerID,
		BaseURL:    baseURL,
	}
	return model, func(provider string) (string, error) {
		if provider == providerID {
			return apiKey, nil
		}
		return "", fmt.Errorf("no key for %s", provider)
	}
}

// resolveThinkingLevel maps the THINKING_LEVEL env var to an agent.ThinkingLevel.
func resolveThinkingLevel() agent.ThinkingLevel {
	switch strings.ToLower(os.Getenv("THINKING_LEVEL")) {
	case "low":
		return agent.ThinkingLevelLow
	case "medium":
		return agent.ThinkingLevelMedium
	case "high":
		return agent.ThinkingLevelHigh
	default:
		return agent.ThinkingLevelOff
	}
}
