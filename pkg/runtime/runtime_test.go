package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// turn scripts one LLM call: either it yields an assistant message or it fails.
type turn struct {
	msg *types.AssistantMessage
	err error
}

func assistantText(text string) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       types.RoleAssistant,
		StopReason: "stop",
		Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		Timestamp:  time.Now().UnixMilli(),
	}
}

func assistantToolCall(id, name string, args map[string]any) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       types.RoleAssistant,
		StopReason: "toolUse",
		Content:    []types.ContentBlock{&types.ToolCallContent{Type: "toolCall", ID: id, Name: name, Arguments: args}},
		Timestamp:  time.Now().UnixMilli(),
	}
}

// script drives a sequence of LLM turns. idx is shared so a single script can
// span an initial run and a later resume.
type script struct {
	turns []turn
	idx   int
}

func (s *script) streamFn() types.StreamFn {
	return func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		if s.idx >= len(s.turns) {
			return nil, errors.New("script exhausted")
		}
		t := s.turns[s.idx]
		s.idx++
		if t.err != nil {
			return nil, t.err
		}
		stream := types.NewEventStream()
		go func() {
			stream.Push(types.StreamEvent{Type: types.EventDone, Reason: t.msg.StopReason, Message: t.msg})
			stream.Resolve(t.msg, nil)
			stream.Close()
		}()
		return stream, nil
	}
}

type echoTool struct{ calls int }

func (t *echoTool) Name() string        { return "echo" }
func (t *echoTool) Label() string       { return "Echo" }
func (t *echoTool) Description() string  { return "Echo tool" }
func (t *echoTool) Parameters() any      { return nil }
func (t *echoTool) Execute(_ context.Context, _ string, args map[string]any, _ types.ToolUpdateCallback) (types.ToolResult, error) {
	t.calls++
	value, _ := args["value"].(string)
	return types.ToolResult{Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "echoed: " + value}}}, nil
}

func newAgent(s *script, tool types.Tool) *agent.Agent {
	a := agent.NewAgent(types.Config{
		Model:    &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"},
		StreamFn: s.streamFn(),
	})
	if tool != nil {
		a.SetTools([]types.Tool{tool})
	}
	return a
}

func TestRunCheckpointsAndCompletes(t *testing.T) {
	ctx := context.Background()
	s := &script{turns: []turn{
		{msg: assistantToolCall("t1", "echo", map[string]any{"value": "hi"})},
		{msg: assistantText("done")},
	}}
	store := NewMemoryStore()
	rt := New(newAgent(s, &echoTool{}), store, "sess")

	if err := rt.Run(ctx, "go"); err != nil {
		t.Fatalf("run: %v", err)
	}

	latest, err := rt.Latest(ctx)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest.Status != types.SessionStatusCompleted {
		t.Fatalf("expected completed, got %s", latest.Status)
	}
	// user, assistant(toolcall), toolResult, assistant(final)
	if len(latest.Messages) != 4 {
		t.Fatalf("expected 4 committed messages, got %d", len(latest.Messages))
	}
	history, _ := rt.History(ctx)
	// 4 per-message checkpoints + 1 terminal.
	if len(history) != 5 {
		t.Fatalf("expected 5 checkpoints, got %d", len(history))
	}
}

func TestResumeContinuesAfterFailure(t *testing.T) {
	ctx := context.Background()
	tool := &echoTool{}
	s := &script{turns: []turn{
		{msg: assistantToolCall("t1", "echo", map[string]any{"value": "hi"})},
		{err: errors.New("simulated provider outage")}, // crash on the second LLM call
		{msg: assistantText("recovered")},              // served on resume
	}}
	store := NewMemoryStore()
	rt := New(newAgent(s, tool), store, "sess")

	if err := rt.Run(ctx, "go"); err == nil {
		t.Fatal("expected run to fail")
	}
	failed, _ := rt.Latest(ctx)
	if failed.Status != types.SessionStatusFailed {
		t.Fatalf("expected failed status, got %s", failed.Status)
	}

	resumed, err := rt.Resume(ctx)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !resumed {
		t.Fatal("expected resume to continue the run")
	}

	final, _ := rt.Latest(ctx)
	if final.Status != types.SessionStatusCompleted {
		t.Fatalf("expected completed after resume, got %s", final.Status)
	}
	msgs, _ := final.Restore()
	last, ok := msgs[len(msgs)-1].(types.AssistantMessage)
	if !ok || textOf(last) != "recovered" {
		t.Fatalf("expected final message 'recovered', got %#v", msgs[len(msgs)-1])
	}
	// Re-entrancy: the tool already ran before the crash and must not run again.
	if tool.calls != 1 {
		t.Fatalf("expected tool to execute exactly once, got %d", tool.calls)
	}
}

