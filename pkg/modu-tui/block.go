package modutui

type RenderedLine struct {
	Text   string
	Gutter int
}

type BlockRender struct {
	Lines []RenderedLine
}

type MarkdownRenderer interface {
	Render(string) (string, error)
}

type RenderContext struct {
	ContentWidth int
	Markdown     MarkdownRenderer
	Hooks        Hooks
}

type Block interface {
	Render(RenderContext) BlockRender
}

func (r *BlockRender) Add(text string, gutter int) {
	r.Lines = append(r.Lines, RenderedLine{Text: text, Gutter: gutter})
}
