// Package crontools exposes cron_add / cron_list / cron_remove as
// types.Tool implementations, so the CodingSession driving each task
// can manage the modu_cron task file via natural language.
//
// All three tools serialize on the same package-level mutex so concurrent
// task executions (queue/kill policies, or just two tasks firing in the
// same second) cannot race on the YAML file. Mutating tools additionally
// take an advisory flock (<config>.lock) so writers in other processes —
// the `modu_code cron daemon` process vs an interactive/Telegram modu_code
// session using the cron extension — are serialized too.
//
// The daemon hot-reloads config.yaml, so task changes take effect without
// restart when the daemon is running.
package crontools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/scheduler"
)

// fileMu guards all reads + writes of the modu_cron config file performed by
// these tools. Held for the duration of a load-modify-save sequence.
var fileMu sync.Mutex

// New returns the three cron-management tools bound to cfgPath.
func New(cfgPath string) []types.Tool {
	return []types.Tool{
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
	return `Add a new scheduled task to the modu_cron config. The cron expression uses the 6-field form (second minute hour day-of-month month day-of-week). The id must be unique. The daemon hot-reloads config changes when it is running.`
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
			"goal": map[string]any{
				"type":        "string",
				"description": "Optional objective to create as a persistent goal when this task fires. The runner continues hidden goal turns until update_goal is verified complete or a cap stops the run.",
			},
			"timezone": map[string]any{
				"type":        "string",
				"description": "Optional IANA timezone for the cron schedule, e.g. \"Asia/Shanghai\". Empty means the scheduler process local timezone.",
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
			"channels": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional notification channel names to notify when the task completes",
			},
		},
		"required": []string{"id", "cron", "prompt"},
	}
}

