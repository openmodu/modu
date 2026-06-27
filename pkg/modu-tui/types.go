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
	Role     Role
	Text     string
	Tool     bool
	ToolID   string
	ToolName string
	Summary  string
	Detail   string
	Expanded bool
	Language string
	Code     string
}

type ToolPermissionState string

const (
	ToolPermissionUnknown  ToolPermissionState = ""
	ToolPermissionPending  ToolPermissionState = "pending"
	ToolPermissionApproved ToolPermissionState = "approved"
	ToolPermissionDenied   ToolPermissionState = "denied"
)

type ToolCall struct {
	ID      string
	Name    string
	Summary string
	Detail  string
}

type Hooks struct {
	ToolPermission func(ToolCall) ToolPermissionState
}

type MessageBlockFactory func(Message) (Block, bool)

type Options struct {
	Width           int
	Height          int
	InitialMessages []Message
	StreamReply     string
	StatusHint      string
	Hooks           Hooks
	BlockFactories  []MessageBlockFactory
	BlockGap        int
}
