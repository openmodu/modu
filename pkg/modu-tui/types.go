// Package modutui provides a Bubble Tea v2 full-screen transcript viewport with
// a fixed bottom input line, mouse selection, OSC52 clipboard copy, collapsible
// tool blocks, and optional simulated streaming.
package modutui

const DefaultStatusHint = "拖拽选择→复制 · 点 ▸ 折叠 · Enter 发送 · 滚轮滚动 · ctrl+End 到底 · Ctrl+C 退出"

type Role int

const (
	RoleUser Role = iota
	RoleAssistant
)

type Message struct {
	Role           Role
	Text           string
	Thinking       bool
	Tool           bool
	ToolID         string
	ToolName       string
	Summary        string
	Detail         string
	ToolInput      string
	ToolOutput     string
	ToolCode       string
	ToolLanguage   string
	ToolError      bool
	ToolDone       bool
	ToolNoCollapse bool
	Expanded       bool
	Preformatted   bool
	Plain          bool
	Language       string
	Code           string
}

type ToolPermissionState string

const (
	ToolPermissionUnknown  ToolPermissionState = ""
	ToolPermissionPending  ToolPermissionState = "pending"
	ToolPermissionApproved ToolPermissionState = "approved"
	ToolPermissionDenied   ToolPermissionState = "denied"
)

type ToolCall struct {
	ID         string
	Name       string
	Summary    string
	Detail     string
	Input      string
	Output     string
	Code       string
	Language   string
	Error      bool
	Done       bool
	NoCollapse bool
}

type TodoItem struct {
	Content string
	Status  string
}

type ToolApprovalDecision string

const (
	ToolApprovalAllow       ToolApprovalDecision = "allow"
	ToolApprovalAllowAlways ToolApprovalDecision = "allow_always"
	ToolApprovalDeny        ToolApprovalDecision = "deny"
	ToolApprovalDenyAlways  ToolApprovalDecision = "deny_always"
)

type ToolApprovalRequest struct {
	ID       string
	ToolName string
	Summary  string
	Detail   string
}

type ToolApprovalResult struct {
	Request  ToolApprovalRequest
	Decision ToolApprovalDecision
}

type SubmitKind string

const (
	SubmitKindPrompt   SubmitKind = "prompt"
	SubmitKindFollowUp SubmitKind = "followup"
	SubmitKindSteer    SubmitKind = "steer"
)

type SubmitEvent struct {
	Text string
	Kind SubmitKind
}

type Hooks struct {
	ToolPermission       func(ToolCall) ToolPermissionState
	ToolApprovalDecision func(ToolApprovalResult)
	InputHistoryChanged  func([]string)
	SlashCommand         func(line string)
	Interrupt            func()
	Submit               func(text string)
	SubmitMessage        func(SubmitEvent)
}

type MessageBlockFactory func(Message) (Block, bool)

type SlashCommand struct {
	Name        string
	Description string
}

type Options struct {
	Width           int
	Height          int
	InitialMessages []Message
	InputHistory    []string
	Todos           []TodoItem
	StreamReply     string
	StatusHint      string
	Footer          string
	InfoCardLines   []string
	DisableMouse    bool
	ArrowKeysScroll bool
	Hooks           Hooks
	BlockFactories  []MessageBlockFactory
	BlockGap        int
	SlashCommands   []SlashCommand
}

type AppendMessageMsg struct {
	Message Message
}

type SetStatusMsg struct {
	Status string
}

type SetFooterMsg struct {
	Footer string
}

type SetBusyMsg struct {
	Busy bool
}

type SetTodosMsg struct {
	Todos []TodoItem
}

type ClearMessagesMsg struct{}

type RequestToolApprovalMsg struct {
	Request ToolApprovalRequest
	Respond chan<- ToolApprovalDecision
}

type CancelToolApprovalMsg struct {
	ID string
}
