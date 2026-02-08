//go:build !llama
// +build !llama

package llm

import "fmt"

// NewLLM 创建LLM实例
// 默认版本返回MockLLM
func NewLLM(cfg ModelConfig) (LLM, error) {
	fmt.Println("Initializing MockLLM (use -tags llama for real LLM)")
	// MockLLM 使用 dimensions=300 来匹配默认的 embeddinggemma
	return NewMockLLM(300), nil
}
