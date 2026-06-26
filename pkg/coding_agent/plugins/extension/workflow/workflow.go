// Package workflow provides a JavaScript-scripted workflow extension for orchestrating
// multiple forked coding-agent children through ExtensionAPI.ForkSession.
package workflow

import (
	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

// Extension registers the workflow tool.
type Extension struct {
	cfg        Config
	api        extension.ExtensionAPI
	registry   *workflowRegistry
	activities *workflowActivityRegistry
}

// Config controls workflow runtime defaults.
type Config struct {
	Concurrency int
	MaxAgents   int
	Disabled    bool
}

// DefaultConfig returns conservative workflow defaults.
func DefaultConfig() Config {
	return Config{Concurrency: 4, MaxAgents: 1000}
}

// New constructs a workflow extension with default configuration.
func New() *Extension {
	return &Extension{cfg: DefaultConfig(), registry: newWorkflowRegistry(), activities: newWorkflowActivityRegistry()}
}

// Name implements extension.Extension.
func (e *Extension) Name() string { return "workflow" }

// ApplyConfig implements extension.ConfigurableExtension. Receives the
// `config:` map for this extension from extensions.yaml before Init.
func (e *Extension) ApplyConfig(cfg map[string]any) error {
	return e.cfg.apply(cfg)
}

// Init implements extension.Extension.
func (e *Extension) Init(api extension.ExtensionAPI) error {
	e.api = api
	if e.registry == nil {
		e.registry = newWorkflowRegistry()
	}
	if e.activities == nil {
		e.activities = newWorkflowActivityRegistry()
	}
	if e.cfg.Disabled || workflowDisabledByEnv() {
		return nil
	}
	api.RegisterTool(newTool(e))
	api.RegisterCommand("workflows", "List, show, inspect, control, resume, restart, or save workflow runs: /workflows [list|show <run-id|latest>|agent <run-id|latest> <agent-id>|transcript <run-id|latest> <agent-id>|agent-stop <run-id|latest> <agent-id>|agent-restart <run-id|latest> <agent-id>|pause <run-id>|stop <run-id>|resume <run-id|latest>]", e.cmdWorkflows)
	api.RegisterCommand("deep-research", "Run the bundled deep research workflow: /deep-research <question>", e.cmdDeepResearch)
	api.On("subagent_child_event", e.onChildEvent)
	api.On("subagent_child_usage", e.onChildUsage)
	e.registerSavedWorkflowCommands(api)
	return nil
}

func (e *Extension) onChildEvent(ev types.Event) {
	if e == nil || e.activities == nil || e.registry == nil {
		return
	}
	agentID, ok := e.activities.agentID(ev.TaskID)
	if !ok {
		return
	}
	runID := workflowRunIDFromBubbleID(ev.TaskID)
	if runID == "" {
		return
	}
	activity := workflowActivityFromEvent(ev)
	e.activities.add(ev.TaskID, activity)
	e.registry.updateAgentActivity(runID, agentID, activity)
}

func (e *Extension) onChildUsage(ev types.Event) {
	if e == nil || e.activities == nil || e.registry == nil {
		return
	}
	agentID, ok := e.activities.agentID(ev.TaskID)
	if !ok {
		return
	}
	runID := workflowRunIDFromBubbleID(ev.TaskID)
	if runID == "" {
		return
	}
	activity := workflowActivityFromUsage(ev)
	e.activities.add(ev.TaskID, activity)
	e.registry.updateAgentActivity(runID, agentID, activity)
}

func init() {
	extension.Register("workflow", func() extension.Extension { return New() })
}
