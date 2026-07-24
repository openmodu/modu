package modutui

import (
	"strings"
	"time"
)

// Node is a data-only rendering primitive. The closed set prevents business
// packages from smuggling terminal rendering behavior into the shared UI
// kernel. Add a standard node after a second real use case appears.
type Node interface {
	isNode()
}

type TextNode struct {
	Text string
}

func (TextNode) isNode() {}

type MarkdownNode struct {
	Text string
}

func (MarkdownNode) isNode() {}

type CodeNode struct {
	Language string
	Code     string
}

func (CodeNode) isNode() {}

type ThinkingNode struct {
	Text     string
	Expanded bool
}

func (ThinkingNode) isNode() {}

type ToolNode struct {
	Call       ToolCall
	Permission ToolPermissionState
	Expanded   bool
}

func (ToolNode) isNode() {}

// TableNode.Rows includes the header as the first row.
type TableNode struct {
	Rows [][]string
}

func (TableNode) isNode() {}

type ListItem struct {
	Label  string
	Detail string
}

type ListNode struct {
	Ordered bool
	Items   []ListItem
}

func (ListNode) isNode() {}

type KeyValue struct {
	Key   string
	Value string
}

type KeyValueNode struct {
	Items []KeyValue
}

func (KeyValueNode) isNode() {}

type ProgressNode struct {
	Label   string
	Current int
	Total   int
	Status  string
}

func (ProgressNode) isNode() {}

// Entry is one stable transcript item composed from standard nodes.
type Entry struct {
	ID    string
	Role  Role
	Nodes []Node
	Plain bool
}

// Action is a stable interaction identity with an opaque business payload.
// The UI kernel round-trips Payload unchanged and never interprets it.
type Action struct {
	ID      string
	Payload any
}

func cloneEntry(entry Entry) Entry {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Nodes = cloneNodes(entry.Nodes)
	return entry
}

func cloneNodes(nodes []Node) []Node {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		switch node := node.(type) {
		case TextNode:
			out = append(out, node)
		case MarkdownNode:
			out = append(out, node)
		case CodeNode:
			out = append(out, node)
		case ThinkingNode:
			out = append(out, node)
		case ToolNode:
			out = append(out, node)
		case TableNode:
			rows := make([][]string, len(node.Rows))
			for i := range node.Rows {
				rows[i] = append([]string(nil), node.Rows[i]...)
			}
			node.Rows = rows
			out = append(out, node)
		case ListNode:
			node.Items = append([]ListItem(nil), node.Items...)
			out = append(out, node)
		case KeyValueNode:
			node.Items = append([]KeyValue(nil), node.Items...)
			out = append(out, node)
		case ProgressNode:
			out = append(out, node)
		}
	}
	return out
}

func toolNodeFromEntry(entry Entry) (ToolNode, int, bool) {
	for i, node := range entry.Nodes {
		if tool, ok := node.(ToolNode); ok {
			return tool, i, true
		}
	}
	return ToolNode{}, -1, false
}

func thinkingNodeFromEntry(entry Entry) (ThinkingNode, int, bool) {
	for i, node := range entry.Nodes {
		if thinking, ok := node.(ThinkingNode); ok {
			return thinking, i, true
		}
	}
	return ThinkingNode{}, -1, false
}

func setEntryToolNode(entry *Entry, index int, node ToolNode) {
	if entry == nil || index < 0 || index >= len(entry.Nodes) {
		return
	}
	entry.Nodes[index] = node
}

func setEntryExpanded(entry *Entry, expanded bool) bool {
	if entry == nil {
		return false
	}
	if tool, index, ok := toolNodeFromEntry(*entry); ok {
		if tool.Call.NoCollapse {
			return false
		}
		tool.Expanded = expanded
		entry.Nodes[index] = tool
		return true
	}
	if thinking, index, ok := thinkingNodeFromEntry(*entry); ok {
		thinking.Expanded = expanded
		entry.Nodes[index] = thinking
		return true
	}
	return false
}

