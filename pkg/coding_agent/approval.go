package coding_agent

import (
	"errors"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/agent"
)

// ApprovalManager manages tool execution permissions.
// It remembers always-allow and always-deny decisions per tool name. Interactive
// "always" decisions for bash are scoped to the exact command so approving a
// safe command does not silently approve a later dangerous one.
type ApprovalManager struct {
	mu              sync.RWMutex
	alwaysAllow     map[string]bool
	alwaysDeny      map[string]bool
	alwaysAllowBash map[string]bool
	alwaysDenyBash  map[string]bool
	rules           PermissionConfig
	observer        ApprovalObserver
	blocker         func(toolName string, args map[string]any) (bool, string)
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
		alwaysAllow:     make(map[string]bool),
		alwaysDeny:      make(map[string]bool),
		alwaysAllowBash: make(map[string]bool),
		alwaysDenyBash:  make(map[string]bool),
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

func (m *ApprovalManager) SetBlocker(fn func(toolName string, args map[string]any) (bool, string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocker = fn
}

// Approve is the AgentConfig.ApproveTool implementation.
func (m *ApprovalManager) Approve(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
	// exit_plan_mode runs its own interactive plan-approval gate inside the
	// tool (so the user's rejection feedback can flow back to the model), so
	// the agent-level gate must let it through without prompting.
	if toolName == "exit_plan_mode" || isGoalStateTool(toolName) {
		return agent.ToolApprovalAllow, nil
	}

	m.mu.RLock()
	blocker := m.blocker
	observer := m.observer
	m.mu.RUnlock()
	if blocker != nil {
		if blocked, reason := blocker(toolName, args); blocked {
			if reason == "" {
				reason = toolName + " is blocked"
			}
			if observer != nil {
				observer.OnPermissionDenied(toolName, toolCallID, args, reason)
			}
			return agent.ToolApprovalDeny, errors.New(reason)
		}
	}

	bashCommand := approvalBashCommand(toolName, args)
	bashNeedsApproval := isDangerousBashCommand(bashCommand)

	m.mu.RLock()
	if bashCommand != "" && m.alwaysDenyBash[bashCommand] {
		m.mu.RUnlock()
		return agent.ToolApprovalDeny, nil
	}
	if m.alwaysDeny[toolName] {
		m.mu.RUnlock()
		return agent.ToolApprovalDeny, nil
	}
	bashAllowed := bashCommand != "" && m.alwaysAllowBash[bashCommand]
	toolAllowed := !bashNeedsApproval && m.alwaysAllow[toolName]
	cb := m.callback
	rules := m.rules
	observer = m.observer
	m.mu.RUnlock()

	if decision, reason, ok := evaluateDenyPermissionRules(rules, toolName, args); ok {
		if !decision.IsAllow() && observer != nil {
			observer.OnPermissionDenied(toolName, toolCallID, args, reason)
		}
		return decision, nil
	}

	if bashAllowed || toolAllowed {
		return agent.ToolApprovalAllow, nil
	}

	if !bashNeedsApproval {
		if decision, reason, ok := evaluateAllowPermissionRules(rules, toolName, args); ok {
			if !decision.IsAllow() && observer != nil {
				observer.OnPermissionDenied(toolName, toolCallID, args, reason)
			}
			return decision, nil
		}
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

	// Persist always decisions. Interactive bash decisions are intentionally
	// command-scoped; programmatic AllowAlways("bash") remains tool-wide.
	if bashCommand != "" && decision == agent.ToolApprovalAllowAlways {
		m.mu.Lock()
		m.alwaysAllowBash[bashCommand] = true
		delete(m.alwaysDenyBash, bashCommand)
		m.mu.Unlock()
	} else if bashCommand != "" && decision == agent.ToolApprovalDenyAlways {
		m.mu.Lock()
		m.alwaysDenyBash[bashCommand] = true
		delete(m.alwaysAllowBash, bashCommand)
		m.mu.Unlock()
	} else if decision == agent.ToolApprovalAllowAlways {
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

func evaluateDenyPermissionRules(rules PermissionConfig, toolName string, args map[string]any) (agent.ToolApprovalDecision, string, bool) {
	for _, denied := range rules.DenyTools {
		if strings.TrimSpace(denied) == toolName {
			return agent.ToolApprovalDeny, "denied by permission rules", true
		}
	}
	if toolName != "bash" {
		return "", "", false
	}
	command := approvalBashCommand(toolName, args)
	if command == "" {
		return "", "", false
	}
	for _, denied := range rules.DenyBashPrefixes {
		if denied = strings.TrimSpace(denied); denied != "" && strings.HasPrefix(command, denied) {
			return agent.ToolApprovalDeny, "bash command denied by permission rules", true
		}
	}
	return "", "", false
}

func evaluateAllowPermissionRules(rules PermissionConfig, toolName string, args map[string]any) (agent.ToolApprovalDecision, string, bool) {
	for _, allowed := range rules.AllowTools {
		if strings.TrimSpace(allowed) == toolName {
			return agent.ToolApprovalAllow, "", true
		}
	}
	if isAutoAllowedReadOnlyTool(toolName) {
		return agent.ToolApprovalAllow, "", true
	}
	if toolName != "bash" {
		return "", "", false
	}
	command := approvalBashCommand(toolName, args)
	if command == "" {
		return "", "", false
	}
	if len(rules.AllowBashPrefixes) > 0 {
		for _, allowed := range rules.AllowBashPrefixes {
			if allowed = strings.TrimSpace(allowed); allowed != "" && strings.HasPrefix(command, allowed) {
				return agent.ToolApprovalAllow, "", true
			}
		}
		return agent.ToolApprovalDeny, "bash command not allowed by permission rules", true
	}
	return agent.ToolApprovalAllow, "", true
}

func isAutoAllowedReadOnlyTool(toolName string) bool {
	switch toolName {
	case "read", "grep", "find", "ls":
		return true
	default:
		return false
	}
}

func isGoalStateTool(toolName string) bool {
	switch toolName {
	case "create_goal", "get_goal", "update_goal":
		return true
	default:
		return false
	}
}

func approvalBashCommand(toolName string, args map[string]any) string {
	if toolName != "bash" {
		return ""
	}
	command, _ := args["command"].(string)
	return strings.TrimSpace(command)
}

// AllowAlways marks a tool as always allowed.
func (m *ApprovalManager) AllowAlways(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysAllow[toolName] = true
	delete(m.alwaysDeny, toolName)
	if toolName == "bash" {
		m.alwaysAllowBash = make(map[string]bool)
		m.alwaysDenyBash = make(map[string]bool)
	}
}

// DenyAlways marks a tool as always denied.
func (m *ApprovalManager) DenyAlways(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysDeny[toolName] = true
	delete(m.alwaysAllow, toolName)
	if toolName == "bash" {
		m.alwaysAllowBash = make(map[string]bool)
		m.alwaysDenyBash = make(map[string]bool)
	}
}

// ClearDecision removes any cached always-allow or always-deny decision for a tool,
// so it will be asked interactively again on the next call.
func (m *ApprovalManager) ClearDecision(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.alwaysAllow, toolName)
	delete(m.alwaysDeny, toolName)
	if toolName == "bash" {
		m.alwaysAllowBash = make(map[string]bool)
		m.alwaysDenyBash = make(map[string]bool)
	}
}

// Reset clears all cached decisions and the callback.
func (m *ApprovalManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysAllow = make(map[string]bool)
	m.alwaysDeny = make(map[string]bool)
	m.alwaysAllowBash = make(map[string]bool)
	m.alwaysDenyBash = make(map[string]bool)
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
