package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	codetui "github.com/openmodu/modu/cmd/modu_code/internal/tui"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func runModuTUIModelSelect(ctx context.Context, session *coding_agent.CodingSession, client modutui.Client) {
	if session == nil {
		return
	}
	dialog := codetui.NewDialog(client, moduTUITerminalStatusTTL)
	models := session.GetAvailableModels()
	sort.Slice(models, func(i, j int) bool {
		if models[i].ProviderID == models[j].ProviderID {
			return models[i].ID < models[j].ID
		}
		return models[i].ProviderID < models[j].ProviderID
	})
	if len(models) == 0 {
		dialog.Post("no models configured")
		return
	}

	current := session.GetModel()
	options := make([]modutui.HumanPromptOption, 0, min(len(models), 9))
	for i, model := range models {
		if i >= 9 {
			break
		}
		label := model.Name
		if strings.TrimSpace(label) == "" {
			label = model.ID
		}
		label = fmt.Sprintf("%s (%s / %s)", label, model.ProviderID, model.ID)
		if current != nil && current.ProviderID == model.ProviderID && current.ID == model.ID {
			label = "* " + label
		}
		options = append(options, modutui.HumanPromptOption{
			Label: label,
			Value: model.ProviderID + "/" + model.ID,
		})
	}
	result, completed, err := dialog.RunFlow(ctx, codetui.Flow{
		ID: "model-select",
		Steps: []codetui.FlowStep{{
			ID:      "model",
			Kind:    codetui.FlowStepChoice,
			Title:   "Model",
			Body:    "Choose active model",
			Options: options,
		}},
	})
	if err != nil || !completed {
		dialog.Status("model unchanged")
		return
	}
	providerID, modelID, ok := strings.Cut(result.Value("model"), "/")
	if !ok || providerID == "" || modelID == "" {
		dialog.Post("invalid model selection")
		return
	}
	before := session.GetModel()
	if err := session.SetModelByID(providerID, modelID); err != nil {
		dialog.Post("error: " + err.Error())
		dialog.Status("model unchanged")
		return
	}
	after := session.GetModel()
	if before != nil && after != nil && before.ProviderID == after.ProviderID && before.ID == after.ID {
		dialog.Status("model unchanged")
		return
	}
	dialog.Status("model changed; context cleared")
}
