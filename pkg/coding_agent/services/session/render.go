package session

import (
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

// IsVisibleEntry reports whether an entry should appear in a rendered session
// tree (messages, branch summaries, compactions, model changes).
func IsVisibleEntry(entry SessionEntry) bool {
	switch entry.Type {
	case EntryTypeMessage, EntryTypeBranchSummary, EntryTypeCompaction, EntryTypeModelChange:
		return true
	default:
		return false
	}
}

// NearestVisibleParent walks up the parent chain from parentID and returns the
// first ancestor present in visible, or "" if none.
func NearestVisibleParent(parentID string, lookup map[string]SessionEntry, visible map[string]struct{}) string {
	for parentID != "" {
		if _, ok := visible[parentID]; ok {
			return parentID
		}
		parent, ok := lookup[parentID]
		if !ok {
			return ""
		}
		parentID = parent.ParentID
	}
	return ""
}

// EntryRole returns the message role for a message entry, or "".
func EntryRole(entry SessionEntry) string {
	if entry.Type != EntryTypeMessage {
		return ""
	}
	switch data := entry.Data.(type) {
	case MessageData:
		return string(data.Role)
	case map[string]any:
		role, _ := data["role"].(string)
		return role
	default:
		return ""
	}
}

// TreeNodeLabel returns the label to show for a tree node: the explicit label
// if set, otherwise a derived label for branch-summary entries.
func TreeNodeLabel(entry SessionEntry, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if entry.Type != EntryTypeBranchSummary {
		return ""
	}
	switch data := entry.Data.(type) {
	case BranchSummaryData:
		return branchSummaryLabel(data.FromID)
	case map[string]any:
		fromID, _ := data["fromId"].(string)
		return branchSummaryLabel(fromID)
	default:
		return ""
	}
}

func branchSummaryLabel(fromID string) string {
	fromID = strings.TrimSpace(fromID)
	if fromID == "" {
		return ""
	}
	if len(fromID) > 8 {
		fromID = fromID[:8]
	}
	return "from #" + fromID
}

// EntryPreview returns a short text preview of an entry's content, handling both
// typed entry data and its JSON-decoded map form.
func EntryPreview(entry SessionEntry) string {
	switch data := entry.Data.(type) {
	case MessageData:
		return previewContent(data.Content)
	case BranchSummaryData:
		return data.Summary
	case CompactionData:
		return data.Summary
	case ModelChangeData:
		if data.Provider != "" {
			return data.Provider + "/" + data.ModelID
		}
		return data.ModelID
	case map[string]any:
		if summary, _ := data["summary"].(string); summary != "" {
			return summary
		}
		if content, ok := data["content"]; ok {
			return previewContent(content)
		}
		if model, _ := data["modelId"].(string); model != "" {
			provider, _ := data["provider"].(string)
			if provider != "" {
				return provider + "/" + model
			}
			return model
		}
	}
	return ""
}

func previewContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []types.ContentBlock:
		var parts []string
		for _, block := range value {
			if text, ok := block.(*types.TextContent); ok && text != nil && text.Text != "" {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, " ")
	case []any:
		var parts []string
		for _, block := range value {
			if m, ok := block.(map[string]any); ok {
				text, _ := m["text"].(string)
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(value)
	}
}
