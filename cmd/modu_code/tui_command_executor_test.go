package main

import (
	"context"
	"testing"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/slash"
)

func TestModuTUICommandExecutorRoutesAliasFromRegisteredDefinition(t *testing.T) {
	var steeredText string
	var requireActive bool
	executor, err := newModuTUICommandExecutor(moduTUICommandExecutorOptions{
		Client: modutui.NewClient(func(any) {}),
		QueueSteer: func(text string, required bool) {
			steeredText = text
			requireActive = required
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	executor.Execute(context.Background(), "/S change direction")

	if steeredText != "change direction" || !requireActive {
		t.Fatalf("steer = %q, requireActive = %v", steeredText, requireActive)
	}
}

func TestModuTUICommandExecutorSuggestionsUseRegisteredAliases(t *testing.T) {
	executor, err := newModuTUICommandExecutor(moduTUICommandExecutorOptions{})
	if err != nil {
		t.Fatal(err)
	}

	commands := executor.Suggestions()
	seen := make(map[string]modutui.SlashCommand)
	for _, command := range commands {
		seen[command.Name] = command
	}
	for _, name := range []string{"/steer", "/s", "/followup", "/f", "/config", "/channel", "/clone", "/branch-session", "/new"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing registered suggestion %q in %#v", name, commands)
		}
	}
	if got := seen["/s"].Description; got != "Alias for /steer" {
		t.Fatalf("/s description = %q", got)
	}
}

func TestModuTUICommandExecutorRegistersEveryBuiltinWithHandler(t *testing.T) {
	executor, err := newModuTUICommandExecutor(moduTUICommandExecutorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range executor.registry.Commands() {
		if command.Handler == nil {
			t.Fatalf("registered command %q has no handler", command.Name)
		}
	}

	seen := make(map[string]struct{})
	for _, command := range executor.Suggestions() {
		seen[command.Name] = struct{}{}
	}
	for _, definition := range slash.CommandDefinitions() {
		for _, name := range append([]string{definition.Name}, definition.Aliases...) {
			if _, ok := seen[name]; !ok {
				t.Fatalf("built-in command %q is missing from the unified registry", name)
			}
		}
	}
}
