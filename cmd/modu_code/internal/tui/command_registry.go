package tui

import (
	"context"
	"fmt"
	"strings"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

type CommandInvocation struct {
	Line string
	Name string
	Args string
}

type CommandHandler func(context.Context, CommandInvocation)

type Command struct {
	Name        string
	Aliases     []string
	Description string
	Foreground  bool
	Handler     CommandHandler
}

type ResolvedCommand struct {
	Command    Command
	Invocation CommandInvocation
}

// CommandRegistry is immutable after construction. It owns command identity,
// aliases, descriptions, dispatch, help, and completion metadata.
type CommandRegistry struct {
	commands []Command
	lookup   map[string]int
}

func (r CommandRegistry) Commands() []Command {
	out := make([]Command, len(r.commands))
	for i, command := range r.commands {
		out[i] = command
		out[i].Aliases = append([]string(nil), command.Aliases...)
	}
	return out
}

func NewCommandRegistry(commands ...Command) (CommandRegistry, error) {
	registry := CommandRegistry{lookup: make(map[string]int)}
	for _, command := range commands {
		command.Name = normalizeCommandName(command.Name)
		command.Description = strings.TrimSpace(command.Description)
		if command.Name == "" {
			return CommandRegistry{}, fmt.Errorf("command name is required")
		}
		command.Aliases = normalizeAliases(command.Aliases)
		index := len(registry.commands)
		for _, name := range append([]string{command.Name}, command.Aliases...) {
			if existing, ok := registry.lookup[name]; ok {
				existingName := command.Name
				if existing >= 0 && existing < len(registry.commands) {
					existingName = registry.commands[existing].Name
				}
				return CommandRegistry{}, fmt.Errorf("command %s duplicates %s", name, existingName)
			}
			registry.lookup[name] = index
		}
		registry.commands = append(registry.commands, command)
	}
	return registry, nil
}

func (r CommandRegistry) Dispatch(ctx context.Context, line string) bool {
	resolved, ok := r.Resolve(line)
	if !ok {
		return false
	}
	handler := resolved.Command.Handler
	if handler == nil {
		return false
	}
	handler(ctx, resolved.Invocation)
	return true
}

func (r CommandRegistry) Resolve(line string) (ResolvedCommand, bool) {
	invocation, ok := ParseCommand(line)
	if !ok {
		return ResolvedCommand{}, false
	}
	index, ok := r.lookup[invocation.Name]
	if !ok || index < 0 || index >= len(r.commands) {
		return ResolvedCommand{}, false
	}
	return ResolvedCommand{
		Command:    r.commands[index],
		Invocation: invocation,
	}, true
}

// ResolveDynamic resolves immutable host commands first, then runtime commands
// such as extensions, skills, and prompt templates through the same registry
// protocol.
func (r CommandRegistry) ResolveDynamic(line string, dynamic []modutui.SlashCommand, handler CommandHandler) (ResolvedCommand, bool) {
	if resolved, ok := r.Resolve(line); ok {
		return resolved, true
	}
	invocation, ok := ParseCommand(line)
	if !ok {
		return ResolvedCommand{}, false
	}
	for _, candidate := range dynamic {
		name := normalizeCommandName(candidate.Name)
		if name == "" || name != invocation.Name {
			continue
		}
		return ResolvedCommand{
			Command: Command{
				Name:        name,
				Description: strings.TrimSpace(candidate.Description),
				Foreground:  true,
				Handler:     handler,
			},
			Invocation: invocation,
		}, true
	}
	return ResolvedCommand{}, false
}

func (r CommandRegistry) Suggestions(dynamic ...[]modutui.SlashCommand) []modutui.SlashCommand {
	seen := make(map[string]struct{})
	out := make([]modutui.SlashCommand, 0, len(r.lookup))
	add := func(name, description string) {
		name = normalizeCommandName(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, modutui.SlashCommand{
			Name:        name,
			Description: strings.TrimSpace(description),
		})
	}
	for _, command := range r.commands {
		add(command.Name, command.Description)
		for _, alias := range command.Aliases {
			add(alias, "Alias for "+command.Name)
		}
	}
	for _, commands := range dynamic {
		for _, command := range commands {
			add(command.Name, command.Description)
		}
	}
	return out
}

func ParseCommand(line string) (CommandInvocation, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return CommandInvocation{}, false
	}
	name, args, _ := strings.Cut(line, " ")
	name = normalizeCommandName(name)
	if name == "" {
		return CommandInvocation{}, false
	}
	return CommandInvocation{
		Line: line,
		Name: name,
		Args: strings.TrimSpace(args),
	}, true
}

func normalizeCommandName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "/")
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return ""
	}
	return "/" + name
}

func normalizeAliases(aliases []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = normalizeCommandName(alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	return out
}
