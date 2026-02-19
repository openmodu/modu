package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/moms"

	// Register Anthropic provider.
	_ "github.com/crosszan/modu/pkg/llm/providers/anthropic"
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

	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

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

	// Default model: claude-3-5-sonnet.
	modelID := os.Getenv("MOMS_MODEL")
	if modelID == "" {
		modelID = "claude-sonnet-4-5"
	}

	model := &llm.Model{
		ID:            modelID,
		Name:          modelID + " (Anthropic)",
		Api:           llm.Api(llm.KnownApiAnthropicMessages),
		Provider:      llm.Provider(llm.KnownProviderAnthropic),
		Input:         []string{"text", "image"},
		ContextWindow: 200000,
		MaxTokens:     8192,
		Cost: llm.ModelCost{
			Input:      3.0 / 1e6,
			Output:     15.0 / 1e6,
			CacheRead:  0.3 / 1e6,
			CacheWrite: 3.75 / 1e6,
		},
	}

	getAPIKey := func(provider string) (string, error) {
		if provider == string(llm.KnownProviderAnthropic) {
			if anthropicKey != "" {
				return anthropicKey, nil
			}
		}
		return "", fmt.Errorf("no API key for provider: %s", provider)
	}

	bot, err := moms.NewBot(tgToken, sandbox, workingDir, model, getAPIKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create bot: %v\n", err)
		os.Exit(1)
	}

	// Start events watcher.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventsWatcher := moms.NewEventsWatcher(workingDir, func(chatID int64, filename, text string) {
		bot.TriggerEvent(ctx, chatID, filename, text)
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

	if err := bot.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Bot error: %v\n", err)
		os.Exit(1)
	}
}
