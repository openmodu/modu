package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

func runModuTUI(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions) error {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		_ = n
	}

	initial := messagesFromAgentMessages(session.GetMessages())
	var program *tea.Program
	var programMu sync.RWMutex
	send := func(msg tea.Msg) {
		programMu.RLock()
		p := program
		programMu.RUnlock()
		if p != nil {
			p.Send(msg)
		}
	}

	if !noApprove {
		session.SetPrompter(&moduTUIPrompter{ctx: ctx, send: send})
	}

	width, height := initialTerminalSize(int(os.Stdout.Fd()), 120, 35)
	ui := modutui.NewModel(modutui.Options{
		Width:           width,
		Height:          height,
		InitialMessages: initial,
		StatusHint:      "Enter 发送 · Ctrl+C 退出 · 当前为 modu-tui runner",
		Hooks: modutui.Hooks{
			Submit: func(text string) {
				go func() {
					send(modutui.SetBusyMsg{Busy: true})
					send(modutui.SetStatusMsg{Status: "running"})
					if err := session.Prompt(ctx, text); err != nil && err != context.Canceled {
						send(modutui.AppendMessageMsg{Message: modutui.Message{
							Role: modutui.RoleAssistant,
							Text: "error: " + err.Error(),
						}})
						send(modutui.SetStatusMsg{Status: "error"})
					} else {
						send(modutui.SetStatusMsg{Status: "idle"})
					}
					send(modutui.SetBusyMsg{Busy: false})
				}()
			},
		},
	})

	unsubAgent := session.Subscribe(func(ev types.Event) {
		for _, msg := range messagesFromAgentEvent(ev) {
			send(modutui.AppendMessageMsg{Message: msg})
		}
	})
	defer unsubAgent()

	unsubSession := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		if msg, ok := messageFromSessionEvent(ev); ok {
			send(modutui.AppendMessageMsg{Message: msg})
		}
	})
	defer unsubSession()

	prog := tea.NewProgram(ui, tea.WithContext(ctx), tea.WithWindowSize(width, height))
	programMu.Lock()
	program = prog
	programMu.Unlock()
	_, err := prog.Run()
	return err
}

func initialTerminalSize(fd int, fallbackWidth, fallbackHeight int) (int, int) {
	width, height, err := term.GetSize(fd)
	if err != nil || width <= 0 || height <= 0 {
		return fallbackWidth, fallbackHeight
	}
	return width, height
}

type moduTUIPrompter struct {
	ctx  context.Context
	send func(tea.Msg)
}

func (p *moduTUIPrompter) Confirm(title, body string, defaultYes bool) bool {
	p.notify(title, body)
	return defaultYes
}

func (p *moduTUIPrompter) Select(title string, options []string) string {
	p.notify(title, "selection prompt is not available in modu-tui runner yet")
	if len(options) == 0 {
		return ""
	}
	return options[0]
}

func (p *moduTUIPrompter) ApprovePlan(plan string, steps []string) string {
	body := strings.TrimSpace(plan)
	if len(steps) > 0 {
		body += "\n\n" + strings.Join(steps, "\n")
	}
	p.notify("plan approval required", body)
	return "reject: approval UI is not available in modu-tui runner yet"
}

func (p *moduTUIPrompter) ApproveTool(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error) {
	if p == nil || p.send == nil {
		return types.ToolApprovalDeny, nil
	}
	ch := make(chan modutui.ToolApprovalDecision, 1)
	p.send(modutui.RequestToolApprovalMsg{
		Request: modutui.ToolApprovalRequest{
			ID:       toolCallID,
			ToolName: toolName,
			Summary:  "approval required: " + toolName,
			Detail:   formatJSON(args),
		},
		Respond: ch,
	})
	select {
	case decision := <-ch:
		return toolApprovalDecisionToTypes(decision), nil
	case <-p.ctx.Done():
		return types.ToolApprovalDeny, p.ctx.Err()
	}
}

func (p *moduTUIPrompter) notify(summary, detail string) {
	if p == nil || p.send == nil {
		return
	}
	p.send(modutui.AppendMessageMsg{Message: modutui.Message{
		Tool:     true,
		ToolName: "approval",
		Summary:  summary,
		Detail:   detail,
		Expanded: true,
	}})
}

