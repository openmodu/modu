package coding_agent

import (
	"sync"

	"github.com/crosszan/modu/pkg/agent"
)

// ApprovalManager manages tool execution permissions.
// It remembers always-allow and always-deny decisions per tool name,
// and delegates unknown tools to the configured callback.
type ApprovalManager struct {
	mu         sync.RWMutex
	alwaysAllow map[string]bool
	alwaysDeny  map[string]bool
	// callback is called when no cached decision exists.
	// If nil, all tools are auto-approved.
	callback func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error)
}

// NewApprovalManager creates an ApprovalManager with no cached rules.
func NewApprovalManager() *ApprovalManager {
	return &ApprovalManager{
		alwaysAllow: make(map[string]bool),
		alwaysDeny:  make(map[string]bool),
	}
}

// SetCallback sets the interactive approval callback.
func (m *ApprovalManager) SetCallback(fn func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callback = fn
}

// Approve is the AgentConfig.ApproveTool implementation.
func (m *ApprovalManager) Approve(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
	m.mu.RLock()
	if m.alwaysAllow[toolName] {
		m.mu.RUnlock()
		return agent.ToolApprovalAllow, nil
	}
	if m.alwaysDeny[toolName] {
		m.mu.RUnlock()
		return agent.ToolApprovalDeny, nil
	}
	cb := m.callback
	m.mu.RUnlock()

	if cb == nil {
		return agent.ToolApprovalAllow, nil
	}

	decision, err := cb(toolName, toolCallID, args)
	if err != nil {
		return agent.ToolApprovalDeny, err
	}

	// Persist always decisions
	if decision == agent.ToolApprovalAllowAlways {
		m.mu.Lock()
		m.alwaysAllow[toolName] = true
		m.mu.Unlock()
	} else if decision == agent.ToolApprovalDenyAlways {
		m.mu.Lock()
		m.alwaysDeny[toolName] = true
		m.mu.Unlock()
	}

	return decision, nil
}

// AllowAlways marks a tool as always allowed.
func (m *ApprovalManager) AllowAlways(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysAllow[toolName] = true
	delete(m.alwaysDeny, toolName)
}

// DenyAlways marks a tool as always denied.
func (m *ApprovalManager) DenyAlways(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysDeny[toolName] = true
	delete(m.alwaysAllow, toolName)
}

// Reset clears all cached decisions and the callback.
func (m *ApprovalManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysAllow = make(map[string]bool)
	m.alwaysDeny = make(map[string]bool)
	m.callback = nil
}

// --- CodingSession helpers ---

// SetToolApprovalCallback sets the interactive approval callback on the session.
// When set, the callback is called before each tool execution that is not
// already covered by an always-allow or always-deny rule.
// Passing nil disables interactive approval (all tools auto-approved).
func (cs *CodingSession) SetToolApprovalCallback(fn func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error)) {
	cs.approvalManager.SetCallback(fn)
}

// AllowToolAlways marks a tool as always allowed for this session.
func (cs *CodingSession) AllowToolAlways(toolName string) {
	cs.approvalManager.AllowAlways(toolName)
}

// DenyToolAlways marks a tool as always denied for this session.
func (cs *CodingSession) DenyToolAlways(toolName string) {
	cs.approvalManager.DenyAlways(toolName)
}

// ResetToolApprovals clears all cached tool approval rules and the callback.
func (cs *CodingSession) ResetToolApprovals() {
	cs.approvalManager.Reset()
}
