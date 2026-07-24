package main

import (
	"context"
	"strings"

	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/slash"
	"github.com/openmodu/modu/pkg/types"
)

type moduTUICommandExecutorOptions struct {
	Session          *coding_agent.CodingSession
	Model            *types.Model
	Hooks            CommandHooks
	Client           modutui.Client
	KeepAgentBusy    func() bool
	StartConfig      func()
	StartChannel     func()
	StartModelSelect func()
	QueueFollowUp    func(string, bool)
	QueueSteer       func(string, bool)
}

type moduTUICommandExecutor struct {
	session          *coding_agent.CodingSession
	model            *types.Model
	hooks            CommandHooks
	client           modutui.Client
	presenter        codetui.Presenter
	keepAgentBusy    func() bool
	startConfig      func()
	startChannel     func()
	startModelSelect func()
	queueFollowUp    func(string, bool)
	queueSteer       func(string, bool)
	builtinCommands  []slash.CommandDefinition
	modelCommand     slash.CommandDefinition
	registry         codetui.CommandRegistry
}

func newModuTUICommandExecutor(options moduTUICommandExecutorOptions) (*moduTUICommandExecutor, error) {
	executor := &moduTUICommandExecutor{
		session:          options.Session,
		model:            options.Model,
		hooks:            options.Hooks,
		client:           options.Client,
		presenter:        codetui.NewPresenter(options.Client),
		keepAgentBusy:    options.KeepAgentBusy,
		startConfig:      options.StartConfig,
		startChannel:     options.StartChannel,
		startModelSelect: options.StartModelSelect,
		queueFollowUp:    options.QueueFollowUp,
		queueSteer:       options.QueueSteer,
		builtinCommands:  slash.CommandDefinitions(),
	}
	for _, definition := range executor.builtinCommands {
		if definition.Name == "/model" {
			executor.modelCommand = definition
			break
		}
	}
	registry, err := codetui.NewCommandRegistry(executor.commandDefinitions()...)
	if err != nil {
		return nil, err
	}
	executor.registry = registry
	return executor, nil
}

func (e *moduTUICommandExecutor) Execute(ctx context.Context, line string) {
	if e == nil {
		return
	}
	if resolved, ok := e.registry.ResolveDynamic(line, moduTUIDynamicSlashCommands(e.session), e.executeAgentCommand); ok {
		if resolved.Command.Handler == nil {
			e.presenter.Text(modutui.RoleAssistant, "command is not executable: "+resolved.Invocation.Name)
			return
		}
		if resolved.Command.Foreground {
			e.withActivity(line, func() {
				resolved.Command.Handler(ctx, resolved.Invocation)
			})
		} else {
			resolved.Command.Handler(ctx, resolved.Invocation)
		}
		return
	}
	e.presenter.Text(modutui.RoleAssistant, "unknown command: "+line)
}

func (e *moduTUICommandExecutor) Suggestions() []modutui.SlashCommand {
	if e == nil {
		return nil
	}
	return e.registry.Suggestions(moduTUIDynamicSlashCommands(e.session))
}

func (e *moduTUICommandExecutor) withActivity(line string, run func()) {
	e.client.SetBusy(true)
	e.client.SetStatus(moduTUISlashRunningStatus(line), 0)
	defer func() {
		e.client.SetTodos(moduTUITodos(e.session))
		if e.keepAgentBusy != nil && e.keepAgentBusy() {
			return
		}
		e.client.SetBusy(false)
		e.client.SetStatus("idle", 0)
	}()
	run()
}

func (e *moduTUICommandExecutor) commandDefinitions() []codetui.Command {
	product := []codetui.Command{
		{
			Name:        "/help",
			Aliases:     []string{"/h"},
			Description: "Show available commands",
			Foreground:  true,
			Handler:     e.handleHelp,
		},
		{
			Name:        "/config",
			Description: "Configure providers and models",
			Handler:     e.handleConfig,
		},
		{
			Name:        "/channel",
			Description: "Configure Telegram or Feishu",
			Handler:     e.handleChannel,
		},
		{
			Name:        "/model",
			Description: "Switch the active model",
			Handler:     e.handleModel,
		},
		{
			Name:        "/workflows",
			Description: "Show workflow cockpit",
			Handler:     e.handleWorkflow,
		},
		{
			Name:        "/tool-output",
			Description: "Show full local output for a tool call",
			Foreground:  true,
			Handler:     e.handleToolOutput,
		},
		{
			Name:        "/steer",
			Aliases:     []string{"/s"},
			Description: "Steer the active task",
			Handler:     e.handleSteer,
		},
		{
			Name:        "/followup",
			Aliases:     []string{"/f"},
			Description: "Queue a follow-up message",
			Handler:     e.handleFollowUp,
		},
	}
	for _, definition := range e.builtinCommands {
		if definition.Name == "/model" {
			continue
		}
		definition := definition
		product = append(product, codetui.Command{
			Name:        definition.Name,
			Aliases:     definition.Aliases,
			Description: definition.Description,
			Foreground:  true,
			Handler: func(ctx context.Context, invocation codetui.CommandInvocation) {
				e.executeBuiltin(ctx, invocation, definition)
			},
		})
	}
	return product
}

