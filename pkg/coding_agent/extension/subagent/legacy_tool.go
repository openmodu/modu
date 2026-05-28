package subagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
)

// legacySpawnSubagentTool preserves the old spawn_subagent tool surface while
// moving registration and dispatch behind the subagent extension.
type legacySpawnSubagentTool struct {
	ext *Extension
}

func newLegacySpawnSubagentTool(ext *Extension) *legacySpawnSubagentTool {
	return &legacySpawnSubagentTool{ext: ext}
}

func (t *legacySpawnSubagentTool) Name() string   { return "spawn_subagent" }
func (t *legacySpawnSubagentTool) Label() string  { return "Spawn Subagent" }
func (t *legacySpawnSubagentTool) Parallel() bool { return true }

func (t *legacySpawnSubagentTool) Description() string {
	var b strings.Builder
	b.WriteString("Delegate a task to a named subagent profile. Compatibility alias for the subagent extension tool.")
	if t.ext != nil && t.ext.loader != nil {
		defs := t.ext.loader.List()
		if len(defs) > 0 {
			b.WriteString("\n\nAvailable agents:")
			for _, def := range defs {
				desc := def.Description
				if desc == "" {
					desc = "(no description)"
				}
				fmt.Fprintf(&b, "\n  - %s: %s", def.Name, desc)
			}
		}
	}
	return b.String()
}

func (t *legacySpawnSubagentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the subagent profile.",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "The task or question to send to the subagent.",
			},
		},
		"required": []string{"name", "task"},
	}
}

func (t *legacySpawnSubagentTool) Execute(ctx context.Context, _ string, args map[string]any, onUpdate agent.ToolUpdateCallback) (agent.ToolResult, error) {
	name, _ := args["name"].(string)
	task, _ := args["task"].(string)
	if name == "" {
		return errResult(`spawn_subagent: "name" is required`), nil
	}
	if task == "" {
		return errResult(`spawn_subagent: "task" is required`), nil
	}
	if t.ext == nil || t.ext.loader == nil {
		return errResult("spawn_subagent: subagent extension is not initialized"), nil
	}
	if _, ok := t.ext.loader.Get(name); !ok {
		return errResult(fmt.Sprintf("spawn_subagent: subagent %q not found", name)), nil
	}
	if onUpdate != nil {
		onUpdate(okResult(fmt.Sprintf("Running subagent %q...", name), nil))
	}

	text, err := forkOne(ctx, t.ext, name, task, callOptions{})
	if err != nil {
		return errResult(fmt.Sprintf("spawn_subagent: %v", err)), nil
	}
	details := map[string]string{"subagent": name}
	if taskID := extractTaskID(text); taskID != "" {
		details["task_id"] = taskID
		details["status"] = "running"
	}
	return okResult(text, details), nil
}
