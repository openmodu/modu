package contextmgr

import "github.com/openmodu/modu/pkg/types"

// defaultContextWindow is the assumed context window when the model does not
// report one.
const defaultContextWindow = 128000

// WindowFor returns the model's context window in tokens, falling back to
// defaultContextWindow when the model is unknown or does not report one.
func WindowFor(model *types.Model) int {
	if model != nil && model.ContextWindow > 0 {
		return model.ContextWindow
	}
	return defaultContextWindow
}
