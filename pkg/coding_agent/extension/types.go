package extension

import (
	"github.com/openmodu/modu/pkg/agent"
)

// Extension is the interface that all extensions must implement.
type Extension interface {
	// Name returns the unique name of the extension.
	Name() string
	// Init initializes the extension with the provided API.
	Init(api ExtensionAPI) error
}

// RuntimeStateProvider is optionally implemented by extensions that expose
// lightweight state for RuntimeState JSON and host UIs.
type RuntimeStateProvider interface {
	RuntimeState() any
}

// ConfigurableExtension is optionally implemented by extensions that accept
// per-extension configuration. The map is loaded verbatim from the `config:`
// block of one entry in extensions.yaml and applied **before** Init.
//
// Implementations are responsible for their own schema validation and should
// return a descriptive error for unrecognized keys when validation matters.
type ConfigurableExtension interface {
	ApplyConfig(cfg map[string]any) error
}

// MessageOptions mirrors the extension message metadata exposed by pi-style
// extensions for hidden follow-up prompts.
type MessageOptions struct {
	CustomType string
	Display    bool
	DeliverAs  string
}

// ExtensionAPI provides the API available to extensions.
type ExtensionAPI interface {
	// RegisterTool registers a new tool provided by the extension.
	RegisterTool(tool agent.AgentTool)
	// RegisterCommand registers a slash command handler.
	RegisterCommand(name, description string, handler CommandHandler)
	// AddHook registers a hook that wraps tool execution.
	AddHook(hook ToolHook)
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
	// SessionID returns the active persisted session id.
	SessionID() string
	// SessionDir returns the directory that contains the active session file.
	SessionDir() string
	// AgentDir returns the agent runtime/configuration directory.
	AgentDir() string
	// Cwd returns the active working directory.
	Cwd() string
	// IsIdle reports whether the host agent is not currently streaming.
	IsIdle() bool
	// HasPendingMessages reports whether queued steering/follow-up messages exist.
	HasPendingMessages() bool
	// SendFollowUpMessage queues a follow-up message and triggers a turn if idle.
	SendFollowUpMessage(text string) error
	// SendMessageWithOptions injects a message with extension metadata.
	SendMessageWithOptions(text string, options MessageOptions) error
	// Notify sends a user-visible extension notification to the host UI.
	Notify(extensionName, text string)
	// Confirm asks the host UI for a yes/no decision. When no interactive UI is
	// configured, implementations return defaultYes.
	Confirm(title, body string, defaultYes bool) bool
	// Select asks the host UI to choose one of the provided options.
	Select(title string, options []string) string
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
