package providers

import (
	"context"
)

// Provider 统一 LLM provider 接口
type Provider interface {
	ID() string
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	Stream(ctx context.Context, req *ChatRequest) (EventStream, error)
}
