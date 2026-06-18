package extension

import (
	"context"

	"github.com/openmodu/modu/pkg/types"
)

// ForkOptions configures a one-shot child agent spawned by ExtensionAPI.ForkSession.
// All fields are optional except Task; empty values fall back to whatever the
// host's main session is using.
type ForkOptions struct {
	// Name is the child agent/profile name used for lifecycle reporting.
	// Empty means the host may use a generic name.
	Name string
	// SystemPrompt is the child agent's system prompt. Empty means "use a
	// minimal default" — implementations should still produce something
	// usable, but extensions are expected to provide a real prompt.
	SystemPrompt string
	// Task is the user message the child agent runs against. Required.
	Task string
	// AllowedTools restricts the child's tool set to the named tools.
	// Empty means "inherit every tool the caller has active".
	AllowedTools []string
	// DisallowedTools removes tools after AllowedTools is applied.
	// Names that aren't present are silently ignored.
	DisallowedTools []string
	// Model overrides the model ID the child uses. Empty means "use the
	// caller's current model".
	Model string
	// Context controls whether the child starts fresh or with a copy of the
	// parent session messages. Known values: ""/"fresh" and "fork".
	Context string
	// Cwd requests a working directory for the child. Relative paths resolve
	// against the parent session cwd.
	Cwd string
	// ThinkingLevel maps to pkg/types.ThinkingLevel ("off"/"low"/...).
	// Empty means "inherit".
	ThinkingLevel string
	// PermissionMode is a host-defined permission policy: known values
	// today are "" (default) and "read-only". Unknown values are treated
	// as default.
	PermissionMode string
	// MaxTurns caps how many model turns the child may take. Zero means
	// "unlimited" (subject to the underlying agent's own limits).
	MaxTurns int
	// Background, when true, requests asynchronous execution. The host
	// schedules the child on a goroutine and returns a task-id reference
	// instead of the child's final reply. Hosts that don't support
	// background execution may either ignore this flag or return an
	// error — the extension must be prepared for both.
	Background bool
	// ParentTaskID links a background fork to the task that caused it, when
	// the host persists task metadata.
	ParentTaskID string
	// OutputPath saves the child result to a file after execution.
	OutputPath string
	// OutputMode controls the returned text when OutputPath is set. Known
	// value: "file-only"; empty means inline plus a saved-file reference.
	OutputMode string
	// Isolation requests an isolation strategy for the child. Known
	// values: "" (run in the caller's cwd) and "worktree" (host creates a
	// fresh git worktree, binds file/shell tools to it, and cleans up on
	// completion). Unknown values are treated as "".
	Isolation string
	// Skills lists skill identifiers to load into the child's system
	// prompt. The host resolves each name through its skill registry and
	// silently skips unknown entries.
	Skills []string
	// MemoryScope selects which memory bank to inject into the child's
	// system prompt: "" / "none" / "user" / "project" / "both".
	// Unknown values behave like "".
	MemoryScope string
	// SessionDir, when non-empty, asks the host to place this child's
	// per-run directory (containing session.jsonl + status.json) under a
	// caller-supplied parent path instead of the host's default
	// background-task run root. Relative paths resolve against the parent
	// session cwd. Only meaningful for background forks; ignored otherwise.
	SessionDir string
	// BubbleTaskID, when set, makes the host subscribe to this child's live
	// agent events and re-emit the control-relevant ones (turn_end,
	// tool_execution_end, agent_end) as "subagent_child_event" tagged with
	// this id — for both synchronous and background children. Batch dispatch
	// sets it to the batch id so all of a batch's children aggregate under a
	// single id for control accounting. Empty means: background children
	// bubble under their own host task id; synchronous children do not bubble.
	BubbleTaskID string
}

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

// TaskSnapshot is a lightweight view of a host-managed background task.
type TaskSnapshot struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Summary     string `json:"summary"`
	Agent       string `json:"agent,omitempty"`
	Task        string `json:"task,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
	RunDir      string `json:"runDir,omitempty"`
	StatusFile  string `json:"statusFile,omitempty"`
	SessionFile string `json:"sessionFile,omitempty"`
	OutputFile  string `json:"outputFile,omitempty"`
	Output      string `json:"output,omitempty"`
	Error       string `json:"error,omitempty"`
	CreatedAt   int64  `json:"createdAt,omitempty"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
}

// ExtensionAPI provides the API available to extensions.
type ExtensionAPI interface {
	// RegisterTool registers a new tool provided by the extension.
	RegisterTool(tool types.Tool)
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
	// PermissionMode returns the host session's permission mode, when configured.
	PermissionMode() string
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
	// BackgroundTasks returns a snapshot of host-managed background tasks.
	BackgroundTasks() []TaskSnapshot
	// InterruptBackgroundTask requests cancellation for a live host-managed
	// background task. It returns false when the task is unknown or no longer
	// live in this process.
	InterruptBackgroundTask(id, reason string) (TaskSnapshot, bool)
	// ForkSession spawns a one-shot child agent with a custom system
	// prompt and tool whitelist, returning the child's final assistant
	// text. The child runs synchronously in the caller's goroutine until
	// it stops or ctx is cancelled. Errors include child agent failures
	// and host-side dispatch problems (e.g. fork support not wired).
	ForkSession(ctx context.Context, opts ForkOptions) (string, error)
}

// CommandHandler handles a slash command invocation.
type CommandHandler func(args string) error

// EventHandler handles an agent event.
type EventHandler func(event types.Event)

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
	After func(toolName string, args map[string]any, result types.ToolResult)
	// Transform allows modifying the tool result before returning it.
	Transform func(toolName string, result types.ToolResult) types.ToolResult
}
