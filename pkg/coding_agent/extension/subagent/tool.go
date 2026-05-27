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
// The tool name is "subagent". The extension also exposes "spawn_subagent" as
// a compatibility alias for callers that still use the old tool surface.
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
    replaced with the prior step's reply before dispatch.

Management actions:
  - list: show discovered subagent profiles.
  - get: show one profile's full detail; requires "agent".
  - create: create a new profile; requires "config" object with name plus
    optional description / systemPrompt / tools / model / scope etc.
  - update: merge updates into an existing profile; requires "agent" and "config".
  - delete: remove a profile; requires "agent".
  - status: show runtime background subagent tasks; pass id for one task.
  - resume: restart a completed/failed/interrupted background task with a follow-up message.
  - interrupt: cancel a live background task in this process.
  - doctor: show read-only setup diagnostics.
  - intercom: read the per-task intercom inbox; pair with the subagent_intercom_send tool to send messages.`)
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
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "get", "create", "update", "delete", "status", "resume", "interrupt", "doctor", "intercom"},
				"description": "Management action. Omit for execution mode.",
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Task id or prefix for action=status|resume|interrupt.",
			},
			"config": map[string]any{
				"type":        "object",
				"description": "Profile config for action=create|update. Recognised keys: name, description, systemPrompt, scope, model, tools, disallowed_tools, skills, memory, permission_mode, background, effort, isolation, default_context, thinking, max_turns, default_reads, default_progress, harness_block_tools.",
			},
			"agentScope": map[string]any{
				"type":        "string",
				"enum":        []string{"user", "project", "both"},
				"description": "Filter discovered agents by source for action=list|get. Default 'both'.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Follow-up message for action=resume, or optional reason for action=interrupt.",
			},
			"agent": map[string]any{
				"type":        "string",
				"description": "Profile name (required for single mode).",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "Task description (required for single mode).",
			},
			"async": map[string]any{
				"type":        "boolean",
				"description": "Single mode only. When true, run this call in the background and return a task id; when false, force foreground even if the profile defaults to background.",
			},
			"output": map[string]any{
				"type":        "string",
				"description": "Optional file path for saving execution output. Relative paths are stored under the subagent tool-results directory.",
			},
			"outputMode": map[string]any{
				"type":        "string",
				"enum":        []string{"inline", "file-only"},
				"description": "When output is set, inline appends a saved-file reference after the normal output; file-only returns only the saved-file reference.",
			},
			"reads": map[string]any{
				"description": "Optional files the child should read before running. Use an array of paths, true to use the profile default, or false to disable profile defaults.",
				"oneOf": []any{
					map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					map[string]any{"type": "boolean"},
				},
			},
			"progress": map[string]any{
				"type":        "boolean",
				"description": "When true, instruct the child to maintain progress.md for this run; false disables profile defaults.",
			},
			"chainDir": map[string]any{
				"type":        "string",
				"description": "Optional directory used for progress.md and relative reads in chain-style runs.",
			},
			"context": map[string]any{
				"type":        "string",
				"enum":        []string{"fresh", "fork"},
				"description": "Run with fresh context (default) or fork a copy of the parent session messages.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional model override for this execution.",
			},
			"skill": map[string]any{
				"description": "Skill override for this execution. Use a string, array of strings, true for profile defaults, or false to disable profile skills.",
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					map[string]any{"type": "boolean"},
				},
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Optional child working directory. Relative paths resolve against the parent session cwd.",
			},
			"thinking": map[string]any{
				"type":        "string",
				"description": "Per-call thinking level override (off/minimal/low/medium/high/xhigh). Empty inherits the profile's setting.",
			},
			"includeProgress": map[string]any{
				"type":        "boolean",
				"description": "When true, append the progress.md body to the tool result after a '---\\n\\n## Progress' marker. Useful for chained runs where the orchestrator wants the full trace alongside the final reply.",
			},
			"artifacts": map[string]any{
				"type":        "boolean",
				"description": "When true, write per-run debug artifacts (input/output/metadata) under tool-results/<project>/subagents/artifacts/<runID>/.",
			},
			"sessionDir": map[string]any{
				"type":        "string",
				"description": "Parent dir for background-child run files (session.jsonl, status.json). Relative paths resolve against the parent session cwd. Only meaningful for background forks.",
			},
			"clarify": map[string]any{
				"type":        "boolean",
				"description": "When true, show a preview of what would run and require host confirmation before dispatching. The non-TUI fallback uses api.Confirm — see PARITY.md for the in-line edit gap.",
			},
			"control": map[string]any{
				"type": "object",
				"description": "Subagent control overrides. Today only activeNoticeAfterMs is wired " +
					"(emits api.Notify when a batch async run is still running past that many ms); " +
					"other fields are accepted but not yet honored — see PARITY.md.",
				"properties": map[string]any{
					"enabled":                           map[string]any{"type": "boolean"},
					"activeNoticeAfterMs":               map[string]any{"type": "integer", "minimum": 1},
					"needsAttentionAfterMs":             map[string]any{"type": "integer", "minimum": 1},
					"activeNoticeAfterTurns":            map[string]any{"type": "integer", "minimum": 1},
					"activeNoticeAfterTokens":           map[string]any{"type": "integer", "minimum": 1},
					"failedToolAttemptsBeforeAttention": map[string]any{"type": "integer", "minimum": 1},
					"notifyOn":                          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"notifyChannels":                    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			"parallel": map[string]any{
				"type":        "array",
				"description": "List of {agent, task, output?, outputMode?, reads?, progress?, model?, skill?} pairs to run concurrently (required for parallel mode).",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent":      map[string]any{"type": "string"},
						"task":       map[string]any{"type": "string"},
						"output":     map[string]any{"type": "string"},
						"outputMode": map[string]any{"type": "string", "enum": []string{"inline", "file-only"}},
						"reads": map[string]any{
							"oneOf": []any{
								map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								map[string]any{"type": "boolean"},
							},
						},
						"progress": map[string]any{"type": "boolean"},
						"chainDir": map[string]any{"type": "string"},
						"model":    map[string]any{"type": "string"},
						"cwd":      map[string]any{"type": "string"},
						"thinking": map[string]any{"type": "string"},
						"skill": map[string]any{
							"oneOf": []any{
								map[string]any{"type": "string"},
								map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								map[string]any{"type": "boolean"},
							},
						},
					},
					"required": []string{"agent", "task"},
				},
			},
			"tasks": map[string]any{
				"type":        "array",
				"description": "Pi-style parallel task list. Same item shape as parallel, with optional count to repeat an item.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent":      map[string]any{"type": "string"},
						"task":       map[string]any{"type": "string"},
						"count":      map[string]any{"type": "integer", "minimum": 1},
						"output":     map[string]any{"type": "string"},
						"outputMode": map[string]any{"type": "string", "enum": []string{"inline", "file-only"}},
						"reads":      map[string]any{"oneOf": []any{map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, map[string]any{"type": "boolean"}}},
						"progress":   map[string]any{"type": "boolean"},
						"chainDir":   map[string]any{"type": "string"},
						"model":      map[string]any{"type": "string"},
						"cwd":        map[string]any{"type": "string"},
						"thinking":   map[string]any{"type": "string"},
						"skill":      map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, map[string]any{"type": "boolean"}}},
					},
					"required": []string{"agent", "task"},
				},
			},
			"concurrency": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Parallel/tasks mode max concurrent child runs. Defaults to unbounded for the current call.",
			},
			"worktree": map[string]any{
				"type":        "boolean",
				"description": "Parallel/tasks mode: force every child into an isolated git worktree (overrides per-profile isolation).",
			},
			"chain": map[string]any{
				"type":        "array",
				"description": "Sequential list of {agent, task, ...} steps or {parallel:[...], concurrency?} groups. {previous} in task is substituted with the prior step or group reply.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent":      map[string]any{"type": "string"},
						"task":       map[string]any{"type": "string"},
						"output":     map[string]any{"type": "string"},
						"outputMode": map[string]any{"type": "string", "enum": []string{"inline", "file-only"}},
						"reads": map[string]any{
							"oneOf": []any{
								map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								map[string]any{"type": "boolean"},
							},
						},
						"progress": map[string]any{"type": "boolean"},
						"chainDir": map[string]any{"type": "string"},
						"model":    map[string]any{"type": "string"},
						"cwd":      map[string]any{"type": "string"},
						"thinking": map[string]any{"type": "string"},
						"skill": map[string]any{
							"oneOf": []any{
								map[string]any{"type": "string"},
								map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
								map[string]any{"type": "boolean"},
							},
						},
						"parallel":    map[string]any{"type": "array"},
						"concurrency": map[string]any{"type": "integer", "minimum": 1},
						"failFast":    map[string]any{"type": "boolean", "description": "When the parallel group hits a failure, cancel in-flight siblings and abort the surrounding chain."},
						"worktree":    map[string]any{"type": "boolean", "description": "Force every item in this parallel group into an isolated git worktree (overrides per-profile isolation)."},
					},
				},
			},
		},
	}
}

func (t *subagentTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	if action, _ := args["action"].(string); action != "" {
		text, err := runAction(ctx, t.ext, action, args)
		if err != nil {
			return errResult(fmt.Sprintf("subagent: %v", err)), nil
		}
		return okResult(text, detailsFromText(text)), nil
	}

	mode, _ := args["mode"].(string)
	if mode == "" {
		if _, ok := args["chain"]; ok {
			mode = "chain"
		} else if _, ok := args["parallel"]; ok {
			mode = "parallel"
		} else if _, ok := args["tasks"]; ok {
			mode = "parallel"
		} else {
			mode = "single"
		}
	}

	// Clarify gate: build a preview of what would run and confirm with the
	// user before dispatching. Without a TUI we cannot offer an in-line
	// edit step, so the gate is preview + yes/no.
	if clarifyRequested(args) {
		if proceed, abortText := runClarifyGate(t.ext, mode, args); !proceed {
			return okResult(abortText, nil), nil
		}
	}

	// Top-level parallel/chain/tasks calls can elect to run as a single
	// background batch — either via `async:true` or via the
	// force_top_level_async config. The single-mode async path is handled
	// inside runSingle and uses the host's per-child Background flag.
	if shouldBatchAsync(t.ext, args, mode) {
		reply, dispatchErr := dispatchBatchAsync(ctx, t.ext, mode, args)
		if dispatchErr != nil {
			return errResult(fmt.Sprintf("subagent: %v", dispatchErr)), nil
		}
		return okResult(reply, detailsFromText(reply)), nil
	}

	// Optional per-run debug artifacts. The synchronous branch wraps the
	// dispatch directly. The batch async branch handles its own artifact
	// lifecycle inside dispatchBatchAsync so the run id matches the batch
	// task id callers poll for.
	var artRun *artifactRun
	if isArtifactsRequested(args) {
		var artErr error
		artRun, artErr = startArtifactRun(t.ext, "", mode, args)
		if artErr != nil {
			return errResult(fmt.Sprintf("subagent: %v", artErr)), nil
		}
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
		_ = artRun.complete("", err)
		return errResult(fmt.Sprintf("subagent: %v", err)), nil
	}
	text, err = applyOutputOptions(t.ext, args, text)
	if err != nil {
		_ = artRun.complete(text, err)
		return errResult(fmt.Sprintf("subagent: %v", err)), nil
	}
	text = appendIncludedProgress(t.ext, args, text)
	if artRun != nil {
		_ = artRun.complete(text, nil)
		text = strings.TrimRight(text, "\n") + "\n\n[artifacts: " + artRun.path() + "]"
	}
	details := map[string]string{}
	if agentName, _ := args["agent"].(string); agentName != "" {
		details["subagent"] = agentName
	}
	if taskID := extractTaskID(text); taskID != "" {
		details["task_id"] = taskID
		details["status"] = "running"
	}
	return okResult(text, details), nil
}

func detailsFromText(text string) map[string]string {
	taskID := extractTaskID(text)
	if taskID == "" {
		return nil
	}
	return map[string]string{"task_id": taskID, "status": "running"}
}

func okResult(text string, details map[string]string) agent.AgentToolResult {
	result := agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
	if len(details) > 0 {
		result.Details = details
	}
	return result
}

func errResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		IsError: true,
	}
}

func extractTaskID(text string) string {
	const marker = "task_id="
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(marker):]
	end := len(rest)
	for i, r := range rest {
		if r == '.' || r == ',' || r == ')' || r == ' ' || r == '\n' || r == '\t' {
			end = i
			break
		}
	}
	return rest[:end]
}
