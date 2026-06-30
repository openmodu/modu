package coding_agent

import "github.com/openmodu/modu/pkg/coding_agent/foundation/config"

func memoryFeatureEnabled(cfg *config.Config) bool {
	return cfg == nil || cfg.FeatureMemoryTool()
}
