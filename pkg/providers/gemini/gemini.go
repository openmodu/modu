// Package gemini provides a Gemini provider using the official google.golang.org/genai SDK.
package gemini

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

const DefaultModel = "gemini-2.0-flash"

type geminiProvider struct {
	id     string
	client *genai.Client
	model  string
}

// New creates a Gemini provider. model defaults to DefaultModel when empty.
func New(ctx context.Context, apiKey, id, model string) (providers.Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini: GOOGLE_API_KEY is required")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: create client: %w", err)
	}
	if id == "" {
		id = "gemini"
	}
	if model == "" {
		model = DefaultModel
	}
	return &geminiProvider{id: id, client: client, model: model}, nil
}

func (p *geminiProvider) ID() string { return p.id }

func (p *geminiProvider) Chat(ctx context.Context, req *providers.ChatRequest) (*providers.ChatResponse, error) {
	contents := messagesToContents(req.Messages)
	model := req.Model
	if model == "" {
		model = p.model
	}
	resp, err := p.client.Models.GenerateContent(ctx, model, contents, nil)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	text := resp.Text()
	return &providers.ChatResponse{
		ID:    p.id,
		Model: model,
		Message: providers.Message{
			Role:    providers.RoleAssistant,
			Content: text,
		},
	}, nil
}

func (p *geminiProvider) Stream(ctx context.Context, req *providers.ChatRequest) (types.EventStream, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	contents := messagesToContents(req.Messages)
	stream := types.NewEventStream()
	go p.stream(ctx, model, contents, stream)
	return stream, nil
}

func (p *geminiProvider) stream(ctx context.Context, model string, contents []*genai.Content, stream *types.EventStreamImpl) {
	defer stream.Close()

	partial := &types.AssistantMessage{
		Role:       "assistant",
		Content:    []types.ContentBlock{},
		ProviderID: p.id,
		Model:      model,
	}
	stream.Push(types.StreamEvent{Type: types.EventStart, Partial: partial})

	var buf strings.Builder
	started := false

	for resp, err := range p.client.Models.GenerateContentStream(ctx, model, contents, nil) {
		if err != nil {
			partial.StopReason = "error"
			stream.Push(types.StreamEvent{Type: types.EventError, Error: err})
			stream.Resolve(partial, err)
			return
		}
		delta := resp.Text()
		if delta == "" {
			continue
		}
		if !started {
			started = true
			partial.Content = append(partial.Content, &types.TextContent{Type: "text", Text: ""})
			stream.Push(types.StreamEvent{Type: types.EventTextStart, ContentIndex: 0, Partial: partial})
		}
		buf.WriteString(delta)
		if tc, ok := partial.Content[0].(*types.TextContent); ok {
			tc.Text = buf.String()
		}
		stream.Push(types.StreamEvent{Type: types.EventTextDelta, ContentIndex: 0, Delta: delta, Partial: partial})
	}

	if started {
		stream.Push(types.StreamEvent{
			Type:         types.EventTextEnd,
			ContentIndex: 0,
			Content:      buf.String(),
			Partial:      partial,
		})
	}

	partial.StopReason = "end_turn"
	stream.Push(types.StreamEvent{Type: types.EventDone, Reason: partial.StopReason, Message: partial})
	stream.Resolve(partial, nil)
}

func messagesToContents(msgs []providers.Message) []*genai.Content {
	out := make([]*genai.Content, 0, len(msgs))
	for _, m := range msgs {
		role := genai.Role(genai.RoleUser)
		if m.Role == providers.RoleAssistant {
			role = genai.Role(genai.RoleModel)
		}
		var text string
		switch v := m.Content.(type) {
		case string:
			text = v
		case fmt.Stringer:
			text = v.String()
		default:
			text = fmt.Sprintf("%v", v)
		}
		out = append(out, genai.NewContentFromText(text, role))
	}
	return out
}
