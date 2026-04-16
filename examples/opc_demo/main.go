package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

const opcCLI = "/Users/ityike/Code/OPC/opc-cli"

const systemPrompt = `You are an OPC (Oral Production Console) assistant — an AI that controls a multi-engine TTS and ASR command-line tool.

You have access to the following tools:
- opc_tts: Generate speech audio from text
- opc_say: Generate speech and play on a device
- opc_asr: Transcribe audio to text or subtitles
- opc_voices: List available TTS voices
- opc_discover: Discover AirPlay/DLNA playback devices
- opc_config: View or update OPC configuration
- opc_asr_split: Fix long subtitle lines by splitting at a given point

When the user asks you to generate speech, use opc_tts or opc_say.
When the user asks about voices, use opc_voices.
When the user asks to transcribe audio, use opc_asr.
When the user asks about devices, use opc_discover.
When the user asks to change settings, use opc_config.

Always respond in the same language as the user.
When a command succeeds, summarize the result concisely.
When a command fails, explain the error and suggest fixes.
`

func main() {
	// Provider config - default to local LM Studio
	baseURL := envOr("OPC_LLM_BASE_URL", "http://192.168.5.149:1234/v1")
	modelName := envOr("OPC_LLM_MODEL", "qwen/qwen3.5-35b-a3b")
	providerID := envOr("OPC_LLM_PROVIDER", "lmstudio")

	providers.Register(openai.New(
		providerID,
		openai.WithBaseURL(baseURL),
	))

	model := &types.Model{
		ID:         modelName,
		Name:       modelName,
		ProviderID: providerID,
	}

	a := agent.NewAgent(agent.AgentConfig{
		InitialState: &agent.AgentState{
			SystemPrompt:  systemPrompt,
			Model:         model,
			ThinkingLevel: agent.ThinkingLevelLow,
			Tools: []agent.AgentTool{
				&TTSTool{},
				&SayTool{},
				&ASRTool{},
				&VoicesTool{},
				&DiscoverTool{},
				&ConfigTool{},
				&ASRSplitTool{},
			},
		},
	})

	// Event stream rendering
	a.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.EventTypeMessageUpdate:
			if event.StreamEvent != nil && event.StreamEvent.Type == "text_delta" {
				fmt.Print(event.StreamEvent.Delta)
			}
		case agent.EventTypeToolExecutionStart:
			fmt.Printf("\n\033[2m>> %s\033[0m", event.ToolName)
			if args, ok := event.Args.(map[string]any); ok {
				if text, _ := args["text"].(string); text != "" {
					preview := text
					if len(preview) > 60 {
						preview = preview[:57] + "..."
					}
					fmt.Printf(" \033[2m\"%s\"\033[0m", preview)
				}
				if audio, _ := args["audio"].(string); audio != "" {
					fmt.Printf(" \033[2m%s\033[0m", audio)
				}
			}
			fmt.Println()
		case agent.EventTypeToolExecutionEnd:
			if event.IsError {
				fmt.Println("\033[31m   [ERROR]\033[0m")
			}
		case agent.EventTypeMessageEnd:
			if _, ok := event.Message.(types.AssistantMessage); ok {
				fmt.Println()
			} else if _, ok := event.Message.(*types.AssistantMessage); ok {
				fmt.Println()
			}
		}
	})

	fmt.Println("OPC Agent (powered by modu)")
	fmt.Println("Type your request, or 'quit' to exit.")
	fmt.Println("---")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "quit" || input == "exit" || input == "q" {
			fmt.Println("bye!")
			break
		}

		if err := a.Prompt(context.Background(), input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
