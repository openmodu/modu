package coding_agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/tools/planning"
)

// PlanStatus describes the current plan-mode lifecycle state and approved
// plan artifacts.
type PlanStatus struct {
	Active         bool
	PlanFile       string
	PlanExists     bool
	RevisionCount  int
	TodoTotal      int
	TodoPending    int
	TodoInProgress int
	TodoCompleted  int
}

// PlanRevision describes one persisted approved-plan snapshot.
type PlanRevision struct {
	Path    string
	Name    string
	ModTime time.Time
}

// planController owns plan-mode state (the flag, its lock, and the approval
// callback) and drives the session through s. It implements
// planning.PlanModeManager directly, so the plan tools talk to it without an
// extra adapter layer.
type planController struct {
	s          *CodingSession
	mu         sync.RWMutex
	mode       bool
	decisionCb func(plan string, steps []string) string
}

func newPlanController(s *CodingSession) *planController { return &planController{s: s} }

func (p *planController) enabled() bool {
	return p != nil && p.s != nil && p.s.config.FeaturePlanMode()
}

// IsPlanMode reports whether the session is currently in plan mode.
func (p *planController) IsPlanMode() bool {
	if p == nil || p.s == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

// EnterPlanMode enables plan mode.
func (p *planController) EnterPlanMode() {
	if !p.enabled() {
		return
	}
	p.mu.Lock()
	p.mode = true
	p.mu.Unlock()
	p.s.refreshDynamicSystemPrompt()
	p.s.writeRuntimeState()
}

// ExitPlanMode disables plan mode. steps, when provided, replace the todo list
// so execution follows the approved plan.
func (p *planController) ExitPlanMode(plan string, steps []string) {
	if !p.enabled() {
		return
	}
	p.mu.Lock()
	p.mode = false
	p.mu.Unlock()
	if strings.TrimSpace(plan) != "" {
		_ = p.s.writeLatestPlan(plan)
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
			p.s.SetTodos(items)
		}
	}
	p.s.refreshDynamicSystemPrompt()
	p.s.writeRuntimeState()
}

