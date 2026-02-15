package extension

import (
	"fmt"
	"sync"

	"github.com/crosszan/modu/pkg/agent"
)

// Runner manages the lifecycle of extensions and provides the ExtensionAPI.
type Runner struct {
	extensions []Extension
	tools      []agent.AgentTool
	commands   []Command
	hooks      []ToolHook
	handlers   map[string][]EventHandler
	sendMsg    func(text string) error
	setTools   func(names []string)
	setModel   func(provider, modelID string) error
	mu         sync.RWMutex
}

// NewRunner creates a new extension runner.
func NewRunner() *Runner {
	return &Runner{
		handlers: make(map[string][]EventHandler),
	}
}

// SetCallbacks configures the callbacks for the extension API.
func (r *Runner) SetCallbacks(
	sendMsg func(text string) error,
	setTools func(names []string),
	setModel func(provider, modelID string) error,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sendMsg = sendMsg
	r.setTools = setTools
	r.setModel = setModel
}

// Init initializes all extensions.
func (r *Runner) Init(extensions []Extension) error {
	r.mu.Lock()
	r.extensions = extensions
	r.mu.Unlock()

	for _, ext := range extensions {
		if err := ext.Init(r); err != nil {
			return fmt.Errorf("failed to init extension %s: %w", ext.Name(), err)
		}
	}
	return nil
}

// RegisterTool implements ExtensionAPI.
func (r *Runner) RegisterTool(tool agent.AgentTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools = append(r.tools, tool)
}

// RegisterCommand implements ExtensionAPI.
func (r *Runner) RegisterCommand(name string, handler CommandHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, Command{
		Name:    name,
		Handler: handler,
	})
}

// On implements ExtensionAPI.
func (r *Runner) On(event string, handler EventHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[event] = append(r.handlers[event], handler)
}

// SendMessage implements ExtensionAPI.
func (r *Runner) SendMessage(text string) error {
	r.mu.RLock()
	fn := r.sendMsg
	r.mu.RUnlock()
	if fn == nil {
		return fmt.Errorf("sendMessage not configured")
	}
	return fn(text)
}

// SetActiveTools implements ExtensionAPI.
func (r *Runner) SetActiveTools(names []string) {
	r.mu.RLock()
	fn := r.setTools
	r.mu.RUnlock()
	if fn != nil {
		fn(names)
	}
}

// SetModel implements ExtensionAPI.
func (r *Runner) SetModel(provider, modelID string) error {
	r.mu.RLock()
	fn := r.setModel
	r.mu.RUnlock()
	if fn == nil {
		return fmt.Errorf("setModel not configured")
	}
	return fn(provider, modelID)
}

// GetCommands implements ExtensionAPI.
func (r *Runner) GetCommands() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Command, len(r.commands))
	copy(result, r.commands)
	return result
}

// GetTools returns all tools registered by extensions.
func (r *Runner) GetTools() []agent.AgentTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]agent.AgentTool, len(r.tools))
	copy(result, r.tools)
	return result
}

// EmitEvent dispatches an event to all registered handlers.
func (r *Runner) EmitEvent(event agent.AgentEvent) {
	r.mu.RLock()
	handlers := r.handlers[string(event.Type)]
	r.mu.RUnlock()

	for _, h := range handlers {
		h(event)
	}
}

// AddHook adds a tool execution hook.
func (r *Runner) AddHook(hook ToolHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook)
}

// GetHooks returns all registered tool hooks.
func (r *Runner) GetHooks() []ToolHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]ToolHook, len(r.hooks))
	copy(result, r.hooks)
	return result
}

// ExecuteCommand finds and executes a slash command.
func (r *Runner) ExecuteCommand(name, args string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, cmd := range r.commands {
		if cmd.Name == name {
			return cmd.Handler(args)
		}
	}
	return fmt.Errorf("command not found: %s", name)
}

// Destroy cleans up all extensions.
func (r *Runner) Destroy() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extensions = nil
	r.tools = nil
	r.commands = nil
	r.hooks = nil
	r.handlers = make(map[string][]EventHandler)
}
