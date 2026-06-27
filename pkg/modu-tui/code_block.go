package modutui

import "strings"

type CodeBlock struct {
	Marker   string
	Language string
	Code     string
}

func (b CodeBlock) Render(ctx RenderContext) BlockRender {
	lang := strings.TrimSpace(b.Language)
	fence := "```" + lang + "\n" + strings.TrimRight(b.Code, "\n") + "\n```"
	body := fence
	if ctx.Markdown != nil {
		if out, err := ctx.Markdown.Render(fence); err == nil {
			body = strings.Trim(out, "\n")
		}
	}
	return bodyLines(b.Marker, body, max(1, ctx.ContentWidth), func(s string) string { return s })
}
