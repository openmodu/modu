package providers

import (
	"context"
)

// Stream 流式接口
type Stream interface {
	Events() <-chan StreamEvent
	Result() (*ChatResponse, error)
	Close()
}

// Provider 统一 LLM provider 接口
type Provider interface {
	ID() string
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	Stream(ctx context.Context, req *ChatRequest) (Stream, error)
}
