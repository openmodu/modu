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
	"github.com/openmodu/modu/pkg/coding_agent/subagent"
	"github.com/openmodu/modu/pkg/coding_agent/tools"
)

type planModeAdapter struct {
	session *CodingSession
}

// PlanStatus describes the current plan-mode lifecycle state and approved
// plan artifacts.
type PlanStatus struct {
	Active         bool
	PlanFile       string
	PlanExists     bool
	TodoTotal      int
	TodoPending    int
	TodoInProgress int
	TodoCompleted  int
}

func (a planModeAdapter) EnterPlanMode() {
	if a.session == nil {
		return
	}
	if !a.session.config.FeaturePlanMode() {
		return
	}
	func() {
		a.session.planMu.Lock()
		defer a.session.planMu.Unlock()
		a.session.planMode = true
	}()
	a.session.refreshDynamicSystemPrompt()
	a.session.writeRuntimeState()
}

func (a planModeAdapter) ExitPlanMode(plan string, steps []string) {
	if a.session == nil {
		return
	}
	if !a.session.config.FeaturePlanMode() {
		return
	}
	func() {
		a.session.planMu.Lock()
		defer a.session.planMu.Unlock()
		a.session.planMode = false
	}()
	if strings.TrimSpace(plan) != "" {
		_ = a.session.writeLatestPlan(plan)
	}
	// Approved steps become the execution todo list so the agent works
	// through the plan sub-task by sub-task.
	if len(steps) > 0 {
		items := make([]TodoItem, 0, len(steps))
		for _, step := range steps {
			step = strings.TrimSpace(step)
			if step == "" {
				continue
			}
			items = append(items, TodoItem{Content: step, Status: "pending"})
		}
		if len(items) > 0 {
			a.session.SetTodos(items)
		}
	}
	a.session.refreshDynamicSystemPrompt()
	a.session.writeRuntimeState()
}

func (a planModeAdapter) IsPlanMode() bool {
	if a.session == nil {
		return false
	}
	a.session.planMu.RLock()
	defer a.session.planMu.RUnlock()
	return a.session.planMode
}

// SubmitPlan is the plan approval gate. It asks the user to decide, then
// either exits plan mode and seeds the todo list (approve) or stays in plan
// mode and relays the rejection feedback to the model.
func (a planModeAdapter) SubmitPlan(ctx context.Context, plan string, steps []string) string {
	if a.session == nil || !a.session.config.FeaturePlanMode() {
		if a.session != nil {
			a.session.ExitPlanMode(plan, steps)
		}
		return "Plan recorded. Proceed to implement it."
	}

	decision := a.session.requestPlanDecision(ctx, plan, steps)
	verdict, feedback, _ := strings.Cut(decision, ":")
	verdict = strings.TrimSpace(verdict)
	feedback = strings.TrimSpace(feedback)

	switch verdict {
	case "approve", "approve_auto":
		a.session.ExitPlanMode(plan, steps)
		if verdict == "approve_auto" {
			a.session.AllowToolAlways("write")
			a.session.AllowToolAlways("edit")
			a.session.AllowToolAlways("bash")
		}
		msg := "Plan approved. Plan mode is now off."
		if len(steps) > 0 {
			msg += " The plan steps are now your todo list — execute them in order, exactly one in_progress at a time, marking each completed when done."
		} else {
			msg += " Proceed to implement the plan."
		}
		if verdict == "approve_auto" {
			msg += " The user chose auto-accept: file edits will not prompt for the rest of this session."
		}
		return msg
	default: // reject / reject:<feedback>
		if feedback == "" {
			return "The user REJECTED the plan and is still in plan mode. " +
				"Do not make any changes. Ask what they want changed, revise " +
				"the plan, and call exit_plan_mode again."
		}
		return "The user REJECTED the plan and is still in plan mode. " +
			"Do not make any changes. Their feedback:\n\n" + feedback +
			"\n\nRevise the plan accordingly and call exit_plan_mode again."
	}
}

