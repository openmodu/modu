package types

import "context"

type ToolInput struct {
	Tools               []Tool
	Calls               []ToolCallContent
	Events              EventSink
	ApproveTool         func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error)
	GetSteeringMessages func() ([]AgentMessage, error)
	EnableInterrupts    bool
}

type ToolOutput struct {
	Messages []AgentMessage
	Results  []ToolResultMessage
	Steering []AgentMessage
}

type ToolResult struct {
	Content []ContentBlock `json:"content"`
	Details any            `json:"details"`
	IsError bool           `json:"isError,omitempty"`
}

type ToolUpdateCallback func(partial ToolResult)

type Tool interface {
	Name() string
	Label() string
	Description() string
	Parameters() any
	Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate ToolUpdateCallback) (ToolResult, error)
}

type ToolContext struct {
	Cwd        string
	BaseTools  []Tool
	ExtraTools []Tool
	Features   map[string]bool
	Values     map[string]any
}

func (c ToolContext) FeatureEnabled(name string) bool {
	if c.Features == nil {
		return false
	}
	return c.Features[name]
}

func (c ToolContext) Value(name string) any {
	if c.Values == nil {
		return nil
	}
	return c.Values[name]
}

type ToolProvider interface {
	Tools(ctx ToolContext) []Tool
}

type ToolRebinder interface {
	Rebind(tool Tool, ctx ToolContext) (Tool, bool)
}

type ToolManager interface {
	ToolProvider
	ToolRebinder
}

type ParallelTool interface {
	Parallel() bool
}

type ToolApprovalDecision string

const (
	ToolApprovalAllow       ToolApprovalDecision = "allow"
	ToolApprovalAllowAlways ToolApprovalDecision = "allow_always"
	ToolApprovalDeny        ToolApprovalDecision = "deny"
	ToolApprovalDenyAlways  ToolApprovalDecision = "deny_always"
)

func (d ToolApprovalDecision) IsAllow() bool {
	return d == ToolApprovalAllow || d == ToolApprovalAllowAlways
}
