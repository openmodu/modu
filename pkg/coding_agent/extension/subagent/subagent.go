// Package subagent is an extension that delegates work to named profiles via
// the host's ExtensionAPI.ForkSession. It is the planned replacement for the
// inline spawn_subagent wiring in pkg/coding_agent.
//
// Profile loading reuses pkg/coding_agent/subagent.Loader so this extension
// understands the same Markdown frontmatter (Tools / DisallowedTools / Model
// / ThinkingLevel / MaxTurns / PermissionMode etc.) without re-parsing
// anything.
//
// The extension always registers the "subagent" management/execution tool:
//
//   - AgentsDir empty → scan standard host paths via Loader.Discover
//   - AgentsDir set → scan only that explicit directory
//   - No profiles found → keep list/doctor usable, skip spawn_subagent alias
//
// This keeps diagnostics visible on installs that have not created a
// subagent profile yet.
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
	// staleTaskIDs records subagent background tasks that the host loaded
	// from a previous session's status.json. The owning goroutine died with
	// that session, so the task cannot actually be "running" in the host's
	// new lifetime. Populated at Init by reconcileStaleTasks.
	staleTaskIDs map[string]bool
	// batchTasks tracks top-level parallel/chain runs that this extension is
	// driving as a single background batch. The host's task list only sees
	// per-child ForkSession calls, so the batch layer needs its own pool.
	batchTasks *batchTaskRegistry
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

// Init implements extension.Extension. It always registers the subagent tool
// for management actions and only exposes the legacy spawn_subagent alias when
// at least one profile is discovered.
func (e *Extension) Init(api extension.ExtensionAPI) error {
	e.api = api
	e.loader = csubagent.NewLoader()
	e.discover()
	api.RegisterTool(newSubagentTool(e))
	if e.loader.Count() > 0 {
		api.RegisterTool(newLegacySpawnSubagentTool(e))
	}
	e.staleTaskIDs = reconcileStaleTasks(api)
	e.batchTasks = newBatchTaskRegistry()
	api.RegisterCommand("run", "Run one subagent: /run <agent> [task]", e.cmdRun)
	api.RegisterCommand("parallel", "Run subagents in parallel: /parallel <agent> <task> -> <agent> <task>", e.cmdParallel)
	api.RegisterCommand("chain", "Run subagents in sequence: /chain <agent> <task> -> <agent> <task>", e.cmdChain)
	api.RegisterCommand("subagents-doctor", "Show read-only subagent setup diagnostics", e.cmdDoctor)
	return nil
}

// discover (re-)populates the loader using the same rules as Init: the
// configured AgentsDir when set, otherwise the host's standard discovery
// paths. Safe to call after a CRUD mutation rewrote profile files.
func (e *Extension) discover() {
	if e.loader == nil {
		e.loader = csubagent.NewLoader()
	}
	e.loader.Reset()
	if e.cfg.AgentsDir == "" {
		if e.api != nil {
			e.loader.Discover(e.api.AgentDir(), e.api.Cwd())
		}
	} else {
		e.loader.DiscoverExtra(e.cfg.AgentsDir)
	}
}

// RuntimeState exposes discovered subagent profiles to host UIs.
func (e *Extension) RuntimeState() any {
	type profile struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Source      string `json:"source,omitempty"`
		FilePath    string `json:"filePath,omitempty"`
		Background  bool   `json:"background,omitempty"`
		Isolation   string `json:"isolation,omitempty"`
	}
	state := struct {
		Agents []profile `json:"agents"`
		Count  int       `json:"count"`
	}{
		Agents: []profile{},
	}
	if e.loader == nil {
		return state
	}
	for _, def := range e.loader.List() {
		state.Agents = append(state.Agents, profile{
			Name:        def.Name,
			Description: def.Description,
			Source:      def.Source,
			FilePath:    def.FilePath,
			Background:  def.Background,
			Isolation:   def.Isolation,
		})
	}
	state.Count = len(state.Agents)
	return state
}

// init registers the subagent extension as a builtin so anonymous-importing
// this package is enough to wire it up.
func init() {
	extension.Register("subagent", func() extension.Extension { return New() })
}
