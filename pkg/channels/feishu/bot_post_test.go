package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestSendAndReplyPostUseFeishuRichText(t *testing.T) {
	type messageRequest struct {
		ReceiveID string `json:"receive_id"`
		MsgType   string `json:"msg_type"`
		Content   string `json:"content"`
	}

	var (
		mu       sync.Mutex
		requests []messageRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"test-token","expire":7200}`))
		case "/open-apis/im/v1/messages":
			if got := r.URL.Query().Get("receive_id_type"); got != "chat_id" {
				t.Errorf("receive_id_type = %q, want chat_id", got)
			}
			var req messageRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode send request: %v", err)
			}
			mu.Lock()
			requests = append(requests, req)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"message_id":"sent-id"}}`))
		case "/open-apis/im/v1/messages/source-id/reply":
			var req messageRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode reply request: %v", err)
			}
			mu.Lock()
			requests = append(requests, req)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"message_id":"reply-id"}}`))
		default:
			http.Error(w, fmt.Sprintf("unexpected path %s", r.URL.Path), http.StatusNotFound)
		}
	}))
	defer server.Close()

	bot, err := NewBot("app-id", "app-secret", nil, nil)
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

	if got, err := bot.sendPost(context.Background(), "chat-id", "**send** [link](https://example.com)"); err != nil {
		t.Fatalf("sendPost() error = %v", err)
	} else if got != "sent-id" {
		t.Fatalf("sendPost() message ID = %q, want sent-id", got)
	}
	if got, err := bot.replyPost(context.Background(), "source-id", "# Reply\n\nbody"); err != nil {
		t.Fatalf("replyPost() error = %v", err)
	} else if got != "reply-id" {
		t.Fatalf("replyPost() message ID = %q, want reply-id", got)
	}

	mu.Lock()
	gotRequests := append([]messageRequest(nil), requests...)
	mu.Unlock()
	if len(gotRequests) != 2 {
		t.Fatalf("message request count = %d, want 2", len(gotRequests))
	}
	for i, req := range gotRequests {
		if req.MsgType != "post" {
			t.Errorf("request %d msg_type = %q, want post", i, req.MsgType)
		}
		post := decodePostContent(t, req.Content)
		if len(post.ZhCN.Content) == 0 {
			t.Errorf("request %d has no rich-text paragraphs", i)
		}
	}
	if gotRequests[0].ReceiveID != "chat-id" {
		t.Errorf("send receive_id = %q, want chat-id", gotRequests[0].ReceiveID)
	}
}

func TestReplyPostRejectsEmptyMessageID(t *testing.T) {
	bot, err := NewBot("app-id", "app-secret", nil, nil)
	if err != nil {
		t.Fatalf("NewBot() error = %v", err)
	}
	if _, err := bot.replyPost(context.Background(), " ", "body"); err == nil {
		t.Fatal("replyPost() error = nil, want empty message_id error")
	}
}
