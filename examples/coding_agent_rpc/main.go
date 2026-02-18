// Package main demonstrates the RPC mode.
//
// This example starts the RPC server which reads JSON-line commands from stdin
// and writes JSON-line responses/events to stdout.
//
// Usage (interactive — type JSON commands on stdin):
//
//	OLLAMA_HOST=192.168.5.149 go run ./examples/coding_agent_rpc/
//
// Example commands to paste:
//
//	{"id":"1","command":"get_state"}
//	{"id":"2","command":"cycle_thinking_level"}
//	{"id":"3","command":"set_auto_retry","data":{"enabled":true}}
//	{"id":"4","command":"prompt","data":{"message":"What is 2+2?"}}
//	{"id":"5","command":"get_messages"}
//	{"id":"6","command":"abort"}
//
// Or pipe commands programmatically:
//
//	echo '{"id":"1","command":"get_state"}' | go run ./examples/coding_agent_rpc/
package main

import (
	"context"
	"fmt"
	"os"

	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
	"github.com/crosszan/modu/pkg/coding_agent/modes/rpc"
	"github.com/crosszan/modu/pkg/coding_agent/tools"
	"github.com/crosszan/modu/pkg/llm"
	_ "github.com/crosszan/modu/pkg/llm/providers/ollama"
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

	model := &llm.Model{
		ID:            ollamaModel,
		Name:          ollamaModel + " (Ollama)",
		Api:           llm.Api(llm.KnownApiOllama),
		Provider:      llm.Provider(llm.KnownProviderOllama),
		BaseURL:       fmt.Sprintf("http://%s:11434", ollamaHost),
		Input:         []string{"text"},
		ContextWindow: 32768,
		MaxTokens:     4096,
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

	// Create and run RPC mode — reads commands from stdin, writes to stdout
	rpcMode := rpc.NewRpcMode(session)
	if err := rpcMode.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "RPC mode error: %v\n", err)
		os.Exit(1)
	}
}
