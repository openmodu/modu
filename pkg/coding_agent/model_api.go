package coding_agent

import (
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

func resolveScopedModels(configured, explicit []string) []string {
	if len(explicit) > 0 {
		return append([]string(nil), explicit...)
	}
	return append([]string(nil), configured...)
}

// SetModel changes the active model.
func (s *CodingSession) SetModel(model *types.Model) {
	changed := s.model == nil || s.model.ProviderID != model.ProviderID || s.model.ID != model.ID
	s.model = model
	s.agent.SetModel(model)
	s.ctxMgr.SetModel(model)
	if s.promptBuilder != nil {
		s.promptBuilder.SetModel(model)
	}
	if changed {
		_ = s.ClearConversation()
	}
	s.refreshDynamicSystemPrompt()

	_ = s.sessionManager.Append(session.NewEntry(session.EntryTypeModelChange, "", session.ModelChangeData{
		Provider: model.ProviderID,
		ModelID:  model.ID,
	}))
	s.writeRuntimeState()
	s.emitSessionEvent(SessionEvent{
		Type:     SessionEventModelChange,
		Provider: model.ProviderID,
		ModelID:  model.ID,
	})
}

// SetModelByID changes the active model by provider and model ID.
func (s *CodingSession) SetModelByID(provider, modelID string) error {
	llmModel := providers.GetModel(provider, modelID)
	if llmModel == nil {
		return fmt.Errorf("model not found: %s/%s", provider, modelID)
	}
	s.SetModel(llmModel)
	return nil
}

// SetModelByName changes the active model by configured display name or model ID.
func (s *CodingSession) SetModelByName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("model name is required")
	}
	var matches []*types.Model
	for _, model := range s.GetAvailableModels() {
		if model.Name == name || model.ID == name || model.ProviderID+"/"+model.ID == name || model.ProviderID+":"+model.ID == name {
			matches = append(matches, model)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("model not found: %s", name)
	}
	if len(matches) > 1 {
		return fmt.Errorf("model %q is ambiguous; use /model <provider> <modelId>", name)
	}
	s.SetModel(matches[0])
	return nil
}

// CycleModel cycles to the next model in the scopedModels list.
// Returns the new model, or nil if no scoped models are configured.
func (s *CodingSession) CycleModel() *types.Model {
	if len(s.scopedModels) == 0 {
		return nil
	}

	currentID := s.model.ID
	nextIdx := 0
	for i, id := range s.scopedModels {
		if id == currentID {
			nextIdx = (i + 1) % len(s.scopedModels)
			break
		}
	}

	nextID := s.scopedModels[nextIdx]
	llmModel := providers.GetModel("", nextID)
	var model *types.Model
	if llmModel != nil {
		model = llmModel
	} else {
		model = &types.Model{ID: nextID, Name: nextID}
	}

	s.SetModel(model)
	return model
}

// GetModel returns the current model.
func (s *CodingSession) GetModel() *types.Model {
	return s.model
}

// GetAvailableModels returns all registered models from all providers.
func (s *CodingSession) GetAvailableModels() []*types.Model {
	if len(s.scopedModels) > 0 {
		result := make([]*types.Model, 0, len(s.scopedModels))
		for _, id := range s.scopedModels {
			if model := providers.GetModel("", id); model != nil {
				result = append(result, model)
			}
		}
		return result
	}
	var result []*types.Model
	for _, p := range providers.GetAllProviders() {
		result = append(result, providers.GetModels(p)...)
	}
	return result
}

// GetAllAvailableModels returns all registered models, ignoring session scope.
func (s *CodingSession) GetAllAvailableModels() []*types.Model {
	var result []*types.Model
	for _, p := range providers.GetAllProviders() {
		result = append(result, providers.GetModels(p)...)
	}
	return result
}

// GetScopedModelIDs returns the session-local model scope used for cycling.
func (s *CodingSession) GetScopedModelIDs() []string {
	return append([]string(nil), s.scopedModels...)
}

// SetScopedModelIDs updates the session-local model scope used for cycling.
func (s *CodingSession) SetScopedModelIDs(ids []string) {
	s.scopedModels = resolveScopedModels(nil, ids)
	s.writeRuntimeState()
}
