package tools

import (
	"context"
	"fmt"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/types"
)

func init() {
	// Let the session instantiate the tool since it requires the MemoryStore
}

// MemoryStore defines the interface for the backend storage used by the memory tool.
type MemoryStore interface {
	ReadLongTerm() string
	WriteLongTerm(content string) error
	AppendToday(content string) error
}

// MemoryTool allows the model to write data to long-term memory or daily notes.
type MemoryTool struct {
	store MemoryStore
}

// NewMemoryTool creates a new memory tool instance.
func NewMemoryTool(store MemoryStore) *MemoryTool {
	return &MemoryTool{store: store}
}

func (t *MemoryTool) Name() string {
	return "memo"
}

func (t *MemoryTool) Label() string {
	return "Write Memory"
}

func (t *MemoryTool) Description() string {
	return `Write important facts, decisions, or user preferences to long-term memory or daily notes.
Use this tool proactively to record architectural choices, project rules, or recurring tasks
so that you can remember them across server restarts and context compactions.

Operations:
- 'record_long_term': Overwrites or appends critical project facts to MEMORY.md.
- 'record_daily': Appends a scratchpad note or daily log to today's date.`
}

func (t *MemoryTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"description": "Must be 'record_long_term' or 'record_daily'",
				"enum":        []string{"record_long_term", "record_daily"},
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The specific markdown content, notes, or facts to remember.",
			},
		},
		"required": []string{"operation", "content"},
	}
}

func (t *MemoryTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.store == nil {
		return textResult("Error: memory store is not configured for this session"), nil
	}

	operation, _ := args["operation"].(string)
	content, _ := args["content"].(string)

	if content == "" {
		return textResult("Error: content cannot be empty"), nil
	}

	switch operation {
	case "record_long_term":
		// Prevent accidental full overwrite by appending unless it explicitly requests overwrite.
		// For simplicity, we just append to the existing MEMORY.md contents here.
		existing := t.store.ReadLongTerm()
		var newContent string
		if existing == "" {
			newContent = content
		} else {
			newContent = existing + "\n\n" + content
		}

		err := t.store.WriteLongTerm(newContent)
		if err != nil {
			return textResult(fmt.Sprintf("Failed to write to long-term memory: %v", err)), nil
		}
		return textResult("Successfully recorded to long-term memory."), nil

	case "record_daily":
		err := t.store.AppendToday(content)
		if err != nil {
			return textResult(fmt.Sprintf("Failed to append to daily notes: %v", err)), nil
		}
		return textResult("Successfully appended to today's daily notes."), nil

	default:
		return textResult(fmt.Sprintf("Unknown operation: %s", operation)), nil
	}
}

// textResult creates a simple text AgentToolResult.
func textResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		Details: map[string]any{"result": text},
	}
}
