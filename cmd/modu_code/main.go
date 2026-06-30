// modu_code is an interactive coding assistant built on modu's CodingAgent and
// pkg/modu-tui. It provides a REPL where the AI can read, write, and search
// files in the current working directory.
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
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/coding_agent/modes/rpc"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	_ "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/goal"     // register builtin extension via init()
	_ "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/subagent" // register builtin extension via init()
	_ "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/workflow" // register builtin extension via init()

	"github.com/openmodu/modu/cmd/modu_code/internal/acp"
	"github.com/openmodu/modu/cmd/modu_code/internal/provider"
)

var runTUI = runModuTUI
var interactiveExitOutput io.Writer = os.Stdout

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
		worktree    = flag.Bool("worktree", false, "start in an isolated git worktree")
		noWorktree  = flag.Bool("no-worktree", false, "deprecated: current checkout is already the default")
		resumeID    = flag.String("resume", "", "resume a saved session by id (full id or unique prefix)")
	)
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get cwd: %v\n", err)
		os.Exit(1)
	}

	model, getAPIKey := provider.Resolve()
	if model == nil {
		printMissingProviderHint(os.Stderr)
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
		Cwd:               cwd,
		AgentDir:          agentDir,
		Model:             model,
		ThinkingLevel:     thinkingLevel,
		GetAPIKey:         getAPIKey,
		ScopedModels:      provider.ConfiguredModelIDs(),
		ModelConfigPath:   provider.ConfigPath(),
		ResumeSessionID:   *resumeID,
		Extensions:        exts,
		DeferStartupEvent: *printPrompt == "" && !*rpcMode && !*acpMode,
	}
	session, err := coding_agent.NewCodingSession(sessionOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}
	if *worktree && !*noWorktree {
		if err := enterStartupWorktree(session); err != nil {
			fmt.Fprintf(os.Stderr, "worktree: %v\n", err)
			os.Exit(1)
		}
	}
	sessionClosed := false
	closeSession := func() {
		if sessionClosed {
			return
		}
		sessionClosed = true
		session.Close("prompt_input_exit")
	}
	defer closeSession()
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
		ctx, cancel := signalContext()
		defer cancel()
		if err := modes.RunPrint(ctx, modes.PrintOptions{
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
		ctx, cancel := signalContext()
		defer cancel()
		if err := rpc.NewRpcMode(session).Run(ctx); err != nil && err != context.Canceled {
			fmt.Fprintf(os.Stderr, "rpc error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *acpMode {
		ctx, cancel := signalContext()
		defer cancel()
		if err := acp.New(session).Run(ctx); err != nil && err != context.Canceled {
			fmt.Fprintf(os.Stderr, "acp error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// SSH/terminal hang-up (SIGHUP) isn't handled by bubbletea, whose signal
	// handler only watches SIGINT/SIGTERM. Without this, a dropped SSH session
	// kills the process before bubbletea can restore the terminal, leaving mouse
	// tracking enabled so the local shell floods with raw SGR reports
	// (65;25;32M...). Cancel the program context on SIGHUP so bubbletea shuts
	// down cleanly and emits the mouse-disable / alt-screen-exit sequences.
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)
	defer signal.Stop(hangup)
	go func() {
		select {
		case <-hangup:
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := runTUI(ctx, session, model, *noApprove, RunOptions{CommandHooks: CommandHooks{
		Config: func(args string) (string, error) {
			return runConfigHook(args, session)
		},
		ConfigModels:      configModelEntries,
		ConfigProviders:   configProviderEntries,
		ConfigAdd:         configAddModel,
		ConfigSetProvider: configSetProvider,
		ConfigUse: func(target string) (string, error) {
			return configUseModel(target, session)
		},
		ConfigRemove:     configRemoveModel,
		ConfigWorkflows:  func() (string, error) { return configToggleWorkflows(session) },
		SaveScopedModels: provider.SetScopedModelIDs,
	}}); err != nil {
		fmt.Fprintf(os.Stderr, "ui error: %v\n", err)
		os.Exit(1)
	}
	closeSession()
	printInteractiveExitSummary(interactiveExitOutput, session)
}

func printInteractiveExitSummary(out io.Writer, session *coding_agent.CodingSession) {
	if out == nil || session == nil {
		return
	}
	id := strings.TrimSpace(session.GetSessionID())
	if id == "" {
		return
	}
	fmt.Fprintf(out, "Session saved: %s\nResume with: modu_code --resume %s\n", id, id)
}

func printMissingProviderHint(w io.Writer) {
	if w == nil {
		return
	}
	configPath := provider.ConfigPath()
	fmt.Fprintln(w, "No model provider is configured yet.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Quick start:")
	fmt.Fprintln(w, "  1. Create an example config:")
	fmt.Fprintln(w, "     modu_code config init")
	fmt.Fprintln(w, "  2. Edit the config with your provider, model, and API key:")
	fmt.Fprintf(w, "     %s\n", configPath)
	fmt.Fprintln(w, "  3. Check it:")
	fmt.Fprintln(w, "     modu_code config validate")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "For a local OpenAI-compatible server, you can also add a model directly:")
	fmt.Fprintln(w, `  modu_code config add local-qwen lmstudio qwen http://127.0.0.1:1234/v1 lm-studio --description "local coding model"`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment alternatives:")
	fmt.Fprintln(w, "  OPENAI_API_KEY                      uses OPENAI_MODEL or gpt-4o")
	fmt.Fprintln(w, "  DEEPSEEK_API_KEY                    uses DEEPSEEK_MODEL or deepseek-chat")
	fmt.Fprintln(w, "  ANTHROPIC_API_KEY + ANTHROPIC_MODEL")
	fmt.Fprintln(w, "  OLLAMA_HOST + OLLAMA_MODEL")
}

func enterStartupWorktree(session *coding_agent.CodingSession) error {
	if session == nil {
		return nil
	}
	_, err := session.EnterWorktree()
	if err == nil || shouldIgnoreStartupWorktreeError(err) {
		return nil
	}
	return err
}

func shouldIgnoreStartupWorktreeError(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not a git repository") ||
		strings.Contains(msg, "worktree mode is disabled")
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
	return ctx, cancel
}
