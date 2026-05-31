package tui

import (
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

const modelSelectVisibleRows = 10

func currentModelChoiceIndex(choices []*types.Model, current *types.Model) int {
	if current == nil {
		return 0
	}
	for i, choice := range choices {
		if choice.ProviderID == current.ProviderID && choice.ID == current.ID {
			return i
		}
	}
	return 0
}
func modelChoiceLine(model *types.Model, selected, active bool) string {
	prefix := "  "
	if selected {
		prefix = "❯ "
	}
	marker := " "
	if active {
		marker = "*"
	}
	name := model.Name
	if strings.TrimSpace(name) == "" {
		name = model.ID
	}
	return fmt.Sprintf("%s%s %s (%s / %s)", prefix, marker, name, model.ProviderID, model.ID)
}

func sameTUIModel(a, b *types.Model) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ProviderID == b.ProviderID && a.ID == b.ID
}
