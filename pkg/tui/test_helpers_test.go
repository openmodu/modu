package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

func testUIModel() *types.Model {
	return &types.Model{
		ID:         "test-model",
		Name:       "Test Model",
		ProviderID: "test",
	}
}

func newUITestSession(t *testing.T) *coding_agent.CodingSession {
	t.Helper()

	return newUITestSessionWithStream(t, func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			msg := testAssistantMessageForLastUser(model, llmCtx)
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	})
}

func newUITestSessionWithStream(t *testing.T, streamFn types.StreamFn) *coding_agent.CodingSession {
	t.Helper()

	root := t.TempDir()
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:      root,
		AgentDir: filepath.Join(root, ".coding_agent"),
		Model:    testUIModel(),
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
		StreamFn: streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func testAssistantMessageForLastUser(model *types.Model, llmCtx *types.LLMContext) *types.AssistantMessage {
	last := llmCtx.Messages[len(llmCtx.Messages)-1]
	userText := ""
	if msg, ok := last.(types.UserMessage); ok {
		userText, _ = msg.Content.(string)
	}
	return &types.AssistantMessage{
		Role:       "assistant",
		ProviderID: model.ProviderID,
		Model:      model.ID,
		StopReason: "stop",
		Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "assistant: " + userText}},
		Timestamp:  time.Now().UnixMilli(),
	}
}