// SubmitPlan is the plan approval gate. It asks the user to decide, then either
// exits plan mode and seeds the todo list (approve) or stays in plan mode and
// relays the rejection feedback to the model.
func (p *planController) SubmitPlan(ctx context.Context, plan string, steps []string) string {
	if !p.enabled() {
		p.ExitPlanMode(plan, steps)
		return "Plan recorded. Proceed to implement it."
	}

	decision := p.requestDecision(ctx, plan, steps)
	verdict, feedback, _ := strings.Cut(decision, ":")
	verdict = strings.TrimSpace(verdict)
	feedback = strings.TrimSpace(feedback)

	switch verdict {
	case "approve", "approve_auto":
		p.ExitPlanMode(plan, steps)
		if verdict == "approve_auto" {
			p.s.AllowToolAlways("write")
			p.s.AllowToolAlways("edit")
			p.s.AllowToolAlways("bash")
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

// requestDecision presents the plan to the user and returns their decision.
// With no callback (headless / --no-approve) the plan is auto-approved so
// non-interactive runs are not blocked.
func (p *planController) requestDecision(ctx context.Context, plan string, steps []string) string {
	p.mu.RLock()
	cb := p.decisionCb
	p.mu.RUnlock()
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

func (p *planController) setDecisionCallback(fn func(plan string, steps []string) string) {
	p.mu.Lock()
	p.decisionCb = fn
	p.mu.Unlock()
}

func (p *planController) status() PlanStatus {
	status := PlanStatus{
		Active:   p.IsPlanMode(),
		PlanFile: p.s.RuntimePaths().PlanFile,
	}
	if status.PlanFile != "" {
		if _, err := os.Stat(status.PlanFile); err == nil {
			status.PlanExists = true
		}
	}
	status.RevisionCount = len(p.listRevisions())
	for _, item := range p.s.GetTodos() {
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

func (p *planController) clear() error {
	status := p.status()
	if status.PlanFile != "" {
		if err := os.Remove(status.PlanFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	p.s.SetTodos(nil)
	p.s.writeRuntimeState()
	return nil
}

func (p *planController) listRevisions() []PlanRevision {
	paths := p.s.RuntimePaths()
	entries, err := os.ReadDir(paths.PlansDir)
	if err != nil {
		return nil
	}
	revisions := make([]PlanRevision, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "revision-") || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		revisions = append(revisions, PlanRevision{
			Path:    filepath.Join(paths.PlansDir, entry.Name()),
			Name:    entry.Name(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(revisions, func(i, j int) bool {
		if revisions[i].ModTime.Equal(revisions[j].ModTime) {
			return revisions[i].Name > revisions[j].Name
		}
		return revisions[i].ModTime.After(revisions[j].ModTime)
	})
	return revisions
}

func (p *planController) replaceTools() {
	s := p.s
	if !s.config.FeaturePlanMode() {
		s.activeTools = removeToolByName(s.activeTools, "enter_plan_mode")
		s.activeTools = removeToolByName(s.activeTools, "exit_plan_mode")
		stateTools := removeToolByName(s.agent.GetState().Tools, "enter_plan_mode")
		stateTools = removeToolByName(stateTools, "exit_plan_mode")
		s.agent.SetTools(stateTools)
		return
	}
	enter := planning.NewEnterPlanModeTool(p)
	exit := planning.NewExitPlanModeTool(p)
	s.activeTools = replaceTool(s.activeTools, enter)
	s.activeTools = replaceTool(s.activeTools, exit)
	stateTools := replaceTool(s.agent.GetState().Tools, enter)
	stateTools = replaceTool(stateTools, exit)
	s.agent.SetTools(stateTools)
}

// --- CodingSession delegates (preserve the public API surface) ---

// IsPlanMode reports whether the session is currently in plan mode.
func (s *CodingSession) IsPlanMode() bool { return s.plan.IsPlanMode() }

// EnterPlanMode enables plan mode for the current session.
func (s *CodingSession) EnterPlanMode() { s.plan.EnterPlanMode() }

// ExitPlanMode disables plan mode for the current session.
func (s *CodingSession) ExitPlanMode(plan string, steps []string) { s.plan.ExitPlanMode(plan, steps) }

// SetPlanDecisionCallback wires the interactive plan-approval prompt. The
// callback returns "approve", "approve_auto", "reject", or "reject:<feedback>".
func (s *CodingSession) SetPlanDecisionCallback(fn func(plan string, steps []string) string) {
	s.plan.setDecisionCallback(fn)
}

// PlanStatus returns plan-mode state, latest persisted plan path, and todo
// counters seeded by an approved plan.
func (s *CodingSession) PlanStatus() PlanStatus { return s.plan.status() }

// ClearPlan removes the latest persisted plan artifact and clears the current
// todo list seeded from an approved plan. It does not toggle plan mode.
func (s *CodingSession) ClearPlan() error { return s.plan.clear() }

// ListPlanRevisions returns approved-plan snapshots, newest first.
func (s *CodingSession) ListPlanRevisions() []PlanRevision { return s.plan.listRevisions() }

// writeLatestPlan persists the approved plan as the latest plan file plus a
// timestamped revision snapshot.
func (s *CodingSession) writeLatestPlan(plan string) error {
	paths := s.RuntimePaths()
	if err := os.MkdirAll(paths.PlansDir, 0o755); err != nil {
		return err
	}
	content := []byte(strings.TrimSpace(plan) + "\n")
	if err := os.WriteFile(paths.PlanFile, content, 0o600); err != nil {
		return err
	}
	revisionPath := filepath.Join(paths.PlansDir, fmt.Sprintf("revision-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(revisionPath, content, 0o600); err != nil {
		return err
	}
	s.writeRuntimeState()
	return nil
}
