package coding_agent

import (
	"github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/types"
)

func (s *engine) toolContext(cwd string) types.ToolContext {
	values := map[string]any{}
	if s != nil && s.artifactStore != nil {
		values[tools.ValueArtifacts] = s.artifactStore
	}
	return types.ToolContext{Cwd: cwd, Values: values}
}
