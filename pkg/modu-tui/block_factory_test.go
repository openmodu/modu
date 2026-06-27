package modutui

import (
	"strings"
	"testing"
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
