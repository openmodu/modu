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
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/crosszan/modu/pkg/agent"
	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
	"github.com/crosszan/modu/pkg/coding_agent/modes"
	"github.com/crosszan/modu/pkg/coding_agent/modes/rpc"
	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/providers/openai"
	"github.com/crosszan/modu/pkg/tui"
	"github.com/crosszan/modu/pkg/types"
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

	renderer := tui.NewRenderer(os.Stdout)
	input := tui.NewInput(os.Stdin, os.Stdout)
	input.OnCtrlR = renderer.ExpandLastTool
	input.OnPromptChange = renderer.SetActivePrompt

	// Wire tool approval (default on; disabled with --no-approve).
	if !*noApprove {
		approvalCh := make(chan tui.ApprovalRequest, 1)
		input.ApprovalRequests = approvalCh
		session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
			respCh := make(chan string, 1)
			approvalCh <- tui.ApprovalRequest{
				ToolName:   toolName,
				ToolCallID: toolCallID,
				Args:       args,
				Response:   respCh,
			}
			decision := <-respCh
			return agent.ToolApprovalDecision(decision), nil
		})
	}

	renderer.PrintBanner(model.Name, cwd)

	// Wire agent events to the renderer.
	unsub := session.Subscribe(func(ev agent.AgentEvent) {
		renderer.HandleEvent(ev)
	})
	defer unsub()

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

	exit := func() { os.Exit(0) }

	ctx := context.Background()

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
		if handled, exit := handleSlash(ctx, line, session, renderer, model); handled {
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

		stats := session.GetSessionStats()
		renderer.PrintUsage(stats.TotalTokens)
		renderer.PrintSeparator()
	}
}

// handleSlash processes built-in /commands. Returns (handled, shouldExit).
func handleSlash(ctx context.Context, line string, session *coding_agent.CodingSession, r *tui.Renderer, model *types.Model) (bool, bool) {
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

	default:
		// Let the session handle unknown slash commands (skills, etc.).
		return false, false
	}
}

func printHelp(r *tui.Renderer) {
	lines := []string{
		"built-in commands:",
		"  /help, /h      — show this help",
		"  /quit, /exit   — exit",
		"  /clear         — clear the screen",
		"  /model         — show current model",
		"  /compact       — compact the conversation context",
		"  /tokens        — show total token usage",
		"  /tools         — list active tools",
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
