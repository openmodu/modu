// Package modutui provides a Bubble Tea v2 full-screen transcript viewport with
// a fixed bottom input line, mouse selection, OSC52 clipboard copy, collapsible
// tool blocks, and optional simulated streaming.
package modutui

const DefaultStatusHint = "Ctrl+V/拖入图片 · 拖拽选择→复制 · Enter 发送 · 滚轮滚动 · ctrl+End 到底 · Ctrl+C 退出"

type Role int

const (
	RoleUser Role = iota
	RoleAssistant
)

type ToolPermissionState string

const (
	ToolPermissionUnknown  ToolPermissionState = ""
	ToolPermissionPending  ToolPermissionState = "pending"
	ToolPermissionApproved ToolPermissionState = "approved"
	ToolPermissionDenied   ToolPermissionState = "denied"
)

type ToolCall struct {
	ID           string
	Name         string
	Summary      string
	Detail       string
	Input        string
	Output       string
	ArtifactID   string
	ArtifactPath string
	ArtifactText string
	ArtifactErr  string
	ArtifactRead bool
	Truncated    bool
	BatchSize    int
	BatchID      string
	Code         string
	Language     string
	Error        bool
	Done         bool
	NoCollapse   bool
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

type HumanPromptOption struct {
	Label string
	Value string
}

type HumanPromptRequest struct {
	ID           string
	Title        string
	Body         string
	Options      []HumanPromptOption
	DefaultIndex int
}

type HumanTextRequest struct {
	ID          string
	Title       string
	Body        string
	Placeholder string
	Default     string
	Secret      bool
	Required    bool
}

type SubmitKind string

const (
	SubmitKindPrompt   SubmitKind = "prompt"
	SubmitKindFollowUp SubmitKind = "followup"
	SubmitKindSteer    SubmitKind = "steer"
)

type SubmitEvent struct {
	Text   string
	Images []ImageAttachment
	Kind   SubmitKind
}

// ImageAttachment is an image held by the input until the user submits it.
// Data contains the encoded file bytes (PNG, JPEG, GIF, or WebP), not base64.
type ImageAttachment struct {
	Name     string
	MimeType string
	Data     []byte
}

// Services contains host-owned queries and IO. Model.Update schedules every
// service call through tea.Cmd; rendering never calls a service.
type Services struct {
	ToolPermission      func(ToolCall) ToolPermissionState
	ReadClipboardImages func() ([]ImageAttachment, error)
	ResolvePastedImages func(content string) ([]ImageAttachment, bool, error)
	SlashCommands       func() []SlashCommand
	LoadToolArtifact    func(path string) (string, error)
}

type EntryBlockFactory func(Entry) (Block, bool)

type SlashCommand struct {
	Name        string
	Description string
}

type Panel struct {
	ID        string
	Title     string
	Subtitle  string
	Lines     []string
	Rows      []PanelRow
	Shortcuts []PanelShortcut
	Selected  int
	Footer    string
	Markdown  bool
	// Meta is opaque to modutui: it round-trips through panel updates
	// unchanged so a caller can stash its own stable panel identity (e.g. which
	// entity a panel is showing) instead of having to recover it later by
	// parsing rendered Rows.
	Meta any
}

type PanelShortcut struct {
	Key     string
	Label   string
	Command string
	Action  Action
}

type PanelRow struct {
	Label   string
	Detail  string
	Value   string
	Command string
	Action  Action
}

type PanelAction struct {
	PanelID string
	Index   int
	Row     PanelRow
	Command string
	Action  Action
}

type Options struct {
	Width           int
	Height          int
	InitialEntries  []Entry
	InputHistory    []string
	Todos           []TodoItem
	StreamReply     string
	StatusHint      string
	Footer          string
	InfoCardLines   []string
	DisableMouse    bool
	ArrowKeysScroll bool
	Services        Services
	IntentHandler   func(Intent)
	BlockFactories  []EntryBlockFactory
	BlockGap        int
	SlashCommands   []SlashCommand
}

type RequestToolApprovalMsg struct {
	Request ToolApprovalRequest
	Respond chan<- ToolApprovalDecision
}

type CancelToolApprovalMsg struct {
	ID string
}

type RequestHumanPromptMsg struct {
	Request HumanPromptRequest
	Respond chan<- string
}

type CancelHumanPromptMsg struct {
	ID string
}

type RequestHumanTextMsg struct {
	Request HumanTextRequest
	Respond chan<- string
}

type CancelHumanTextMsg struct {
	ID string
}
