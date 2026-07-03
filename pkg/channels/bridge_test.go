package channels

import (
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestMessageDeduper(t *testing.T) {
	deduper := newMessageDeduper(2)
	if deduper.Seen("a") {
		t.Fatal("first key should not be seen")
	}
	if !deduper.Seen("a") {
		t.Fatal("duplicate key should be seen")
	}
	if deduper.Seen("") {
		t.Fatal("empty key should never be considered seen")
	}
	if deduper.Seen("b") {
		t.Fatal("first second key should not be seen")
	}
	if deduper.Seen("c") {
		t.Fatal("first third key should not be seen")
	}
	if deduper.Seen("a") {
		t.Fatal("oldest key should be evicted")
	}
}

func TestChannelMessageKey(t *testing.T) {
	ctx := fakeChannelContext{chatID: 123, messageTS: "om_1"}
	got := channelMessageKey("feishu", ctx)
	want := "feishu:123:om_1"
	if got != want {
		t.Fatalf("channelMessageKey = %q, want %q", got, want)
	}

	sameMessageDifferentChat := channelMessageKey("feishu", fakeChannelContext{chatID: 456, messageTS: "om_1"})
	if sameMessageDifferentChat == got {
		t.Fatal("different chats must produce different keys")
	}
	if key := channelMessageKey("feishu", fakeChannelContext{chatID: 123}); key != "" {
		t.Fatalf("empty message ts key = %q, want empty", key)
	}
}

func TestQueueCommandArg(t *testing.T) {
	arg, ok := queueCommandArg("/followup next", "/followup", "/f")
	if !ok || arg != "next" {
		t.Fatalf("queueCommandArg = (%q, %v), want (next, true)", arg, ok)
	}
	arg, ok = queueCommandArg("/f", "/followup", "/f")
	if !ok || arg != "" {
		t.Fatalf("queueCommandArg short empty = (%q, %v), want empty true", arg, ok)
	}
	if _, ok := queueCommandArg("/foobar next", "/followup", "/f"); ok {
		t.Fatal("unexpected match for unrelated command")
	}
}

func TestLastAssistantTextAfter(t *testing.T) {
	messages := []types.AgentMessage{
		types.UserMessage{Role: types.RoleUser, Content: "hi"},
		types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "old"},
			},
		},
		types.UserMessage{Role: types.RoleUser, Content: "next"},
		types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "new"},
			},
			Usage: types.AgentUsage{TotalTokens: 12},
		},
	}

	got := lastAssistantTextAfter(messages, 2)
	want := "new\n\n12 tokens"
	if got != want {
		t.Fatalf("lastAssistantTextAfter = %q, want %q", got, want)
	}
}

type fakeChannelContext struct {
	chatID    int64
	messageTS string
}

func (f fakeChannelContext) Respond(string, bool) error      { return nil }
func (f fakeChannelContext) ReplaceMessage(string) error     { return nil }
func (f fakeChannelContext) RespondInThread(string) error    { return nil }
func (f fakeChannelContext) SendCard(string) (int, error)    { return 0, nil }
func (f fakeChannelContext) EditCard(int, string) error      { return nil }
func (f fakeChannelContext) SetWorking(bool) error           { return nil }
func (f fakeChannelContext) UploadFile(string, string) error { return nil }
func (f fakeChannelContext) DeleteMessage() error            { return nil }
func (f fakeChannelContext) ChatID() int64                   { return f.chatID }
func (f fakeChannelContext) MessageText() string             { return "" }
func (f fakeChannelContext) MessageTS() string               { return f.messageTS }
func (f fakeChannelContext) SenderName() string              { return "" }
func (f fakeChannelContext) Images() []types.ImageContent    { return nil }
