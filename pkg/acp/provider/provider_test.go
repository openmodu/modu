package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

func collectText(t *testing.T, es types.EventStream) (string, *types.AssistantMessage, error) {
	t.Helper()
	var sb strings.Builder
	for ev := range es.Events() {
		if ev.Type == types.EventTextDelta {
			sb.WriteString(ev.Delta)
		}
	}
	msg, err := es.Result()
	return sb.String(), msg, err
}

func makeReq(prompt string) *providers.ChatRequest {
	return &providers.ChatRequest{
		Model: "acp-test",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: prompt},
		},
	}
}

func TestStream_HelloWorld(t *testing.T) {
	a := newMockAgent()
	a.promptScript = []sessionUpdate{
		{text: "Hello"},
		{text: ", "},
		{text: "world"},
		{stopReason: "end_turn"},
	}
	a.run()

	p := buildProvider(a, "/tmp/repo")
	es, err := p.Stream(context.Background(), makeReq("hi"))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	text, msg, err := collectText(t, es)
	if err != nil {
		t.Fatalf("result err: %v", err)
	}
	if text != "Hello, world" {
		t.Errorf("text = %q", text)
	}
	if msg.StopReason != "end_turn" {
		t.Errorf("stopReason = %q", msg.StopReason)
	}

	// Agent should have seen initialize → session/new → session/prompt.
	calls := a.methodCalls()
	want := []string{"initialize", "session/new", "session/prompt"}
	if len(calls) < len(want) {
		t.Fatalf("got calls %v, want prefix %v", calls, want)
	}
	for i, m := range want {
		if calls[i] != m {
			t.Errorf("call[%d] = %q, want %q", i, calls[i], m)
		}
	}
}

func TestStream_ReusesSession(t *testing.T) {
	a := newMockAgent()
	a.promptScript = []sessionUpdate{{text: "hi"}, {stopReason: "end_turn"}}
	a.run()

	p := buildProvider(a, "/tmp/repo")

	for i := 0; i < 3; i++ {
		es, err := p.Stream(context.Background(), makeReq("p"))
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		_, _, _ = collectText(t, es)
	}

	if got := a.countCalls("initialize"); got != 1 {
		t.Errorf("initialize called %d times, want 1", got)
	}
	if got := a.countCalls("session/new"); got != 1 {
		t.Errorf("session/new called %d times, want 1", got)
	}
	if got := a.countCalls("session/prompt"); got != 3 {
		t.Errorf("session/prompt called %d times, want 3", got)
	}
}

func TestStream_InitializeError(t *testing.T) {
	a := newMockAgent()
	a.initializeError = "handshake rejected"
	a.run()

	p := buildProvider(a, "/tmp/repo")
	_, err := p.Stream(context.Background(), makeReq("hi"))
	if err == nil {
		t.Fatal("expected error when initialize fails")
	}
	if !strings.Contains(err.Error(), "handshake rejected") {
		t.Errorf("err = %v; want contains handshake rejected", err)
	}
	// Provider must not have created a session.
	if got := a.countCalls("session/new"); got != 0 {
		t.Errorf("session/new called %d times, want 0", got)
	}
}

func TestStream_CtxCancel(t *testing.T) {
	a := newMockAgent()
	// Hold the prompt so it never returns; only cancellation ends the turn.
	a.holdPrompt = true
	a.run()

	p := buildProvider(a, "/tmp/repo")
	ctx, cancel := context.WithCancel(context.Background())
	es, err := p.Stream(ctx, makeReq("hi"))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	// Drive the prompt to reach the agent, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		for range es.Events() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream never drained after cancel")
	}

	_, resErr := es.Result()
	if !errors.Is(resErr, context.Canceled) {
		t.Errorf("Result err = %v, want context.Canceled", resErr)
	}
	// The cancel notification is written synchronously but processed by
	// the mock's read goroutine — poll briefly.
	deadline := time.Now().Add(time.Second)
	for !a.cancelled.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.cancelled.Load() {
		t.Error("agent never received session/cancel")
	}
}

func TestChat_NonStreaming(t *testing.T) {
	a := newMockAgent()
	a.promptScript = []sessionUpdate{
		{text: "42"},
		{stopReason: "end_turn"},
	}
	a.run()

	p := buildProvider(a, "/tmp")
	resp, err := p.Chat(context.Background(), makeReq("meaning of life?"))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	s, _ := resp.Message.Content.(string)
	if s != "42" {
		t.Errorf("content = %q", s)
	}
	if resp.FinishReason != "end_turn" {
		t.Errorf("finish = %q", resp.FinishReason)
	}
}

func TestStream_EmptyPrompt_Errors(t *testing.T) {
	a := newMockAgent()
	a.run()
	p := buildProvider(a, "/tmp")

	req := &providers.ChatRequest{
		Model:    "acp-test",
		Messages: []providers.Message{{Role: providers.RoleSystem, Content: "sys"}},
	}
	if _, err := p.Stream(context.Background(), req); err == nil {
		t.Error("expected error for prompt with no user message")
	}
}

func TestStream_MultimodalUserContent(t *testing.T) {
	a := newMockAgent()
	a.promptScript = []sessionUpdate{{text: "ok"}, {stopReason: "end_turn"}}
	a.run()
	p := buildProvider(a, "/tmp")

	req := &providers.ChatRequest{
		Model: "acp-test",
		Messages: []providers.Message{{
			Role: providers.RoleUser,
			Content: []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{"type": "text", "text": "world"},
			},
		}},
	}
	es, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	text, _, err := collectText(t, es)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if text != "ok" {
		t.Errorf("text = %q", text)
	}

	// session/prompt params should carry the concatenated text.
	for _, msg := range a.history {
		if msg.Method == "session/prompt" {
			// Serialize to inspect — using raw map shape.
			var params struct {
				Prompt []map[string]string `json:"prompt"`
			}
			if err := msg.ParseParams(&params); err == nil && len(params.Prompt) > 0 {
				if !strings.Contains(params.Prompt[0]["text"], "hello") ||
					!strings.Contains(params.Prompt[0]["text"], "world") {
					t.Errorf("prompt text missing parts: %q", params.Prompt[0]["text"])
				}
			}
		}
	}
}

func TestProvider_IDAndRegister(t *testing.T) {
	a := newMockAgent()
	a.run()
	p := buildProvider(a, "/tmp")
	if p.ID() != "acp:test" {
		t.Errorf("ID = %q", p.ID())
	}

	// Sanity: the registry accepts it via the interface.
	providers.Register(p)
	got, ok := providers.Get("acp:test")
	if !ok {
		t.Fatal("provider not in registry")
	}
	if got.ID() != "acp:test" {
		t.Errorf("got.ID = %q", got.ID())
	}
}
