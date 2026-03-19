package extension

import (
	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// Extension is the interface that all extensions must implement.
type Extension interface {
	// Name returns the unique name of the extension.
	Name() string
	// Init initializes the extension with the provided API.
	Init(api ExtensionAPI) error
}

// ExtensionAPI provides the API available to extensions.
type ExtensionAPI interface {
	// RegisterTool registers a new tool provided by the extension.
	RegisterTool(tool agent.AgentTool)
	// RegisterCommand registers a slash command handler.
	RegisterCommand(name string, handler CommandHandler)
	// On registers an event handler.
	On(event string, handler EventHandler)
	// SendMessage injects a message into the conversation.
	SendMessage(text string) error
	// SetActiveTools sets which tools are currently active.
	SetActiveTools(names []string)
	// SetModel changes the active model.
	SetModel(provider, modelID string) error
	// GetCommands returns all registered commands.
	GetCommands() []Command
}

// CommandHandler handles a slash command invocation.
type CommandHandler func(args string) error

// EventHandler handles an agent event.
type EventHandler func(event agent.AgentEvent)

// Command represents a registered slash command.
type Command struct {
	Name        string
	Description string
	Handler     CommandHandler
}

// ToolHook allows extensions to intercept tool calls.
type ToolHook struct {
	// Before is called before tool execution. Return false to cancel.
	Before func(toolName string, args map[string]any) bool
	// After is called after tool execution with the result.
	After func(toolName string, args map[string]any, result agent.AgentToolResult)
	// Transform allows modifying the tool result before returning it.
	Transform func(toolName string, result agent.AgentToolResult) agent.AgentToolResult
}

// ToolDefinition wraps tool metadata for extension registration.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// ToLlmToolDef converts to an LLM ToolDefinition.
func (d ToolDefinition) ToLlmToolDef() types.ToolDefinition {
	return types.ToolDefinition{
		Name:        d.Name,
		Description: d.Description,
		Parameters:  d.Parameters,
	}
}
