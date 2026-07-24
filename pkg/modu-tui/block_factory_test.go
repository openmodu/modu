package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestCustomBlockFactoryOverridesEntryRendering(t *testing.T) {
	m := NewModel(Options{
		Width:  40,
		Height: 8,
		InitialEntries: []Entry{
			{Role: RoleAssistant, Nodes: []Node{TextNode{Text: "original"}}},
		},
		BlockFactories: []EntryBlockFactory{
			func(entry Entry) (Block, bool) {
				text := entry.Nodes[0].(TextNode).Text
				return TextBlock{Marker: "X ", Text: "factory " + text}, true
			},
		},
	})
	got := strings.Join(m.Lines(), "\n")
	if !strings.Contains(got, "factory original") {
		t.Fatalf("custom block factory was not used:\n%s", got)
	}
}

func TestDefaultAssistantMarkerIsWhite(t *testing.T) {
	if got, want := assistantMarkerStyle.GetForeground(), lipgloss.Color("231"); got != want {
		t.Fatalf("assistant marker foreground = %#v, want %#v", got, want)
	}
}

func TestPlainEntryRendersWithoutMarker(t *testing.T) {
	m := NewModel(Options{
		Width:  40,
		Height: 8,
		InitialEntries: []Entry{
			{Role: RoleAssistant, Nodes: []Node{TextNode{Text: "✓ Completed (2s)"}}, Plain: true},
		},
	})
	got := strings.Join(m.Lines(), "\n")
	if !strings.Contains(got, "✓ Completed (2s)") {
		t.Fatalf("plain message missing text:\n%s", got)
	}
	if strings.Contains(got, "● ✓ Completed") {
		t.Fatalf("plain message should not render assistant marker:\n%s", got)
	}
}
