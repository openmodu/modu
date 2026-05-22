package tgbot

import (
	"context"
	"path/filepath"
	"testing"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

type fakeChannelContext struct {
	responses []string
}

func (f *fakeChannelContext) Respond(text string, _ bool) error {
	f.responses = append(f.responses, text)
	return nil
}
func (f *fakeChannelContext) ReplaceMessage(text string) error {
	f.responses = append(f.responses, text)
	return nil
}
func (f *fakeChannelContext) RespondInThread(text string) error {
	f.responses = append(f.responses, text)
	return nil
}
func (f *fakeChannelContext) SendCard(text string) (int, error) {
	f.responses = append(f.responses, text)
	return 1, nil
}
func (f *fakeChannelContext) EditCard(_ int, text string) error {
	f.responses = append(f.responses, text)
	return nil
}
func (f *fakeChannelContext) SetWorking(bool) error { return nil }
func (f *fakeChannelContext) UploadFile(string, string) error {
	return nil
}
func (f *fakeChannelContext) DeleteMessage() error         { return nil }
func (f *fakeChannelContext) ChatID() int64                { return 1 }
func (f *fakeChannelContext) MessageText() string          { return "" }
func (f *fakeChannelContext) MessageTS() string            { return "1" }
func (f *fakeChannelContext) SenderName() string           { return "tester" }
func (f *fakeChannelContext) Images() []types.ImageContent { return nil }

func TestTelegramQueuedInputRoutesFollowUpWhenActive(t *testing.T) {
	session := newTelegramTestSession(t)
	active := &activeTelegramPrompt{}
	ctx, cancel := context.WithCancel(context.Background())
	token := active.Set(cancel)
	defer active.Clear(token)
	defer cancel()
	ch := &fakeChannelContext{}

	if !handleTelegramQueuedInput(ch, session, active, "after this") {
		t.Fatal("expected active telegram prompt to queue follow-up")
	}
	steering, followUp := session.GetAgent().QueuedMessageCounts()
	if steering != 0 || followUp != 1 {
		t.Fatalf("expected steering=0 followUp=1, got steering=%d followUp=%d", steering, followUp)
	}
	if len(ch.responses) != 1 || ch.responses[0] != "queued follow up" {
		t.Fatalf("expected follow-up ack, got %#v", ch.responses)
	}
	if ctx.Err() != nil {
		t.Fatalf("follow-up should not cancel active turn: %v", ctx.Err())
	}
}

func TestTelegramQueuedInputRoutesSteerAndCancels(t *testing.T) {
	session := newTelegramTestSession(t)
	active := &activeTelegramPrompt{}
	ctx, cancel := context.WithCancel(context.Background())
	token := active.Set(cancel)
	defer active.Clear(token)
	ch := &fakeChannelContext{}

	if !handleTelegramQueuedInput(ch, session, active, "/s change direction") {
		t.Fatal("expected /s to queue steer")
	}
	steering, followUp := session.GetAgent().QueuedMessageCounts()
	if steering != 1 || followUp != 0 {
		t.Fatalf("expected steering=1 followUp=0, got steering=%d followUp=%d", steering, followUp)
	}
	if ctx.Err() == nil {
		t.Fatal("expected steer to cancel active turn")
	}
	if len(ch.responses) != 1 || ch.responses[0] != "queued steer" {
		t.Fatalf("expected steer ack, got %#v", ch.responses)
	}
}

func TestTelegramQueueCommandsRequireActiveTask(t *testing.T) {
	session := newTelegramTestSession(t)
	ch := &fakeChannelContext{}

	if !handleTelegramQueuedInput(ch, session, &activeTelegramPrompt{}, "/f later") {
		t.Fatal("expected explicit follow-up command to be handled")
	}
	if got := session.GetAgent().QueuedMessageCount(); got != 0 {
		t.Fatalf("expected no queued messages without active task, got %d", got)
	}
	if len(ch.responses) != 1 || ch.responses[0] != "no active task for follow up" {
		t.Fatalf("expected inactive follow-up response, got %#v", ch.responses)
	}

	ch.responses = nil
	if !handleTelegramQueuedInput(ch, session, &activeTelegramPrompt{}, "/steer") {
		t.Fatal("expected empty steer command to be handled")
	}
	if len(ch.responses) != 1 || ch.responses[0] != "steer requires a message" {
		t.Fatalf("expected empty steer response, got %#v", ch.responses)
	}
}

func TestTelegramQueueCommandArg(t *testing.T) {
	if arg, ok := telegramQueueCommandArg("/followup next", "/followup", "/f"); !ok || arg != "next" {
		t.Fatalf("expected /followup arg, got arg=%q ok=%v", arg, ok)
	}
	if arg, ok := telegramQueueCommandArg("/f", "/followup", "/f"); !ok || arg != "" {
		t.Fatalf("expected empty /f arg, got arg=%q ok=%v", arg, ok)
	}
	if _, ok := telegramQueueCommandArg("/foobar next", "/followup", "/f"); ok {
		t.Fatal("expected unrelated command not to match")
	}
}

func newTelegramTestSession(t *testing.T) *coding_agent.CodingSession {
	t.Helper()
	dir := t.TempDir()
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:      dir,
		AgentDir: filepath.Join(dir, ".coding_agent"),
		Model: &types.Model{
			ID:         "test-model",
			Name:       "Test Model",
			ProviderID: "test",
		},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatalf("new coding session: %v", err)
	}
	return session
}
