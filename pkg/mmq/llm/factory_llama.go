//go:build llama
// +build llama

package llm

import "fmt"

// NewLLM 创建LLM实例
// llama标签版本返回LlamaCpp
func NewLLM(cfg ModelConfig) (LLM, error) {
	fmt.Println("Initializing LlamaCpp LLM")
	return NewLlamaCpp(cfg), nil
}
