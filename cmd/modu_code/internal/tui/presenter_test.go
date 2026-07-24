package tui

import (
	"testing"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func TestPresenterEmitsStandardEntries(t *testing.T) {
	var messages []any
	presenter := NewPresenter(modutui.NewClient(func(message any) {
		messages = append(messages, message)
	}))

	presenter.Text(modutui.RoleAssistant, "plain")
	presenter.Markdown(modutui.RoleAssistant, "# markdown")
	presenter.Upsert("job", modutui.RoleAssistant, modutui.ProgressNode{Current: 1, Total: 2})
	presenter.Remove("job")

	if len(messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(messages))
	}
	text := messages[0].(modutui.UpdateMsg).Update.(modutui.AppendEntryUpdate).Entry
	if len(text.Nodes) != 1 {
		t.Fatalf("text entry = %#v", text)
	}
	if _, ok := text.Nodes[0].(modutui.TextNode); !ok {
		t.Fatalf("text node = %T", text.Nodes[0])
	}
	markdown := messages[1].(modutui.UpdateMsg).Update.(modutui.AppendEntryUpdate).Entry
	if _, ok := markdown.Nodes[0].(modutui.MarkdownNode); !ok {
		t.Fatalf("markdown node = %T", markdown.Nodes[0])
	}
	upsert := messages[2].(modutui.UpdateMsg).Update.(modutui.UpsertEntryUpdate)
	if upsert.Entry.ID != "job" {
		t.Fatalf("upsert entry = %#v", upsert.Entry)
	}
	if remove := messages[3].(modutui.UpdateMsg).Update.(modutui.RemoveEntryUpdate); remove.ID != "job" {
		t.Fatalf("remove = %#v", remove)
	}
}