// requestPlanDecision presents the plan to the user and returns their
// decision. With no callback (headless / --no-approve) the plan is
// auto-approved so non-interactive runs are not blocked.
func (s *CodingSession) requestPlanDecision(ctx context.Context, plan string, steps []string) string {
	s.planMu.RLock()
	cb := s.planDecisionCb
	s.planMu.RUnlock()
	if cb == nil {
		return "approve"
	}
	done := make(chan string, 1)
	go func() { done <- cb(plan, steps) }()
	select {
	case d := <-done:
		if strings.TrimSpace(d) == "" {
			return "reject"
		}
		return d
	case <-ctx.Done():
		return "reject"
	}
}

// SetPlanDecisionCallback wires the interactive plan-approval prompt. The
// callback returns "approve", "approve_auto", "reject", or "reject:<feedback>".
func (s *CodingSession) SetPlanDecisionCallback(fn func(plan string, steps []string) string) {
	s.planMu.Lock()
	s.planDecisionCb = fn
	s.planMu.Unlock()
}

// IsPlanMode reports whether the session is currently in plan mode.
func (s *CodingSession) IsPlanMode() bool {
	s.planMu.RLock()
	defer s.planMu.RUnlock()
	return s.planMode
}

// EnterPlanMode enables plan mode for the current session.
func (s *CodingSession) EnterPlanMode() {
	planModeAdapter{session: s}.EnterPlanMode()
}

// ExitPlanMode disables plan mode for the current session. steps, when
// provided, replace the todo list so execution follows the approved plan.
func (s *CodingSession) ExitPlanMode(plan string, steps []string) {
	planModeAdapter{session: s}.ExitPlanMode(plan, steps)
}

// PlanStatus returns plan-mode state, latest persisted plan path, and current
// todo counters seeded by an approved plan.
func (s *CodingSession) PlanStatus() PlanStatus {
	status := PlanStatus{
		Active:   s.IsPlanMode(),
		PlanFile: s.RuntimePaths().PlanFile,
	}
	if status.PlanFile != "" {
		if _, err := os.Stat(status.PlanFile); err == nil {
			status.PlanExists = true
		}
	}
	for _, item := range s.GetTodos() {
		status.TodoTotal++
		switch item.Status {
		case "pending":
			status.TodoPending++
		case "in_progress":
			status.TodoInProgress++
		case "completed":
			status.TodoCompleted++
		}
	}
	return status
}

func (s *CodingSession) replacePlanTools() {
	if !s.config.FeaturePlanMode() {
		s.activeTools = removeAgentToolByName(s.activeTools, "enter_plan_mode")
		s.activeTools = removeAgentToolByName(s.activeTools, "exit_plan_mode")
		stateTools := removeAgentToolByName(s.agent.GetState().Tools, "enter_plan_mode")
		stateTools = removeAgentToolByName(stateTools, "exit_plan_mode")
		s.agent.SetTools(stateTools)
		return
	}
	enter := tools.NewEnterPlanModeTool(planModeAdapter{session: s})
	exit := tools.NewExitPlanModeTool(planModeAdapter{session: s})
	s.activeTools = replaceAgentTool(s.activeTools, enter)
	s.activeTools = replaceAgentTool(s.activeTools, exit)
	stateTools := replaceAgentTool(s.agent.GetState().Tools, enter)
	stateTools = replaceAgentTool(stateTools, exit)
	s.agent.SetTools(stateTools)
}

type worktreeAdapter struct {
	session *CodingSession
}

// WorktreeStatus describes the current isolated worktree lifecycle state.
type WorktreeStatus struct {
	Active      bool
	Path        string
	OriginalCwd string
	Cwd         string
	Exists      bool
}

func (a worktreeAdapter) EnterWorktree() (string, error) {
	if a.session == nil {
		return "", fmt.Errorf("worktree session is not configured")
	}
	return a.session.EnterWorktree()
}

func (a worktreeAdapter) ExitWorktree() error {
	if a.session == nil {
		return fmt.Errorf("worktree session is not configured")
	}
	return a.session.ExitWorktree()
}

func (a worktreeAdapter) ActiveWorktree() string {
	if a.session == nil {
		return ""
	}
	return a.session.worktreePath
}

// ActiveWorktree returns the currently active isolated worktree path, if any.
func (s *CodingSession) ActiveWorktree() string {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	return s.worktreePath
}

