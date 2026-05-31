// Package approval is the tool-execution permission service: it caches
// always-allow/deny decisions, evaluates permission rules, classifies dangerous
// bash commands, and falls back to an interactive callback. It is a self-
// contained L2 service — the session composes it and supplies an Observer; the
// package never depends back on the session.
package approval

import (
	"errors"
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/config"
	"github.com/openmodu/modu/pkg/types"
)

// Manager manages tool execution permissions.
// It remembers always-allow and always-deny decisions per tool name. Interactive
// "always" decisions for bash are scoped to the exact command so approving a
// safe command does not silently approve a later dangerous one.
type Manager struct {
	mu              sync.RWMutex
	alwaysAllow     map[string]bool
	alwaysDeny      map[string]bool
	alwaysAllowBash map[string]bool
	alwaysDenyBash  map[string]bool
	rules           config.PermissionConfig
	observer        Observer
	blocker         func(toolName string, args map[string]any) (bool, string)
	// callback is called when no cached decision exists.
	// If nil, all tools are auto-approved.
	callback func(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error)
}

// Observer is notified when a permission is requested or denied.
type Observer interface {
	OnPermissionRequest(toolName, toolCallID string, args map[string]any)
	OnPermissionDenied(toolName, toolCallID string, args map[string]any, reason string)
}

// New creates a Manager with no cached rules.
func New() *Manager {
	return &Manager{
		alwaysAllow:     make(map[string]bool),
		alwaysDeny:      make(map[string]bool),
		alwaysAllowBash: make(map[string]bool),
		alwaysDenyBash:  make(map[string]bool),
	}
}

// SetCallback sets the interactive approval callback.
func (m *Manager) SetCallback(fn func(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callback = fn
}

func (m *Manager) SetRules(rules config.PermissionConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = rules
}

func (m *Manager) SetObserver(observer Observer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observer = observer
}

func (m *Manager) SetBlocker(fn func(toolName string, args map[string]any) (bool, string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocker = fn
}

// Approve is the types.Config.ApproveTool implementation.
func (m *Manager) Approve(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error) {
	// exit_plan_mode runs its own interactive plan-approval gate inside the
	// tool (so the user's rejection feedback can flow back to the model), so
	// the agent-level gate must let it through without prompting.
	if toolName == "exit_plan_mode" || isGoalStateTool(toolName) {
		return types.ToolApprovalAllow, nil
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
			return types.ToolApprovalDeny, errors.New(reason)
		}
	}

	bashCommand := approvalBashCommand(toolName, args)
	bashNeedsApproval := isDangerousBashCommand(bashCommand)

	m.mu.RLock()
	if bashCommand != "" && m.alwaysDenyBash[bashCommand] {
		m.mu.RUnlock()
		return types.ToolApprovalDeny, nil
	}
	if m.alwaysDeny[toolName] {
		m.mu.RUnlock()
		return types.ToolApprovalDeny, nil
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
		return types.ToolApprovalAllow, nil
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
		return types.ToolApprovalAllow, nil
	}
	if observer != nil {
		observer.OnPermissionRequest(toolName, toolCallID, args)
	}

	decision, err := cb(toolName, toolCallID, args)
	if err != nil {
		if observer != nil {
			observer.OnPermissionDenied(toolName, toolCallID, args, err.Error())
		}
		return types.ToolApprovalDeny, err
	}
	if !decision.IsAllow() && observer != nil {
		observer.OnPermissionDenied(toolName, toolCallID, args, string(decision))
	}

	// Persist always decisions. Interactive bash decisions are intentionally
	// command-scoped; programmatic AllowAlways("bash") remains tool-wide.
	if bashCommand != "" && decision == types.ToolApprovalAllowAlways {
		m.mu.Lock()
		m.alwaysAllowBash[bashCommand] = true
		delete(m.alwaysDenyBash, bashCommand)
		m.mu.Unlock()
	} else if bashCommand != "" && decision == types.ToolApprovalDenyAlways {
		m.mu.Lock()
		m.alwaysDenyBash[bashCommand] = true
		delete(m.alwaysAllowBash, bashCommand)
		m.mu.Unlock()
	} else if decision == types.ToolApprovalAllowAlways {
		m.mu.Lock()
		m.alwaysAllow[toolName] = true
		m.mu.Unlock()
	} else if decision == types.ToolApprovalDenyAlways {
		m.mu.Lock()
		m.alwaysDeny[toolName] = true
		m.mu.Unlock()
	}

	return decision, nil
}

func evaluateDenyPermissionRules(rules config.PermissionConfig, toolName string, args map[string]any) (types.ToolApprovalDecision, string, bool) {
	for _, denied := range rules.DenyTools {
		if strings.TrimSpace(denied) == toolName {
			return types.ToolApprovalDeny, "denied by permission rules", true
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
			return types.ToolApprovalDeny, "bash command denied by permission rules", true
		}
	}
	return "", "", false
}

func evaluateAllowPermissionRules(rules config.PermissionConfig, toolName string, args map[string]any) (types.ToolApprovalDecision, string, bool) {
	for _, allowed := range rules.AllowTools {
		if strings.TrimSpace(allowed) == toolName {
			return types.ToolApprovalAllow, "", true
		}
	}
	if isAutoAllowedReadOnlyTool(toolName) {
		return types.ToolApprovalAllow, "", true
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
				return types.ToolApprovalAllow, "", true
			}
		}
		return types.ToolApprovalDeny, "bash command not allowed by permission rules", true
	}
	return types.ToolApprovalAllow, "", true
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
func (m *Manager) AllowAlways(toolName string) {
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
func (m *Manager) DenyAlways(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysDeny[toolName] = true
	delete(m.alwaysAllow, toolName)
	if toolName == "bash" {
		m.alwaysAllowBash = make(map[string]bool)
		m.alwaysDenyBash = make(map[string]bool)
	}
}

// ClearDecision removes any cached always-allow or always-deny decision for a
// tool, so it will be asked interactively again on the next call.
func (m *Manager) ClearDecision(toolName string) {
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
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysAllow = make(map[string]bool)
	m.alwaysDeny = make(map[string]bool)
	m.alwaysAllowBash = make(map[string]bool)
	m.alwaysDenyBash = make(map[string]bool)
	m.rules = config.PermissionConfig{}
	m.observer = nil
	m.callback = nil
}
