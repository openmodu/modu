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
//	MOMS_TG_TOKEN      → Telegram bot token (enables Telegram bot)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/coding_agent/modes/rpc"

	"github.com/openmodu/modu/cmd/modu_code/internal/acp"
	"github.com/openmodu/modu/cmd/modu_code/internal/provider"
	"github.com/openmodu/modu/pkg/tui"
)

func main() {
	var (
		printPrompt = flag.String("p", "", "run in print mode: send prompt and output result to stdout")
		printJSON   = flag.Bool("json", false, "with -p: output NDJSON event stream instead of plain text")
		rpcMode     = flag.Bool("rpc", false, "run in RPC mode: JSON-line protocol over stdin/stdout")
		noApprove   = flag.Bool("no-approve", false, "skip user approval for tool executions (auto-allow all)")
		acpMode     = flag.Bool("acp", false, "run as ACP stdio server (JSON-RPC 2.0 LDJSON)")
	)
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get cwd: %v\n", err)
		os.Exit(1)
	}

	model, getAPIKey := provider.Resolve()
	if model == nil {
		fmt.Fprintln(os.Stderr, "no provider configured")
		fmt.Fprintln(os.Stderr, "set one of: ANTHROPIC_API_KEY, OPENAI_API_KEY, DEEPSEEK_API_KEY, OLLAMA_HOST+OLLAMA_MODEL")
		os.Exit(1)
	}

	thinkingLevel := provider.ResolveThinkingLevel()
	agentDir := coding_agent.DefaultAgentDir()

	sessionOpts := coding_agent.CodingSessionOptions{
		Cwd:           cwd,
		AgentDir:      agentDir,
		Model:         model,
		ThinkingLevel: thinkingLevel,
		GetAPIKey:     getAPIKey,
	}
	if *acpMode {
		sessionOpts.CustomSystemPrompt = "You are Modu Code, an AI coding assistant. " +
			"Do not refer to yourself as Claude, GPT, Gemini, or any other model name. " +
			"You are Modu Code."
	}
	session, err := coding_agent.NewCodingSession(sessionOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}
	defer session.Close("prompt_input_exit")

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

	if *acpMode {
		ctx, cancel := context.WithCancel(context.Background())
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			cancel()
		}()
		if err := acp.New(session).Run(ctx); err != nil && err != context.Canceled {
			fmt.Fprintf(os.Stderr, "acp error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tui.Run(ctx, session, model, *noApprove); err != nil {
		fmt.Fprintf(os.Stderr, "ui error: %v\n", err)
		os.Exit(1)
	}
}
