package tools

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func init() {
	// Let the session instantiate the tool since it requires the MemoryStore
}

// MemoryStore defines the interface for the backend storage used by the memory tool.
type MemoryStore interface {
	ReadLongTerm() string
	WriteLongTerm(content string) error
	ReadGlobalLongTerm() string
	WriteGlobalLongTerm(content string) error
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
- 'record_long_term': Appends critical facts to MEMORY.md. Use scope 'global' for cross-project facts (user preferences, personal rules) or 'project' (default) for project-specific facts.
- 'record_daily': Appends a scratchpad note or daily log to today's date (project scope).`
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
		"scope": map[string]any{
				"type":        "string",
				"description": "Storage scope for 'record_long_term': 'project' (default, current project only) or 'global' (shared across all projects, e.g. user preferences).",
				"enum":        []string{"project", "global"},
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
	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = "project"
	}

	if content == "" {
		return textResult("Error: content cannot be empty"), nil
	}

	switch operation {
	case "record_long_term":
		var (
			existing string
			writeErr error
		)
		if scope == "global" {
			existing = t.store.ReadGlobalLongTerm()
		} else {
			existing = t.store.ReadLongTerm()
		}
		var newContent string
		if existing == "" {
			newContent = content
		} else {
			newContent = existing + "\n\n" + content
		}
		if scope == "global" {
			writeErr = t.store.WriteGlobalLongTerm(newContent)
		} else {
			writeErr = t.store.WriteLongTerm(newContent)
		}
		if writeErr != nil {
			return textResult(fmt.Sprintf("Failed to write to long-term memory: %v", writeErr)), nil
		}
		return textResult(fmt.Sprintf("Successfully recorded to %s long-term memory.", scope)), nil

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
