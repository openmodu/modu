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

func (h *mockHost) NestedContextMessage(text string) types.AgentMessage {
	h.nested = append(h.nested, text)
	return userMsg(text)
}

func (h *mockHost) IsTransient(msg types.AgentMessage) bool {
	return strings.HasPrefix(messageText(msg), "[transient] ")
}

func userMsg(text string) types.UserMessage {
	return types.UserMessage{
		Role:    "user",
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
}

func messageText(msg types.AgentMessage) string {
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

func summaryStreamFn() types.StreamFn {
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
func newManager(t *testing.T, host *mockHost, stream types.StreamFn) (*Manager, *agent.Agent) {
	t.Helper()
	dir := t.TempDir()
	sm, err := session.NewManager(filepath.Join(dir, ".agent"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ag := agent.NewAgent(types.Config{})
	m := New(Deps{
		Agent:          ag,
		Resources:      resource.NewLoader(filepath.Join(dir, ".agent"), dir),
		SessionManager: sm,
		StreamFn:       func() types.StreamFn { return stream },
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

func TestTokensUntilCompaction(t *testing.T) {
	host := &mockHost{}
	m, _ := newManager(t, host, nil)
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{AutoCompaction: true, Threshold: 75})
	m.AddUsage(2500)

	remaining, ok := m.TokensUntilCompaction()
	if !ok {
		t.Fatal("expected context remaining to be available")
	}
	if remaining != 5000 {
		t.Fatalf("expected 5000 tokens until compaction, got %d", remaining)
	}

	m.AddUsage(6000)
	remaining, ok = m.TokensUntilCompaction()
	if !ok {
		t.Fatal("expected context remaining to remain available")
	}
	if remaining != 0 {
		t.Fatalf("expected remaining tokens to clamp at 0, got %d", remaining)
	}

	m.SetPolicy(Policy{AutoCompaction: false, Threshold: 75})
	if _, ok := m.TokensUntilCompaction(); ok {
		t.Fatal("expected unavailable context remaining when auto-compaction is disabled")
	}
}

func TestCompactReplacesMessagesAndResets(t *testing.T) {
	host := &mockHost{}
	m, ag := newManager(t, host, summaryStreamFn())
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{PreserveRecent: 2})
	for i := 0; i < 4; i++ {
		ag.AppendMessage(userMsg("msg"))
	}
	ag.AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{
		&types.ToolCallContent{Type: "toolCall", ID: "read-1", Name: "read", Arguments: map[string]any{"path": "old.go"}},
	}})
	ag.AppendMessage(types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{
		&types.ToolCallContent{Type: "toolCall", ID: "edit-1", Name: "edit", Arguments: map[string]any{"path": "new.go"}},
	}})
	ag.AppendMessage(userMsg("tail one"))
	ag.AppendMessage(userMsg("tail two"))
	m.AddUsage(1234)

	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}

	msgs := ag.GetState().Messages
	// summary + compacted-range user anchors + PreserveRecent(2) preserved
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages (summary + 2 user anchors + 2 preserved), got %d", len(msgs))
	}
	if !strings.Contains(messageText(msgs[0]), "mock summary") {
		t.Fatalf("expected summary as first message, got %q", messageText(msgs[0]))
	}
	if m.Tokens() != 0 {
		t.Fatalf("expected tokens reset, got %d", m.Tokens())
	}
	if host.startCalls != 1 || host.doneCalls != 1 {
		t.Fatalf("expected one start/done for manual compact, got start=%d done=%d", host.startCalls, host.doneCalls)
	}
	entries := m.deps.SessionManager.Load()
	if len(entries) != 1 || entries[0].Type != session.EntryTypeCompaction {
		t.Fatalf("expected one compaction session entry, got %#v", entries)
	}
	data, ok := entries[0].Data.(session.CompactionData)
	if !ok {
		t.Fatalf("expected compaction data, got %T", entries[0].Data)
	}
	if data.TokensBefore != 1234 {
		t.Fatalf("expected compaction tokensBefore 1234, got %d", data.TokensBefore)
	}
	if data.OriginalCount != 8 || data.NewCount != len(msgs) {
		t.Fatalf("unexpected compaction counts: original=%d new=%d messages=%d", data.OriginalCount, data.NewCount, len(msgs))
	}
	if data.PreservedUserMessages != 2 {
		t.Fatalf("expected two preserved user anchors, got %d", data.PreservedUserMessages)
	}
	if len(data.ReadFiles) != 1 || data.ReadFiles[0] != "old.go" {
		t.Fatalf("expected read file metadata, got %#v", data.ReadFiles)
	}
	if len(data.ModifiedFiles) != 1 || data.ModifiedFiles[0] != "new.go" {
		t.Fatalf("expected modified file metadata, got %#v", data.ModifiedFiles)
	}
}

func TestCompactNoopDoesNotRecordOrReset(t *testing.T) {
	host := &mockHost{}
	m, ag := newManager(t, host, summaryStreamFn())
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{PreserveRecent: 4})
	ag.AppendMessage(userMsg("one"))
	ag.AppendMessage(userMsg("two"))
	m.AddUsage(1234)

	changed, err := m.CompactIfNeeded(context.Background())
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if changed {
		t.Fatal("expected no-op compaction to report changed=false")
	}

	if host.startCalls != 0 || host.doneCalls != 0 {
		t.Fatalf("no-op compaction should not emit events, got start=%d done=%d", host.startCalls, host.doneCalls)
	}
	if got := m.Tokens(); got != 1234 {
		t.Fatalf("no-op compaction should not reset tokens, got %d", got)
	}
	if entries := m.deps.SessionManager.Load(); len(entries) != 0 {
		t.Fatalf("no-op compaction should not write session entries, got %#v", entries)
	}
	if msgs := ag.GetState().Messages; len(msgs) != 2 || messageText(msgs[0]) != "one" || messageText(msgs[1]) != "two" {
		t.Fatalf("no-op compaction should leave messages unchanged, got %#v", msgs)
	}
}

