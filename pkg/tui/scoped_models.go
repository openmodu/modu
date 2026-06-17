package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/openmodu/modu/pkg/types"
)

func (b *bubbleTUI) runScopedModelsCommand(args string) tea.Cmd {
	if b.session == nil {
		b.model.setTransientStatus("no session")
		return nil
	}
	fields := strings.Fields(args)
	if len(fields) == 0 || fields[0] == "list" || fields[0] == "ls" {
		return b.appendSystemSection("Scoped Models", b.scopedModelsSummary())
	}
	switch fields[0] {
	case "edit":
		b.openScopedModelsSelect()
		return nil
	case "clear", "all":
		if err := b.saveScopedModelIDs(nil); err != nil {
			return b.appendSystemSection("Scoped Models", "error: "+err.Error())
		}
		return b.appendSystemSection("Scoped Models", "scope: all models")
	case "set":
		return b.updateScopedModelsFromArgs("set", fields[1:])
	case "add":
		return b.updateScopedModelsFromArgs("add", fields[1:])
	case "remove", "rm":
		return b.updateScopedModelsFromArgs("remove", fields[1:])
	default:
		return b.appendSystemSection("Scoped Models", scopedModelsUsage())
	}
}

func (b *bubbleTUI) updateScopedModelsFromArgs(action string, args []string) tea.Cmd {
	if len(args) == 0 {
		return b.appendSystemSection("Scoped Models", scopedModelsUsage())
	}
	resolved, problems := b.resolveScopedModelArgs(args)
	if len(problems) > 0 {
		return b.appendSystemSection("Scoped Models", strings.Join(problems, "\n"))
	}
	current := make(map[string]bool)
	for _, id := range b.session.GetScopedModelIDs() {
		current[id] = true
	}
	if len(current) == 0 && action == "add" {
		for _, model := range b.session.GetAllAvailableModels() {
			current[model.ID] = true
		}
	}
	switch action {
	case "set":
		current = map[string]bool{}
		for _, model := range resolved {
			current[model.ID] = true
		}
	case "add":
		for _, model := range resolved {
			current[model.ID] = true
		}
	case "remove":
		for _, model := range resolved {
			delete(current, model.ID)
		}
	}
	ids := sortedScopedIDs(current, b.session.GetAllAvailableModels())
	if len(ids) == len(b.session.GetAllAvailableModels()) {
		ids = nil
	}
	if err := b.saveScopedModelIDs(ids); err != nil {
		return b.appendSystemSection("Scoped Models", "error: "+err.Error())
	}
	return b.appendSystemSection("Scoped Models", b.scopedModelsSummary())
}

func (b *bubbleTUI) scopedModelsSummary() string {
	all := b.session.GetAllAvailableModels()
	sortTUIModels(all, b.session.GetModel())
	scoped := make(map[string]bool)
	for _, id := range b.session.GetScopedModelIDs() {
		scoped[id] = true
	}
	var lines []string
	if len(scoped) == 0 {
		lines = append(lines, "scope: all models")
	} else {
		lines = append(lines, fmt.Sprintf("scope: %d model(s)", len(scoped)))
	}
	for _, model := range all {
		marker := " "
		if len(scoped) == 0 || scoped[model.ID] {
			marker = "*"
		}
		lines = append(lines, fmt.Sprintf("%s %s", marker, scopedModelLabel(model)))
	}
	lines = append(lines, "")
	lines = append(lines, scopedModelsUsage())
	return strings.Join(lines, "\n")
}

func scopedModelsUsage() string {
	return strings.Join([]string{
		"commands:",
		"  /scoped-models list",
		"  /scoped-models set <model> [model...]",
		"  /scoped-models add <model> [model...]",
		"  /scoped-models remove <model> [model...]",
		"  /scoped-models clear",
		"  /scoped-models edit",
	}, "\n")
}

func (b *bubbleTUI) resolveScopedModelArgs(args []string) ([]*types.Model, []string) {
	var resolved []*types.Model
	var problems []string
	for _, arg := range args {
		model, err := b.resolveScopedModelArg(arg)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		resolved = append(resolved, model)
	}
	return resolved, problems
}

func (b *bubbleTUI) resolveScopedModelArg(arg string) (*types.Model, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, fmt.Errorf("empty model target")
	}
	var matches []*types.Model
	for _, model := range b.session.GetAllAvailableModels() {
		if scopedModelMatches(model, arg) {
			matches = append(matches, model)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("model not found: %s", arg)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("model %q is ambiguous; use provider/model", arg)
	}
	return matches[0], nil
}

func scopedModelMatches(model *types.Model, target string) bool {
	return target == model.ID ||
		target == model.Name ||
		target == model.ProviderID+"/"+model.ID ||
		target == model.ProviderID+":"+model.ID
}

func scopedModelLabel(model *types.Model) string {
	name := strings.TrimSpace(model.Name)
	if name == "" {
		name = model.ID
	}
	return fmt.Sprintf("%s (%s / %s)", name, model.ProviderID, model.ID)
}

func sortedScopedIDs(selected map[string]bool, models []*types.Model) []string {
	ids := make([]string, 0, len(selected))
	seen := map[string]bool{}
	for _, model := range models {
		if selected[model.ID] && !seen[model.ID] {
			ids = append(ids, model.ID)
			seen[model.ID] = true
		}
	}
	sort.Strings(ids)
	return ids
}

func (b *bubbleTUI) saveScopedModelIDs(ids []string) error {
	b.session.SetScopedModelIDs(ids)
	if b.commandHooks.SaveScopedModels != nil {
		return b.commandHooks.SaveScopedModels(ids)
	}
	return nil
}
