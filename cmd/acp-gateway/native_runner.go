package main

import (
	"context"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

// nativeRunner adapts any providers.Provider to the Runner interface.
// It is used for Tier-1 SDK-based agents (Gemini, etc.) that run in-process
// rather than as ACP subprocesses. Permissions are not supported — native
// agents must handle tool decisions themselves.
type nativeRunner struct {
	id       string
	provider providers.Provider
	model    string
}

func newNativeRunner(id string, p providers.Provider, model string) *nativeRunner {
	return &nativeRunner{id: id, provider: p, model: model}
}

func (r *nativeRunner) AgentID() string { return r.id }

func (r *nativeRunner) Run(ctx context.Context, prompt, _ string, hooks RunnerHooks) (*types.AssistantMessage, error) {
	actualPrompt := prompt
	if hooks.SystemPrompt != "" {
		actualPrompt = "[System instructions]\n" + hooks.SystemPrompt + "\n\n[User]\n" + prompt
	}
	req := &providers.ChatRequest{
		Model: r.model,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: actualPrompt},
		},
	}
	es, err := r.provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	for ev := range es.Events() {
		if hooks.OnEvent != nil {
			hooks.OnEvent(ev)
		}
	}
	return es.Result()
}
