package main

import (
	"context"
	"errors"
	"strings"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

type moduTUIPrompter struct {
	ctx    context.Context
	client modutui.Client
}

func (p *moduTUIPrompter) Confirm(title, body string, defaultYes bool) bool {
	defaultIndex := 1
	if defaultYes {
		defaultIndex = 0
	}
	choice := p.requestHumanPrompt(modutui.HumanPromptRequest{
		Title: title,
		Body:  body,
		Options: []modutui.HumanPromptOption{
			{Label: "Yes", Value: "yes"},
			{Label: "No", Value: "no"},
		},
		DefaultIndex: defaultIndex,
	})
	if choice == "" {
		return defaultYes
	}
	return choice == "yes"
}

func (p *moduTUIPrompter) Select(title string, options []string) string {
	if len(options) == 0 {
		return ""
	}
	promptOptions := make([]modutui.HumanPromptOption, 0, len(options))
	for _, option := range options {
		promptOptions = append(promptOptions, modutui.HumanPromptOption{Label: option, Value: option})
	}
	choice := p.requestHumanPrompt(modutui.HumanPromptRequest{
		Title:        title,
		Options:      promptOptions,
		DefaultIndex: 0,
	})
	if choice == "" {
		return options[0]
	}
	return choice
}

func (p *moduTUIPrompter) ApprovePlan(plan string, steps []string) string {
	body := strings.TrimSpace(plan)
	if len(steps) > 0 {
		body += "\n\n" + strings.Join(steps, "\n")
	}
	choice := p.requestHumanPrompt(modutui.HumanPromptRequest{
		Title: "Plan approval required",
		Body:  body,
		Options: []modutui.HumanPromptOption{
			{Label: "Approve", Value: "approve"},
			{Label: "Approve + auto", Value: "approve_auto"},
			{Label: "Reject", Value: "reject"},
		},
		DefaultIndex: 2,
	})
	switch choice {
	case "approve", "approve_auto":
		return choice
	default:
		return "reject: rejected in modu-tui"
	}
}

func (p *moduTUIPrompter) ApproveTool(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error) {
	if p == nil {
		return types.ToolApprovalDeny, nil
	}
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	decision, err := p.client.AskToolApproval(ctx, modutui.ToolApprovalRequest{
		ID:       toolCallID,
		ToolName: toolName,
		Summary:  "approval required: " + toolName,
		Detail:   toolInputFromArgs(toolName, args),
	})
	if errors.Is(err, modutui.ErrClientUnavailable) {
		return types.ToolApprovalDeny, nil
	}
	if err != nil {
		return types.ToolApprovalDeny, err
	}
	return toolApprovalDecisionToTypes(decision), nil
}

func (p *moduTUIPrompter) requestHumanPrompt(req modutui.HumanPromptRequest) string {
	if p == nil {
		return ""
	}
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	choice, err := p.client.AskChoice(ctx, req)
	if err != nil {
		return ""
	}
	return choice
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