func messagesFromAgentEvent(ev types.Event) []modutui.Message {
	switch ev.Type {
	case types.EventTypeMessageEnd:
		if isUserMessage(ev.Message) {
			return nil
		}
		return messagesFromAgentMessage(ev.Message)
	case types.EventTypeToolExecutionStart:
		input := toolInputFromArgs(ev.ToolName, ev.Args)
		return []modutui.Message{{
			Tool:      true,
			ToolID:    ev.ToolCallID,
			ToolName:  ev.ToolName,
			Summary:   toolRunningSummary(ev.ToolName),
			Detail:    input,
			ToolInput: input,
		}}
	case types.EventTypeToolExecutionEnd:
		return []modutui.Message{{
			Tool:       true,
			ToolID:     ev.ToolCallID,
			ToolName:   ev.ToolName,
			Summary:    toolDoneSummary(ev.ToolName, ev.IsError),
			ToolOutput: toolOutputFromResult(ev.Result),
			ToolError:  ev.IsError,
			ToolDone:   true,
			Expanded:   ev.IsError,
		}}
	default:
		return nil
	}
}

func isUserMessage(msg types.AgentMessage) bool {
	switch msg.(type) {
	case types.UserMessage, *types.UserMessage:
		return true
	default:
		return false
	}
}

func messagesFromAgentMessages(messages []types.AgentMessage) []modutui.Message {
	out := make([]modutui.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, messagesFromAgentMessage(msg)...)
	}
	return out
}

func messagesFromAgentMessage(msg types.AgentMessage) []modutui.Message {
	switch m := msg.(type) {
	case types.UserMessage:
		return []modutui.Message{{Role: modutui.RoleUser, Text: contentText(m.Content)}}
	case *types.UserMessage:
		if m == nil {
			return nil
		}
		return []modutui.Message{{Role: modutui.RoleUser, Text: contentText(m.Content)}}
	case types.AssistantMessage:
		return messagesFromAssistantMessage(m)
	case *types.AssistantMessage:
		if m == nil {
			return nil
		}
		return messagesFromAssistantMessage(*m)
	case types.ToolResultMessage:
		return []modutui.Message{messageFromToolResult(m)}
	case *types.ToolResultMessage:
		if m == nil {
			return nil
		}
		return []modutui.Message{messageFromToolResult(*m)}
	default:
		return nil
	}
}

func messagesFromAssistantMessage(msg types.AssistantMessage) []modutui.Message {
	var out []modutui.Message
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *types.TextContent:
			if b != nil && strings.TrimSpace(b.Text) != "" {
				out = append(out, modutui.Message{Role: modutui.RoleAssistant, Text: b.Text})
			}
		case *types.ThinkingContent:
			if b != nil && strings.TrimSpace(b.Thinking) != "" {
				out = append(out, modutui.Message{Role: modutui.RoleAssistant, Text: "Thinking:\n\n" + b.Thinking})
			}
		case *types.ToolCallContent:
			if b != nil {
				input := toolInputFromArgs(b.Name, b.Arguments)
				out = append(out, modutui.Message{
					Tool:      true,
					ToolID:    b.ID,
					ToolName:  b.Name,
					Summary:   toolRunningSummary(b.Name),
					Detail:    input,
					ToolInput: input,
				})
			}
		}
	}
	if len(out) == 0 && msg.ErrorMessage != "" {
		out = append(out, modutui.Message{Role: modutui.RoleAssistant, Text: "error: " + msg.ErrorMessage})
	}
	return out
}

func messageFromToolResult(msg types.ToolResultMessage) modutui.Message {
	return modutui.Message{
		Tool:       true,
		ToolID:     msg.ToolCallID,
		ToolName:   msg.ToolName,
		Summary:    toolDoneSummary(msg.ToolName, msg.IsError),
		ToolOutput: contentBlocksText(msg.Content),
		ToolError:  msg.IsError,
		ToolDone:   true,
		Expanded:   msg.IsError,
	}
}

