package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/types"
)

// SpawnSubagentTool lets the main agent delegate a task to a named subagent.
// The subagent runs in-process with its own agent.Agent instance and returns
// its final text response as the tool result.
type SpawnSubagentTool struct {
	cwd        string
	agentDir   string
	loader     *subagent.Loader
	allTools   []agent.AgentTool
	model      *types.Model
	getAPIKey  func(string) (string, error)
	streamFn   agent.StreamFn
	prepareDef func(*subagent.SubagentDefinition) *subagent.SubagentDefinition
	taskStore  BackgroundTaskStore
}

// NewSpawnSubagentTool creates a SpawnSubagentTool.
func NewSpawnSubagentTool(
	cwd string,
	agentDir string,
	loader *subagent.Loader,
	allTools []agent.AgentTool,
	model *types.Model,
	getAPIKey func(string) (string, error),
	streamFn agent.StreamFn,
	prepareDef func(*subagent.SubagentDefinition) *subagent.SubagentDefinition,
	taskStore BackgroundTaskStore,
) *SpawnSubagentTool {
	return &SpawnSubagentTool{
		cwd:        cwd,
		agentDir:   agentDir,
		loader:     loader,
		allTools:   allTools,
		model:      model,
		getAPIKey:  getAPIKey,
		streamFn:   streamFn,
		prepareDef: prepareDef,
		taskStore:  taskStore,
	}
}

func (t *SpawnSubagentTool) Name() string   { return "spawn_subagent" }
func (t *SpawnSubagentTool) Label() string  { return "Spawn Subagent" }
func (t *SpawnSubagentTool) Parallel() bool { return true }
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
	if t.prepareDef != nil {
		def = t.prepareDef(def)
	}
	if def != nil && def.Background {
		return t.runBackground(ctx, def, name, task), nil
	}

	if onUpdate != nil {
		onUpdate(agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Running subagent %q…", name),
			}},
		})
	}

	result, err := t.runSubagent(ctx, def, task)
	if err != nil {
		return spawnErrResult(fmt.Sprintf("spawn_subagent: %v", err)), nil
	}

	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: result}},
		Details: map[string]string{"subagent": name},
	}, nil
}

func (t *SpawnSubagentTool) runBackground(ctx context.Context, def *subagent.SubagentDefinition, name, task string) agent.AgentToolResult {
	if t.taskStore == nil {
		return spawnErrResult("spawn_subagent: background execution is not configured")
	}
	taskID := t.taskStore.Create("subagent", fmt.Sprintf("%s: %s", name, task))
	go func() {
		result, err := t.runSubagent(context.Background(), def, task)
		if err != nil {
			t.taskStore.Fail(taskID, err.Error())
			return
		}
		t.taskStore.Complete(taskID, result)
	}()

	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{
			Type: "text",
			Text: fmt.Sprintf("Started subagent %q in background. Use task_output with task_id=%s to inspect the result.", name, taskID),
		}},
		Details: map[string]string{
			"subagent": name,
			"task_id":  taskID,
			"status":   "running",
		},
	}
}

func (t *SpawnSubagentTool) runSubagent(ctx context.Context, def *subagent.SubagentDefinition, task string) (string, error) {
	if def != nil && strings.EqualFold(def.Isolation, "worktree") {
		return t.runInWorktree(ctx, def, task)
	}
	return subagent.Run(ctx, def, task, t.allTools, t.model, t.getAPIKey, t.streamFn)
}

func (t *SpawnSubagentTool) runInWorktree(ctx context.Context, def *subagent.SubagentDefinition, task string) (string, error) {
	root, err := gitOutput(t.cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("worktree isolation requires a git repository: %w", err)
	}
	baseDir := filepath.Join(t.agentDir, "worktrees")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(baseDir, fmt.Sprintf("subagent-wt-%d", time.Now().UnixMilli()))
	if _, err := runGit(root, "worktree", "add", "--detach", path, "HEAD"); err != nil {
		return "", err
	}
	defer func() {
		_, _ = runGit(root, "worktree", "remove", "--force", path)
	}()

	rebound := rebindToolsForCwd(t.allTools, path)
	return subagent.Run(ctx, def, task, rebound, t.model, t.getAPIKey, t.streamFn)
}

func rebindToolsForCwd(allTools []agent.AgentTool, cwd string) []agent.AgentTool {
	out := make([]agent.AgentTool, 0, len(allTools))
	for _, tool := range allTools {
		switch tool.Name() {
		case "read":
			out = append(out, NewReadTool(cwd))
		case "write":
			out = append(out, NewWriteTool(cwd))
		case "edit":
			out = append(out, NewEditTool(cwd))
		case "bash":
			out = append(out, NewBashTool(cwd))
		case "grep":
			out = append(out, NewGrepTool(cwd))
		case "find":
			out = append(out, NewFindTool(cwd))
		case "ls":
			out = append(out, NewLsTool(cwd))
		default:
			out = append(out, tool)
		}
	}
	return out
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := runGit(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func spawnErrResult(msg string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: msg}},
	}
}