// WorktreeStatus returns the current isolated worktree state without mutating
// the session. Exists is only true when the active worktree path is still on
// disk.
func (s *CodingSession) WorktreeStatus() WorktreeStatus {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	status := WorktreeStatus{
		Active:      s.worktreePath != "",
		Path:        s.worktreePath,
		OriginalCwd: s.originalCwd,
		Cwd:         s.cwd,
	}
	if status.Path != "" {
		if _, err := os.Stat(status.Path); err == nil {
			status.Exists = true
		}
	}
	return status
}

func (s *CodingSession) replaceWorktreeTools() {
	if !s.config.FeatureWorktreeMode() {
		s.activeTools = removeAgentToolByName(s.activeTools, "enter_worktree")
		s.activeTools = removeAgentToolByName(s.activeTools, "exit_worktree")
		stateTools := removeAgentToolByName(s.agent.GetState().Tools, "enter_worktree")
		stateTools = removeAgentToolByName(stateTools, "exit_worktree")
		s.agent.SetTools(stateTools)
		return
	}
	enter := tools.NewEnterWorktreeTool(worktreeAdapter{session: s})
	exit := tools.NewExitWorktreeTool(worktreeAdapter{session: s})
	s.activeTools = replaceAgentTool(s.activeTools, enter)
	s.activeTools = replaceAgentTool(s.activeTools, exit)
	stateTools := replaceAgentTool(s.agent.GetState().Tools, enter)
	stateTools = replaceAgentTool(stateTools, exit)
	s.agent.SetTools(stateTools)
}

func (s *CodingSession) EnterWorktree() (string, error) {
	if !s.config.FeatureWorktreeMode() {
		return "", fmt.Errorf("worktree mode is disabled by settings")
	}
	s.worktreeMu.Lock()
	if s.worktreePath != "" {
		path := s.worktreePath
		s.worktreeMu.Unlock()
		return path, nil
	}

	root, err := gitOutput(s.cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		s.worktreeMu.Unlock()
		return "", fmt.Errorf("enter_worktree: not a git repository: %w", err)
	}

	baseDir := filepath.Join(s.agentDir, "worktrees")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		s.worktreeMu.Unlock()
		return "", err
	}
	path := filepath.Join(baseDir, fmt.Sprintf("wt-%d", time.Now().UnixMilli()))
	if _, err := runGit(root, "worktree", "add", "--detach", path, "HEAD"); err != nil {
		s.worktreeMu.Unlock()
		return "", fmt.Errorf("enter_worktree: %w", err)
	}

	s.originalCwd = s.cwd
	oldCwd := s.cwd
	s.worktreePath = path
	s.cwd = path
	s.refreshToolsForCwd(path)
	s.worktreeMu.Unlock()
	s.refreshDynamicSystemPrompt()
	s.runHarnessWorktreeCreate(path)
	s.runHarnessCwdChanged(oldCwd, path)
	s.writeRuntimeState()
	return path, nil
}

func (s *CodingSession) ExitWorktree() error {
	s.worktreeMu.Lock()
	if s.worktreePath == "" {
		s.worktreeMu.Unlock()
		return nil
	}
	path := s.worktreePath
	restore := s.originalCwd
	root, err := gitOutput(path, "rev-parse", "--show-toplevel")
	if err == nil {
		_, _ = runGit(root, "worktree", "remove", "--force", path)
	}
	s.worktreePath = ""
	if restore != "" {
		oldCwd := s.cwd
		s.cwd = restore
		s.originalCwd = ""
		s.refreshToolsForCwd(restore)
		s.runHarnessCwdChanged(oldCwd, restore)
	}
	s.worktreeMu.Unlock()
	s.refreshDynamicSystemPrompt()
	s.runHarnessWorktreeRemove(path)
	s.writeRuntimeState()
	return nil
}

