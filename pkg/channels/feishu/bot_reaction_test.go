package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/openmodu/modu/pkg/channels"
)

func TestHandleMessageEventAddsFlashReactionBeforeDispatch(t *testing.T) {
	var reactionAdded atomic.Bool
	reactionBody := make(chan string, 1)
	server := newReactionTestServer(t, func(body string) (int, string) {
		reactionBody <- body
		reactionAdded.Store(true)
		return 0, "success"
	})
	defer server.Close()

	dispatched := make(chan bool, 1)
	bot := newReactionTestBot(t, server, func(context.Context, channels.ChannelContext) {
		dispatched <- reactionAdded.Load()
	})

	if err := bot.handleMessageEvent(context.Background(), textMessageEvent("source-id", "hello")); err != nil {
		t.Fatalf("handleMessageEvent() error = %v", err)
	}
	select {
	case reactionWasAdded := <-dispatched:
		if !reactionWasAdded {
			t.Fatal("message handler ran before the acknowledgement reaction was added")
		}
	case <-time.After(time.Second):
		t.Fatal("message handler was not called")
	}

	var body struct {
		ReactionType struct {
			EmojiType string `json:"emoji_type"`
		} `json:"reaction_type"`
	}
	if err := json.Unmarshal([]byte(<-reactionBody), &body); err != nil {
		t.Fatalf("decode reaction body: %v", err)
	}
	if got := body.ReactionType.EmojiType; got != flashOfInspirationEmojiType {
		t.Fatalf("emoji_type = %q, want %q", got, flashOfInspirationEmojiType)
	}
}

func TestHandleMessageEventContinuesWhenReactionFails(t *testing.T) {
	server := newReactionTestServer(t, func(string) (int, string) {
		return 231001, "reaction type is invalid"
	})
	defer server.Close()

	dispatched := make(chan struct{}, 1)
	bot := newReactionTestBot(t, server, func(context.Context, channels.ChannelContext) {
		dispatched <- struct{}{}
	})
	var debugLog strings.Builder
	bot.SetDebugLogger(func(format string, args ...any) {
		debugLog.WriteString(fmt.Sprintf(format, args...))
		debugLog.WriteByte('\n')
	})

	if err := bot.handleMessageEvent(context.Background(), textMessageEvent("source-id", "hello")); err != nil {
		t.Fatalf("handleMessageEvent() error = %v", err)
	}
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("reaction failure prevented message dispatch")
	}
	if got := debugLog.String(); !strings.Contains(got, "reaction failed") {
		t.Fatalf("debug log missing reaction failure: %q", got)
	}
}

func TestAcknowledgeMessageRejectsEmptyMessageID(t *testing.T) {
	bot, err := NewBot("app-id", "app-secret", nil, nil)
	if err != nil {
		t.Fatalf("NewBot() error = %v", err)
	}
	if err := bot.acknowledgeMessage(context.Background(), " "); err == nil {
		t.Fatal("acknowledgeMessage() error = nil, want empty message_id error")
	}
}

func newReactionTestServer(t *testing.T, reactionResponse func(body string) (code int, message string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"test-token","expire":7200}`))
		case "/open-apis/im/v1/messages/source-id/reactions":
			var body json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode reaction request: %v", err)
			}
			code, message := reactionResponse(string(body))
			_, _ = fmt.Fprintf(w, `{"code":%d,"msg":%q,"data":{"reaction_id":"reaction-id"}}`, code, message)
		default:
			http.Error(w, fmt.Sprintf("unexpected path %s", r.URL.Path), http.StatusNotFound)
		}
	}))
}

func newReactionTestBot(t *testing.T, server *httptest.Server, handler channels.MessageHandler) *Bot {
	t.Helper()
	bot, err := NewBot("app-id", "app-secret", handler, nil)
	if err != nil {
		t.Fatalf("NewBot() error = %v", err)
	}
	bot.client = lark.NewClient(
		"app-id",
		"app-secret",
		lark.WithOpenBaseUrl(server.URL),
		lark.WithHttpClient(server.Client()),
		lark.WithEnableTokenCache(false),
		lark.WithLogger(noopLogger{}),
	)
	return bot
}

func textMessageEvent(messageID, message string) *larkim.P2MessageReceiveV1 {
	chatID := "chat-id"
	chatType := "p2p"
	messageType := "text"
	content, _ := json.Marshal(map[string]string{"text": message})
	contentJSON := string(content)
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageId:   &messageID,
				ChatId:      &chatID,
				ChatType:    &chatType,
				MessageType: &messageType,
				Content:     &contentJSON,
			},
		},
	}
}
