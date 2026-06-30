package coding_agent

import (
	"context"

	"github.com/openmodu/modu/pkg/coding_agent/services/contextmgr"
	"github.com/openmodu/modu/pkg/types"
)

// Compact triggers context compaction for the session.
func (s *engine) Compact(ctx context.Context) error {
	return s.ctxMgr.Compact(ctx)
}

// CompactIfNeeded triggers context compaction and reports whether it changed
// the live message history.
func (s *CodingSession) CompactIfNeeded(ctx context.Context) (bool, error) {
	if s == nil || s.engine == nil || s.ctxMgr == nil {
		return false, nil
	}
	return s.ctxMgr.CompactIfNeeded(ctx)
}

// CodingSession implements contextmgr.Host, supplying the session-specific
// behaviour the context manager reaches for: compaction lifecycle events and
// the transient-message envelope/identification convention.
var _ contextmgr.Host = (*engine)(nil)

func (s *engine) EmitCompactionStart() {
	s.emitSessionEvent(SessionEvent{Type: SessionEventCompactionStart})
}

func (s *engine) EmitCompactionDone() {
	s.emitSessionEvent(SessionEvent{Type: SessionEventCompactionDone})
}

func (s *engine) NestedContextMessage(text string) types.AgentMessage {
	return (&CustomMessage{
		Source: nestedContextSource,
		Text:   text,
	}).ToLlmMessage()
}

func (s *engine) IsTransient(msg types.AgentMessage) bool {
	return isTransientContextMessage(msg)
}

// compactionPolicy snapshots the current compaction tuning from config for the
// context manager.
func (s *engine) compactionPolicy() contextmgr.Policy {
	return contextmgr.Policy{
		AutoCompaction:             s.config.AutoCompaction,
		PreserveRecent:             s.config.CompactionSettings.PreserveRecentMessages,
		PreserveUserMessagesTokens: s.config.CompactionSettings.PreserveUserMessagesTokens,
		Threshold:                  s.config.CompactionSettings.MaxContextPercentage,
	}
}