func TestCompactCanDisablePreservedUserAnchorsWithPolicy(t *testing.T) {
	host := &mockHost{}
	m, ag := newManager(t, host, summaryStreamFn())
	m.SetModel(testModel(10000))
	m.SetPolicy(Policy{PreserveRecent: 2, PreserveUserMessagesTokens: -1})
	for i := 0; i < 6; i++ {
		ag.AppendMessage(userMsg("msg"))
	}

	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}

	msgs := ag.GetState().Messages
	if len(msgs) != 3 {
		t.Fatalf("expected summary plus 2 preserved messages when user anchors are disabled, got %d", len(msgs))
	}
	if !strings.Contains(messageText(msgs[0]), "mock summary") {
		t.Fatalf("expected summary as first message, got %q", messageText(msgs[0]))
	}
}

func TestCompactPrunesTransientMessagesBeforeSummarizing(t *testing.T) {
	host := &mockHost{}
	m, ag := newManager(t, host, nil)
	m.SetPolicy(Policy{PreserveRecent: 1})
	ag.AppendMessage(userMsg("[transient] nested context should not be summarized"))
	ag.AppendMessage(userMsg("old real user request"))
	ag.AppendMessage(userMsg("tail real user request"))

	if err := m.Compact(context.Background()); err != nil {
		t.Fatalf("compact: %v", err)
	}

	msgs := ag.GetState().Messages
	if len(msgs) != 2 {
		t.Fatalf("expected summary plus preserved tail after pruning transient, got %d", len(msgs))
	}
	allText := strings.Join([]string{messageText(msgs[0]), messageText(msgs[1])}, "\n")
	if strings.Contains(allText, "nested context should not be summarized") {
		t.Fatalf("transient context leaked into compacted history:\n%s", allText)
	}
	if !strings.Contains(allText, "old real user request") || !strings.Contains(allText, "tail real user request") {
		t.Fatalf("expected real user messages to remain represented, got:\n%s", allText)
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
		Agent:          agent.NewAgent(types.Config{}),
		Resources:      resource.NewLoader(filepath.Join(dir, ".agent"), dir),
		SessionManager: sm,
		StreamFn:       func() types.StreamFn { return nil },
		APIKey:         func(string) (string, error) { return "", nil },
		Host:           host,
	})

	event := types.Event{
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
	m.OnToolExecutionEnd(types.Event{ToolName: "todo_write", Args: map[string]any{"path": "x"}})
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
		Agent:          agent.NewAgent(types.Config{}),
		Resources:      resource.NewLoader(filepath.Join(dir, ".agent"), dir),
		SessionManager: sm,
		StreamFn:       func() types.StreamFn { return nil },
		APIKey:         func(string) (string, error) { return "", nil },
		Host:           host,
	})
	// Already folded into the system prompt — should not be re-injected.
	m.MarkInitialContext([]string{agentsPath})

	m.OnToolExecutionEnd(types.Event{ToolName: "read", Args: map[string]any{"path": filepath.Join(dir, "main.go")}})
	if len(host.nested) != 0 {
		t.Fatalf("initial context should be suppressed, got %d injections", len(host.nested))
	}
}
