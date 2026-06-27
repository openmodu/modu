package modutui

type ToolCallBlock struct {
	CollapsibleBlock
	Call       ToolCall
	Permission ToolPermissionState
}

func (b ToolCallBlock) Render(ctx RenderContext) BlockRender {
	permission := b.Permission
	if permission == ToolPermissionUnknown && ctx.Hooks.ToolPermission != nil {
		permission = ctx.Hooks.ToolPermission(b.Call)
	}
	block := b.CollapsibleBlock
	if permission != ToolPermissionUnknown {
		block.Summary += " · permission " + string(permission)
	}
	return block.Render(ctx)
}
