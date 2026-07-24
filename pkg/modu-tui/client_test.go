package modutui

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClientDispatchesTypedUpdates(t *testing.T) {
	var messages []any
	client := NewClient(func(msg any) {
		messages = append(messages, msg)
	})

	client.AppendEntry(Entry{Nodes: []Node{TextNode{Text: "hello"}}})
	client.SetStatus("running", time.Second)
	client.SetBusy(true)
	client.OpenPanel(Panel{ID: "workflow"})
	client.ClosePanel("workflow")

	if len(messages) != 5 {
		t.Fatalf("dispatched messages = %d, want 5", len(messages))
	}
	if _, ok := clientUpdate(t, messages[0]).(AppendEntryUpdate); !ok {
		t.Fatalf("message 0 = %#v, want AppendEntryUpdate", messages[0])
	}
	if status, ok := clientUpdate(t, messages[1]).(SetStatusUpdate); !ok || status.Status != "running" || status.TTL != time.Second {
		t.Fatalf("message 1 = %#v, want running status", messages[1])
	}
	if busy, ok := clientUpdate(t, messages[2]).(SetBusyUpdate); !ok || !busy.Busy {
		t.Fatalf("message 2 = %#v, want busy", messages[2])
	}
	if panel, ok := clientUpdate(t, messages[3]).(ShowPanelUpdate); !ok || panel.Panel.ID != "workflow" {
		t.Fatalf("message 3 = %#v, want workflow panel", messages[3])
	}
	if close, ok := clientUpdate(t, messages[4]).(ClosePanelUpdate); !ok || close.ID != "workflow" {
		t.Fatalf("message 4 = %#v, want workflow close", messages[4])
	}
}

func TestClientAskChoiceOwnsResponseChannel(t *testing.T) {
	sent := make(chan any, 2)
	client := NewClient(func(msg any) {
		sent <- msg
	})
	result := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := client.AskChoice(context.Background(), HumanPromptRequest{ID: "choice"})
		result <- value
		errs <- err
	}()

	msg := <-sent
	request, ok := msg.(RequestHumanPromptMsg)
	if !ok {
		t.Fatalf("sent %T, want RequestHumanPromptMsg", msg)
	}
	request.Respond <- "selected"
	if got := <-result; got != "selected" {
		t.Fatalf("choice = %q, want selected", got)
	}
	if err := <-errs; err != nil {
		t.Fatalf("AskChoice error: %v", err)
	}
}

func TestClientAskTextCancelsPromptWithContext(t *testing.T) {
	sent := make(chan any, 2)
	client := NewClient(func(msg any) {
		sent <- msg
	})
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := client.AskText(ctx, HumanTextRequest{ID: "secret"})
		errs <- err
	}()

	if _, ok := (<-sent).(RequestHumanTextMsg); !ok {
		t.Fatal("first client message should request human text")
	}
	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("AskText error = %v, want context.Canceled", err)
	}
	if cancelMsg, ok := (<-sent).(CancelHumanTextMsg); !ok || cancelMsg.ID != "secret" {
		t.Fatalf("cancel message = %#v, want secret prompt cancellation", cancelMsg)
	}
}

func TestClientDialogFailsWhenUnavailable(t *testing.T) {
	client := Client{}

	if _, err := client.AskChoice(context.Background(), HumanPromptRequest{}); !errors.Is(err, ErrClientUnavailable) {
		t.Fatalf("AskChoice error = %v, want ErrClientUnavailable", err)
	}
	if _, err := client.AskText(context.Background(), HumanTextRequest{}); !errors.Is(err, ErrClientUnavailable) {
		t.Fatalf("AskText error = %v, want ErrClientUnavailable", err)
	}
	if _, err := client.AskToolApproval(context.Background(), ToolApprovalRequest{}); !errors.Is(err, ErrClientUnavailable) {
		t.Fatalf("AskToolApproval error = %v, want ErrClientUnavailable", err)
	}
}

func TestClientApplyDispatchesStandardUpdates(t *testing.T) {
	var messages []any
	client := NewClient(func(message any) {
		messages = append(messages, message)
	})

	entry := Entry{
		ID:    "job",
		Role:  RoleAssistant,
		Nodes: []Node{TableNode{Rows: [][]string{{"name"}, {"modu"}}}},
	}
	client.Apply(AppendEntryUpdate{Entry: entry})
	client.Apply(UpsertEntryUpdate{Entry: entry})
	client.Apply(RemoveEntryUpdate{ID: "job"})
	client.Apply(SetTodoListUpdate{Items: []TodoItem{{Content: "test", Status: "pending"}}})
	client.Apply(ShowPanelUpdate{Panel: Panel{ID: "jobs"}})
	client.Apply(SetStatusUpdate{Status: "running", TTL: time.Second})
	client.Apply(SetBusyUpdate{Busy: true})
	client.Apply(&SetFooterUpdate{Footer: "model · cwd"})

	if len(messages) != 8 {
		t.Fatalf("messages = %d, want 8", len(messages))
	}
	if _, ok := clientUpdate(t, messages[0]).(AppendEntryUpdate); !ok {
		t.Fatalf("append update sent %T", messages[0])
	}
	if _, ok := clientUpdate(t, messages[1]).(UpsertEntryUpdate); !ok {
		t.Fatalf("upsert update sent %T", messages[1])
	}
	if _, ok := clientUpdate(t, messages[2]).(RemoveEntryUpdate); !ok {
		t.Fatalf("remove update sent %T", messages[2])
	}
	if _, ok := clientUpdate(t, messages[3]).(SetTodoListUpdate); !ok {
		t.Fatalf("todo update sent %T", messages[3])
	}
	if _, ok := clientUpdate(t, messages[4]).(ShowPanelUpdate); !ok {
		t.Fatalf("panel update sent %T", messages[4])
	}
	if _, ok := clientUpdate(t, messages[5]).(SetStatusUpdate); !ok {
		t.Fatalf("status update sent %T", messages[5])
	}
	if _, ok := clientUpdate(t, messages[6]).(SetBusyUpdate); !ok {
		t.Fatalf("busy update sent %T", messages[6])
	}
	if footer, ok := clientUpdate(t, messages[7]).(SetFooterUpdate); !ok || footer.Footer != "model · cwd" {
		t.Fatalf("footer update sent %#v", messages[7])
	}

	table := entry.Nodes[0].(TableNode)
	table.Rows[1][0] = "changed"
	entry.Nodes[0] = table
	appended := clientUpdate(t, messages[0]).(AppendEntryUpdate).Entry.Nodes[0].(TableNode)
	if appended.Rows[1][0] != "modu" {
		t.Fatalf("client retained caller-owned node data: %#v", appended.Rows)
	}
}

func clientUpdate(t *testing.T, message any) Update {
	t.Helper()
	envelope, ok := message.(UpdateMsg)
	if !ok {
		t.Fatalf("message = %T, want UpdateMsg", message)
	}
	return envelope.Update
}
