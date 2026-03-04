// Package main demonstrates Print mode: text and JSON output modes.
//
// Usage:
//
//	# Text mode (default) — outputs only the final assistant response
//	OLLAMA_HOST=192.168.5.149 go run ./examples/coding_agent_print/
//
//	# JSON mode — outputs each event as a JSON line
//	OLLAMA_HOST=192.168.5.149 go run ./examples/coding_agent_print/ --json
package main

import (
	"context"
	"fmt"
	"os"

	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
	"github.com/crosszan/modu/pkg/coding_agent/modes"
	"github.com/crosszan/modu/pkg/coding_agent/tools"
	"github.com/crosszan/modu/pkg/providers"
)

func main() {
	ollamaHost := "192.168.5.149"
	ollamaModel := "qwen3-coder-next"

	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		ollamaHost = h
	}
	if m := os.Getenv("OLLAMA_MODEL"); m != "" {
		ollamaModel = m
	}

	// Determine output mode from CLI flag
	mode := modes.PrintModeText
	for _, arg := range os.Args[1:] {
		if arg == "--json" || arg == "-json" {
			mode = modes.PrintModeJSON
		}
	}

	// Register Ollama as an OpenAI-compatible provider
	providers.Register(providers.NewOpenAIChatCompletionsProvider(
		"ollama",
		providers.WithBaseURL(fmt.Sprintf("http://%s:1234/v1", ollamaHost)),
	))

	model := &providers.Model{
		ID:         ollamaModel,
		Name:       ollamaModel + " (Ollama)",
		ProviderID: "ollama",
	}

	cwd, _ := os.Getwd()

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:   cwd,
		Model: model,
		Tools: tools.AllTools(cwd),
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create session: %v\n", err)
		os.Exit(1)
	}

	// RunPrint sends prompts sequentially.
	// - text mode: silently processes, then outputs the final assistant text
	// - json mode: outputs a header, then each agent event as a JSON line, then footer
	err = modes.RunPrint(context.Background(), modes.PrintOptions{
		Mode:    mode,
		Output:  os.Stdout,
		Session: session,
		Messages: []string{
			"Use the ls tool to list files in the current directory, then tell me what this project is about in one sentence.",
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "RunPrint error: %v\n", err)
		os.Exit(1)
	}
}
