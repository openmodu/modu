package tui

import (
	"context"
	"strings"
	"testing"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func TestCommandRegistryDispatchesCanonicalNameAndAlias(t *testing.T) {
	var calls []CommandInvocation
	registry, err := NewCommandRegistry(Command{
		Name:        "followup",
		Aliases:     []string{"f"},
		Description: "Queue a follow-up",
		Foreground:  true,
		Handler: func(_ context.Context, invocation CommandInvocation) {
			calls = append(calls, invocation)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !registry.Dispatch(context.Background(), " /FOLLOWUP  first ") {
		t.Fatal("canonical command was not dispatched")
	}
	if !registry.Dispatch(context.Background(), "/f second") {
		t.Fatal("alias was not dispatched")
	}
	if registry.Dispatch(context.Background(), "/unknown") {
		t.Fatal("unknown command should not be dispatched")
	}
	if len(calls) != 2 || calls[0].Name != "/followup" || calls[0].Args != "first" || calls[1].Name != "/f" || calls[1].Args != "second" {
		t.Fatalf("calls = %#v", calls)
	}
	resolved, ok := registry.Resolve("/f later")
	if !ok || !resolved.Command.Foreground || resolved.Invocation.Args != "later" {
		t.Fatalf("resolved = %#v, %v", resolved, ok)
	}
}

func TestCommandRegistrySuggestionsMergeAndDeduplicate(t *testing.T) {
	registry, err := NewCommandRegistry(
		Command{Name: "/help", Aliases: []string{"/h"}, Description: "Help"},
		Command{Name: "/config", Description: "Configure"},
	)
	if err != nil {
		t.Fatal(err)
	}

	got := registry.Suggestions([]modutui.SlashCommand{
		{Name: "help", Description: "duplicate"},
		{Name: "review", Description: "Review changes"},
	})
	var names []string
	for _, command := range got {
		names = append(names, command.Name)
	}
	if strings.Join(names, ",") != "/help,/h,/config,/review" {
		t.Fatalf("suggestions = %#v", got)
	}
	if got[1].Description != "Alias for /help" {
		t.Fatalf("alias description = %q", got[1].Description)
	}
}

func TestCommandRegistryCommandsReturnsDefensiveCopy(t *testing.T) {
	registry, err := NewCommandRegistry(Command{
		Name:    "/help",
		Aliases: []string{"/h"},
	})
	if err != nil {
		t.Fatal(err)
	}
	commands := registry.Commands()
	commands[0].Aliases[0] = "/changed"
	if got := registry.Commands()[0].Aliases[0]; got != "/h" {
		t.Fatalf("registry alias mutated through Commands(): %q", got)
	}
}

func TestCommandRegistryResolvesDynamicCommandWithHandler(t *testing.T) {
	var got CommandInvocation
	handler := func(_ context.Context, invocation CommandInvocation) {
		got = invocation
	}
	registry, err := NewCommandRegistry(Command{Name: "/help", Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := registry.ResolveDynamic("/review cmd/modu_code", []modutui.SlashCommand{{
		Name:        "review",
		Description: "Review changes",
	}}, handler)
	if !ok || !resolved.Command.Foreground || resolved.Command.Description != "Review changes" {
		t.Fatalf("ResolveDynamic() = %#v, %v", resolved, ok)
	}
	resolved.Command.Handler(context.Background(), resolved.Invocation)
	if got.Name != "/review" || got.Args != "cmd/modu_code" {
		t.Fatalf("dynamic invocation = %#v", got)
	}
}

func TestCommandRegistryRejectsDuplicateIdentity(t *testing.T) {
	tests := []struct {
		name     string
		commands []Command
	}{
		{
			name: "between commands",
			commands: []Command{
				{Name: "/steer", Aliases: []string{"/s"}},
				{Name: "/s"},
			},
		},
		{
			name:     "alias repeats canonical name",
			commands: []Command{{Name: "/steer", Aliases: []string{"/steer"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCommandRegistry(tt.commands...)
			if err == nil || !strings.Contains(err.Error(), "duplicates") {
				t.Fatalf("duplicate error = %v", err)
			}
		})
	}
}

func TestParseCommandRejectsNonCommand(t *testing.T) {
	for _, line := range []string{"", "hello", "/", "/bad name extra"} {
		invocation, ok := ParseCommand(line)
		if line == "/bad name extra" {
			if !ok || invocation.Name != "/bad" || invocation.Args != "name extra" {
				t.Fatalf("ParseCommand(%q) = %#v, %v", line, invocation, ok)
			}
			continue
		}
		if ok {
			t.Fatalf("ParseCommand(%q) unexpectedly succeeded: %#v", line, invocation)
		}
	}
}
