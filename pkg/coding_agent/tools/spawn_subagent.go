package tools

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/types"
)

// SpawnSubagentTool lets the main agent delegate a task to a named subagent.
// The subagent runs in-process with its own agent.Agent instance and returns
// its final text response as the tool result.
type SpawnSubagentTool struct {
	loader    *subagent.Loader
	allTools  []agent.AgentTool
	model     *types.Model
	getAPIKey func(string) (string, error)
}

// NewSpawnSubagentTool creates a SpawnSubagentTool.
func NewSpawnSubagentTool(
	loader *subagent.Loader,
	allTools []agent.AgentTool,
	model *types.Model,
	getAPIKey func(string) (string, error),
) *SpawnSubagentTool {
	return &SpawnSubagentTool{
		loader:    loader,
		allTools:  allTools,
		model:     model,
		getAPIKey: getAPIKey,
	}
}

func (t *SpawnSubagentTool) Name() string  { return "spawn_subagent" }
func (t *SpawnSubagentTool) Label() string { return "Spawn Subagent" }
func (t *SpawnSubagentTool) Description() string {
	return `Delegate a task to a named subagent. The subagent runs with its own LLM
context and tool set, then returns its final response.`
}

func (t *SpawnSubagentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the subagent (must match a definition in agents/)",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "The task or question to send to the subagent",
			},
		},
		"required": []string{"name", "task"},
	}
}

func (t *SpawnSubagentTool) Execute(
	ctx context.Context,
	_ string,
	args map[string]any,
	onUpdate agent.AgentToolUpdateCallback,
) (agent.AgentToolResult, error) {
	name, _ := args["name"].(string)
	task, _ := args["task"].(string)

	if name == "" {
		return spawnErrResult("spawn_subagent: \"name\" is required"), nil
	}
	if task == "" {
		return spawnErrResult("spawn_subagent: \"task\" is required"), nil
	}
	def, ok := t.loader.Get(name)
	if !ok {
		return spawnErrResult(fmt.Sprintf("spawn_subagent: subagent %q not found", name)), nil
	}

	onUpdate(agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{
			Type: "text",
			Text: fmt.Sprintf("Running subagent %q…", name),
		}},
	})

	result, err := subagent.Run(ctx, def, task, t.allTools, t.model, t.getAPIKey)
	if err != nil {
		return spawnErrResult(fmt.Sprintf("spawn_subagent: %v", err)), nil
	}

	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: result}},
		Details: map[string]string{"subagent": name},
	}, nil
}

func spawnErrResult(msg string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: msg}},
	}
}
