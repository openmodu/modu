package modutui

type MarkdownBlock struct {
	Marker string
	Text   string
}

func (b MarkdownBlock) Render(ctx RenderContext) BlockRender {
	body := b.Text
	if out, err := renderMarkdownWithBorderedTables(ctx.Markdown, b.Text, max(1, ctx.ContentWidth)); err == nil {
		body = out
	}
	return bodyLines(b.Marker, body, max(1, ctx.ContentWidth), func(s string) string { return s })
}
