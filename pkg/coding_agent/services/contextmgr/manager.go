// Package contextmgr owns a session's conversation-window concerns: token
// accounting, auto-compaction policy and execution, dynamic path-context
// injection, and transient-message pruning. It depends only on lower-level
// packages (agent, compaction, resource, session, types); everything specific
// to the hosting session is reached through the small Host interface, so this
// package never imports coding_agent.
package contextmgr

import (
	"context"
	"fmt"
	"sync"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
	"github.com/openmodu/modu/pkg/coding_agent/services/compaction"
	"github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/types"
)

// Host supplies the session-specific behaviour the manager cannot own without
// depending back on the coding_agent package.
type Host interface {
	// EmitCompactionStart / EmitCompactionDone notify the host of the
	// compaction lifecycle so it can surface session events.
	EmitCompactionStart()
	EmitCompactionDone()
	// NestedContextMessage wraps injected path-context text in the host's
	// transient message envelope.
	NestedContextMessage(text string) agent.AgentMessage
	// IsTransient reports whether a conversation message is host-injected
	// transient context that should be pruned at the end of a turn.
	IsTransient(msg agent.AgentMessage) bool
}

// Deps are the stable collaborators handed to the manager at construction.
// StreamFn is a getter rather than a value because the session may swap its
// stream function after the manager is built.
type Deps struct {
	Agent          *agent.Agent
	Resources      *resource.Loader
	SessionManager *session.Manager
	StreamFn       func() agent.StreamFn
	APIKey         func(provider string) (string, error)
	Host           Host
}

// Policy holds the compaction tuning that can change during a session.
type Policy struct {
	AutoCompaction bool
	PreserveRecent int
	// Threshold is the percentage of the context window at which
	// auto-compaction fires. A value <= 0 falls back to defaultThreshold.
	Threshold float64
}

const defaultThreshold = 80.0

// Manager owns conversation-window state and behaviour for one session.
type Manager struct {
	deps Deps

	mu           sync.Mutex
	totalTokens  int
	isCompacting bool
	model        *types.Model
	policy       Policy

	contextMu      sync.Mutex
	loadedContexts map[string]struct{}
}

// New creates a Manager. SetModel and SetPolicy should be called to seed the
// current model and compaction policy.
func New(deps Deps) *Manager {
	return &Manager{
		deps:           deps,
		loadedContexts: make(map[string]struct{}),
	}
}

// SetModel updates the model used for window sizing and compaction.
func (m *Manager) SetModel(model *types.Model) {
	m.mu.Lock()
	m.model = model
	m.mu.Unlock()
}

// SetPolicy updates the compaction tuning.
func (m *Manager) SetPolicy(p Policy) {
	m.mu.Lock()
	m.policy = p
	m.mu.Unlock()
}

// AddUsage accumulates token usage reported by the model.
func (m *Manager) AddUsage(tokens int) {
	m.mu.Lock()
	m.totalTokens += tokens
	m.mu.Unlock()
}

// Tokens returns accumulated token usage since the last compaction.
func (m *Manager) Tokens() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalTokens
}

// IsCompacting reports whether a compaction is currently running.
func (m *Manager) IsCompacting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isCompacting
}

// MarkInitialContext records context-file paths already folded into the system
// prompt so they are not re-injected as dynamic context.
func (m *Manager) MarkInitialContext(paths []string) {
	m.contextMu.Lock()
	defer m.contextMu.Unlock()
	for _, path := range paths {
		m.loadedContexts[path] = struct{}{}
	}
}

// Compact summarizes the conversation and replaces it with the summary.
func (m *Manager) Compact(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	m.mu.Lock()
	m.isCompacting = true
	model := m.model
	preserve := m.policy.PreserveRecent
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.isCompacting = false
		m.mu.Unlock()
	}()

	state := m.deps.Agent.GetState()

	result, err := compaction.Compact(ctx, state.Messages, compaction.Options{
		PreserveRecent: preserve,
		Model:          model,
		GetAPIKey:      m.deps.APIKey,
		StreamFn:       m.deps.StreamFn(),
	})
	if err != nil {
		return fmt.Errorf("compaction failed: %w", err)
	}

	m.deps.Agent.ReplaceMessages(result.Messages)

	// Reset token counter after compaction.
	m.mu.Lock()
	m.totalTokens = 0
	m.mu.Unlock()

	_ = m.deps.SessionManager.Append(session.NewEntry(session.EntryTypeCompaction, "", session.CompactionData{
		Summary:       result.Summary,
		OriginalCount: result.OriginalCount,
		NewCount:      result.NewCount,
	}))

	m.deps.Host.EmitCompactionDone()
	return nil
}

// MaybeAutoCompact triggers compaction when accumulated token usage crosses the
// configured percentage of the model's context window.
func (m *Manager) MaybeAutoCompact(ctx context.Context) {
	m.mu.Lock()
	policy := m.policy
	model := m.model
	tokens := m.totalTokens
	m.mu.Unlock()

	if !policy.AutoCompaction || model == nil {
		return
	}

	threshold := policy.Threshold
	if threshold <= 0 {
		threshold = defaultThreshold
	}

	usagePercent := float64(tokens) / float64(WindowFor(model)) * 100.0
	if usagePercent >= threshold {
		m.deps.Host.EmitCompactionStart()
		_ = m.Compact(ctx)
	}
}
