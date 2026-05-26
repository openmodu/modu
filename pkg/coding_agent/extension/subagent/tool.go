package subagent

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// subagentTool is the agent.AgentTool the extension exposes to the LLM.
//
// The tool name is "subagent" (vs the existing inline "spawn_subagent")
// so the two paths coexist during the rollout. Eventually spawn_subagent
// is removed and this becomes the only delegation tool.
type subagentTool struct {
	ext *Extension
}

func newSubagentTool(ext *Extension) *subagentTool {
	return &subagentTool{ext: ext}
}

func (t *subagentTool) Name() string  { return "subagent" }
func (t *subagentTool) Label() string { return "Subagent" }

func (t *subagentTool) Description() string {
	return `Delegate a focused task to a named subagent profile. Supports three modes:
  - single (default): run one agent on one task and return its final reply.
  - parallel: run multiple agent/task pairs concurrently; result is a JSON-like
    summary keyed by call index.
  - chain: run agent/task pairs sequentially, where each step's task may
    contain {previous} which is replaced with the prior step's reply.`
}

// Parallel returns true so the host can schedule this tool concurrently with
// other tool calls. Internal goroutine handling for the parallel mode is
// independent of this flag.
func (t *subagentTool) Parallel() bool { return true }

func (t *subagentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"single", "parallel", "chain"},
				"description": "Dispatch mode (default: single).",
			},
			"agent": map[string]any{
				"type":        "string",
				"description": "Profile name (required for single mode).",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "Task description (required for single mode).",
			},
			"parallel": map[string]any{
				"type":        "array",
				"description": "List of {agent, task} pairs to run concurrently (required for parallel mode).",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent": map[string]any{"type": "string"},
						"task":  map[string]any{"type": "string"},
					},
					"required": []string{"agent", "task"},
				},
			},
			"chain": map[string]any{
				"type":        "array",
				"description": "Sequential list of {agent, task} pairs. {previous} in task is substituted with the prior step's reply (required for chain mode).",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent": map[string]any{"type": "string"},
						"task":  map[string]any{"type": "string"},
					},
					"required": []string{"agent", "task"},
				},
			},
		},
	}
}

func (t *subagentTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	mode, _ := args["mode"].(string)
	if mode == "" {
		mode = "single"
	}

	var (
		text string
		err  error
	)
	switch mode {
	case "single":
		text, err = runSingle(ctx, t.ext, args)
	case "parallel":
		text, err = runParallel(ctx, t.ext, args)
	case "chain":
		text, err = runChain(ctx, t.ext, args)
	default:
		return errResult(fmt.Sprintf("subagent: unknown mode %q (expected single|parallel|chain)", mode)), nil
	}
	if err != nil {
		return errResult(fmt.Sprintf("subagent: %v", err)), nil
	}
	return okResult(text), nil
}

func okResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
}

func errResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		IsError: true,
	}
}
