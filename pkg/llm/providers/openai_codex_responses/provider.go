package openai_codex_responses

import (
	"fmt"

	"github.com/crosszan/modu/pkg/llm"
)

type OpenAICodexResponsesProvider struct{}

func (p *OpenAICodexResponsesProvider) Api() llm.Api {
	return "openai-codex-responses"
}

func (p *OpenAICodexResponsesProvider) Stream(model *llm.Model, ctx *llm.Context, opts *llm.StreamOptions) (llm.AssistantMessageEventStream, error) {
	return nil, fmt.Errorf("openai codex responses provider not implemented")
}

func (p *OpenAICodexResponsesProvider) StreamSimple(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
	return nil, fmt.Errorf("openai codex responses provider not implemented")
}

func init() {
	llm.RegisterApiProvider(&OpenAICodexResponsesProvider{})
}