func (t *addTool) Execute(ctx context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	id, _ := args["id"].(string)
	cronExpr, _ := args["cron"].(string)
	prompt, _ := args["prompt"].(string)
	goalText, _ := args["goal"].(string)
	timezone, _ := args["timezone"].(string)
	if id == "" || cronExpr == "" || prompt == "" {
		return errorResult("id, cron, and prompt are all required"), nil
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
	channels, err := parseChannels(args["channels"])
	if err != nil {
		return errorResult(err.Error()), nil
	}
	task := config.Task{
		ID:        id,
		Cron:      cronExpr,
		Prompt:    prompt,
		Goal:      strings.TrimSpace(goalText),
		Timezone:  strings.TrimSpace(timezone),
		Enabled:   enabled,
		OnOverlap: overlap,
		Channels:  channels,
	}
	if err := scheduler.ValidateTaskCron(task); err != nil {
		return errorResult(fmt.Sprintf("invalid cron expression %q: %v", cronExpr, err)), nil
	}

	fileMu.Lock()
	defer fileMu.Unlock()
	if unlock, lockErr := acquireFileLock(t.cfgPath + ".lock"); lockErr == nil {
		defer unlock()
	}

	cfg, err := config.Load(t.cfgPath)
	if err != nil {
		return errorResult(fmt.Sprintf("load config: %v", err)), nil
	}
	for _, existing := range cfg.Tasks {
		if existing.ID == id {
			return errorResult(fmt.Sprintf("task %q already exists", id)), nil
		}
	}
	cfg.Tasks = append(cfg.Tasks, task)
	if err := cfg.Tasks[len(cfg.Tasks)-1].ValidateCaps(); err != nil {
		return errorResult(err.Error()), nil
	}
	taskPath := config.ResolveTasksPath(t.cfgPath, cfg)
	if err := config.SaveTasks(taskPath, cfg.Tasks); err != nil {
		return errorResult(fmt.Sprintf("save tasks: %v", err)), nil
	}
	return okResult(fmt.Sprintf("added task %q (cron=%q, enabled=%v). The daemon will hot-reload the config if it is running.", id, cronExpr, enabled), map[string]any{
		"id":   id,
		"path": taskPath,
	}), nil
}

// ─── cron_list ─────────────────────────────────────────────────────────────

type listTool struct{ cfgPath string }

func (t *listTool) Name() string  { return "cron_list" }
func (t *listTool) Label() string { return "List Cron Tasks" }
func (t *listTool) Description() string {
	return `List all tasks currently configured in modu_cron, with their cron expression, timezone, enabled flag, overlap policy, notification channels, prompt, and configured notification channel names.`
}

func (t *listTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *listTool) Execute(ctx context.Context, _ string, _ map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	cfg, err := config.Load(t.cfgPath)
	if err != nil {
		return errorResult(fmt.Sprintf("load config: %v", err)), nil
	}
	if len(cfg.Tasks) == 0 {
		text := "(no tasks configured)"
		if chText := configuredChannelsText(cfg); chText != "" {
			text += "\n" + chText
		}
		return okResult(text, map[string]any{"count": 0}), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d task(s):\n", len(cfg.Tasks))
	for _, task := range cfg.Tasks {
		enabled := "off"
		if task.Enabled {
			enabled = "on"
		}
		policy := task.Policy()
		channelNames := task.NotificationChannels()
		channelText := ""
		if len(channelNames) > 0 {
			channelText = ", channels=" + strings.Join(channelNames, "|")
		}
		timezoneText := ""
		if strings.TrimSpace(task.Timezone) != "" {
			timezoneText = ", timezone=" + strings.TrimSpace(task.Timezone)
		}
		goalText := ""
		if strings.TrimSpace(task.Goal) != "" {
			goalText = ", goal"
		}
		fmt.Fprintf(&b, "- %s [%s, %s, %s%s%s%s]: %s\n", task.ID, task.Cron, enabled, policy, timezoneText, channelText, goalText, task.Prompt)
	}
	if chText := configuredChannelsText(cfg); chText != "" {
		fmt.Fprintf(&b, "\n%s\n", chText)
	}
	tasksJSON, _ := json.Marshal(cfg.Tasks)
	return okResult(strings.TrimRight(b.String(), "\n"), map[string]any{
		"count": len(cfg.Tasks),
		"tasks": json.RawMessage(tasksJSON),
	}), nil
}

func configuredChannelsText(cfg *config.Config) string {
	if cfg == nil || len(cfg.Channels) == 0 {
		return ""
	}
	names := make([]string, 0, len(cfg.Channels))
	for name, ch := range cfg.Channels {
		typ := strings.TrimSpace(ch.Type)
		if typ == "" {
			typ = "unknown"
		}
		names = append(names, fmt.Sprintf("%s(%s)", name, typ))
	}
	sort.Strings(names)
	return "configured channels: " + strings.Join(names, ", ")
}

// ─── cron_remove ───────────────────────────────────────────────────────────

type removeTool struct{ cfgPath string }

func (t *removeTool) Name() string  { return "cron_remove" }
func (t *removeTool) Label() string { return "Remove Cron Task" }
func (t *removeTool) Description() string {
	return `Remove a scheduled task from modu_cron config by id. Returns an error if no task matches. The daemon hot-reloads config changes when it is running.`
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

func (t *removeTool) Execute(ctx context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return errorResult("id is required"), nil
	}

	fileMu.Lock()
	defer fileMu.Unlock()
	if unlock, lockErr := acquireFileLock(t.cfgPath + ".lock"); lockErr == nil {
		defer unlock()
	}

	cfg, err := config.Load(t.cfgPath)
	if err != nil {
		return errorResult(fmt.Sprintf("load config: %v", err)), nil
	}
	for i, t2 := range cfg.Tasks {
		if t2.ID == id {
			cfg.Tasks = append(cfg.Tasks[:i], cfg.Tasks[i+1:]...)
			taskPath := config.ResolveTasksPath(t.cfgPath, cfg)
			if err := config.SaveTasks(taskPath, cfg.Tasks); err != nil {
				return errorResult(fmt.Sprintf("save tasks: %v", err)), nil
			}
			return okResult(fmt.Sprintf("removed task %q. The daemon will hot-reload the config if it is running.", id), map[string]any{
				"id":   id,
				"path": taskPath,
			}), nil
		}
	}
	return errorResult(fmt.Sprintf("task %q not found", id)), nil
}

// ─── helpers ───────────────────────────────────────────────────────────────

func okResult(text string, details map[string]any) types.ToolResult {
	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
		Details: details,
	}
}

func errorResult(text string) types.ToolResult {
	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
	}
}

func parseChannels(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	values, ok := v.([]any)
	if !ok {
		if typed, ok := v.([]string); ok {
			values = make([]any, 0, len(typed))
			for _, s := range typed {
				values = append(values, s)
			}
		} else {
			return nil, fmt.Errorf("channels must be an array of strings")
		}
	}
	var out []string
	seen := make(map[string]bool)
	for _, item := range values {
		name, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("channels must be an array of strings")
		}
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}
