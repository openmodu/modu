package providers

import (
	"context"

	"github.com/openmodu/modu/pkg/types"
)

// Provider is the interface that all LLM providers must implement.
type Provider interface {
	// ID returns the unique identifier for this provider.
	ID() string
	// Stream sends a streaming chat request and returns an EventStream.
	Stream(ctx context.Context, req *ChatRequest) (types.EventStream, error)
	// Chat sends a non-streaming chat request.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}
