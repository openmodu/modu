// Package workflow provides a Lua-scripted workflow extension for orchestrating
// multiple forked coding-agent children through ExtensionAPI.ForkSession.
package workflow

import "github.com/openmodu/modu/pkg/coding_agent/plugins/extension"

// Extension registers the workflow tool.
type Extension struct {
	cfg Config
	api extension.ExtensionAPI
}

// Config controls workflow runtime defaults.
type Config struct {
	Concurrency int
}

// DefaultConfig returns conservative workflow defaults.
func DefaultConfig() Config {
	return Config{Concurrency: 4}
}

// New constructs a workflow extension with default configuration.
func New() *Extension {
	return &Extension{cfg: DefaultConfig()}
}

// Name implements extension.Extension.
func (e *Extension) Name() string { return "workflow" }

// Init implements extension.Extension.
func (e *Extension) Init(api extension.ExtensionAPI) error {
	e.api = api
	api.RegisterTool(newTool(e))
	return nil
}

func init() {
	extension.Register("workflow", func() extension.Extension { return New() })
}