func entryExpanded(entry Entry) bool {
	if tool, _, ok := toolNodeFromEntry(entry); ok {
		return tool.Expanded
	}
	if thinking, _, ok := thinkingNodeFromEntry(entry); ok {
		return thinking.Expanded
	}
	return false
}

// Update is the host-facing state protocol. It is intentionally a small,
// closed set over existing UI regions rather than a layout DSL.
type Update interface {
	isUpdate()
}

// UpdateMsg is the single Bubble Tea transport envelope for host updates.
// Business code should use Client methods or Client.Apply instead of sending
// this message directly.
type UpdateMsg struct {
	Update Update
}

type AppendEntryUpdate struct{ Entry Entry }

func (AppendEntryUpdate) isUpdate() {}

type UpsertEntryUpdate struct{ Entry Entry }

func (UpsertEntryUpdate) isUpdate() {}

type RemoveEntryUpdate struct{ ID string }

func (RemoveEntryUpdate) isUpdate() {}

type ReplaceEntriesUpdate struct{ Entries []Entry }

func (ReplaceEntriesUpdate) isUpdate() {}

type ClearEntriesUpdate struct{}

func (ClearEntriesUpdate) isUpdate() {}

type SetTodoListUpdate struct{ Items []TodoItem }

func (SetTodoListUpdate) isUpdate() {}

type ShowPanelUpdate struct{ Panel Panel }

func (ShowPanelUpdate) isUpdate() {}

type RefreshPanelUpdate struct{ Panel Panel }

func (RefreshPanelUpdate) isUpdate() {}

type ClosePanelUpdate struct{ ID string }

func (ClosePanelUpdate) isUpdate() {}

type SetStatusUpdate struct {
	Status string
	TTL    time.Duration
}

func (SetStatusUpdate) isUpdate() {}

type SetBusyUpdate struct{ Busy bool }

func (SetBusyUpdate) isUpdate() {}

type SetFooterUpdate struct{ Footer string }

func (SetFooterUpdate) isUpdate() {}

func cloneUpdate(update Update) Update {
	switch update := update.(type) {
	case *AppendEntryUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *UpsertEntryUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *RemoveEntryUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *ReplaceEntriesUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *ClearEntriesUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *SetTodoListUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *ShowPanelUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *RefreshPanelUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *ClosePanelUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *SetStatusUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *SetBusyUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case *SetFooterUpdate:
		if update == nil {
			return nil
		}
		return cloneUpdate(*update)
	case AppendEntryUpdate:
		update.Entry = cloneEntry(update.Entry)
		return update
	case UpsertEntryUpdate:
		update.Entry = cloneEntry(update.Entry)
		return update
	case RemoveEntryUpdate:
		return update
	case ReplaceEntriesUpdate:
		entries := make([]Entry, len(update.Entries))
		for i := range update.Entries {
			entries[i] = cloneEntry(update.Entries[i])
		}
		update.Entries = entries
		return update
	case ClearEntriesUpdate:
		return update
	case SetTodoListUpdate:
		update.Items = append([]TodoItem(nil), update.Items...)
		return update
	case ShowPanelUpdate:
		update.Panel = clonePanel(update.Panel)
		return update
	case RefreshPanelUpdate:
		update.Panel = clonePanel(update.Panel)
		return update
	case ClosePanelUpdate:
		return update
	case SetStatusUpdate:
		return update
	case SetBusyUpdate:
		return update
	case SetFooterUpdate:
		return update
	default:
		return nil
	}
}

func clonePanel(panel Panel) Panel {
	panel.Lines = append([]string(nil), panel.Lines...)
	panel.Rows = append([]PanelRow(nil), panel.Rows...)
	panel.Shortcuts = append([]PanelShortcut(nil), panel.Shortcuts...)
	return panel
}
