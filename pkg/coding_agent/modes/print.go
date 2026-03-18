package modes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/crosszan/modu/pkg/agent"
	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
	"github.com/crosszan/modu/pkg/types"
)

// PrintMode specifies the output format for print mode.
type PrintMode string

const (
	PrintModeText PrintMode = "text"
	PrintModeJSON PrintMode = "json"
)

// PrintOptions configures print mode execution.
type PrintOptions struct {
	Mode     PrintMode
	Messages []string // prompts to send sequentially
	Output   io.Writer
	Session  *coding_agent.CodingSession
}

// RunPrint executes print mode: sends prompts sequentially and outputs results.
// In text mode, outputs only the final assistant text.
// In JSON mode, outputs a session header then each AgentEvent as a JSON line.
func RunPrint(ctx context.Context, opts PrintOptions) error {
	if opts.Output == nil {
		opts.Output = os.Stdout
	}
	if opts.Session == nil {
		return fmt.Errorf("session is required")
	}
	if len(opts.Messages) == 0 {
		return fmt.Errorf("at least one message is required")
	}

	switch opts.Mode {
	case PrintModeJSON:
		return runPrintJSON(ctx, opts)
	default:
		return runPrintText(ctx, opts)
	}
}

func runPrintText(ctx context.Context, opts PrintOptions) error {
	var lastAssistantText string

	// Subscribe to capture assistant messages
	unsub := opts.Session.Subscribe(func(event agent.AgentEvent) {
		if event.Type == agent.EventTypeMessageEnd {
			if msg, ok := event.Message.(types.AssistantMessage); ok {
				for _, block := range msg.Content {
					if tc, ok := block.(*types.TextContent); ok {
						lastAssistantText = tc.Text
					}
				}
			}
		}
	})
	defer unsub()

	// Send all prompts sequentially
	for _, msg := range opts.Messages {
		if err := opts.Session.Prompt(ctx, msg); err != nil {
			return fmt.Errorf("prompt failed: %w", err)
		}
		opts.Session.WaitForIdle()
	}

	// Output the final assistant text
	if lastAssistantText != "" {
		fmt.Fprintln(opts.Output, lastAssistantText)
	}

	return nil
}

func runPrintJSON(ctx context.Context, opts PrintOptions) error {
	enc := json.NewEncoder(opts.Output)

	// Output session header
	header := map[string]any{
		"type":      "session_start",
		"sessionId": opts.Session.GetSessionID(),
		"model":     opts.Session.GetModel().ID,
	}
	if err := enc.Encode(header); err != nil {
		return err
	}

	// Subscribe to stream all events as JSON lines.
	// For message_update events the Partial field (cumulative text) is stripped
	// so each line is truly incremental — only the delta is included.
	unsub := opts.Session.Subscribe(func(event agent.AgentEvent) {
		line := map[string]any{"type": string(event.Type)}

		if event.ToolName != "" {
			line["toolName"] = event.ToolName
		}
		if event.ToolCallID != "" {
			line["toolCallId"] = event.ToolCallID
		}
		if event.IsError {
			line["isError"] = true
		}
		if event.Args != nil {
			line["args"] = event.Args
		}
		if event.Result != nil {
			line["result"] = event.Result
		}

		if se := event.StreamEvent; se != nil {
			// Omit Partial (ever-growing cumulative message) — redundant noise.
			line["streamEvent"] = struct {
				Type         string `json:"Type"`
				ContentIndex int    `json:"ContentIndex,omitempty"`
				Delta        string `json:"Delta"`
			}{
				Type:         string(se.Type),
				ContentIndex: se.ContentIndex,
				Delta:        se.Delta,
			}
			// message: only the delta text, not the cumulative partial.
			if event.Type == agent.EventTypeMessageUpdate && se.Delta != "" {
				line["message"] = se.Delta
			}
		} else if event.Message != nil {
			line["message"] = event.Message
		}

		_ = enc.Encode(line)
	})
	defer unsub()

	// Send all prompts sequentially
	for _, msg := range opts.Messages {
		if err := opts.Session.Prompt(ctx, msg); err != nil {
			return fmt.Errorf("prompt failed: %w", err)
		}
		opts.Session.WaitForIdle()
	}

	// Output session end
	_ = enc.Encode(map[string]string{"type": "session_end"})

	return nil
}
