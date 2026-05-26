package coding_agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/coding_agent/tools"
)

// forkSession is the host-side implementation of ExtensionAPI.ForkSession.
//
// It mirrors the dispatch tree used by tools.SpawnSubagentTool so extension
// callers get the same capabilities as the inline spawn_subagent path:
//
//   - opts.Background: schedule the child on a goroutine, return a task-id
//     reference, surface completion through the session's taskManager.
//   - opts.Isolation == "worktree": create a fresh git worktree, rebind
//     file/shell tools to it, run the child there, and remove the worktree
//     on return.
//   - default: run the child synchronously in the caller's cwd.
//
// The synchronous and worktree paths both flow through subagent.Run after
// prepareSubagentDefinition has layered in skills / memory / harness-block
// directives — extension callers therefore get the same system-prompt
// augmentation spawn_subagent gives.
func (cs *CodingSession) forkSession(ctx context.Context, opts extension.ForkOptions) (string, error) {
	def := &subagent.SubagentDefinition{
		Name:            "extension-fork",
		SystemPrompt:    opts.SystemPrompt,
		Tools:           append([]string(nil), opts.AllowedTools...),
		DisallowedTools: append([]string(nil), opts.DisallowedTools...),
		Skills:          append([]string(nil), opts.Skills...),
		MemoryScope:     opts.MemoryScope,
		Model:           opts.Model,
		ThinkingLevel:   agent.ThinkingLevel(opts.ThinkingLevel),
		PermissionMode:  opts.PermissionMode,
		MaxTurns:        opts.MaxTurns,
		Background:      opts.Background,
		Isolation:       opts.Isolation,
	}
	def = prepareSubagentDefinition(def, cs.skillManager, cs.memoryStore)

	if opts.Background {
		return cs.forkInBackground(def, opts.Task)
	}
	if strings.EqualFold(opts.Isolation, "worktree") {
		return cs.forkInWorktree(ctx, def, opts.Task)
	}
	return subagent.Run(
		ctx,
		subagent.WithWorkingDirectory(def, cs.cwd),
		opts.Task,
		cs.activeTools,
		cs.model,
		cs.getAPIKey,
		cs.streamFn,
	)
}

// forkInBackground launches the child on its own goroutine. Returns a
// short string the model can pass to task_output to follow up. If the
// session has no task manager, surfaces a clear error instead of silently
// dropping the request.
func (cs *CodingSession) forkInBackground(def *subagent.SubagentDefinition, task string) (string, error) {
	if cs.taskManager == nil {
		return "", fmt.Errorf("background fork requested but task manager is not configured")
	}
	taskID := cs.taskManager.Create("subagent", fmt.Sprintf("extension-fork: %s", task))
	go func() {
		// context.Background: the child outlives whatever ctx the model
		// passed in. The task manager owns the lifecycle now.
		text, err := subagent.Run(
			context.Background(),
			subagent.WithWorkingDirectory(def, cs.cwd),
			task,
			cs.activeTools,
			cs.model,
			cs.getAPIKey,
			cs.streamFn,
		)
		if err != nil {
			cs.taskManager.Fail(taskID, err.Error())
			return
		}
		cs.taskManager.Complete(taskID, text)
	}()
	return fmt.Sprintf("Started extension-fork in background. Use task_output with task_id=%s to inspect the result.", taskID), nil
}

// forkInWorktree creates a detached git worktree, rebinds file/shell
// tools to that path, runs the child, and removes the worktree on exit.
// Mirrors tools/spawn_subagent.go runInWorktree closely — the duplication
// is short-lived (phase 3.2.B.4 removes the inline path).
func (cs *CodingSession) forkInWorktree(ctx context.Context, def *subagent.SubagentDefinition, task string) (string, error) {
	root, err := gitTopLevelDir(cs.cwd)
	if err != nil {
		return "", fmt.Errorf("worktree isolation requires a git repository: %w", err)
	}
	baseDir := filepath.Join(cs.agentDir, "worktrees")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(baseDir, fmt.Sprintf("subagent-wt-%d", time.Now().UnixMilli()))
	if _, err := runGitCommand(root, "worktree", "add", "--detach", path, "HEAD"); err != nil {
		return "", err
	}
	defer func() {
		// Best-effort cleanup: if the worktree leaks the user can prune
		// it manually via `git worktree prune`.
		_, _ = runGitCommand(root, "worktree", "remove", "--force", path)
	}()
	rebound := rebindToolsToCwd(cs.activeTools, path)
	return subagent.Run(
		ctx,
		subagent.WithWorkingDirectory(def, path),
		task,
		rebound,
		cs.model,
		cs.getAPIKey,
		cs.streamFn,
	)
}

// rebindToolsToCwd returns a copy of tools where cwd-bound file/shell
// tools point at the given path. Unknown tools pass through unchanged.
// Duplicate of tools/spawn_subagent.go rebindToolsForCwd — both go away
// once the inline spawn_subagent path is removed.
func rebindToolsToCwd(allTools []agent.AgentTool, cwd string) []agent.AgentTool {
	out := make([]agent.AgentTool, 0, len(allTools))
	for _, tool := range allTools {
		switch tool.Name() {
		case "read":
			out = append(out, tools.NewReadTool(cwd))
		case "write":
			out = append(out, tools.NewWriteTool(cwd))
		case "edit":
			out = append(out, tools.NewEditTool(cwd))
		case "bash":
			out = append(out, tools.NewBashTool(cwd))
		case "grep":
			out = append(out, tools.NewGrepTool(cwd))
		case "find":
			out = append(out, tools.NewFindTool(cwd))
		case "ls":
			out = append(out, tools.NewLsTool(cwd))
		default:
			out = append(out, tool)
		}
	}
	return out
}

// gitTopLevelDir returns the repo root that contains dir, trimmed of
// trailing whitespace. Useful to translate any cwd inside the repo back
// to the canonical worktree root that `git worktree add` accepts.
func gitTopLevelDir(dir string) (string, error) {
	out, err := runGitCommand(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func runGitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
