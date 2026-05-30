package coding_agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// The tool gate wraps every active tool with the two host-side checks that
// run before execution: plan-mode mutation blocking and the settings-driven
// blockTools deny list. It is the kernel<->tools boundary.

func (s *engine) installHarnessLayer() {
	s.activeTools = wrapHarnessTools(s.activeTools, s)
	s.agent.SetTools(s.activeTools)
}

func wrapHarnessTools(list []agent.Tool, session *engine) []agent.Tool {
	out := make([]agent.Tool, len(list))
	for i, tool := range list {
		if _, ok := tool.(*HarnessWrappedTool); ok {
			out[i] = tool
			continue
		}
		out[i] = &HarnessWrappedTool{inner: tool, session: session}
	}
	return out
}

// HarnessWrappedTool wraps every active tool with the two host-side gates that
// must run before any tool executes: plan-mode mutation blocking and the
// settings-driven blockTools deny list.
type HarnessWrappedTool struct {
	inner   agent.Tool
	session *engine
}

func (w *HarnessWrappedTool) Name() string        { return w.inner.Name() }
func (w *HarnessWrappedTool) Label() string       { return w.inner.Label() }
func (w *HarnessWrappedTool) Description() string { return w.inner.Description() }
func (w *HarnessWrappedTool) Parameters() any     { return w.inner.Parameters() }
func (w *HarnessWrappedTool) WithCwd(cwd string) agent.Tool {
	if rebindable, ok := w.inner.(interface{ WithCwd(string) agent.Tool }); ok {
		return &HarnessWrappedTool{inner: rebindable.WithCwd(cwd), session: w.session}
	}
	return w
}

func (w *HarnessWrappedTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.ToolUpdateCallback) (agent.ToolResult, error) {
	name := w.inner.Name()
	if w.session != nil && w.session.planModeBlocksTool(name) {
		return agent.ToolResult{
			Content: []types.ContentBlock{&types.TextContent{
				Type: "text",
				Text: planModeBlockMessage(name),
			}},
			Details: map[string]any{"isError": true, "blockedBy": "plan_mode"},
		}, nil
	}
	if w.session != nil && w.session.harnessToolBlocked(name) {
		return agent.ToolResult{
			Content: []types.ContentBlock{&types.TextContent{
				Type: "text",
				Text: fmt.Sprintf("harness blocked %s: blocked by settings.json", name),
			}},
			Details: map[string]any{"isError": true},
		}, nil
	}
	return w.inner.Execute(ctx, toolCallID, args, onUpdate)
}

func (w *HarnessWrappedTool) Parallel() bool {
	if p, ok := w.inner.(agent.ParallelTool); ok {
		return p.Parallel()
	}
	return false
}

func (s *engine) harnessToolBlocked(name string) bool {
	if s == nil || s.config == nil {
		return false
	}
	for _, blocked := range s.config.Harness.BlockTools {
		if strings.TrimSpace(blocked) == name {
			return true
		}
	}
	return false
}

func (s *engine) planModeBlocksTool(toolName string) bool {
	if s == nil || !s.IsPlanMode() {
		return false
	}
	switch toolName {
	case "write", "edit", "bash":
		return true
	default:
		return false
	}
}

func planModeBlockMessage(toolName string) string {
	return fmt.Sprintf("%s is blocked while plan mode is active; exit plan mode before making changes", toolName)
}