func (e *moduTUICommandExecutor) handleHelp(_ context.Context, _ codetui.CommandInvocation) {
	var lines []string
	for _, command := range e.registry.Commands() {
		names := append([]string{command.Name}, command.Aliases...)
		line := strings.Join(names, ", ")
		if command.Description != "" {
			line += " — " + command.Description
		}
		lines = append(lines, line)
	}
	static := make(map[string]struct{})
	for _, command := range e.registry.Suggestions() {
		static[command.Name] = struct{}{}
	}
	for _, command := range e.registry.Suggestions(moduTUIDynamicSlashCommands(e.session)) {
		if _, ok := static[command.Name]; ok {
			continue
		}
		line := command.Name
		if description := strings.TrimSpace(command.Description); description != "" {
			line += " — " + description
		}
		lines = append(lines, line)
		static[command.Name] = struct{}{}
	}
	lines = append(lines,
		"",
		"keys",
		"ctrl+j — insert newline",
		"ctrl+l — clear conversation buffer",
		"ctrl+o — toggle expanded tool output",
		"ctrl+c — interrupt running query / exit when idle",
		"esc — interrupt running query / dismiss suggestions",
		"tab — autocomplete slash command",
		"↑ / ↓ — history (or navigate slash suggestions)",
		"",
		"tool approval",
		"y — allow once",
		"a — always allow this tool",
		"n / ESC — deny once",
		"d — always deny this tool",
	)
	e.presenter.Text(modutui.RoleAssistant, "Help\n"+strings.Join(lines, "\n"))
}

func (e *moduTUICommandExecutor) handleConfig(ctx context.Context, invocation codetui.CommandInvocation) {
	if invocation.Args == "" {
		if e.startConfig != nil {
			e.startConfig()
		}
		return
	}
	e.withActivity(invocation.Line, func() {
		if e.hooks.Config == nil {
			e.presenter.Text(modutui.RoleAssistant, "config command is not available")
			return
		}
		output, err := e.hooks.Config(invocation.Args)
		text := strings.TrimSpace(output)
		if err != nil {
			if text != "" {
				text += "\n"
			}
			text += "error: " + err.Error()
		}
		if text == "" {
			text = "config command completed"
		}
		e.presenter.Text(modutui.RoleAssistant, text)
	})
}

func (e *moduTUICommandExecutor) handleChannel(ctx context.Context, invocation codetui.CommandInvocation) {
	if invocation.Args == "" && e.startChannel != nil {
		e.startChannel()
		return
	}
	e.presenter.Text(modutui.RoleAssistant, "usage: /channel")
}

func (e *moduTUICommandExecutor) handleModel(ctx context.Context, invocation codetui.CommandInvocation) {
	if invocation.Args == "" && e.startModelSelect != nil {
		e.startModelSelect()
		return
	}
	e.withActivity(invocation.Line, func() {
		e.executeBuiltin(ctx, invocation, e.modelCommand)
	})
}

func (e *moduTUICommandExecutor) handleWorkflow(ctx context.Context, invocation codetui.CommandInvocation) {
	if panel, ok := moduTUIWorkflowPanelFromSlash(e.session, invocation.Line); ok {
		e.client.OpenPanel(panel)
		return
	}
	e.withActivity(invocation.Line, func() {
		e.executeAgentCommand(ctx, invocation)
	})
}

func (e *moduTUICommandExecutor) handleToolOutput(_ context.Context, invocation codetui.CommandInvocation) {
	handled, text := moduTUIToolOutputSlash(e.session, invocation.Line)
	if handled {
		e.presenter.Text(modutui.RoleAssistant, text)
	}
}

