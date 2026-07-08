package coding_agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/coding_agent/plugins/subagent"
	toolpkg "github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/types"
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
func (cs *engine) forkSession(ctx context.Context, opts extension.ForkOptions) (string, error) {
	childCwd := cs.resolveChildCwd(opts.Cwd)
	def := &subagent.SubagentDefinition{
		Name:            forkName(opts),
		SystemPrompt:    opts.SystemPrompt,
		Tools:           append([]string(nil), opts.AllowedTools...),
		DisallowedTools: append([]string(nil), opts.DisallowedTools...),
		Skills:          append([]string(nil), opts.Skills...),
		MemoryScope:     opts.MemoryScope,
		Model:           opts.Model,
		ThinkingLevel:   types.ThinkingLevel(opts.ThinkingLevel),
		PermissionMode:  opts.PermissionMode,
		MaxTurns:        opts.MaxTurns,
		Background:      opts.Background,
		Isolation:       opts.Isolation,
	}
	memoryEnabled := memoryFeatureEnabled(cs.config)
	def = prepareSubagentDefinition(def, cs.skillManager, cs.memoryStore, memoryEnabled)

	initialMessages, err := cs.initialMessagesForFork(opts.Context, opts.ParentTaskID)
	if err != nil {
		return "", err
	}

	if opts.Background {
		return cs.forkInBackground(ctx, def, childCwd, initialMessages, opts.Task, opts.ParentTaskID, opts.OutputPath, opts.OutputMode, cs.resolveForkSessionDir(opts.SessionDir), opts.BubbleTaskID)
	}
	cs.OnSubagentStart(def.Name, opts.Task, false)
	// Synchronous children bubble their live events only when the caller
	// asked for it via BubbleTaskID (batch dispatch does, so every child of a
	// batch aggregates under the batch id). Plain sync children stay quiet.
	observe := cs.childObserver(opts.BubbleTaskID)
	var (
		result        string
		childMessages []types.AgentMessage
	)
	if strings.EqualFold(opts.Isolation, "worktree") {
		result, childMessages, err = cs.forkInWorktree(ctx, def, initialMessages, opts.Task, observe)
	} else {
		tools := cs.toolsForFork(childCwd, def.Tools)
		runResult, runErr := subagent.RunWithMessagesObserved(
			ctx,
			subagent.WithWorkingDirectory(def, childCwd),
			initialMessages,
			opts.Task,
			tools,
			cs.model,
			cs.getAPIKey,
			cs.streamFn,
			observe,
		)
		result, childMessages, err = runResult.Text, runResult.Messages, runErr
	}
	cs.OnSubagentStop(def.Name, opts.Task, false, result, err)
	cs.emitSubagentChildUsage(opts.BubbleTaskID, childMessages)
	return result, err
}

// childObserver returns an event observer that re-emits a child's events
// under bubbleID, or nil when there is nothing to bubble to. Used for both
// synchronous and background children so the caller can share one code path.
func (cs *engine) childObserver(bubbleID string) func(types.Event) {
	if cs.extensions == nil || bubbleID == "" {
		return nil
	}
	return func(ev types.Event) { cs.emitSubagentChildEvent(bubbleID, ev) }
}

// emitSubagentChildUsage broadcasts a child agent's token usage to
// extensions via the shared event bus. The child transcript carries
// per-assistant-message Usage, so consumers (e.g. the goal extension)
// can fold subagent token spend into their own accounting instead of
// silently undercounting it. No-op when there is nothing to report.
func (cs *engine) emitSubagentChildUsage(taskID string, messages []types.AgentMessage) {
	if cs.extensions == nil || len(messages) == 0 {
		return
	}
	cs.extensions.EmitEvent(types.Event{
		Type:     types.EventType("subagent_child_usage"),
		TaskID:   taskID,
		Messages: messages,
	})
}

