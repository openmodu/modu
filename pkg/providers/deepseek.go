package providers

import "os"

const deepSeekBaseURL = "https://api.deepseek.com/v1"

// NewDeepSeekProvider 创建 DeepSeek provider。
// 使用 OpenAI-compatible chat completions API，默认模型 deepseek-chat。
// apiKey 为空时读取 DEEPSEEK_API_KEY 环境变量。
func NewDeepSeekProvider(apiKey, defaultModel string) Provider {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	if defaultModel == "" {
		defaultModel = "deepseek-chat"
	}
	return NewOpenAIProvider("deepseek", OpenAIConfig{
		BaseURL:      deepSeekBaseURL,
		APIKey:       apiKey,
		DefaultModel: defaultModel,
	})
}
