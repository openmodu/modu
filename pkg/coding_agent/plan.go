package coding_agent

import (
	"github.com/openmodu/modu/pkg/coding_agent/services/plan"
	"github.com/openmodu/modu/pkg/coding_agent/tools/planning"
)

// Plan-mode types alias the plan service so existing callers keep working.
type (
	PlanStatus   = plan.Status
	PlanRevision = plan.Revision
)

// CodingSession implements plan.Host.
var _ plan.Host = (*CodingSession)(nil)

// replacePlanTools registers (or removes) the plan tools. Tool registration is
// a kernel concern; the plan controller is supplied as the tools' manager.
func (s *CodingSession) replacePlanTools() {
	if !s.config.FeaturePlanMode() {
		s.activeTools = removeToolByName(s.activeTools, "enter_plan_mode")
		s.activeTools = removeToolByName(s.activeTools, "exit_plan_mode")
		stateTools := removeToolByName(s.agent.GetState().Tools, "enter_plan_mode")
		stateTools = removeToolByName(stateTools, "exit_plan_mode")
		s.agent.SetTools(stateTools)
		return
	}
	enter := planning.NewEnterPlanModeTool(s.plan)
	exit := planning.NewExitPlanModeTool(s.plan)
	s.activeTools = replaceTool(s.activeTools, enter)
	s.activeTools = replaceTool(s.activeTools, exit)
	stateTools := replaceTool(s.agent.GetState().Tools, enter)
	stateTools = replaceTool(stateTools, exit)
	s.agent.SetTools(stateTools)
}

// --- delegates (preserve the public API surface) ---

// IsPlanMode reports whether the session is currently in plan mode.
func (s *CodingSession) IsPlanMode() bool { return s.plan.IsPlanMode() }

// EnterPlanMode enables plan mode for the current session.
func (s *CodingSession) EnterPlanMode() { s.plan.EnterPlanMode() }

// ExitPlanMode disables plan mode for the current session.
func (s *CodingSession) ExitPlanMode(p string, steps []string) { s.plan.ExitPlanMode(p, steps) }

// SetPlanDecisionCallback wires the interactive plan-approval prompt.
func (s *CodingSession) SetPlanDecisionCallback(fn func(plan string, steps []string) string) {
	s.plan.SetDecisionCallback(fn)
}

// PlanStatus returns plan-mode state, latest plan path, and todo counters.
func (s *CodingSession) PlanStatus() PlanStatus { return s.plan.Status() }

// ClearPlan removes the latest persisted plan artifact and clears the seeded todos.
func (s *CodingSession) ClearPlan() error { return s.plan.Clear() }

// ListPlanRevisions returns approved-plan snapshots, newest first.
func (s *CodingSession) ListPlanRevisions() []PlanRevision { return s.plan.ListRevisions() }
