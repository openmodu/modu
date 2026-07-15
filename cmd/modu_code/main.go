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
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/coding_agent/modes"
	"github.com/openmodu/modu/pkg/coding_agent/modes/rpc"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	_ "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/cron"     // register builtin extension via init()
	_ "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/goal"     // register builtin extension via init()
	_ "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/subagent" // register builtin extension via init()
	_ "github.com/openmodu/modu/pkg/coding_agent/plugins/extension/workflow" // register builtin extension via init()
	cronscheduler "github.com/openmodu/modu/pkg/cron"
	cronconfig "github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_code/internal/acp"
	"github.com/openmodu/modu/pkg/provider"
)

var runTUI = runModuTUI
var interactiveExitOutput io.Writer = os.Stdout

const unconfiguredProviderID = "unconfigured"

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

	interactiveMode := *printPrompt == "" && !*rpcMode && !*acpMode
	model, getAPIKey := provider.Resolve()
	missingProvider := model == nil
	if missingProvider && !interactiveMode {
		printMissingProviderHint(os.Stderr)
		os.Exit(1)
	}
	if missingProvider {
		model = unconfiguredModel()
	}

	thinkingLevel := provider.ResolveThinkingLevel()
	agentDir := coding_agent.DefaultAgentDir()
	sessionCwd, err := resolveStartupResumeCwd(agentDir, cwd, *resumeID, interactiveMode, promptResumeCwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve resume working directory: %v\n", err)
		os.Exit(1)
	}

	// Out is intentionally nil here: writing to stderr from the goal
	// extension bypasses the TUI's inline-mode widget management and
	// corrupts the screen (Tokens/Status/Time fields land in arbitrary
	// rows). All user-facing notifications still reach the scrollback via
	// api.Notify -> SessionEventExtensionNotify -> a "section" uiBlock,
	// which is the only path the TUI can safely render multi-line text.
	// Resolve the active extension set from ~/.modu/extensions.yaml
	// (falls back to every builtin when the file is absent — that keeps the
	// default install behaviorally identical to "goal always on").
	exts, err := extension.LoadEnabled(extension.LoadOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load extensions: %v\n", err)
		os.Exit(1)
	}

	sessionOpts := coding_agent.CodingSessionOptions{
		Cwd:               sessionCwd,
		AgentDir:          agentDir,
		Model:             model,
		ThinkingLevel:     thinkingLevel,
		GetAPIKey:         getAPIKey,
		ScopedModels:      provider.ConfiguredModelIDs(),
		ModelConfigPath:   provider.ConfigPath(),
		ResumeSessionID:   *resumeID,
		Extensions:        exts,
		DeferStartupEvent: interactiveMode,
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
		if err := acp.NewWithOptions(session, acp.Options{NoApprove: *noApprove}).Run(ctx); err != nil && err != context.Canceled {
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

	// The cron scheduler runs embedded in the interactive TUI process — no
	// separate daemon/binary. Zero configured tasks means it just sits idle.
	// Its own log.Printf output is redirected to a file first: the default
	// logger writes straight to the real stderr, which corrupts bubbletea's
	// alt-screen the same way an unguided extension Out writer would.
	startCronScheduler(ctx)

	runOpts := RunOptions{CommandHooks: CommandHooks{
		Config: func(args string) (string, error) {
			return runConfigHook(args, session)
		},
		ConfigModels:    configModelEntries,
		ConfigProviders: configProviderEntries,
		ConfigAdd: func(input ConfigModelInput) (string, error) {
			return configAddModel(input, session)
		},
		ConfigSetProvider: func(input ConfigProviderInput) (string, error) {
			return configSetProvider(input, session)
		},
		ConfigUse: func(target string) (string, error) {
			return configUseModel(target, session)
		},
		ConfigRemove:     configRemoveModel,
		ConfigWorkflows:  func() (string, error) { return configToggleWorkflows(session) },
		SaveScopedModels: provider.SetScopedModelIDs,
	}}
	if missingProvider {
		runOpts.StartupNotice = missingProviderStartupNotice()
	}
	if err := runTUI(ctx, session, model, *noApprove, runOpts); err != nil {
		fmt.Fprintf(os.Stderr, "ui error: %v\n", err)
		os.Exit(1)
	}
	closeSession()
	printInteractiveExitSummary(interactiveExitOutput, session)
}

// startCronScheduler embeds pkg/cron's scheduler loop in this process,
// running until ctx is cancelled. Reads whatever ~/.modu/cron/config.yaml
// and tasks.yaml currently hold — missing files mean zero tasks, not an
// error, so this is always safe to start. The standard `log` package (which
// pkg/cron uses for its own startup/reload/retry messages, and which
// nothing else on this interactive path touches) is redirected to
// ~/.modu/cron/daemon.log first, since writing to the real stderr while
// bubbletea owns the terminal would corrupt the screen.
func startCronScheduler(ctx context.Context) {
	cfgPath := cronconfig.DefaultPath()
	logPath := filepath.Join(filepath.Dir(cfgPath), "daemon.log")
	if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(f)
	} else {
		log.SetOutput(io.Discard)
	}
	go func() {
		if err := cronscheduler.RunScheduler(ctx, cfgPath); err != nil {
			log.Printf("cron scheduler exited: %v", err)
		}
	}()
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

func unconfiguredModel() *types.Model {
	return &types.Model{
		ID:         "unconfigured",
		Name:       "No model configured",
		ProviderID: unconfiguredProviderID,
	}
}

func printMissingProviderHint(w io.Writer) {
	if w == nil {
		return
	}
	fmt.Fprint(w, missingProviderHintText())
}

func missingProviderStartupNotice() string {
	configPath := provider.ConfigPath()
	return strings.TrimSpace(fmt.Sprintf(`No model provider is configured yet.

Open /config to set up a provider, API key, and model.

Recommended flow:
  1. Type /config
  2. Choose Provider and enter an API key/base URL
  3. If models are discovered, choose Active Model

Config file:
  %s
`, configPath))
}

func missingProviderHintText() string {
	configPath := provider.ConfigPath()
	return fmt.Sprintf(`No model provider is configured yet.

Quick start:
  1. Create an example config:
     modu_code config init
  2. Edit the config with your provider, model, and API key:
     %s
  3. Check it:
     modu_code config validate

For a local OpenAI-compatible server, you can also add a model directly:
  modu_code config add local-qwen lmstudio qwen http://127.0.0.1:1234/v1 lm-studio --description "local coding model"

Environment alternatives:
  OPENAI_API_KEY                      uses OPENAI_MODEL or gpt-4o
  DEEPSEEK_API_KEY                    uses DEEPSEEK_MODEL or deepseek-chat
  ANTHROPIC_API_KEY + ANTHROPIC_MODEL
  OLLAMA_HOST + OLLAMA_MODEL
`, configPath)
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
