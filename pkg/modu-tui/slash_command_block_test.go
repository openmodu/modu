package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSlashCommandBlockRendersSelectedCommandInCard(t *testing.T) {
	lines := SlashCommandBlock{
		Commands: []SlashCommand{
			{Name: "/help", Description: "Show help"},
			{Name: "/model", Description: "Switch model"},
		},
		Selected: 1,
	}.RenderWidth(40)
	text := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"┏", "› /model", "Switch model", "┗"} {
		if !strings.Contains(text, want) {
			t.Fatalf("slash card missing %q:\n%s", want, text)
		}
	}
}

func TestMatchSlashCommandsFiltersByPrefix(t *testing.T) {
	got := matchSlashCommands("/mo", []SlashCommand{
		{Name: "help"},
		{Name: "/model", Description: "Switch model"},
	})
	if len(got) != 1 || got[0].Name != "/model" {
		t.Fatalf("matches = %#v, want /model", got)
	}
	if got := matchSlashCommands("/model qwen", []SlashCommand{{Name: "/model"}}); len(got) != 0 {
		t.Fatalf("slash matches should close after arguments, got %#v", got)
	}
}
