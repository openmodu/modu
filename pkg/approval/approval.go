// Package approval defines the data type used to request user approval
// before a tool executes. It is a leaf package so both UI and channel
// integrations (e.g. Telegram bot) can depend on it without forming an
// import cycle.
package approval

// Request is delivered when a tool requires user approval before
// executing. The receiver replies on Response with one of: "allow",
// "allow_always", "deny", "deny_always".
//
// Cancel, if non-nil, is closed by the caller when the decision has
// already been made externally (e.g. via Telegram). The receiver should
// dismiss its prompt and return without sending on Response.
type Request struct {
	ToolName   string
	ToolCallID string
	Args       map[string]any
	Response   chan<- string
	Cancel     <-chan struct{}
}
