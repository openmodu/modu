package modes

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

func TestRunPrintOutputsQueuedFollowUpTurn(t *testing.T) {
	dir := t.TempDir()
	model := &types.Model{ID: "mock", ProviderID: "openai"}
	var calls atomic.Int64
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		n := calls.Add(1)
		return printTestAssistantStream(model, "turn "+string(rune('0'+n))), nil
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  dir + "/.coding_agent",
		Model:     model,
		GetAPIKey: func(provider string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatalf("NewCodingSession: %v", err)
	}
	defer session.Close("test_done")
	session.FollowUp("queued follow-up")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := RunPrint(ctx, PrintOptions{
		Mode:     PrintModeJSON,
		Messages: []string{"start"},
		Output:   &out,
		Session:  session,
	}); err != nil {
		t.Fatalf("RunPrint: %v\noutput:\n%s", err, out.String())
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("stream calls=%d, want 2; output:\n%s", got, out.String())
	}
}

func TestRunPrintDrainsFollowUpQueuedAfterAgentEnd(t *testing.T) {
	dir := t.TempDir()
	model := &types.Model{ID: "mock", ProviderID: "openai"}
	var calls atomic.Int64
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		n := calls.Add(1)
		return printTestAssistantStream(model, "turn "+string(rune('0'+n))), nil
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       dir,
		AgentDir:  dir + "/.coding_agent",
		Model:     model,
		GetAPIKey: func(provider string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatalf("NewCodingSession: %v", err)
	}
	defer session.Close("test_done")
	var queued atomic.Bool
	unsub := session.Subscribe(func(event types.Event) {
		if event.Type == types.EventTypeAgentEnd && queued.CompareAndSwap(false, true) {
			session.FollowUp("queued after agent_end")
		}
	})
	defer unsub()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := RunPrint(ctx, PrintOptions{
		Mode:     PrintModeJSON,
		Messages: []string{"start"},
		Output:   &out,
		Session:  session,
	}); err != nil {
		t.Fatalf("RunPrint: %v\noutput:\n%s", err, out.String())
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("stream calls=%d, want 2; output:\n%s", got, out.String())
	}
}

func printTestAssistantStream(model *types.Model, text string) types.EventStream {
	stream := types.NewEventStream()
	go func() {
		defer stream.Close()
		msg := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			ProviderID: model.ProviderID,
			Model:      model.ID,
			StopReason: "stop",
			Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
			Timestamp:  time.Now().UnixMilli(),
		}
		stream.Push(types.StreamEvent{Type: types.EventDone, Reason: "stop", Message: msg})
		stream.Resolve(msg, nil)
	}()
	return stream
}
