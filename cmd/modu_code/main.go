// modu_code is an interactive coding assistant built on modu's CodingAgent and
// pkg/tui. It provides a REPL where the AI can read, write, and search files in
// the current working directory.
//
// Provider selection (first matching wins):
//
//	ANTHROPIC_API_KEY  → Anthropic via OpenAI-compatible endpoint
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
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
	_ "github.com/openmodu/modu/pkg/coding_agent/extension/goal"     // register builtin extension via init()
	_ "github.com/openmodu/modu/pkg/coding_agent/extension/subagent" // register builtin extension via init()
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/coding_agent/modes/rpc"

	"github.com/openmodu/modu/cmd/modu_code/internal/acp"
	"github.com/openmodu/modu/cmd/modu_code/internal/provider"
	"github.com/openmodu/modu/pkg/tui"
)

var runTUI = tui.RunWithOptions

func main() {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		if err := runConfigCommand(os.Args[2:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			os.Exit(1)
		}
		return
	}

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
		fmt.Fprintf(os.Stderr, "configure models in %s or set one of: ANTHROPIC_API_KEY, OPENAI_API_KEY, DEEPSEEK_API_KEY, OLLAMA_HOST+OLLAMA_MODEL\n", provider.ConfigPath())
		os.Exit(1)
	}

	thinkingLevel := provider.ResolveThinkingLevel()
	agentDir := coding_agent.DefaultAgentDir()

	// Out is intentionally nil here: writing to stderr from the goal
	// extension bypasses the TUI's inline-mode widget management and
	// corrupts the screen (Tokens/Status/Time fields land in arbitrary
	// rows). All user-facing notifications still reach the scrollback via
	// api.Notify -> SessionEventExtensionNotify -> a "section" uiBlock,
	// which is the only path the TUI can safely render multi-line text.
	// Resolve the active extension set from ~/.modu_code/extensions.yaml
	// (falls back to every builtin when the file is absent — that keeps the
	// default install behaviorally identical to "goal always on").
	exts, err := extension.LoadEnabled(extension.LoadOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load extensions: %v\n", err)
		os.Exit(1)
	}

	sessionOpts := coding_agent.CodingSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		Model:           model,
		ThinkingLevel:   thinkingLevel,
		GetAPIKey:       getAPIKey,
		ScopedModels:    provider.ConfiguredModelIDs(),
		ModelConfigPath: provider.ConfigPath(),
		Extensions:      exts,
	}
	session, err := coding_agent.NewCodingSession(sessionOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}
	defer session.Close("prompt_input_exit")
	unsubModelPersist := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		if ev.Type != coding_agent.SessionEventModelChange || ev.Provider == "" || ev.ModelID == "" {
			return
		}
		_ = provider.SaveActiveModel(ev.Provider, ev.ModelID)
	})
	defer unsubModelPersist()

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
	if err := runTUI(ctx, session, model, *noApprove, tui.RunOptions{CommandHooks: tui.CommandHooks{
		Config: runConfigHook,
	}}); err != nil {
		fmt.Fprintf(os.Stderr, "ui error: %v\n", err)
		os.Exit(1)
	}
}

func runConfigHook(args string) (string, error) {
	fields := strings.Fields(args)
	var out bytes.Buffer
	err := runConfigCommand(fields, &out, nil)
	return out.String(), err
}

func runConfigCommand(args []string, stdout, stderr io.Writer) error {
	_ = stderr
	if len(args) == 0 {
		return fmt.Errorf("usage: modu_code config <example|init|validate>")
	}
	switch args[0] {
	case "example":
		_, err := fmt.Fprint(stdout, provider.ExampleConfigJSON())
		return err
	case "init":
		force := len(args) > 1 && args[1] == "--force"
		if len(args) > 2 || (len(args) == 2 && !force) {
			return fmt.Errorf("usage: modu_code config init [--force]")
		}
		path, err := provider.InitConfig(force)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "wrote config: %s\n", path)
		return err
	case "validate":
		if len(args) != 1 {
			return fmt.Errorf("usage: modu_code config validate")
		}
		result := provider.ValidateConfig()
		fmt.Fprintf(stdout, "config: %s\n", result.Path)
		fmt.Fprintf(stdout, "models: %d\n", result.ModelCount)
		if result.Active != "" {
			fmt.Fprintf(stdout, "active: %s\n", result.Active)
		}
		if len(result.Problems) == 0 {
			_, err := fmt.Fprintln(stdout, "status: ok")
			return err
		}
		fmt.Fprintf(stdout, "problems (%d):\n", len(result.Problems))
		for _, problem := range result.Problems {
			fmt.Fprintf(stdout, "  - %s\n", problem)
		}
		return fmt.Errorf("config validation failed")
	default:
		return fmt.Errorf("unknown config command %q; expected example, init, or validate", strings.TrimSpace(args[0]))
	}
}
