package coding_agent

import (
	"context"
	"encoding/json"

	"github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/types"
)

func (s *CodingSession) gitRuntimeState() map[string]any {
	tool := tools.NewGitPreflightTool(s.cwd)
	result, err := tool.Execute(context.Background(), "runtime-git", nil, nil)
	if err != nil || len(result.Content) == 0 {
		return map[string]any{"available": false}
	}
	text := ""
	if tc, ok := result.Content[0].(*types.TextContent); ok && tc != nil {
		text = tc.Text
	}
	if text == "" {
		return map[string]any{"available": false}
	}
	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) != nil {
		return map[string]any{"available": false}
	}
	payload["available"] = true
	return payload
}
