// Package plan is the plan-mode service: it owns the plan-mode flag and the
// approval callback, drives the approve/reject gate, and persists approved
// plans. It reaches the kernel only through the narrow Host interface, and
// implements planning.PlanModeManager structurally so the kernel can register
// the plan tools against it.
package plan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/services/todo"
)

// Host is the set of kernel capabilities the plan service needs.
type Host interface {
	PlanFile() string
	PlansDir() string
	GetTodos() []todo.Item
	SetTodos([]todo.Item)
	AllowToolAlways(tool string)
	RefreshSystemPrompt()
	WriteRuntimeState()
	PlanModeEnabled() bool
}

// Status describes the current plan-mode lifecycle state and approved-plan
// artifacts.
type Status struct {
	Active         bool
	PlanFile       string
	PlanExists     bool
	RevisionCount  int
	TodoTotal      int
	TodoPending    int
	TodoInProgress int
	TodoCompleted  int
}

// Revision describes one persisted approved-plan snapshot.
type Revision struct {
	Path    string
	Name    string
	ModTime time.Time
}

// Controller owns plan-mode state and drives the session through host.
type Controller struct {
	host       Host
	mu         sync.RWMutex
	mode       bool
	decisionCb func(plan string, steps []string) string
}

// New creates a plan controller bound to a host.
func New(host Host) *Controller { return &Controller{host: host} }

func (c *Controller) enabled() bool {
	return c != nil && c.host != nil && c.host.PlanModeEnabled()
}

// IsPlanMode reports whether the session is currently in plan mode.
func (c *Controller) IsPlanMode() bool {
	if c == nil || c.host == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode
}

// EnterPlanMode enables plan mode.
func (c *Controller) EnterPlanMode() {
	if !c.enabled() {
		return
	}
	c.mu.Lock()
	c.mode = true
	c.mu.Unlock()
	c.host.RefreshSystemPrompt()
	c.host.WriteRuntimeState()
}

// ExitPlanMode disables plan mode. steps, when provided, replace the todo list
// so execution follows the approved plan.
func (c *Controller) ExitPlanMode(plan string, steps []string) {
	if !c.enabled() {
		return
	}
	c.mu.Lock()
	c.mode = false
	c.mu.Unlock()
	if strings.TrimSpace(plan) != "" {
		_ = c.writeLatestPlan(plan)
	}
	if len(steps) > 0 {
		items := make([]todo.Item, 0, len(steps))
		for _, step := range steps {
			step = strings.TrimSpace(step)
			if step == "" {
				continue
			}
			items = append(items, todo.Item{Content: step, Status: "pending"})
		}
		if len(items) > 0 {
			c.host.SetTodos(items)
		}
	}
	c.host.RefreshSystemPrompt()
	c.host.WriteRuntimeState()
}

// SubmitPlan is the plan approval gate.
func (c *Controller) SubmitPlan(ctx context.Context, plan string, steps []string) string {
	if !c.enabled() {
		c.ExitPlanMode(plan, steps)
		return "Plan recorded. Proceed to implement it."
	}

	decision := c.requestDecision(ctx, plan, steps)
	verdict, feedback, _ := strings.Cut(decision, ":")
	verdict = strings.TrimSpace(verdict)
	feedback = strings.TrimSpace(feedback)

	switch verdict {
	case "approve", "approve_auto":
		c.ExitPlanMode(plan, steps)
		if verdict == "approve_auto" {
			c.host.AllowToolAlways("write")
			c.host.AllowToolAlways("edit")
			c.host.AllowToolAlways("bash")
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

func (c *Controller) requestDecision(ctx context.Context, plan string, steps []string) string {
	c.mu.RLock()
	cb := c.decisionCb
	c.mu.RUnlock()
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

// SetDecisionCallback wires the interactive plan-approval prompt.
func (c *Controller) SetDecisionCallback(fn func(plan string, steps []string) string) {
	c.mu.Lock()
	c.decisionCb = fn
	c.mu.Unlock()
}

// Status returns plan-mode state, latest plan path, and todo counters.
func (c *Controller) Status() Status {
	status := Status{
		Active:   c.IsPlanMode(),
		PlanFile: c.host.PlanFile(),
	}
	if status.PlanFile != "" {
		if _, err := os.Stat(status.PlanFile); err == nil {
			status.PlanExists = true
		}
	}
	status.RevisionCount = len(c.ListRevisions())
	for _, item := range c.host.GetTodos() {
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

// Clear removes the latest persisted plan and clears the seeded todo list.
func (c *Controller) Clear() error {
	status := c.Status()
	if status.PlanFile != "" {
		if err := os.Remove(status.PlanFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	c.host.SetTodos(nil)
	c.host.WriteRuntimeState()
	return nil
}

// ListRevisions returns approved-plan snapshots, newest first.
func (c *Controller) ListRevisions() []Revision {
	dir := c.host.PlansDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	revisions := make([]Revision, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "revision-") || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		revisions = append(revisions, Revision{
			Path:    filepath.Join(dir, entry.Name()),
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

func (c *Controller) writeLatestPlan(plan string) error {
	if err := os.MkdirAll(c.host.PlansDir(), 0o755); err != nil {
		return err
	}
	content := []byte(strings.TrimSpace(plan) + "\n")
	if err := os.WriteFile(c.host.PlanFile(), content, 0o600); err != nil {
		return err
	}
	revisionPath := filepath.Join(c.host.PlansDir(), fmt.Sprintf("revision-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(revisionPath, content, 0o600); err != nil {
		return err
	}
	c.host.WriteRuntimeState()
	return nil
}