// refreshDynamicSystemPrompt rebuilds the system prompt from scratch every
// turn. The skills XML block, context files, and memory are all regenerated
// against the current filesystem so edits to skill files (or new skills
// dropped into the skills dir) are reflected without restarting the session.
func (s *CodingSession) refreshDynamicSystemPrompt() {
	s.refreshResourcePaths()
	if s.promptBuilder != nil && s.skillManager != nil {
		// FormatForPrompt rediscovers under the hood, so the XML block
		// always reflects what's on disk right now.
		s.promptBuilder.SetSkillsPrompt(s.skillManager.FormatForPrompt())
	}

	var parts []string
	if s.promptBuilder != nil {
		parts = append(parts, s.promptBuilder.Build())
	}

	s.planMu.RLock()
	planMode := s.planMode
	s.planMu.RUnlock()
	s.worktreeMu.Lock()
	worktreePath := s.worktreePath
	s.worktreeMu.Unlock()
	if planMode {
		parts = append(parts, "## Active Mode: Plan\n"+
			"You are in plan mode. write, edit, and bash are blocked — you cannot "+
			"change anything yet. Your job is to produce a plan good enough that "+
			"the user approves it without follow-up questions.\n\n"+
			"Investigate first, do not guess:\n"+
			"- Read the actual files end to end, not just snippets. Trace every "+
			"call site, type, and config the change touches.\n"+
			"- Find existing patterns/tests to follow so the plan fits the codebase.\n"+
			"- Identify edge cases, failure modes, and what could break.\n"+
			"- If the request is ambiguous, ask the user before planning — do not "+
			"pick an interpretation silently.\n\n"+
			"Then call `exit_plan_mode` with:\n"+
			"- `plan`: concise markdown covering Goal, Approach, Files to change "+
			"(with paths), Validation/tests, and Risks. Reference real file paths "+
			"and symbols you verified — no vague hand-waving.\n"+
			"- `steps`: an ordered array of small, individually verifiable "+
			"sub-tasks. Each step should be one focused change.\n\n"+
			"After you submit, the user decides:\n"+
			"- APPROVED: plan mode exits, steps become your todo list, implement "+
			"them in order, one in_progress at a time.\n"+
			"- REJECTED (the exit_plan_mode call is denied): you are still in plan "+
			"mode. Make no changes. Use their feedback, revise the plan, and call "+
			"`exit_plan_mode` again.")
	}
	if worktreePath != "" {
		parts = append(parts, "## Active Worktree\nThe session is currently operating inside an isolated git worktree at: "+worktreePath)
	}
	s.agent.SetSystemPrompt(strings.Join(parts, "\n\n---\n\n"))
}

func (s *CodingSession) refreshToolsForCwd(cwd string) {
	var updated []agent.AgentTool
	for _, tool := range s.activeTools {
		switch tool.Name() {
		case "read":
			updated = append(updated, tools.NewReadTool(cwd))
		case "git_preflight":
			updated = append(updated, tools.NewGitPreflightTool(cwd))
		case "write":
			updated = append(updated, tools.NewWriteTool(cwd))
		case "edit":
			updated = append(updated, tools.NewEditTool(cwd))
		case "bash":
			updated = append(updated, tools.NewBashTool(cwd))
		case "grep":
			updated = append(updated, tools.NewGrepTool(cwd))
		case "find":
			updated = append(updated, tools.NewFindTool(cwd))
		case "ls":
			updated = append(updated, tools.NewLsTool(cwd))
		case "spawn_subagent":
			updated = append(updated, tools.NewSpawnSubagentTool(cwd, s.agentDir, s.subagentLoader, updated, s.model, s.getAPIKey, s.streamFn, func(def *subagent.SubagentDefinition) *subagent.SubagentDefinition {
				return prepareSubagentDefinition(def, s.skillManager, s.memoryStore)
			}, s.taskManager, s))
		default:
			updated = append(updated, tool)
		}
	}
	updated = wrapHarnessTools(updated, s)
	s.activeTools = updated
	s.agent.SetTools(updated)
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

func (s *CodingSession) writeLatestPlan(plan string) error {
	paths := s.RuntimePaths()
	if err := os.MkdirAll(paths.PlansDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(paths.PlanFile, []byte(strings.TrimSpace(plan)+"\n"), 0o600); err != nil {
		return err
	}
	s.writeRuntimeState()
	return nil
}
