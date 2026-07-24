package tui

import (
	"errors"
	"testing"
	"time"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func TestDialogPostResultAndStatus(t *testing.T) {
	var sent []any
	dialog := NewDialog(modutui.NewClient(func(message any) {
		sent = append(sent, message)
	}), 3*time.Second)

	dialog.PostResult("Channel", "saved", errors.New("warning"))
	dialog.Status("done")

	message, ok := sent[0].(modutui.UpdateMsg)
	entry, entryOK := message.Update.(modutui.AppendEntryUpdate)
	text, textOK := entry.Entry.Nodes[0].(modutui.TextNode)
	if !ok || !entryOK || !textOK || text.Text != "Channel\n\nsaved\nerror: warning" {
		t.Fatalf("result message = %#v", sent[0])
	}
	statusMessage, ok := sent[1].(modutui.UpdateMsg)
	status, statusOK := statusMessage.Update.(modutui.SetStatusUpdate)
	if !ok || !statusOK || status.Status != "done" || status.TTL != 3*time.Second {
		t.Fatalf("status message = %#v", sent[1])
	}
}