// emitSubagentChildEvent re-emits a background child agent's lifecycle events
// to extensions, tagged with the child's task id, so an extension (e.g.
// subagent control) can track a running child's turn count, failed-tool
// count, and token usage in flight rather than only at completion. Only the
// coarse lifecycle events useful for control are forwarded, to keep the bus
// quiet. The original child event type travels in Reason.
func (cs *engine) emitSubagentChildEvent(taskID string, ev types.Event) {
	if cs.extensions == nil || taskID == "" {
		return
	}
	switch ev.Type {
	case types.EventTypeTurnEnd, types.EventTypeToolExecutionEnd, types.EventTypeAgentEnd:
	default:
		return
	}
	cs.extensions.EmitEvent(types.Event{
		Type:     types.EventType("subagent_child_event"),
		TaskID:   taskID,
		Reason:   string(ev.Type),
		ToolName: ev.ToolName,
		Args:     ev.Args,
		Result:   ev.Result,
		IsError:  ev.IsError,
		Message:  ev.Message, // carries per-turn Usage on turn_end
	})
}

// forkInBackground launches the child on its own goroutine. Returns a
// short string the model can pass to task_output to follow up. If the
// session has no task manager, surfaces a clear error instead of silently
// dropping the request. When sessionDirOverride is non-empty the task's
// session.jsonl/status.json land under that parent dir; otherwise the
// task manager picks its default run root.
func (cs *engine) forkInBackground(ctx context.Context, def *subagent.SubagentDefinition, childCwd string, initialMessages []types.AgentMessage, task, parentID, outputPath, outputMode, sessionDirOverride, bubbleOverride string) (string, error) {
	if cs.taskManager == nil {
		return "", fmt.Errorf("background fork requested but task manager is not configured")
	}
	name := "extension-fork"
	if def != nil && strings.TrimSpace(def.Name) != "" {
		name = def.Name
	}
	outputPath = cs.resolveForkOutputPath(outputPath)
	taskID := cs.taskManager.CreateWithMetadataInDir("subagent", fmt.Sprintf("%s: %s", name, task), name, task, parentID, outputPath, sessionDirOverride)
	// Events bubble under the caller-supplied id when set (batch children all
	// share the batch id) so a batch's control counters aggregate across its
	// children; otherwise under this background task's own id.
	bubbleID := taskID
	if bubbleOverride != "" {
		bubbleID = bubbleOverride
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cs.taskManager.RegisterCancel(taskID, cancel)
	go func() {
		defer cs.taskManager.UnregisterCancel(taskID)
		if def != nil {
			cs.OnSubagentStart(def.Name, task, true)
		}
		tools := cs.toolsForFork(childCwd, def.Tools)
		result, err := subagent.RunWithMessagesObserved(
			runCtx,
			subagent.WithWorkingDirectory(def, childCwd),
			initialMessages,
			task,
			tools,
			cs.model,
			cs.getAPIKey,
			cs.streamFn,
			cs.childObserver(bubbleID),
		)
		text := result.Text
		cs.emitSubagentChildUsage(bubbleID, result.Messages)
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

func (cs *engine) resolveChildCwd(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return cs.cwd
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(filepath.Join(cs.cwd, cwd))
}

func (cs *engine) resolveForkOutputPath(path string) string {
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
func (cs *engine) resolveForkSessionDir(path string) string {
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

func (cs *engine) loadSubagentParentMessages(parentID string) ([]types.AgentMessage, error) {
	if strings.TrimSpace(parentID) == "" || cs.taskManager == nil {
		return nil, nil
	}
	parent, ok := cs.taskManager.Get(parentID)
	if !ok || strings.TrimSpace(parent.SessionFile) == "" {
		return nil, nil
	}
	return loadSubagentSessionMessages(parent.SessionFile)
}

func (cs *engine) initialMessagesForFork(mode, parentID string) ([]types.AgentMessage, error) {
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
		return append([]types.AgentMessage(nil), cs.agent.GetState().Messages...), nil
	default:
		return nil, fmt.Errorf("unknown fork context %q (expected fresh|fork)", mode)
	}
}

// forkInWorktree creates a detached git worktree, rebinds file/shell
// tools to that path, runs the child, and removes the worktree on exit.
// Mirrors the legacy spawn_subagent worktree behavior closely.
func (cs *engine) forkInWorktree(ctx context.Context, def *subagent.SubagentDefinition, initialMessages []types.AgentMessage, task string, observe func(types.Event)) (string, []types.AgentMessage, error) {
	root, err := gitTopLevelDir(cs.cwd)
	if err != nil {
		return "", nil, fmt.Errorf("worktree isolation requires a git repository: %w", err)
	}
	baseDir := filepath.Join(cs.agentDir, "worktrees")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", nil, err
	}
	path := filepath.Join(baseDir, uuid.NewString(), filepath.Base(root))
	if _, err := runGitCommand(root, "worktree", "add", "--detach", path, "HEAD"); err != nil {
		return "", nil, err
	}
	defer func() {
		// Best-effort cleanup: if the worktree leaks the user can prune
		// it manually via `git worktree prune`.
		_, _ = runGitCommand(root, "worktree", "remove", "--force", path)
		removeEmptyWorktreeParents(path, baseDir)
	}()
	rebound := cs.toolsForFork(path, def.Tools)
	result, err := subagent.RunWithMessagesObserved(
		ctx,
		subagent.WithWorkingDirectory(def, path),
		initialMessages,
		task,
		rebound,
		cs.model,
		cs.getAPIKey,
		cs.streamFn,
		observe,
	)
	if err != nil {
		return "", result.Messages, err
	}
	return result.Text, result.Messages, nil
}

func forkName(opts extension.ForkOptions) string {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return "extension-fork"
	}
	return name
}

func (cs *engine) toolsForFork(cwd string, requested []string) []types.Tool {
	tools := cs.activeTools
	if len(requested) == 0 && cs.agent != nil {
		tools = cs.agent.GetState().Tools
	}
	if cwd != cs.cwd {
		tools = cs.rebindToolsToCwd(cwd, tools)
	}
	return ensureRequestedReadOnlyTools(tools, requested, cwd)
}

func ensureRequestedReadOnlyTools(active []types.Tool, requested []string, cwd string) []types.Tool {
	if len(requested) == 0 {
		return active
	}
	have := make(map[string]bool, len(active))
	for _, tool := range active {
		have[tool.Name()] = true
	}
	want := make(map[string]bool, len(requested))
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name != "" {
			want[name] = true
		}
	}
	out := append([]types.Tool(nil), active...)
	for _, tool := range toolpkg.ReadOnlyTools(cwd) {
		name := tool.Name()
		if name == "read" {
			continue
		}
		if want[name] && !have[name] {
			out = append(out, tool)
			have[name] = true
		}
	}
	for _, tool := range toolpkg.ResearchTools() {
		name := tool.Name()
		if want[name] && !have[name] {
			out = append(out, tool)
			have[name] = true
		}
	}
	return out
}

// rebindToolsToCwd returns a copy of tools where cwd-bound tools point at the
// given path. Unknown tools pass through unchanged.
func (cs *engine) rebindToolsToCwd(cwd string, in []types.Tool) []types.Tool {
	out := make([]types.Tool, 0, len(in))
	for _, tool := range in {
		if rebound, ok := cs.toolProvider.Rebind(tool, cs.toolContext(cwd)); ok {
			out = append(out, rebound)
			continue
		}
		if rebindable, ok := tool.(interface{ WithCwd(string) types.Tool }); ok {
			out = append(out, rebindable.WithCwd(cwd))
		} else {
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

func removeEmptyWorktreeParents(path, base string) {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return
	}
	for parent := filepath.Dir(path); parent != baseAbs && parent != "." && parent != string(os.PathSeparator); parent = filepath.Dir(parent) {
		parentAbs, err := filepath.Abs(parent)
		if err != nil || parentAbs == baseAbs {
			return
		}
		if err := os.Remove(parent); err != nil {
			return
		}
	}
}
