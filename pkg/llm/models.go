package llm

import "strings"

var (
	Models = make(map[Provider]map[string]*Model)
)

func init() {
	openaiModels := make(map[string]*Model)
	openaiModels["gpt-4o"] = &Model{
		ID:            "gpt-4o",
		Name:          "GPT-4o",
		Api:           "openai-responses",
		Provider:      "openai",
		BaseURL:       "https://api.openai.com/v1",
		Reasoning:     false,
		Input:         []string{"text", "image"},
		Cost:          ModelCost{Input: 5.0, Output: 15.0},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
	Models["openai"] = openaiModels

	anthropicModels := make(map[string]*Model)
	anthropicModels["claude-3-5-sonnet-20240620"] = &Model{
		ID:            "claude-3-5-sonnet-20240620",
		Name:          "Claude 3.5 Sonnet",
		Api:           "anthropic-messages",
		Provider:      "anthropic",
		BaseURL:       "https://api.anthropic.com",
		Reasoning:     false,
		Input:         []string{"text", "image"},
		Cost:          ModelCost{Input: 3.0, Output: 15.0},
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
	Models["anthropic"] = anthropicModels

	deepseekModels := make(map[string]*Model)
	deepseekModels["deepseek-chat"] = &Model{
		ID:            "deepseek-chat",
		Name:          "DeepSeek Chat",
		Api:           "deepseek-chat-completions",
		Provider:      "deepseek",
		BaseURL:       "https://api.deepseek.com/v1",
		Reasoning:     false,
		Input:         []string{"text"},
		Cost:          ModelCost{Input: 0, Output: 0},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
	Models["deepseek"] = deepseekModels
}

func GetModel(provider Provider, id string) *Model {
	if p, ok := Models[provider]; ok {
		return p[id]
	}
	return nil
}

func GetProviders() []Provider {
	out := make([]Provider, 0, len(Models))
	for provider := range Models {
		out = append(out, provider)
	}
	return out
}

func GetModels(provider Provider) []*Model {
	if p, ok := Models[provider]; ok {
		out := make([]*Model, 0, len(p))
		for _, model := range p {
			out = append(out, model)
		}
		return out
	}
	return nil
}

func CalculateCost(model *Model, usage *Usage) {
	usage.Cost.Input = (model.Cost.Input / 1000000) * float64(usage.Input)
	usage.Cost.Output = (model.Cost.Output / 1000000) * float64(usage.Output)
	usage.Cost.CacheRead = (model.Cost.CacheRead / 1000000) * float64(usage.CacheRead)
	usage.Cost.CacheWrite = (model.Cost.CacheWrite / 1000000) * float64(usage.CacheWrite)
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}

func SupportsXHigh(model *Model) bool {
	if model == nil {
		return false
	}
	if strings.Contains(model.ID, "gpt-5.2") || strings.Contains(model.ID, "gpt-5.3") {
		return true
	}
	if model.Api == "anthropic-messages" {
		return strings.Contains(model.ID, "opus-4-6") || strings.Contains(model.ID, "opus-4.6")
	}
	return false
}

func ModelsAreEqual(a *Model, b *Model) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Provider == b.Provider
}
