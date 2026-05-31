package coding_agent

import (
	"encoding/json"
	"path/filepath"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/config"
	"github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/types"
)

// GetConfig returns the current configuration.
func (s *CodingSession) GetConfig() *config.Config {
	return s.config
}

func (s *CodingSession) EffectiveConfigJSON() string {
	if s.config == nil {
		return "{}\n"
	}
	payload := map[string]any{
		"config": s.config,
		"paths": map[string]string{
			"global":  filepath.Join(s.agentDir, "settings.json"),
			"project": filepath.Join(s.cwd, ".coding_agent", "settings.json"),
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(data) + "\n"
}

// CycleThinkingLevel cycles through: off -> low -> medium -> high -> off.
func (s *CodingSession) CycleThinkingLevel() types.ThinkingLevel {
	var next types.ThinkingLevel
	switch s.thinkingLevel {
	case types.ThinkingLevelOff:
		next = types.ThinkingLevelLow
	case types.ThinkingLevelLow:
		next = types.ThinkingLevelMedium
	case types.ThinkingLevelMedium:
		next = types.ThinkingLevelHigh
	case types.ThinkingLevelHigh:
		next = types.ThinkingLevelOff
	default:
		next = types.ThinkingLevelLow
	}

	s.SetThinkingLevel(next)
	return next
}

// SetThinkingLevel sets the thinking level.
func (s *CodingSession) SetThinkingLevel(level types.ThinkingLevel) {
	s.thinkingLevel = level
	s.agent.SetThinkingLevel(level)
	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeThinkingChange, "", session.ThinkingChangeData{
		Level: level,
	}))
	s.emitSessionEvent(SessionEvent{
		Type:  SessionEventThinkingChange,
		Level: string(level),
	})
	s.writeRuntimeState()
}

// GetThinkingLevel returns the current thinking level.
func (s *engine) GetThinkingLevel() types.ThinkingLevel {
	return s.thinkingLevel
}

// SetAutoCompaction enables or disables auto-compaction.
func (s *CodingSession) SetAutoCompaction(enabled bool) {
	s.config.AutoCompaction = enabled
	s.ctxMgr.SetPolicy(s.compactionPolicy())
	s.writeRuntimeState()
}

// SetAutoRetry enables or disables auto-retry.
func (s *CodingSession) SetAutoRetry(enabled bool) {
	s.config.AutoRetry = enabled
	s.retryManager.SetEnabled(enabled)
}

// AbortRetry cancels any pending retry wait.
func (s *CodingSession) AbortRetry() {
	s.retryManager.AbortRetry()
}