func (e *moduTUICommandExecutor) handleSteer(_ context.Context, invocation codetui.CommandInvocation) {
	if invocation.Args == "" {
		e.client.SetStatus("/steer requires a message", 0)
		return
	}
	if e.queueSteer == nil {
		e.client.SetStatus("no active task to steer", 0)
		return
	}
	e.queueSteer(invocation.Args, true)
}

func (e *moduTUICommandExecutor) handleFollowUp(_ context.Context, invocation codetui.CommandInvocation) {
	if invocation.Args == "" {
		e.client.SetStatus("/followup requires a message", 0)
		return
	}
	if e.queueFollowUp == nil {
		e.client.SetStatus("no active task to followup", 0)
		return
	}
	e.queueFollowUp(invocation.Args, true)
}

func (e *moduTUICommandExecutor) executeBuiltin(ctx context.Context, invocation codetui.CommandInvocation, definition slash.CommandDefinition) {
	if e.session == nil {
		e.presenter.Text(modutui.RoleAssistant, "command requires an active session")
		return
	}
	printer := &moduTUISlashPrinter{}
	previousSessionFile := e.session.GetSessionFile()
	exit := definition.Execute(ctx, invocation.Name, invocation.Args, e.session, printer, e.model)
	if printer.clear {
		e.client.ClearTranscript()
	}
	if e.session.GetSessionFile() != previousSessionFile {
		e.client.ReplaceEntries(moduTUITranscriptEntries(e.session, newModuTUIEventPresenter()))
		e.client.SetFooter(moduTUIFooter(e.session))
	}
	if entry, ok := printer.Entry(); ok {
		e.client.AppendEntry(entry)
	}
	if exit {
		e.client.Quit()
	}
}

func (e *moduTUICommandExecutor) executeAgentCommand(ctx context.Context, invocation codetui.CommandInvocation) {
	if e.session == nil {
		e.presenter.Text(modutui.RoleAssistant, "unknown command: "+invocation.Line)
		return
	}
	if err := e.session.Prompt(ctx, invocation.Line); err != nil && err != context.Canceled {
		e.presenter.Text(modutui.RoleAssistant, "error: "+err.Error())
		e.client.SetStatus("error", 0)
	}
}

type moduTUISlashPrinter struct {
	nodes []modutui.Node
	clear bool
}

func (p *moduTUISlashPrinter) PrintInfo(s string) {
	if text := strings.TrimSpace(s); text != "" {
		p.nodes = append(p.nodes, modutui.TextNode{Text: text})
	}
}

func (p *moduTUISlashPrinter) PrintError(err error) {
	if err != nil {
		p.nodes = append(p.nodes, modutui.TextNode{Text: "error: " + err.Error()})
	}
}

func (p *moduTUISlashPrinter) PrintSection(title string, lines []string) {
	if title = strings.TrimSpace(title); title != "" {
		p.nodes = append(p.nodes, modutui.TextNode{Text: title})
	}
	items := make([]modutui.ListItem, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			items = append(items, modutui.ListItem{Label: line})
		}
	}
	if len(items) > 0 {
		p.nodes = append(p.nodes, modutui.ListNode{Items: items})
	}
}

func (p *moduTUISlashPrinter) ClearScreen() {
	p.clear = true
}

func (p *moduTUISlashPrinter) Entry() (modutui.Entry, bool) {
	if len(p.nodes) == 0 {
		return modutui.Entry{}, false
	}
	nodes := make([]modutui.Node, len(p.nodes))
	copy(nodes, p.nodes)
	return modutui.Entry{Role: modutui.RoleAssistant, Nodes: nodes}, true
}

func moduTUIDynamicSlashCommands(session *coding_agent.CodingSession) []modutui.SlashCommand {
	if session == nil {
		return nil
	}
	var commands []modutui.SlashCommand
	for _, command := range session.RegisteredSlashCommands() {
		commands = append(commands, modutui.SlashCommand{Name: command.Name, Description: command.Description})
	}
	for _, skill := range session.GetSkills() {
		commands = append(commands, modutui.SlashCommand{Name: skill.Name, Description: skill.Description})
	}
	for _, prompt := range session.GetPromptTemplates() {
		description := prompt.Description
		if prompt.ArgumentHint != "" {
			if description != "" {
				description += " "
			}
			description += "(" + prompt.ArgumentHint + ")"
		}
		commands = append(commands, modutui.SlashCommand{Name: prompt.Name, Description: description})
	}
	return commands
}
