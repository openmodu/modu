package google_gemini_cli

import (
	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/llm/providers/google"
)

type GoogleGeminiCLIProvider struct{}

func (p *GoogleGeminiCLIProvider) Api() llm.Api {
	return "google-gemini-cli"
}

func (p *GoogleGeminiCLIProvider) Stream(model *llm.Model, ctx *llm.Context, opts *llm.StreamOptions) (llm.AssistantMessageEventStream, error) {
	var simple *llm.SimpleStreamOptions
	if opts != nil {
		s := llm.SimpleStreamOptions{StreamOptions: *opts}
		simple = &s
	}
	return p.StreamSimple(model, ctx, simple)
}

func (p *GoogleGeminiCLIProvider) StreamSimple(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
	return google.StreamGemini(model, ctx, opts)
}

func init() {
	llm.RegisterApiProvider(&GoogleGeminiCLIProvider{})
}