func toolRunningSummary(toolName string) string {
	if strings.EqualFold(toolName, "bash") {
		return "Running shell command"
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	return "Running " + name
}

func toolDoneSummary(toolName string, isError bool) string {
	if strings.EqualFold(toolName, "bash") {
		if isError {
			return "Shell command failed"
		}
		return "Ran 1 shell command"
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	if isError {
		return name + " failed"
	}
	return "Ran " + name
}

func toolInputFromArgs(toolName string, args any) string {
	if strings.EqualFold(toolName, "bash") {
		if command, ok := mapStringValue(args, "command"); ok {
			return command
		}
	}
	return formatJSON(args)
}

func mapStringValue(v any, key string) (string, bool) {
	switch m := v.(type) {
	case map[string]any:
		value, ok := m[key].(string)
		return value, ok
	case map[string]string:
		value, ok := m[key]
		return value, ok
	default:
		return "", false
	}
}

func toolOutputFromResult(result any) string {
	switch r := result.(type) {
	case types.ToolResult:
		if text := contentBlocksText(r.Content); text != "" {
			return text
		}
		return formatJSON(r.Details)
	case *types.ToolResult:
		if r == nil {
			return ""
		}
		if text := contentBlocksText(r.Content); text != "" {
			return text
		}
		return formatJSON(r.Details)
	default:
		return formatJSON(result)
	}
}

func messageFromSessionEvent(ev coding_agent.SessionEvent) (modutui.Message, bool) {
	switch ev.Type {
	case coding_agent.SessionEventModelChange:
		return infoMessage("model: " + ev.Provider + "/" + ev.ModelID), true
	case coding_agent.SessionEventThinkingChange:
		return infoMessage("thinking: " + ev.Level), true
	case coding_agent.SessionEventCwdChanged:
		return infoMessage("cwd: " + ev.NewCwd), true
	case coding_agent.SessionEventWorktreeCreate:
		return infoMessage("worktree: " + ev.Path), true
	case coding_agent.SessionEventWorktreeRemove:
		return infoMessage("worktree removed: " + ev.Path), true
	case coding_agent.SessionEventSubagentStart:
		return infoMessage("subagent start: " + ev.SubagentName + "\n" + ev.SubagentTask), true
	case coding_agent.SessionEventSubagentStop:
		text := "subagent stop: " + ev.SubagentName
		if ev.ErrorMessage != "" {
			text += "\nerror: " + ev.ErrorMessage
		}
		if ev.SubagentResult != "" {
			text += "\n" + ev.SubagentResult
		}
		return infoMessage(text), true
	case coding_agent.SessionEventPermissionReq:
		return infoMessage("permission requested: " + ev.ToolName), true
	case coding_agent.SessionEventPermissionDeny:
		text := "permission denied: " + ev.ToolName
		if ev.Reason != "" {
			text += "\n" + ev.Reason
		}
		return infoMessage(text), true
	case coding_agent.SessionEventExtensionNotify:
		text := ev.Message
		if ev.ExtensionName != "" {
			text = ev.ExtensionName + ": " + text
		}
		return infoMessage(text), true
	default:
		return modutui.Message{}, false
	}
}

func infoMessage(text string) modutui.Message {
	return modutui.Message{Role: modutui.RoleAssistant, Text: strings.TrimSpace(text)}
}

func contentText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []types.ContentBlock:
		return contentBlocksText(c)
	default:
		return fmt.Sprint(c)
	}
}

func contentBlocksText(blocks []types.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case *types.TextContent:
			if b != nil && b.Text != "" {
				parts = append(parts, b.Text)
			}
		case *types.ThinkingContent:
			if b != nil && b.Thinking != "" {
				parts = append(parts, b.Thinking)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func formatJSON(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

func toolApprovalDecisionToTypes(decision modutui.ToolApprovalDecision) types.ToolApprovalDecision {
	switch decision {
	case modutui.ToolApprovalAllow:
		return types.ToolApprovalAllow
	case modutui.ToolApprovalAllowAlways:
		return types.ToolApprovalAllowAlways
	case modutui.ToolApprovalDenyAlways:
		return types.ToolApprovalDenyAlways
	default:
		return types.ToolApprovalDeny
	}
}
