// Package crontools exposes cron_add / cron_list / cron_remove as
// agent.AgentTool implementations, so the CodingSession driving each task
// can manage the modu_cron config file via natural language.
//
// All three tools serialize on the same package-level mutex so concurrent
// task executions (queue/kill policies, or just two tasks firing in the
// same second) cannot race on the YAML file.
//
// Caveat: daemon does not currently hot-reload config.yaml; an agent that
// adds or removes a task must tell the user to restart the daemon for the
// schedule to take effect. The tool descriptions communicate this.
package crontools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
	"github.com/openmodu/modu/cmd/modu_cron/internal/scheduler"
)

// fileMu guards all reads + writes of the modu_cron config file performed by
// these tools. Held for the duration of a load-modify-save sequence.
var fileMu sync.Mutex

// New returns the three cron-management tools bound to cfgPath.
func New(cfgPath string) []agent.AgentTool {
	return []agent.AgentTool{
		&addTool{cfgPath: cfgPath},
		&listTool{cfgPath: cfgPath},
		&removeTool{cfgPath: cfgPath},
	}
}

// ─── cron_add ──────────────────────────────────────────────────────────────

type addTool struct{ cfgPath string }

func (t *addTool) Name() string  { return "cron_add" }
func (t *addTool) Label() string { return "Add Cron Task" }
func (t *addTool) Description() string {
	return `Add a new scheduled task to the modu_cron config. The cron expression uses the 6-field form (second minute hour day-of-month month day-of-week). The id must be unique. After adding, tell the user to restart the daemon (modu_cron daemon) for the schedule to take effect.`
}

func (t *addTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "Unique task id, lowercase-hyphen recommended (e.g. \"daily-summary\")",
			},
			"cron": map[string]any{
				"type":        "string",
				"description": "6-field cron expression, e.g. \"0 0 9 * * *\" for 9am daily, or \"@every 5m\"",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Prompt the agent will be given when this task fires",
			},
			"enabled": map[string]any{
				"type":        "boolean",
				"description": "Whether the schedule is active (default true)",
			},
			"on_overlap": map[string]any{
				"type":        "string",
				"enum":        []string{"skip", "queue", "kill"},
				"description": "Behavior when the previous run is still in flight (default skip)",
			},
		},
		"required": []string{"id", "cron", "prompt"},
	}
}

func (t *addTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	id, _ := args["id"].(string)
	cronExpr, _ := args["cron"].(string)
	prompt, _ := args["prompt"].(string)
	if id == "" || cronExpr == "" || prompt == "" {
		return errorResult("id, cron, and prompt are all required"), nil
	}
	if err := scheduler.ValidateCron(cronExpr); err != nil {
		return errorResult(fmt.Sprintf("invalid cron expression %q: %v", cronExpr, err)), nil
	}
	enabled := true
	if v, ok := args["enabled"].(bool); ok {
		enabled = v
	}
	overlap := config.OverlapSkip
	if v, ok := args["on_overlap"].(string); ok && v != "" {
		switch config.OverlapPolicy(v) {
		case config.OverlapSkip, config.OverlapQueue, config.OverlapKill:
			overlap = config.OverlapPolicy(v)
		default:
			return errorResult(fmt.Sprintf("on_overlap must be skip|queue|kill, got %q", v)), nil
		}
	}

	fileMu.Lock()
	defer fileMu.Unlock()

	cfg, err := config.Load(t.cfgPath)
	if err != nil {
		return errorResult(fmt.Sprintf("load config: %v", err)), nil
	}
	for _, existing := range cfg.Tasks {
		if existing.ID == id {
			return errorResult(fmt.Sprintf("task %q already exists", id)), nil
		}
	}
	cfg.Tasks = append(cfg.Tasks, config.Task{
		ID:        id,
		Cron:      cronExpr,
		Prompt:    prompt,
		Enabled:   enabled,
		OnOverlap: overlap,
	})
	if err := config.Save(t.cfgPath, cfg); err != nil {
		return errorResult(fmt.Sprintf("save config: %v", err)), nil
	}
	return okResult(fmt.Sprintf("added task %q (cron=%q, enabled=%v). Restart the daemon for the schedule to take effect.", id, cronExpr, enabled), map[string]any{
		"id":   id,
		"path": t.cfgPath,
	}), nil
}

// ─── cron_list ─────────────────────────────────────────────────────────────

type listTool struct{ cfgPath string }

func (t *listTool) Name() string        { return "cron_list" }
func (t *listTool) Label() string       { return "List Cron Tasks" }
func (t *listTool) Description() string { return `List all tasks currently configured in modu_cron, with their cron expression, enabled flag, overlap policy, and prompt.` }

func (t *listTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *listTool) Execute(ctx context.Context, _ string, _ map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	cfg, err := config.Load(t.cfgPath)
	if err != nil {
		return errorResult(fmt.Sprintf("load config: %v", err)), nil
	}
	if len(cfg.Tasks) == 0 {
		return okResult("(no tasks configured)", map[string]any{"count": 0}), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d task(s):\n", len(cfg.Tasks))
	for _, task := range cfg.Tasks {
		enabled := "off"
		if task.Enabled {
			enabled = "on"
		}
		policy := task.Policy()
		fmt.Fprintf(&b, "- %s [%s, %s, %s]: %s\n", task.ID, task.Cron, enabled, policy, task.Prompt)
	}
	tasksJSON, _ := json.Marshal(cfg.Tasks)
	return okResult(strings.TrimRight(b.String(), "\n"), map[string]any{
		"count": len(cfg.Tasks),
		"tasks": json.RawMessage(tasksJSON),
	}), nil
}

// ─── cron_remove ───────────────────────────────────────────────────────────

type removeTool struct{ cfgPath string }

func (t *removeTool) Name() string  { return "cron_remove" }
func (t *removeTool) Label() string { return "Remove Cron Task" }
func (t *removeTool) Description() string {
	return `Remove a scheduled task from modu_cron config by id. Returns an error if no task matches. After removing, tell the user to restart the daemon for the change to take effect.`
}

func (t *removeTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "Task id to remove",
			},
		},
		"required": []string{"id"},
	}
}

func (t *removeTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return errorResult("id is required"), nil
	}

	fileMu.Lock()
	defer fileMu.Unlock()

	cfg, err := config.Load(t.cfgPath)
	if err != nil {
		return errorResult(fmt.Sprintf("load config: %v", err)), nil
	}
	for i, t2 := range cfg.Tasks {
		if t2.ID == id {
			cfg.Tasks = append(cfg.Tasks[:i], cfg.Tasks[i+1:]...)
			if err := config.Save(t.cfgPath, cfg); err != nil {
				return errorResult(fmt.Sprintf("save config: %v", err)), nil
			}
			return okResult(fmt.Sprintf("removed task %q. Restart the daemon for the change to take effect.", id), map[string]any{
				"id":   id,
				"path": t.cfgPath,
			}), nil
		}
	}
	return errorResult(fmt.Sprintf("task %q not found", id)), nil
}

// ─── helpers ───────────────────────────────────────────────────────────────

func okResult(text string, details map[string]any) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
		Details: details,
	}
}

func errorResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
	}
}
