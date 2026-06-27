package modutui

import "strings"

type ApprovalBlock struct {
	Request  ToolApprovalRequest
	Expanded bool
}

func (b ApprovalBlock) Render(ctx RenderContext) BlockRender {
	req := b.Request
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		summary = "approval required: " + req.ToolName
	}
	return ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{
			Summary:  summary,
			Detail:   req.Detail,
			Expanded: b.Expanded,
		},
		Call: ToolCall{
			ID:      req.ID,
			Name:    req.ToolName,
			Summary: summary,
			Detail:  req.Detail,
		},
		Permission: ToolPermissionPending,
	}.Render(ctx)
}

func (b ApprovalBlock) ActionsLine() string {
	return "[y] allow  [a] always allow  [n] deny  [d] always deny  [esc] deny"
}
