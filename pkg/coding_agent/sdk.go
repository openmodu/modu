package coding_agent

import (
	"encoding/json"
	"os"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/coding_agent/extension"
	"github.com/crosszan/modu/pkg/providers"
)

// CreateSessionOptions configures session creation via the SDK factory.
type CreateSessionOptions struct {
	Cwd            string
	AgentDir       string
	Model          *providers.Model
	ThinkingLevel  agent.ThinkingLevel
	ScopedModels   []string
	Tools          []agent.AgentTool
	CustomTools    []agent.AgentTool
	Extensions     []extension.Extension
	SystemPrompt   string
	GetAPIKey      func(provider string) (string, error)
	StreamFn       agent.StreamFn
	SessionFile    string // path to restore an existing session
	AutoCompaction *bool
	AutoRetry      *bool
}

// CreateSessionResult is the result of creating a session via the SDK factory.
type CreateSessionResult struct {
	Session              *CodingSession
	ModelFallbackMessage string // non-empty if the requested model was not found
}

// CreateSession is the SDK factory function that wraps NewCodingSession
// with session restore, model resolution, and default configuration.
func CreateSession(opts CreateSessionOptions) (*CreateSessionResult, error) {
	result := &CreateSessionResult{}

	// Resolve model: if nil, try to use a default
	model := opts.Model
	if model == nil && opts.ScopedModels != nil && len(opts.ScopedModels) > 0 {
		// Try the first scoped model
		for _, sm := range opts.ScopedModels {
			m := providers.GetModel("", sm)
			if m != nil {
				model = m
				break
			}
		}
		if model == nil {
			result.ModelFallbackMessage = "requested model(s) not found, using defaults"
		}
	}

	// Final fallback: create a minimal model
	if model == nil {
		model = &providers.Model{
			ID:   "default",
			Name: "Default Model",
		}
	}

	cs, err := NewCodingSession(CodingSessionOptions{
		Cwd:                opts.Cwd,
		AgentDir:           opts.AgentDir,
		Model:              model,
		ThinkingLevel:      opts.ThinkingLevel,
		Tools:              opts.Tools,
		CustomTools:        opts.CustomTools,
		Extensions:         opts.Extensions,
		CustomSystemPrompt: opts.SystemPrompt,
		GetAPIKey:          opts.GetAPIKey,
		StreamFn:           opts.StreamFn,
	})
	if err != nil {
		return nil, err
	}

	// Apply scoped models
	if len(opts.ScopedModels) > 0 {
		cs.scopedModels = opts.ScopedModels
	}

	// Apply auto-compaction override
	if opts.AutoCompaction != nil {
		cs.config.AutoCompaction = *opts.AutoCompaction
	}

	// Apply auto-retry override
	if opts.AutoRetry != nil {
		cs.retryManager.SetEnabled(*opts.AutoRetry)
		cs.config.AutoRetry = *opts.AutoRetry
	}

	// Restore session from file if provided
	if opts.SessionFile != "" {
		if err := restoreSession(cs, opts.SessionFile); err != nil {
			// Non-fatal: log but continue with empty session
			result.ModelFallbackMessage = "failed to restore session: " + err.Error()
		}
	}

	result.Session = cs
	return result, nil
}

// restoreSession loads messages from a session file and restores them.
func restoreSession(cs *CodingSession, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var messages []json.RawMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return err
	}

	for _, raw := range messages {
		var msg providers.UserMessage
		if err := json.Unmarshal(raw, &msg); err == nil && msg.Role == "user" {
			cs.agent.AppendMessage(msg)
		}
	}

	return nil
}
