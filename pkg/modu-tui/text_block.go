package modutui

type TextBlock struct {
	Marker string
	Text   string
}

func (b TextBlock) Render(ctx RenderContext) BlockRender {
	return bodyLines(b.Marker, b.Text, max(1, ctx.ContentWidth), func(s string) string { return s })
}
