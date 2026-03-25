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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

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

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:           cwd,
		Model:         model,
		ThinkingLevel: thinkingLevel,
		GetAPIKey:     getAPIKey,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}

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

	// ── bubbletea REPL ────────────────────────────────────────────────────────

	// contentCh receives rendered text from BTRenderer and feeds the viewport.
	contentCh := make(chan string, 4096)
	renderer := tui.NewBTRenderer(termWidthOrDefault(), func(text string) {
		select {
		case contentCh <- text:
		default: // drop if buffer full (shouldn't happen at 4096)
		}
	})

	// promptMu serializes session.Prompt() calls between the TUI and any
	// background goroutine (e.g. Telegram) so they never run concurrently.
	var promptMu sync.Mutex

	// Wire session events (compaction, etc.) to the renderer.
	unsubSession := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		switch ev.Type {
		case coding_agent.SessionEventCompactionStart:
			renderer.PrintInfo("compacting context…")
		case coding_agent.SessionEventCompactionDone:
			renderer.PrintInfo("context compacted")
		}
	})
	defer unsubSession()

	// Wire tool approval (default on; disabled with --no-approve).
	var tuiApprovalCh chan tui.ApprovalRequest
	if !*noApprove {
		tuiApprovalCh = make(chan tui.ApprovalRequest, 1)
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

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	// SIGTERM: clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancelCtx()
	}()

	// Auto-start Telegram bot in background if a token is configured.
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

	// Restore previous session and write banner into the content channel
	// before launching bubbletea so the viewport shows it on first render.
	if n, err := session.RestoreMessages(); err != nil {
		renderer.PrintInfo(fmt.Sprintf("(failed to restore session: %v)", err))
	} else if n > 0 {
		renderer.PrintInfo(fmt.Sprintf("(restored previous session — %d messages)", n))
	}
	renderer.PrintBanner(model.Name, cwd, tgUsername)

	// Load persisted input history.
	histFile := session.InputHistoryFile()
	history := loadHistoryLines(histFile)

	// Create and run the bubbletea program.
	btm := newReplModel(session, renderer, contentCh, tuiApprovalCh,
		model, cwd, history, &promptMu, ctx)

	p := tea.NewProgram(btm)
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Save input history on exit.
	if fm, ok := finalModel.(replModel); ok {
		_ = saveHistoryLines(histFile, fm.history)
	}
}

// handleTelegramCommand processes /telegram [subcommand].
func handleTelegramCommand(arg string, r *tui.BTRenderer) {
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

func termWidthOrDefault() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

func printHelp(r *tui.BTRenderer) {
	lines := []string{
		"built-in commands:",
		"  /help, /h           — show this help",
		"  /quit, /exit        — exit",
		"  /clear              — clear the screen",
		"  /model              — show current model",
		"  /compact            — compact the conversation context",
		"  /tokens             — show total token usage",
		"  /tools              — list active tools",
		"  /skills             — list available skills",
		"  /telegram           — show Telegram bot config",
		"  /telegram token <t> — set Telegram bot token",
		"",
		"keyboard:",
		"  Enter          — send message",
		"  Ctrl+R         — expand last tool call output",
		"  Ctrl+Y         — copy all content to clipboard",
		"  Esc            — abort current operation",
		"  Ctrl+C         — exit (when idle)",
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

	// 5. Default: LM Studio local server.
	{
		// modelName := "zai-org/glm-4.7-flash"
		modelName := "qwen/qwen3.5-35b-a3b"
		baseURL := "http://192.168.5.149:1234/v1"
		providerID := "lmstudio"

		if m := os.Getenv("LMSTUDIO_MODEL"); m != "" {
			modelName = m
		}
		if u := os.Getenv("LMSTUDIO_BASE_URL"); u != "" {
			baseURL = u
		}

		providers.Register(openai.New(
			providerID,
			openai.WithBaseURL(baseURL),
		))
		model := &types.Model{
			ID:         modelName,
			Name:       modelName + " (LM Studio)",
			ProviderID: providerID,
		}
		return model, func(provider string) (string, error) { return "", nil }
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
