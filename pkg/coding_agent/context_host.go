package coding_agent

import (
	"context"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/contextmgr"
)

// Compact triggers context compaction for the session.
func (s *CodingSession) Compact(ctx context.Context) error {
	return s.ctxMgr.Compact(ctx)
}

// CodingSession implements contextmgr.Host, supplying the session-specific
// behaviour the context manager reaches for: compaction lifecycle events and
// the transient-message envelope/identification convention.
var _ contextmgr.Host = (*CodingSession)(nil)

func (s *CodingSession) EmitCompactionStart() {
	s.emitSessionEvent(SessionEvent{Type: SessionEventCompactionStart})
}

func (s *CodingSession) EmitCompactionDone() {
	s.emitSessionEvent(SessionEvent{Type: SessionEventCompactionDone})
}

func (s *CodingSession) NestedContextMessage(text string) agent.AgentMessage {
	return (&CustomMessage{
		Source: nestedContextSource,
		Text:   text,
	}).ToLlmMessage()
}

func (s *CodingSession) IsTransient(msg agent.AgentMessage) bool {
	return isTransientContextMessage(msg)
}

// compactionPolicy snapshots the current compaction tuning from config for the
// context manager.
func (s *CodingSession) compactionPolicy() contextmgr.Policy {
	return contextmgr.Policy{
		AutoCompaction: s.config.AutoCompaction,
		PreserveRecent: s.config.CompactionSettings.PreserveRecentMessages,
		Threshold:      s.config.CompactionSettings.MaxContextPercentage,
	}
}
