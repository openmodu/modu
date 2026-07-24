package tui

import modutui "github.com/openmodu/modu/pkg/modu-tui"

// Presenter maps common modu_code output shapes to standard transcript
// entries. Product-specific presenters can compose richer Node lists directly.
type Presenter struct {
	client modutui.Client
}

func NewPresenter(client modutui.Client) Presenter {
	return Presenter{client: client}
}

func (p Presenter) Text(role modutui.Role, text string) {
	p.client.AppendEntry(modutui.Entry{
		Role:  role,
		Nodes: []modutui.Node{modutui.TextNode{Text: text}},
	})
}

func (p Presenter) Markdown(role modutui.Role, text string) {
	p.client.AppendEntry(modutui.Entry{
		Role:  role,
		Nodes: []modutui.Node{modutui.MarkdownNode{Text: text}},
	})
}

func (p Presenter) Upsert(id string, role modutui.Role, nodes ...modutui.Node) {
	p.client.UpsertEntry(modutui.Entry{
		ID:    id,
		Role:  role,
		Nodes: nodes,
	})
}

func (p Presenter) Remove(id string) {
	p.client.RemoveEntry(id)
}
