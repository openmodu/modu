package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestCustomBlockFactoryOverridesMessageRendering(t *testing.T) {
	m := NewModel(Options{
		Width:  40,
		Height: 8,
		InitialMessages: []Message{
			{Role: RoleAssistant, Text: "original"},
		},
		BlockFactories: []MessageBlockFactory{
			func(msg Message) (Block, bool) {
				return TextBlock{Marker: "X ", Text: "factory " + msg.Text}, true
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

func TestPlainMessageRendersWithoutMarker(t *testing.T) {
	m := NewModel(Options{
		Width:  40,
		Height: 8,
		InitialMessages: []Message{
			{Role: RoleAssistant, Text: "✓ Completed (2s)", Preformatted: true, Plain: true},
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
