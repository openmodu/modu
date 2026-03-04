// Package main demonstrates the RPC mode.
//
// This example starts the RPC server which reads JSON-line commands from stdin
// and writes JSON-line responses/events to stdout.
//
// Usage (interactive — type JSON commands on stdin):
//
//	OLLAMA_HOST=192.168.5.149 go run ./examples/coding_agent_rpc/
//
// Example commands to paste (new "type" format, also supports legacy "command" field):
//
//	{"id":"1","type":"get_state"}
//	{"id":"2","type":"cycle_thinking_level"}
//	{"id":"3","type":"set_auto_retry","data":{"enabled":true}}
//	{"id":"4","type":"prompt","message":"What is 2+2?"}
//	{"id":"5","type":"get_messages"}
//	{"id":"6","type":"abort"}
//	{"id":"7","type":"bash","data":{"command":"ls -la"}}
//	{"id":"8","type":"get_session_stats"}
//	{"id":"9","type":"set_session_name","data":{"name":"my-session"}}
//	{"id":"10","type":"get_available_models"}
//	{"id":"11","type":"get_last_assistant_text"}
//	{"id":"12","type":"get_commands"}
//
// Or pipe commands programmatically:
//
//	echo '{"id":"1","type":"get_state"}' | go run ./examples/coding_agent_rpc/
package main

import (
	"context"
	"fmt"
	"os"

	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
	"github.com/crosszan/modu/pkg/coding_agent/modes/rpc"
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

	// Register Ollama as an OpenAI-compatible provider
	providers.Register(providers.NewOpenAIChatCompletionsProvider(
		"ollama",
		providers.WithBaseURL(fmt.Sprintf("http://%s:11434/v1", ollamaHost)),
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

	// Create and run RPC mode — reads commands from stdin, writes to stdout
	rpcMode := rpc.NewRpcMode(session)
	if err := rpcMode.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "RPC mode error: %v\n", err)
		os.Exit(1)
	}
}
