package coding_agent

import (
	"github.com/openmodu/modu/pkg/coding_agent/services/plan"
	"github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/coding_agent/tools/planning"
)

// Plan-mode types alias the plan service so existing callers keep working.
type (
	PlanStatus   = plan.Status
	PlanRevision = plan.Revision
	PlanSnapshot = plan.Snapshot
)

// CodingSession implements plan.Host.
var _ plan.Host = (*engine)(nil)

// replacePlanTools registers (or removes) the plan tools. Tool registration is
// a kernel concern; the plan controller is supplied as the tools' manager.
func (s *engine) replacePlanTools() {
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
func (s *engine) IsPlanMode() bool { return s.plan.IsPlanMode() }

// EnterPlanMode enables plan mode for the current session.
func (s *engine) EnterPlanMode() { s.plan.EnterPlanMode() }

// ExitPlanMode disables plan mode for the current session.
func (s *engine) ExitPlanMode(p string, steps []string) { s.plan.ExitPlanMode(p, steps) }

// SetPlanDecisionCallback wires the interactive plan-approval prompt.
func (s *engine) SetPlanDecisionCallback(fn func(plan string, steps []string) string) {
	s.plan.SetDecisionCallback(fn)
}

// PlanStatus returns plan-mode state, latest plan content, and todo counters.
func (s *engine) PlanStatus() PlanStatus { return s.plan.Status() }

// ClearPlan clears the latest persisted plan snapshot and seeded todos.
func (s *engine) ClearPlan() error { return s.plan.Clear() }

// ListPlanRevisions returns approved-plan snapshots, newest first.
func (s *engine) ListPlanRevisions() []PlanRevision { return s.plan.ListRevisions() }

// LatestPlan returns the currently active approved plan text.
func (s *engine) LatestPlan() string { return s.PlanStatus().LatestPlan }

// AppendPlanSnapshot records plan lifecycle state in the session JSONL.
func (s *engine) AppendPlanSnapshot(snapshot plan.Snapshot) error {
	if s == nil || s.sessionManager == nil {
		return nil
	}
	entry := session.NewEntry(session.EntryTypePlanSnapshot, "", nil)
	if snapshot.CreatedAt == 0 {
		snapshot.CreatedAt = entry.Timestamp
	}
	snapshot.ID = entry.ID
	entry.Data = snapshot
	return s.sessionManager.AppendSidecar(entry)
}

// PlanSnapshots returns all plan lifecycle entries in session order.
func (s *engine) PlanSnapshots() []plan.Snapshot {
	if s == nil || s.sessionManager == nil {
		return nil
	}
	var out []plan.Snapshot
	for _, entry := range s.sessionManager.Load() {
		if entry.Type != session.EntryTypePlanSnapshot {
			continue
		}
		snapshot, ok := planSnapshotFromEntry(entry)
		if !ok {
			continue
		}
		out = append(out, snapshot)
	}
	return out
}

func planSnapshotFromEntry(entry session.SessionEntry) (plan.Snapshot, bool) {
	switch data := entry.Data.(type) {
	case plan.Snapshot:
		if data.ID == "" {
			data.ID = entry.ID
		}
		if data.CreatedAt == 0 {
			data.CreatedAt = entry.Timestamp
		}
		return data, true
	case map[string]any:
		snapshot := plan.Snapshot{
			ID:        stringValue(data["id"]),
			Content:   stringValue(data["content"]),
			Cleared:   boolValue(data["cleared"]),
			CreatedAt: int64Value(data["createdAt"]),
		}
		if snapshot.ID == "" {
			snapshot.ID = entry.ID
		}
		if snapshot.CreatedAt == 0 {
			snapshot.CreatedAt = entry.Timestamp
		}
		return snapshot, true
	default:
		return plan.Snapshot{}, false
	}
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolValue(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func int64Value(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}
