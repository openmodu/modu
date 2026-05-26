// Package subagent is an extension that delegates work to named profiles via
// the host's ExtensionAPI.ForkSession. It is the planned replacement for the
// inline spawn_subagent wiring in pkg/coding_agent, but during the rollout
// double-track both paths coexist — the model can pick either.
//
// Profile loading reuses pkg/coding_agent/subagent.Loader so this extension
// understands the same Markdown frontmatter (Tools / DisallowedTools / Model
// / ThinkingLevel / MaxTurns / PermissionMode etc.) without re-parsing
// anything.
//
// The extension is inert until at least one profile is discovered:
//
//   - AgentsDir empty → no scan, no tool registration
//   - Directory missing → silent skip (Loader.DiscoverExtra absorbs the error)
//   - Directory present but empty → no tool registration
//
// This deliberately avoids "claiming" delegation away from the existing
// spawn_subagent path on installs that never opted in.
package subagent

import (
	"github.com/openmodu/modu/pkg/coding_agent/extension"
	csubagent "github.com/openmodu/modu/pkg/coding_agent/subagent"
)

// Extension implements extension.Extension and extension.ConfigurableExtension.
type Extension struct {
	cfg    Config
	loader *csubagent.Loader
	api    extension.ExtensionAPI
}

// New constructs a subagent extension with default configuration.
func New() *Extension {
	return &Extension{cfg: DefaultConfig()}
}

// Name implements extension.Extension.
func (e *Extension) Name() string { return "subagent" }

// ApplyConfig implements extension.ConfigurableExtension. Receives the
// `config:` map for this extension from extensions.yaml before Init.
func (e *Extension) ApplyConfig(cfg map[string]any) error {
	return e.cfg.apply(cfg)
}

// Init implements extension.Extension. Discovers agent profiles from
// AgentsDir and registers the subagent tool when at least one profile is
// found. Returning nil on "no profiles" is intentional — the extension is
// useless without profiles but its presence is not an error.
func (e *Extension) Init(api extension.ExtensionAPI) error {
	e.api = api
	e.loader = csubagent.NewLoader()
	if e.cfg.AgentsDir != "" {
		e.loader.DiscoverExtra(e.cfg.AgentsDir)
	}
	if e.loader.Count() == 0 {
		return nil
	}
	api.RegisterTool(newSubagentTool(e))
	return nil
}

// init registers the subagent extension as a builtin so anonymous-importing
// this package is enough to wire it up.
func init() {
	extension.Register("subagent", func() extension.Extension { return New() })
}
