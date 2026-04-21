package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openmodu/modu/pkg/channels/feishu"
	"github.com/openmodu/modu/pkg/channels/telegram"
	"github.com/openmodu/modu/pkg/moms"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/skills"
	"github.com/openmodu/modu/pkg/types"
)

func main() {
	// Parse arguments.
	sandboxArg := "host"
	workingDir := ""

	for _, arg := range os.Args[1:] {
		switch {
		case len(arg) > 10 && arg[:10] == "--sandbox=":
			sandboxArg = arg[10:]
		case arg[:2] == "--":
			// Unrecognized flag - ignore.
		default:
			if workingDir == "" {
				workingDir = arg
			}
		}
	}

	if workingDir == "" {
		fmt.Fprintln(os.Stderr, "Usage: moms [--sandbox=host|docker:<container>] <working-directory>")
		os.Exit(1)
	}

	// Read env vars.
	tgToken := os.Getenv("MOMS_TG_TOKEN")
	if tgToken == "" {
		fmt.Fprintln(os.Stderr, "Error: MOMS_TG_TOKEN environment variable is required")
		os.Exit(1)
	}

	// Parse sandbox.
	sandboxCfg, err := moms.ParseSandboxArg(sandboxArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Validate sandbox.
	if err := moms.ValidateSandbox(sandboxCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Sandbox error: %v\n", err)
		os.Exit(1)
	}

	sandbox := moms.NewSandbox(sandboxCfg)

	// Ensure working directory exists.
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create working directory: %v\n", err)
		os.Exit(1)
	}

	// Local LM Studio endpoint.
	const localProviderID = "lmstudio"
	const localBaseURL = "http://192.168.5.149:1234/v1"

	modelID := os.Getenv("MOMS_MODEL")
	if modelID == "" {
		modelID = "zai-org/glm-4.7-flash"
		// modelID = "qwen/qwen3.6-35b-a3b"
	}

	providers.Register(openai.New(localProviderID,
		openai.WithBaseURL(localBaseURL),
		openai.WithAPIKey("lm-studio"), // LM Studio 不校验 key
	))

	model := &types.Model{
		ID:            modelID,
		Name:          modelID + " (LM Studio)",
		Api:           types.KnownApiOpenAIChatCompletions,
		ProviderID:    localProviderID,
		ContextWindow: 32768,
		MaxTokens:     8192,
	}

	getAPIKey := func(provider string) (string, error) {
		if provider == localProviderID {
			return "lm-studio", nil
		}
		return "", fmt.Errorf("no API key for provider: %s", provider)
	}

	// Build skills registry (opt-in via CLAWHUB_AUTH_TOKEN env var).
	registryCfg := skills.RegistryConfig{}
	if clawHubToken := os.Getenv("CLAWHUB_AUTH_TOKEN"); clawHubToken != "" {
		registryCfg.ClawHub = skills.ClawHubConfig{
			Enabled:   true,
			AuthToken: clawHubToken,
		}
		fmt.Println("[moms] ClawHub registry enabled")
	}
	registryMgr := skills.NewRegistryManagerFromConfig(registryCfg)
	searchCache := skills.NewSearchCache(50, 5*time.Minute)

	// Create the dispatcher (owns runner management, logging, context).
	dispatcher := moms.NewDispatcher(sandbox, workingDir, model, getAPIKey, registryMgr, searchCache)

	// Attach dir for telegram file downloads.
	attachDir := workingDir

	bot, err := telegram.NewBot(tgToken, attachDir, dispatcher.HandleMessage, dispatcher.HandleAbort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create bot: %v\n", err)
		os.Exit(1)
	}

	// Optional: Feishu bot via WebSocket long connection.
	feishuAppID := os.Getenv("FEISHU_APP_ID")
	feishuAppSecret := os.Getenv("FEISHU_APP_SECRET")

	// Start events watcher.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventsWatcher := moms.NewEventsWatcher(workingDir, func(chatID int64, filename, text string) {
		dispatcher.TriggerEvent(ctx, chatID, filename, text)
	})
	eventsWatcher.Start()

	// Handle shutdown gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n[moms] Shutting down...")
		eventsWatcher.Stop()
		cancel()
	}()

	fmt.Printf("[moms] Working directory: %s\n", workingDir)
	fmt.Printf("[moms] Sandbox: %s\n", sandboxArg)
	fmt.Printf("[moms] Model: %s\n", modelID)

	// Start Feishu bot if credentials are provided.
	if feishuAppID != "" && feishuAppSecret != "" {
		fsBot, err := feishu.NewBot(feishuAppID, feishuAppSecret, dispatcher.HandleMessage, dispatcher.HandleAbort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create Feishu bot: %v\n", err)
			os.Exit(1)
		}
		go func() {
			if err := fsBot.Run(ctx); err != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "[feishu] bot error: %v\n", err)
			}
		}()
		fmt.Println("[moms] Feishu bot started")
	}

	if err := bot.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Bot error: %v\n", err)
		os.Exit(1)
	}
}
