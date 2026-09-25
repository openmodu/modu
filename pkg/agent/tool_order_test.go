package agent

import (
	"context"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

type orderedTool string

func (t orderedTool) Name() string        { return string(t) }
func (t orderedTool) Label() string       { return string(t) }
func (t orderedTool) Description() string { return string(t) + " description" }
func (t orderedTool) Parameters() any     { return map[string]any{"type": "object"} }
func (t orderedTool) Execute(context.Context, string, map[string]any, types.ToolUpdateCallback) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

func TestToolDefinitionsSortMCPToolsAfterOtherTools(t *testing.T) {
	definitions := toolDefinitions([]types.Tool{
		orderedTool("mcp__zeta__search"),
		orderedTool("write"),
		orderedTool("mcp__alpha__search"),
		orderedTool("read"),
	})

	want := []string{"read", "write", "mcp__alpha__search", "mcp__zeta__search"}
	for i, name := range want {
		if definitions[i].Name != name {
			t.Fatalf("tool %d = %q, want %q", i, definitions[i].Name, name)
		}
	}
}
