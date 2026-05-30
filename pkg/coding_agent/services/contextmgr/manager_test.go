package contextmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/resource"
	"github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/types"
)

// mockHost records the host callbacks so tests can assert on the manager's
// interaction with its session without a real coding_agent.
type mockHost struct {
	startCalls int
	doneCalls  int
	nested     []string
}

func (h *mockHost) EmitCompactionStart() { h.startCalls++ }
func (h *mockHost) EmitCompactionDone()  { h.doneCalls++ }

func (h *mockHost) NestedContextMessage(text string) agent.AgentMessage {
	h.nested = append(h.nested, text)
	return userMsg(text)
}

func (h *mockHost) IsTransient(msg agent.AgentMessage) bool {
	return strings.HasPrefix(messageText(msg), "[transient] ")
}

func userMsg(text string) types.UserMessage {
	return types.UserMessage{
		Role:    "user",
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
}

func messageText(msg agent.AgentMessage) string {
	um, ok := msg.(types.UserMessage)
	if !ok {
		return ""
	}
	blocks, ok := um.Content.([]types.ContentBlock)
	if !ok {
		return ""
	}
	for _, b := range blocks {
		if tc, ok := b.(*types.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func summaryStreamFn() agent.StreamFn {
	return func(ctx context.Context, model *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			msg := &types.AssistantMessage{
				Role:       "assistant",
				ProviderID: model.ProviderID,
				Model:      model.ID,
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "mock summary"}},
			}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}
}

func testModel(window int) *types.Model {
	return &types.Model{ID: "m", ProviderID: "p", ContextWindow: window}
}

// newManager builds a Manager backed by a real agent and session manager in a
// temp dir, plus the supplied mock host.
func newManager(t *testing.T, host *mockHost, stream agent.StreamFn) (*Manager, *agent.Agent) {
	t.Helper()
	dir := t.TempDir()
	sm, err := session.NewManager(filepath.Join(dir, ".agent"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.NewAgent(agent.Config{})
	m := New(Deps{
		Agent:          ag,
		Resources:      resource.NewLoader(filepath.Join(dir, ".agent"), dir),
		SessionManager: sm,
		StreamFn:       func() agent.StreamFn { return stream },
		APIKey:         func(string) (string, error) { return "", nil },
		Host:           host,
	})
	return m, ag
}

func TestMaybeAutoCompactSkipsWhenDisabled(t *testing.T) {
	host := &mockHost{}
	m, _ := newManager(t, host, nil)
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{AutoCompaction: false, Threshold: 80})
	m.AddUsage(9999999)

	m.MaybeAutoCompact(context.Background())

	if host.startCalls != 0 {
		t.Fatalf("disabled auto-compaction should not start compaction, got %d", host.startCalls)
	}
}

func TestMaybeAutoCompactSkipsWhenNoModel(t *testing.T) {
	host := &mockHost{}
	m, _ := newManager(t, host, nil)
	m.SetPolicy(Policy{AutoCompaction: true, Threshold: 80})
	m.AddUsage(9999999)

	m.MaybeAutoCompact(context.Background())

	if host.startCalls != 0 {
		t.Fatalf("nil model should not start compaction, got %d", host.startCalls)
	}
}

func TestMaybeAutoCompactSkipsBelowThreshold(t *testing.T) {
	host := &mockHost{}
	m, _ := newManager(t, host, summaryStreamFn())
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{AutoCompaction: true, Threshold: 80})
	m.AddUsage(5000) // 50% of 10000

	m.MaybeAutoCompact(context.Background())

	if host.startCalls != 0 {
		t.Fatalf("below threshold should not compact, got %d", host.startCalls)
	}
}

func TestMaybeAutoCompactUsesDefaultThreshold(t *testing.T) {
	// Threshold <= 0 should fall back to defaultThreshold (80%).
	host := &mockHost{}
	m, ag := newManager(t, host, summaryStreamFn())
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{AutoCompaction: true, Threshold: 0})
	for i := 0; i < 8; i++ {
		ag.AppendMessage(userMsg("msg"))
	}
	m.AddUsage(7900) // 79% — below default 80, should not compact
	m.MaybeAutoCompact(context.Background())
	if host.startCalls != 0 {
		t.Fatalf("79%% should be below default threshold, got %d starts", host.startCalls)
	}

	m.AddUsage(200) // now 81%
	m.MaybeAutoCompact(context.Background())
	if host.startCalls != 1 {
		t.Fatalf("81%% should exceed default threshold, got %d starts", host.startCalls)
	}
}

func TestMaybeAutoCompactCompactsAboveThreshold(t *testing.T) {
	host := &mockHost{}
	m, ag := newManager(t, host, summaryStreamFn())
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{AutoCompaction: true, PreserveRecent: 4, Threshold: 80})
	for i := 0; i < 10; i++ {
		ag.AppendMessage(userMsg("msg"))
	}
	before := len(ag.GetState().Messages)
	m.AddUsage(9000) // 90%

	m.MaybeAutoCompact(context.Background())

	if host.startCalls != 1 || host.doneCalls != 1 {
		t.Fatalf("expected one start/done, got start=%d done=%d", host.startCalls, host.doneCalls)
	}
	if after := len(ag.GetState().Messages); after >= before {
		t.Fatalf("expected compaction to shrink messages: before=%d after=%d", before, after)
	}
	if m.Tokens() != 0 {
		t.Fatalf("expected tokens reset after compaction, got %d", m.Tokens())
	}
	if m.IsCompacting() {
		t.Fatal("IsCompacting should be false after compaction completes")
	}
}

