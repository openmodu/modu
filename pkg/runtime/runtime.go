// Package runtime layers durable, rewindable, resumable, re-entrant execution
// over pkg/agent. The agent loop is already event-sourced — committed messages
// accumulate in agent state on every message_end event — so the runtime simply
// journals a checkpoint at each commit boundary. From that append-only journal
// it can resume an interrupted run (replay + continue), rewind to an earlier
// point (fork a new head), and be safely re-entered without duplicating work.
package runtime

import (
	"context"
	"sync"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// Runtime binds a single agent session to a durable checkpoint Store.
type Runtime struct {
	agent     *agent.Agent
	store     Store
	sessionID string

	mu  sync.Mutex
	seq int64 // last seq assigned; -1 means "not yet loaded from the store"
}

// New creates a Runtime for the given agent, store and session id. The agent's
// SessionID is set to match so provider-side caching lines up with the journal.
func New(a *agent.Agent, store Store, sessionID string) *Runtime {
	a.SetSessionID(sessionID)
	return &Runtime{agent: a, store: store, sessionID: sessionID, seq: -1}
}

// Run starts a fresh prompt and checkpoints every committed message plus a final
// checkpoint recording the terminal status. The agent rejects concurrent runs,
// which is what makes Run/Resume safe to call repeatedly (re-entrant).
func (r *Runtime) Run(ctx context.Context, prompt any) error {
	if err := r.loadSeq(ctx); err != nil {
		return err
	}
	unsubscribe := r.agent.Subscribe(r.checkpointListener())
	defer unsubscribe()

	runErr := r.agent.Prompt(ctx, prompt)
	r.commitTerminal(ctx, runErr)
	return runErr
}

// Resume reconstructs the latest checkpoint into the agent and continues the run
// if it had not finished. It returns true when execution was actually continued.
// A completed session is a no-op (returns false), making resume idempotent.
func (r *Runtime) Resume(ctx context.Context) (bool, error) {
	if err := r.loadSeq(ctx); err != nil {
		return false, err
	}
	cp, err := r.store.Latest(ctx, r.sessionID)
	if err != nil {
		return false, err
	}
	if cp.Status == types.SessionStatusCompleted {
		return false, nil
	}

	messages, err := cp.Restore()
	if err != nil {
		return false, err
	}
	// Drop synthetic failure markers, then make any interrupted tool calls
	// well-formed so the next model turn is valid.
	messages = stripTrailingErrors(messages)
	messages = repairDanglingToolCalls(messages)

	r.restoreInto(cp, messages)

	// If the conversation already ends on a model answer there is nothing to
	// continue — record completion and stop.
	if len(messages) == 0 || roleKind(messages[len(messages)-1]) == types.RoleAssistant {
		r.commit(ctx, types.SessionStatusCompleted, "resume-complete")
		return false, nil
	}

	unsubscribe := r.agent.Subscribe(r.checkpointListener())
	defer unsubscribe()

	runErr := r.agent.Continue(ctx)
	r.commitTerminal(ctx, runErr)
	return true, runErr
}

// Rewind forks a new head from the checkpoint at seq: it appends a copy with a
// fresh seq (ParentSeq pointing at the source) and restores that state into the
// agent. History is preserved; the caller can then Run or Resume to move forward
// along the new branch.
func (r *Runtime) Rewind(ctx context.Context, seq int64) (Checkpoint, error) {
	if err := r.loadSeq(ctx); err != nil {
		return Checkpoint{}, err
	}
	src, err := r.store.At(ctx, r.sessionID, seq)
	if err != nil {
		return Checkpoint{}, err
	}

	messages, err := src.Restore()
	if err != nil {
		return Checkpoint{}, err
	}
	r.restoreInto(src, messages)

	head := src
	head.Seq = r.nextSeq()
	head.ParentSeq = src.Seq
	head.Status = types.SessionStatusPaused
	head.Error = ""
	head.Label = "rewind"
	if err := r.store.Append(ctx, head); err != nil {
		return Checkpoint{}, err
	}
	return head, nil
}

// History returns the full checkpoint lineage for the session in seq order.
func (r *Runtime) History(ctx context.Context) ([]Checkpoint, error) {
	return r.store.History(ctx, r.sessionID)
}

// Latest returns the most recent checkpoint for the session.
func (r *Runtime) Latest(ctx context.Context) (Checkpoint, error) {
	return r.store.Latest(ctx, r.sessionID)
}

func (r *Runtime) restoreInto(cp Checkpoint, messages []types.AgentMessage) {
	if cp.Model != nil {
		r.agent.SetModel(cp.Model)
	}
	r.agent.SetSystemPrompt(cp.SystemPrompt)
	if cp.Thinking != "" {
		r.agent.SetThinkingLevel(cp.Thinking)
	}
	r.agent.ReplaceMessages(messages)
}

// checkpointListener returns an event handler that commits a checkpoint for each
// committed message. It runs on the agent's single event-drain goroutine, so the
// seq counter needs no extra coordination beyond the runtime mutex.
func (r *Runtime) checkpointListener() func(types.Event) {
	return func(event types.Event) {
		if event.Type == types.EventTypeMessageEnd {
			r.commit(context.Background(), types.SessionStatusRunning, "")
		}
	}
}

func (r *Runtime) commitTerminal(ctx context.Context, runErr error) {
	if runErr != nil {
		r.commit(ctx, types.SessionStatusFailed, "")
		return
	}
	r.commit(ctx, types.SessionStatusCompleted, "")
}

func (r *Runtime) commit(ctx context.Context, status types.SessionStatus, label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	cp, err := snapshot(r.agent.GetState(), r.sessionID, r.seq, r.seq-1, status, label)
	if err != nil {
		// A message we cannot serialize must not be silently dropped from the
		// journal; roll the seq back so the next commit retries the number.
		r.seq--
		return
	}
	if err := r.store.Append(ctx, cp); err != nil {
		r.seq--
	}
}

func (r *Runtime) nextSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	return r.seq
}

// loadSeq syncs the in-memory seq counter with the store so seqs stay monotonic
// across process restarts.
func (r *Runtime) loadSeq(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seq >= 0 {
		return nil
	}
	cp, err := r.store.Latest(ctx, r.sessionID)
	if err == ErrNotFound {
		r.seq = -1
		return nil
	}
	if err != nil {
		return err
	}
	r.seq = cp.Seq
	return nil
}

// roleKind reports the conversational role of a message regardless of whether it
// is stored as a value or pointer.
func roleKind(message types.AgentMessage) string {
	switch normalizeMessage(message).(type) {
	case types.UserMessage:
		return types.RoleUser
	case types.AssistantMessage:
		return types.RoleAssistant
	case types.ToolResultMessage:
		return types.RoleToolResult
	default:
		return ""
	}
}

// stripTrailingErrors removes trailing synthetic failure messages (the empty
// assistant message pkg/agent appends when a run errors or is aborted) so a
// resumed run retries from the last real turn instead of treating the failure
// marker as a finished answer.
func stripTrailingErrors(messages []types.AgentMessage) []types.AgentMessage {
	end := len(messages)
	for end > 0 {
		assistant, ok := normalizeMessage(messages[end-1]).(types.AssistantMessage)
		if !ok || (assistant.StopReason != "error" && assistant.StopReason != "aborted") {
			break
		}
		end--
	}
	return messages[:end]
}
