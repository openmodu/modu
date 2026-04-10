package coding_agent

import (
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/agent"
)

// ApprovalManager manages tool execution permissions.
// It remembers always-allow and always-deny decisions per tool name,
// and delegates unknown tools to the configured callback.
type ApprovalManager struct {
	mu          sync.RWMutex
	alwaysAllow map[string]bool
	alwaysDeny  map[string]bool
	rules       PermissionConfig
	observer    ApprovalObserver
	// callback is called when no cached decision exists.
	// If nil, all tools are auto-approved.
	callback func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error)
}

type ApprovalObserver interface {
	OnPermissionRequest(toolName, toolCallID string, args map[string]any)
	OnPermissionDenied(toolName, toolCallID string, args map[string]any, reason string)
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

func (m *ApprovalManager) SetRules(rules PermissionConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = rules
}

func (m *ApprovalManager) SetObserver(observer ApprovalObserver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observer = observer
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
	rules := m.rules
	observer := m.observer
	m.mu.RUnlock()

	if decision, reason, ok := evaluatePermissionRules(rules, toolName, args); ok {
		if !decision.IsAllow() && observer != nil {
			observer.OnPermissionDenied(toolName, toolCallID, args, reason)
		}
		return decision, nil
	}

	if cb == nil {
		return agent.ToolApprovalAllow, nil
	}
	if observer != nil {
		observer.OnPermissionRequest(toolName, toolCallID, args)
	}

	decision, err := cb(toolName, toolCallID, args)
	if err != nil {
		if observer != nil {
			observer.OnPermissionDenied(toolName, toolCallID, args, err.Error())
		}
		return agent.ToolApprovalDeny, err
	}
	if !decision.IsAllow() && observer != nil {
		observer.OnPermissionDenied(toolName, toolCallID, args, string(decision))
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

func evaluatePermissionRules(rules PermissionConfig, toolName string, args map[string]any) (agent.ToolApprovalDecision, string, bool) {
	for _, denied := range rules.DenyTools {
		if strings.TrimSpace(denied) == toolName {
			return agent.ToolApprovalDeny, "denied by permission rules", true
		}
	}
	for _, allowed := range rules.AllowTools {
		if strings.TrimSpace(allowed) == toolName {
			return agent.ToolApprovalAllow, "", true
		}
	}
	if toolName != "bash" {
		return "", "", false
	}
	command, _ := args["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return "", "", false
	}
	for _, denied := range rules.DenyBashPrefixes {
		if denied = strings.TrimSpace(denied); denied != "" && strings.HasPrefix(command, denied) {
			return agent.ToolApprovalDeny, "bash command denied by permission rules", true
		}
	}
	if len(rules.AllowBashPrefixes) > 0 {
		for _, allowed := range rules.AllowBashPrefixes {
			if allowed = strings.TrimSpace(allowed); allowed != "" && strings.HasPrefix(command, allowed) {
				return agent.ToolApprovalAllow, "", true
			}
		}
		return agent.ToolApprovalDeny, "bash command not allowed by permission rules", true
	}
	return "", "", false
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

// ClearDecision removes any cached always-allow or always-deny decision for a tool,
// so it will be asked interactively again on the next call.
func (m *ApprovalManager) ClearDecision(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.alwaysAllow, toolName)
	delete(m.alwaysDeny, toolName)
}

// Reset clears all cached decisions and the callback.
func (m *ApprovalManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysAllow = make(map[string]bool)
	m.alwaysDeny = make(map[string]bool)
	m.rules = PermissionConfig{}
	m.observer = nil
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

// ClearToolDecision removes any cached always-allow or always-deny decision for a tool.
func (cs *CodingSession) ClearToolDecision(toolName string) {
	cs.approvalManager.ClearDecision(toolName)
}

// ResetToolApprovals clears all cached tool approval rules and the callback.
func (cs *CodingSession) ResetToolApprovals() {
	cs.approvalManager.Reset()
}

func (cs *CodingSession) OnPermissionRequest(toolName, toolCallID string, args map[string]any) {
	cs.runHarnessPermissionRequest(HarnessToolCall{ToolName: toolName, Args: args})
	cs.writeRuntimeState()
}

func (cs *CodingSession) OnPermissionDenied(toolName, toolCallID string, args map[string]any, reason string) {
	cs.runHarnessPermissionDenied(HarnessToolCall{ToolName: toolName, Args: args}, reason)
	cs.writeRuntimeState()
}
