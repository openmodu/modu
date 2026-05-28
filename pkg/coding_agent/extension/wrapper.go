package extension

import (
	"context"

	"github.com/openmodu/modu/pkg/agent"
)

// WrappedTool wraps an Tool with extension hooks.
type WrappedTool struct {
	inner agent.Tool
	hooks []ToolHook
}

// WrapTool creates a new wrapped tool with the given hooks.
func WrapTool(tool agent.Tool, hooks []ToolHook) agent.Tool {
	if len(hooks) == 0 {
		return tool
	}
	return &WrappedTool{inner: tool, hooks: hooks}
}

func (w *WrappedTool) Name() string        { return w.inner.Name() }
func (w *WrappedTool) Label() string       { return w.inner.Label() }
func (w *WrappedTool) Description() string { return w.inner.Description() }
func (w *WrappedTool) Parameters() any     { return w.inner.Parameters() }
func (w *WrappedTool) WithCwd(cwd string) agent.Tool {
	if rebindable, ok := w.inner.(interface{ WithCwd(string) agent.Tool }); ok {
		return &WrappedTool{inner: rebindable.WithCwd(cwd), hooks: w.hooks}
	}
	return w
}

func (w *WrappedTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.ToolUpdateCallback) (agent.ToolResult, error) {
	// Run before hooks
	for _, hook := range w.hooks {
		if hook.Before != nil {
			if !hook.Before(w.inner.Name(), args) {
				return agent.ToolResult{}, nil // Cancelled by hook
			}
		}
	}

	// Execute the tool
	result, err := w.inner.Execute(ctx, toolCallID, args, onUpdate)
	if err != nil {
		return result, err
	}

	// Run after hooks
	for _, hook := range w.hooks {
		if hook.After != nil {
			hook.After(w.inner.Name(), args, result)
		}
	}

	// Run transform hooks
	for _, hook := range w.hooks {
		if hook.Transform != nil {
			result = hook.Transform(w.inner.Name(), result)
		}
	}

	return result, nil
}

// WrapTools wraps multiple tools with the given hooks.
func WrapTools(tools []agent.Tool, hooks []ToolHook) []agent.Tool {
	if len(hooks) == 0 {
		return tools
	}
	wrapped := make([]agent.Tool, len(tools))
	for i, tool := range tools {
		wrapped[i] = WrapTool(tool, hooks)
	}
	return wrapped
}
