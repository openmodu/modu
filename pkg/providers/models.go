package providers

import (
	"strings"

	"github.com/crosszan/modu/pkg/types"
)

// Models is the global model registry, keyed by provider name → model ID.
var Models = make(map[string]map[string]*types.Model)

func init() {
	openaiModels := make(map[string]*types.Model)
	openaiModels["gpt-4o"] = &types.Model{
		ID:            "gpt-4o",
		Name:          "GPT-4o",
		Api:           types.KnownApiOpenAIResponses,
		ProviderID:    types.KnownProviderOpenAI,
		BaseURL:       "https://api.openai.com/v1",
		Reasoning:     false,
		Input:         []string{"text", "image"},
		Cost:          types.ModelCost{Input: 5.0, Output: 15.0},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
	Models["openai"] = openaiModels

	anthropicModels := make(map[string]*types.Model)
	anthropicModels["claude-3-5-sonnet-20240620"] = &types.Model{
		ID:            "claude-3-5-sonnet-20240620",
		Name:          "Claude 3.5 Sonnet",
		Api:           types.KnownApiAnthropicMessages,
		ProviderID:    types.KnownProviderAnthropic,
		BaseURL:       "https://api.anthropic.com",
		Reasoning:     false,
		Input:         []string{"text", "image"},
		Cost:          types.ModelCost{Input: 3.0, Output: 15.0},
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
	Models["anthropic"] = anthropicModels

	deepseekModels := make(map[string]*types.Model)
	deepseekModels["deepseek-chat"] = &types.Model{
		ID:            "deepseek-chat",
		Name:          "DeepSeek Chat",
		Api:           types.KnownApiDeepSeekChat,
		ProviderID:    types.KnownProviderDeepSeek,
		BaseURL:       "https://api.deepseek.com/v1",
		Reasoning:     false,
		Input:         []string{"text"},
		Cost:          types.ModelCost{Input: 0, Output: 0},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
	Models["deepseek"] = deepseekModels
}

// GetModel returns a model by provider and ID. If provider is empty, searches all providers.
func GetModel(provider string, id string) *types.Model {
	if provider != "" {
		if p, ok := Models[provider]; ok {
			return p[id]
		}
		return nil
	}
	// Search all providers.
	for _, p := range Models {
		if m, ok := p[id]; ok {
			return m
		}
	}
	return nil
}

// GetAllProviders returns all registered provider names.
func GetAllProviders() []string {
	out := make([]string, 0, len(Models))
	for provider := range Models {
		out = append(out, provider)
	}
	return out
}

// GetModels returns all models registered under the given provider.
func GetModels(provider string) []*types.Model {
	if p, ok := Models[provider]; ok {
		out := make([]*types.Model, 0, len(p))
		for _, model := range p {
			out = append(out, model)
		}
		return out
	}
	return nil
}

// CalculateCost computes the cost fields on an AgentUsage based on the model's pricing.
func CalculateCost(model *types.Model, usage *types.AgentUsage) {
	usage.Cost.Input = (model.Cost.Input / 1000000) * float64(usage.Input)
	usage.Cost.Output = (model.Cost.Output / 1000000) * float64(usage.Output)
	usage.Cost.CacheRead = (model.Cost.CacheRead / 1000000) * float64(usage.CacheRead)
	usage.Cost.CacheWrite = (model.Cost.CacheWrite / 1000000) * float64(usage.CacheWrite)
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}

// SupportsXHigh returns true if the model supports xhigh thinking level.
func SupportsXHigh(model *types.Model) bool {
	if model == nil {
		return false
	}
	if strings.Contains(model.ID, "gpt-5.2") || strings.Contains(model.ID, "gpt-5.3") {
		return true
	}
	if model.Api == types.KnownApiAnthropicMessages {
		return strings.Contains(model.ID, "opus-4-6") || strings.Contains(model.ID, "opus-4.6")
	}
	return false
}

// ModelsAreEqual returns true if both models refer to the same model.
func ModelsAreEqual(a *types.Model, b *types.Model) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.ProviderID == b.ProviderID
}
