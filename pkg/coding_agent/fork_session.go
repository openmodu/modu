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
// It mirrors the old spawn_subagent dispatch tree so extension callers get
// the same capabilities:
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
	childCwd := cs.resolveChildCwd(opts.Cwd)
	def := &subagent.SubagentDefinition{
		Name:            forkName(opts),
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

	initialMessages, err := cs.initialMessagesForFork(opts.Context, opts.ParentTaskID)
	if err != nil {
		return "", err
	}

	if opts.Background {
		return cs.forkInBackground(ctx, def, childCwd, initialMessages, opts.Task, opts.ParentTaskID, opts.OutputPath, opts.OutputMode, cs.resolveForkSessionDir(opts.SessionDir))
	}
	cs.OnSubagentStart(def.Name, opts.Task, false)
	var (
		result string
	)
	if strings.EqualFold(opts.Isolation, "worktree") {
		result, err = cs.forkInWorktree(ctx, def, initialMessages, opts.Task)
	} else {
		tools := cs.activeTools
		if childCwd != cs.cwd {
			tools = rebindToolsToCwd(cs.activeTools, childCwd)
		}
		runResult, runErr := subagent.RunWithMessages(
			ctx,
			subagent.WithWorkingDirectory(def, childCwd),
			initialMessages,
			opts.Task,
			tools,
			cs.model,
			cs.getAPIKey,
			cs.streamFn,
		)
		result, err = runResult.Text, runErr
	}
	cs.OnSubagentStop(def.Name, opts.Task, false, result, err)
	return result, err
}

// forkInBackground launches the child on its own goroutine. Returns a
// short string the model can pass to task_output to follow up. If the
// session has no task manager, surfaces a clear error instead of silently
// dropping the request. When sessionDirOverride is non-empty the task's
// session.jsonl/status.json land under that parent dir; otherwise the
// task manager picks its default run root.
func (cs *CodingSession) forkInBackground(ctx context.Context, def *subagent.SubagentDefinition, childCwd string, initialMessages []agent.AgentMessage, task, parentID, outputPath, outputMode, sessionDirOverride string) (string, error) {
	if cs.taskManager == nil {
		return "", fmt.Errorf("background fork requested but task manager is not configured")
	}
	name := "extension-fork"
	if def != nil && strings.TrimSpace(def.Name) != "" {
		name = def.Name
	}
	outputPath = cs.resolveForkOutputPath(outputPath)
	taskID := cs.taskManager.CreateWithMetadataInDir("subagent", fmt.Sprintf("%s: %s", name, task), name, task, parentID, outputPath, sessionDirOverride)
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cs.taskManager.RegisterCancel(taskID, cancel)
	go func() {
		defer cs.taskManager.UnregisterCancel(taskID)
		if def != nil {
			cs.OnSubagentStart(def.Name, task, true)
		}
		tools := cs.activeTools
		if childCwd != cs.cwd {
			tools = rebindToolsToCwd(cs.activeTools, childCwd)
		}
		result, err := subagent.RunWithMessages(
			runCtx,
			subagent.WithWorkingDirectory(def, childCwd),
			initialMessages,
			task,
			tools,
			cs.model,
			cs.getAPIKey,
			cs.streamFn,
		)
		text := result.Text
		if taskRecord, ok := cs.taskManager.Get(taskID); ok {
			if writeErr := writeSubagentSessionFile(taskRecord.SessionFile, childCwd, cs.GetSessionID(), taskID, result.Messages); writeErr != nil && err == nil {
				err = writeErr
			}
		}
		if err == nil && strings.TrimSpace(outputPath) != "" {
			savedText, saveErr := saveForkOutput(outputPath, outputMode, text)
			if saveErr != nil {
				err = saveErr
			} else {
				text = savedText
			}
		}
		if def != nil {
			cs.OnSubagentStop(def.Name, task, true, text, err)
		}
		if err != nil {
			cs.taskManager.Fail(taskID, err.Error())
			return
		}
		cs.taskManager.Complete(taskID, text)
	}()
	return fmt.Sprintf("Started extension-fork in background. Use task_output with task_id=%s to inspect the result.", taskID), nil
}

func (cs *CodingSession) resolveChildCwd(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return cs.cwd
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(filepath.Join(cs.cwd, cwd))
}

func (cs *CodingSession) resolveForkOutputPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cs.RuntimePaths().ToolResultsDir, "subagents", path)
}

// resolveForkSessionDir turns a caller-supplied session dir override into
// an absolute path. Empty input passes through so the host's default run
// root is used. Relative input resolves against the parent session's cwd
// to match how Cwd/OutputPath are treated.
func (cs *CodingSession) resolveForkSessionDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Clean(filepath.Join(cs.cwd, path))
}

func saveForkOutput(path, mode, text string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	ref := fmt.Sprintf("Output saved to: %s (%d bytes, %d lines).", path, len([]byte(text)), countLines(text))
	if strings.EqualFold(strings.TrimSpace(mode), "file-only") {
		return ref, nil
	}
	return strings.TrimSpace(text) + "\n\n" + ref, nil
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func (cs *CodingSession) loadSubagentParentMessages(parentID string) ([]agent.AgentMessage, error) {
	if strings.TrimSpace(parentID) == "" || cs.taskManager == nil {
		return nil, nil
	}
	parent, ok := cs.taskManager.Get(parentID)
	if !ok || strings.TrimSpace(parent.SessionFile) == "" {
		return nil, nil
	}
	return loadSubagentSessionMessages(parent.SessionFile)
}

func (cs *CodingSession) initialMessagesForFork(mode, parentID string) ([]agent.AgentMessage, error) {
	if strings.TrimSpace(parentID) != "" {
		return cs.loadSubagentParentMessages(parentID)
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "fresh":
		return nil, nil
	case "fork":
		if cs == nil || cs.agent == nil {
			return nil, nil
		}
		return append([]agent.AgentMessage(nil), cs.agent.GetState().Messages...), nil
	default:
		return nil, fmt.Errorf("unknown fork context %q (expected fresh|fork)", mode)
	}
}

// forkInWorktree creates a detached git worktree, rebinds file/shell
// tools to that path, runs the child, and removes the worktree on exit.
// Mirrors the legacy spawn_subagent worktree behavior closely.
func (cs *CodingSession) forkInWorktree(ctx context.Context, def *subagent.SubagentDefinition, initialMessages []agent.AgentMessage, task string) (string, error) {
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
	result, err := subagent.RunWithMessages(
		ctx,
		subagent.WithWorkingDirectory(def, path),
		initialMessages,
		task,
		rebound,
		cs.model,
		cs.getAPIKey,
		cs.streamFn,
	)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func forkName(opts extension.ForkOptions) string {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return "extension-fork"
	}
	return name
}

// rebindToolsToCwd returns a copy of tools where cwd-bound file/shell
// tools point at the given path. Unknown tools pass through unchanged.
// Duplicate of the legacy spawn_subagent tool rebinding behavior.
func rebindToolsToCwd(allTools []agent.Tool, cwd string) []agent.Tool {
	out := make([]agent.Tool, 0, len(allTools))
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
			if rebindable, ok := tool.(interface{ WithCwd(string) agent.Tool }); ok {
				out = append(out, rebindable.WithCwd(cwd))
			} else {
				out = append(out, tool)
			}
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