func TestResumeCompletedIsNoop(t *testing.T) {
	ctx := context.Background()
	s := &script{turns: []turn{{msg: assistantText("done")}}}
	store := NewMemoryStore()
	rt := New(newAgent(s, nil), store, "sess")

	if err := rt.Run(ctx, "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	resumed, err := rt.Resume(ctx)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed {
		t.Fatal("resume of a completed session should be a no-op")
	}
}

func TestResumeAcrossInstancesUsingFileStore(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("file store: %v", err)
	}

	// First "process": runs and crashes.
	s1 := &script{turns: []turn{
		{msg: assistantToolCall("t1", "echo", map[string]any{"value": "hi"})},
		{err: errors.New("simulated provider outage")},
	}}
	rt1 := New(newAgent(s1, &echoTool{}), store, "sess")
	if err := rt1.Run(ctx, "go"); err == nil {
		t.Fatal("expected first run to fail")
	}

	// Second "process": fresh agent + runtime, same store/session, resumes.
	s2 := &script{turns: []turn{{msg: assistantText("recovered")}}}
	rt2 := New(newAgent(s2, &echoTool{}), store, "sess")
	resumed, err := rt2.Resume(ctx)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !resumed {
		t.Fatal("expected cross-instance resume to continue")
	}

	final, _ := rt2.Latest(ctx)
	if final.Status != types.SessionStatusCompleted {
		t.Fatalf("expected completed, got %s", final.Status)
	}
	// Seq must stay monotonic across instances (no overwrite of the journal).
	history, _ := rt2.History(ctx)
	for i := 1; i < len(history); i++ {
		if history[i].Seq <= history[i-1].Seq {
			t.Fatalf("seq not monotonic at %d: %d <= %d", i, history[i].Seq, history[i-1].Seq)
		}
	}
}

func TestRewindForksToEarlierState(t *testing.T) {
	ctx := context.Background()
	s := &script{turns: []turn{
		{msg: assistantText("first")},
	}}
	store := NewMemoryStore()
	a := newAgent(s, nil)
	rt := New(a, store, "sess")
	if err := rt.Run(ctx, "go"); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Checkpoint at seq 0 holds only the user message (before any assistant reply).
	head, err := rt.Rewind(ctx, 0)
	if err != nil {
		t.Fatalf("rewind: %v", err)
	}
	if head.ParentSeq != 0 {
		t.Fatalf("expected ParentSeq 0, got %d", head.ParentSeq)
	}
	if len(a.GetState().Messages) != 1 {
		t.Fatalf("expected agent rewound to 1 message, got %d", len(a.GetState().Messages))
	}
	if got := rt.mustLatest(ctx, t).Seq; got != head.Seq {
		t.Fatalf("expected latest seq %d after rewind, got %d", head.Seq, got)
	}
}

func (r *Runtime) mustLatest(ctx context.Context, t *testing.T) Checkpoint {
	t.Helper()
	cp, err := r.Latest(ctx)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	return cp
}

func TestRepairDanglingToolCalls(t *testing.T) {
	messages := []types.AgentMessage{
		types.UserMessage{Role: types.RoleUser, Content: "go"},
		types.AssistantMessage{Role: types.RoleAssistant, Content: []types.ContentBlock{
			&types.ToolCallContent{Type: "toolCall", ID: "t1", Name: "echo"},
			&types.ToolCallContent{Type: "toolCall", ID: "t2", Name: "echo"},
		}},
		types.ToolResultMessage{Role: types.RoleToolResult, ToolCallID: "t1", ToolName: "echo"},
	}
	repaired := repairDanglingToolCalls(messages)
	if len(repaired) != 4 {
		t.Fatalf("expected one synthetic result appended, got %d total", len(repaired))
	}
	last, ok := repaired[len(repaired)-1].(types.ToolResultMessage)
	if !ok || last.ToolCallID != "t2" || !last.IsError {
		t.Fatalf("expected synthetic error result for t2, got %#v", repaired[len(repaired)-1])
	}
}

func textOf(msg types.AssistantMessage) string {
	for _, block := range msg.Content {
		if text, ok := block.(*types.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
