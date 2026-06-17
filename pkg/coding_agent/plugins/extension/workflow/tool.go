package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

type workflowTool struct {
	ext *Extension
}

func newTool(ext *Extension) *workflowTool {
	return &workflowTool{ext: ext}
}

func (t *workflowTool) Name() string  { return "workflow" }
func (t *workflowTool) Label() string { return "Workflow" }
func (t *workflowTool) Description() string {
	return strings.Join([]string{
		"Execute a deterministic Lua workflow that orchestrates forked subagents with agent(), parallel(), and pipeline().",
		"Use only when the user explicitly asks for a workflow, fan-out, or multi-agent orchestration.",
		"The script must call meta({name=..., description=...}) before phase/log/agent/parallel/pipeline and must call at least one agent.",
	}, " ")
}

func (t *workflowTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"script": map[string]any{
				"type":        "string",
				"description": "Required raw Lua workflow script, with no Markdown fences. Must call meta({name=..., description=...}) before other workflow APIs.",
			},
			"args": map[string]any{
				"description": "Optional JSON value exposed to the Lua script as global args.",
			},
			"concurrency": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional max concurrent agent runs for this workflow.",
			},
		},
		"required":             []string{"script"},
		"additionalProperties": false,
	}
}

func (t *workflowTool) Execute(ctx context.Context, _ string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	script, _ := args["script"].(string)
	script = normalizeScript(script)
	if script == "" {
		return textResult("workflow: script is required", true, nil), nil
	}
	concurrency, err := decodeConcurrency(args["concurrency"], t.ext.cfg.Concurrency)
	if err != nil {
		return textResult(fmt.Sprintf("workflow: %v", err), true, nil), nil
	}
	runner := newRunner(t.ext.api, runOptions{
		Cwd:         t.ext.api.Cwd(),
		Args:        args["args"],
		Concurrency: concurrency,
		OnUpdate:    onUpdate,
	})
	result, err := runner.run(ctx, script)
	if err != nil {
		return textResult(fmt.Sprintf("workflow: %v", err), true, result.Snapshot), nil
	}
	data, err := json.MarshalIndent(result.Result, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprint(result.Result))
	}
	text := fmt.Sprintf("Workflow %s completed with %d agent(s).\n\nResult:\n%s",
		result.Meta.Name, result.Snapshot.AgentCount, string(data))
	return textResult(text, false, result.Snapshot), nil
}

func normalizeScript(script string) string {
	text := strings.TrimSpace(script)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			text = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}
	return text
}

func decodeConcurrency(raw any, fallback int) (int, error) {
	if fallback <= 0 {
		fallback = 4
	}
	if raw == nil {
		return fallback, nil
	}
	var n int
	switch v := raw.(type) {
	case int:
		n = v
	case int64:
		n = int(v)
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("concurrency must be an integer >= 1")
		}
		n = int(v)
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("concurrency must be an integer >= 1")
		}
		n = int(i)
	default:
		return 0, fmt.Errorf("concurrency must be an integer >= 1")
	}
	if n < 1 {
		return 0, fmt.Errorf("concurrency must be an integer >= 1")
	}
	if n > 16 {
		n = 16
	}
	return n, nil
}

func textResult(text string, isErr bool, details any) types.ToolResult {
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		Details: details,
		IsError: isErr,
	}
}