func TestCompactReplacesMessagesAndResets(t *testing.T) {
	host := &mockHost{}
	m, ag := newManager(t, host, summaryStreamFn())
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{PreserveRecent: 2})
	for i := 0; i < 6; i++ {
		ag.AppendMessage(userMsg("msg"))
	}
	m.AddUsage(1234)

	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}

	msgs := ag.GetState().Messages
	// summary + PreserveRecent(2) preserved
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (summary + 2 preserved), got %d", len(msgs))
	}
	if !strings.Contains(messageText(msgs[0]), "mock summary") {
		t.Fatalf("expected summary as first message, got %q", messageText(msgs[0]))
	}
	if m.Tokens() != 0 {
		t.Fatalf("expected tokens reset, got %d", m.Tokens())
	}
	if host.doneCalls != 1 {
		t.Fatalf("expected EmitCompactionDone once, got %d", host.doneCalls)
	}
}

func TestPruneTransientRemovesMarkedMessages(t *testing.T) {
	host := &mockHost{}
	m, ag := newManager(t, host, nil)
	ag.AppendMessage(userMsg("keep one"))
	ag.AppendMessage(userMsg("[transient] nested context"))
	ag.AppendMessage(userMsg("keep two"))

	m.PruneTransient()

	msgs := ag.GetState().Messages
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after pruning, got %d", len(msgs))
	}
	for _, msg := range msgs {
		if strings.HasPrefix(messageText(msg), "[transient] ") {
			t.Fatal("transient message was not pruned")
		}
	}
}

func TestPruneTransientNoopWhenNothingTransient(t *testing.T) {
	host := &mockHost{}
	m, ag := newManager(t, host, nil)
	ag.AppendMessage(userMsg("a"))
	ag.AppendMessage(userMsg("b"))

	m.PruneTransient()

	if len(ag.GetState().Messages) != 2 {
		t.Fatalf("expected messages untouched, got %d", len(ag.GetState().Messages))
	}
}

func TestOnToolExecutionEndInjectsAndDedupes(t *testing.T) {
	host := &mockHost{}
	dir := t.TempDir()
	sm, err := session.NewManager(filepath.Join(dir, ".agent"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("PATH RULES"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{
		Agent:          agent.NewAgent(agent.Config{}),
		Resources:      resource.NewLoader(filepath.Join(dir, ".agent"), dir),
		SessionManager: sm,
		StreamFn:       func() agent.StreamFn { return nil },
		APIKey:         func(string) (string, error) { return "", nil },
		Host:           host,
	})

	event := agent.Event{
		ToolName: "read",
		Args:     map[string]any{"path": filepath.Join(dir, "main.go")},
	}

	m.OnToolExecutionEnd(event)
	if len(host.nested) != 1 {
		t.Fatalf("expected one nested-context injection, got %d", len(host.nested))
	}
	if !strings.Contains(host.nested[0], "PATH RULES") {
		t.Fatalf("expected injected context to include AGENTS.md content, got %q", host.nested[0])
	}

	// Second access to the same context file must not re-inject it.
	m.OnToolExecutionEnd(event)
	if len(host.nested) != 1 {
		t.Fatalf("expected dedupe to suppress re-injection, got %d", len(host.nested))
	}
}

func TestOnToolExecutionEndIgnoresNonFileTools(t *testing.T) {
	host := &mockHost{}
	m, _ := newManager(t, host, nil)
	m.OnToolExecutionEnd(agent.Event{ToolName: "todo_write", Args: map[string]any{"path": "x"}})
	if len(host.nested) != 0 {
		t.Fatalf("non-file tool should not inject context, got %d", len(host.nested))
	}
}

func TestMarkInitialContextSuppressesInjection(t *testing.T) {
	host := &mockHost{}
	dir := t.TempDir()
	sm, err := session.NewManager(filepath.Join(dir, ".agent"), dir)
	if err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("PATH RULES"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(Deps{
		Agent:          agent.NewAgent(agent.Config{}),
		Resources:      resource.NewLoader(filepath.Join(dir, ".agent"), dir),
		SessionManager: sm,
		StreamFn:       func() agent.StreamFn { return nil },
		APIKey:         func(string) (string, error) { return "", nil },
		Host:           host,
	})
	// Already folded into the system prompt — should not be re-injected.
	m.MarkInitialContext([]string{agentsPath})

	m.OnToolExecutionEnd(agent.Event{ToolName: "read", Args: map[string]any{"path": filepath.Join(dir, "main.go")}})
	if len(host.nested) != 0 {
		t.Fatalf("initial context should be suppressed, got %d injections", len(host.nested))
	}
}
