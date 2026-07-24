package modutui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
)

var ErrClientUnavailable = errors.New("modu-tui client is unavailable")

// Client is the host-facing API for updating a running TUI. It keeps concrete
// Bubble Tea messages and dialog response channels out of business flows.
//
// send must be safe to call from goroutines. A Bubble Tea program can be
// adapted with:
//
//	client := modutui.NewClient(func(msg any) { program.Send(msg) })
type Client struct {
	send func(any)
}

func NewClient(send func(any)) Client {
	return Client{send: send}
}

func (c Client) AppendEntry(entry Entry) {
	c.Apply(AppendEntryUpdate{Entry: entry})
}

func (c Client) UpsertEntry(entry Entry) {
	c.Apply(UpsertEntryUpdate{Entry: entry})
}

func (c Client) RemoveEntry(id string) {
	c.Apply(RemoveEntryUpdate{ID: id})
}

func (c Client) ReplaceEntries(entries []Entry) {
	c.Apply(ReplaceEntriesUpdate{Entries: entries})
}

func (c Client) ClearTranscript() {
	c.Apply(ClearEntriesUpdate{})
}

func (c Client) SetStatus(status string, ttl time.Duration) {
	c.Apply(SetStatusUpdate{Status: status, TTL: ttl})
}

func (c Client) SetFooter(footer string) {
	c.Apply(SetFooterUpdate{Footer: footer})
}

func (c Client) SetBusy(busy bool) {
	c.Apply(SetBusyUpdate{Busy: busy})
}

func (c Client) SetTodos(todos []TodoItem) {
	c.Apply(SetTodoListUpdate{Items: todos})
}

func (c Client) OpenPanel(panel Panel) {
	c.Apply(ShowPanelUpdate{Panel: panel})
}

func (c Client) RefreshPanel(panel Panel) {
	c.Apply(RefreshPanelUpdate{Panel: panel})
}

func (c Client) ClosePanel(panelID string) {
	c.Apply(ClosePanelUpdate{ID: panelID})
}

func (c Client) Quit() {
	c.dispatch(tea.Quit())
}

func (c Client) Apply(update Update) {
	if cloned := cloneUpdate(update); cloned != nil {
		c.dispatch(UpdateMsg{Update: cloned})
	}
}

func (c Client) AskChoice(ctx context.Context, request HumanPromptRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.send == nil {
		return "", ErrClientUnavailable
	}
	response := make(chan string, 1)
	c.dispatch(RequestHumanPromptMsg{Request: request, Respond: response})
	select {
	case value := <-response:
		return value, nil
	case <-ctx.Done():
		c.dispatch(CancelHumanPromptMsg{ID: request.ID})
		return "", ctx.Err()
	}
}

func (c Client) AskText(ctx context.Context, request HumanTextRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.send == nil {
		return "", ErrClientUnavailable
	}
	response := make(chan string, 1)
	c.dispatch(RequestHumanTextMsg{Request: request, Respond: response})
	select {
	case value := <-response:
		return value, nil
	case <-ctx.Done():
		c.dispatch(CancelHumanTextMsg{ID: request.ID})
		return "", ctx.Err()
	}
}

func (c Client) AskToolApproval(ctx context.Context, request ToolApprovalRequest) (ToolApprovalDecision, error) {
	if err := ctx.Err(); err != nil {
		return ToolApprovalDeny, err
	}
	if c.send == nil {
		return ToolApprovalDeny, ErrClientUnavailable
	}
	response := make(chan ToolApprovalDecision, 1)
	c.dispatch(RequestToolApprovalMsg{Request: request, Respond: response})
	select {
	case decision := <-response:
		return decision, nil
	case <-ctx.Done():
		c.dispatch(CancelToolApprovalMsg{ID: request.ID})
		return ToolApprovalDeny, ctx.Err()
	}
}

func (c Client) dispatch(msg any) {
	if c.send != nil {
		c.send(msg)
	}
}
