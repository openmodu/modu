package modutui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestEntryRendersStandardNodes(t *testing.T) {
	model := NewModel(Options{
		Width:  72,
		Height: 20,
		InitialEntries: []Entry{{
			ID:   "report",
			Role: RoleAssistant,
			Nodes: []Node{
				MarkdownNode{Text: "## Result"},
				KeyValueNode{Items: []KeyValue{{Key: "status", Value: "running"}}},
				ListNode{Items: []ListItem{{Label: "inspect"}, {Label: "test", Detail: "pending"}}},
				ProgressNode{Label: "work", Current: 2, Total: 4, Status: "running"},
				CodeNode{Language: "go", Code: "fmt.Println(\"ok\")"},
				TableNode{Rows: [][]string{{"name", "state"}, {"modu", "ready"}}},
			},
		}},
	})

	rendered := ansi.Strip(strings.Join(model.Lines(), "\n"))
	for _, want := range []string{
		"Result",
		"status: running",
		"• inspect",
		"test  pending",
		"work",
		"2/4",
		"fmt.Println",
		"name",
		"ready",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("standard entry missing %q:\n%s", want, rendered)
		}
	}
}

func TestEntryUpsertAndRemoveUseStableID(t *testing.T) {
	var model tea.Model = NewModel(Options{
		Width:  50,
		Height: 12,
		InitialEntries: []Entry{{
			ID:    "job",
			Role:  RoleAssistant,
			Nodes: []Node{TextNode{Text: "old state"}},
		}},
	})

	model, _ = model.Update(UpdateMsg{Update: UpsertEntryUpdate{Entry: Entry{
		ID:    "job",
		Role:  RoleAssistant,
		Nodes: []Node{TextNode{Text: "new state"}},
	}}})
	current := model.(Model)
	rendered := ansi.Strip(strings.Join(current.Lines(), "\n"))
	if strings.Contains(rendered, "old state") || strings.Count(rendered, "new state") != 1 {
		t.Fatalf("upsert should replace one stable entry:\n%s", rendered)
	}

	model, _ = current.Update(UpdateMsg{Update: RemoveEntryUpdate{ID: "job"}})
	current = model.(Model)
	if rendered := ansi.Strip(strings.Join(current.Lines(), "\n")); strings.Contains(rendered, "new state") {
		t.Fatalf("removed entry still rendered:\n%s", rendered)
	}
}

func TestModelAppliesStandardHostUpdates(t *testing.T) {
	var model tea.Model = NewModel(Options{Width: 60, Height: 14})
	apply := func(update Update) Model {
		t.Helper()
		next, _ := model.Update(UpdateMsg{Update: update})
		model = next
		return next.(Model)
	}

	current := apply(AppendEntryUpdate{Entry: Entry{
		ID:    "first",
		Role:  RoleAssistant,
		Nodes: []Node{TextNode{Text: "first entry"}},
	}})
	if len(current.entries) != 1 {
		t.Fatalf("append produced %d messages", len(current.entries))
	}

	current = apply(ReplaceEntriesUpdate{Entries: []Entry{{
		ID:    "replacement",
		Role:  RoleAssistant,
		Nodes: []Node{MarkdownNode{Text: "replacement entry"}},
	}}})
	if len(current.entries) != 1 || current.entries[0].ID != "replacement" {
		t.Fatalf("replace entries = %#v", current.entries)
	}

	current = apply(SetBusyUpdate{Busy: true})
	current = apply(SetTodoListUpdate{Items: []TodoItem{{Content: "verify", Status: "in_progress"}}})
	current = apply(SetStatusUpdate{Status: "running"})
	current = apply(SetFooterUpdate{Footer: "model · cwd"})
	if !current.busy || current.status != "running" || current.footer != "model · cwd" {
		t.Fatalf("chrome state = busy:%v status:%q footer:%q", current.busy, current.status, current.footer)
	}
	if len(current.todos) != 1 || current.todos[0].Content != "verify" {
		t.Fatalf("todos = %#v", current.todos)
	}

	current = apply(ShowPanelUpdate{Panel: Panel{ID: "workflow", Title: "Workflow"}})
	if current.panel == nil || current.panel.ID != "workflow" {
		t.Fatalf("opened panel = %#v", current.panel)
	}
	current = apply(RefreshPanelUpdate{Panel: Panel{ID: "workflow", Title: "Updated"}})
	if current.panel == nil || current.panel.Title != "Updated" {
		t.Fatalf("refreshed panel = %#v", current.panel)
	}
	current = apply(ClosePanelUpdate{ID: "workflow"})
	if current.panel != nil {
		t.Fatalf("closed panel = %#v", current.panel)
	}

	current = apply(ClearEntriesUpdate{})
	if len(current.entries) != 0 {
		t.Fatalf("clear left messages = %#v", current.entries)
	}
}

func TestToolNodeUsesExistingToolLifecycle(t *testing.T) {
	start := Entry{
		ID:   "call-1",
		Role: RoleAssistant,
		Nodes: []Node{ToolNode{Call: ToolCall{
			ID:      "call-1",
			Name:    "bash",
			Summary: "run tests",
			Input:   "go test ./...",
		}}},
	}
	var model tea.Model = NewModel(Options{
		Width:          60,
		Height:         12,
		InitialEntries: []Entry{start},
	})
	finish := Entry{
		ID:   "call-1",
		Role: RoleAssistant,
		Nodes: []Node{ToolNode{Call: ToolCall{
			ID:      "call-1",
			Name:    "bash",
			Summary: "tests passed",
			Output:  "ok",
			Done:    true,
		}}},
	}
	model, _ = model.Update(UpdateMsg{Update: AppendEntryUpdate{Entry: finish}})
	current := model.(Model)

	if len(current.entries) != 1 {
		t.Fatalf("tool lifecycle created %d messages, want 1", len(current.entries))
	}
	node, _, ok := toolNodeFromEntry(current.entries[0])
	if !ok || !node.Call.Done || node.Call.Output != "ok" {
		t.Fatalf("merged tool message = %#v", current.entries[0])
	}
}

func TestPanelEmitsStructuredAction(t *testing.T) {
	actions := make(chan PanelAction, 1)
	var model tea.Model = NewModel(Options{
		Width:  50,
		Height: 12,
		IntentHandler: func(intent Intent) {
			if action, ok := intent.(PanelActionIntent); ok {
				actions <- action.Action
			}
		},
	})
	model, _ = model.Update(UpdateMsg{Update: ShowPanelUpdate{Panel: Panel{
		ID: "jobs",
		Rows: []PanelRow{{
			Label:  "retry",
			Action: Action{ID: "job.retry", Payload: "job-42"},
		}},
	}}})
	model = updateAndRunImmediate(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})

	action := <-actions
	if action.Action.ID != "job.retry" || action.Action.Payload != "job-42" {
		t.Fatalf("structured action = %#v", action)
	}
	if action.PanelID != "jobs" || action.Index != 0 {
		t.Fatalf("panel context = %#v", action)
	}
}
