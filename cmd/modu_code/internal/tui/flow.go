package tui

import (
	"context"
	"fmt"
	"strings"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

type FlowStepKind string

const (
	FlowStepChoice FlowStepKind = "choice"
	FlowStepText   FlowStepKind = "text"
)

// Flow describes a linear interaction using one standard data format.
// Business code owns what the collected values mean and what action to run.
type Flow struct {
	ID    string
	Steps []FlowStep
}

type FlowStep struct {
	ID          string
	Kind        FlowStepKind
	Title       string
	Body        string
	Options     []modutui.HumanPromptOption
	Placeholder string
	Default     string
	Secret      bool
	Required    bool
}

type FlowResult struct {
	ID     string
	Values map[string]string
}

func (r FlowResult) Value(id string) string {
	return r.Values[strings.TrimSpace(id)]
}

func (d Dialog) RunFlow(ctx context.Context, flow Flow) (FlowResult, bool, error) {
	flow.ID = strings.TrimSpace(flow.ID)
	if flow.ID == "" {
		return FlowResult{}, false, fmt.Errorf("flow id is required")
	}
	result := FlowResult{
		ID:     flow.ID,
		Values: make(map[string]string, len(flow.Steps)),
	}
	seen := make(map[string]struct{}, len(flow.Steps))
	for _, step := range flow.Steps {
		step.ID = strings.TrimSpace(step.ID)
		if step.ID == "" {
			return FlowResult{}, false, fmt.Errorf("flow %q has a step without an id", flow.ID)
		}
		if _, ok := seen[step.ID]; ok {
			return FlowResult{}, false, fmt.Errorf("flow %q has duplicate step %q", flow.ID, step.ID)
		}
		seen[step.ID] = struct{}{}
		switch step.Kind {
		case FlowStepChoice:
			if len(step.Options) == 0 {
				return FlowResult{}, false, fmt.Errorf("flow %q choice %q has no options", flow.ID, step.ID)
			}
		case FlowStepText:
		default:
			return FlowResult{}, false, fmt.Errorf("flow %q step %q has unsupported kind %q", flow.ID, step.ID, step.Kind)
		}
	}

	for _, step := range flow.Steps {
		step.ID = strings.TrimSpace(step.ID)
		var (
			value string
			err   error
		)
		switch step.Kind {
		case FlowStepChoice:
			value, err = d.client.AskChoice(ctx, modutui.HumanPromptRequest{
				ID:           flow.ID + ":" + step.ID,
				Title:        step.Title,
				Body:         step.Body,
				Options:      append([]modutui.HumanPromptOption(nil), step.Options...),
				DefaultIndex: -1,
			})
			value = strings.TrimSpace(value)
		case FlowStepText:
			value, err = d.client.AskText(ctx, modutui.HumanTextRequest{
				ID:          flow.ID + ":" + step.ID,
				Title:       step.Title,
				Body:        step.Body,
				Placeholder: step.Placeholder,
				Default:     step.Default,
				Secret:      step.Secret,
				Required:    step.Required,
			})
			if !step.Secret {
				value = strings.TrimSpace(value)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return FlowResult{}, false, ctx.Err()
			}
			return FlowResult{}, false, nil
		}
		if step.Kind == FlowStepChoice && value == "" {
			return FlowResult{}, false, nil
		}
		if step.Required && strings.TrimSpace(value) == "" {
			return FlowResult{}, false, nil
		}
		result.Values[step.ID] = value
	}
	return result, true, nil
}
