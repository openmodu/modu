package subagent

import (
	"context"
	"fmt"
	"strings"

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

// Description is computed each call rather than fixed so model prompts
// always see the current list of discovered agents — replaces the system
// prompt injection that the old spawn_subagent path used.
func (t *subagentTool) Description() string {
	var b strings.Builder
	b.WriteString(`Delegate a focused task to a named subagent profile. Supports three modes:
  - single (default): run one agent on one task and return its final reply.
  - parallel: run multiple agent/task pairs concurrently; result aggregates
    each agent's reply with a [index] header.
  - chain: run agent/task pairs sequentially. {previous} in a task is
    replaced with the prior step's reply before dispatch.`)
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
