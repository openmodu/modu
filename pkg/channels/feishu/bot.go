package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/types"
)

// Bot is the Feishu (Lark) bot that receives messages via WebSocket long connection.
// It handles the Feishu protocol only — all business logic is delegated via callbacks.
type Bot struct {
	appID     string
	appSecret string
	client    *lark.Client
	onMessage channels.MessageHandler
	onAbort   channels.AbortHandler

	// chatID string → int64 stable mapping (FNV hash)
	chatIDMap sync.Map

	// card slot int → feishu message_id
	cardMsgMap     sync.Map
	cardMsgCounter atomic.Int64
}

// NewBot creates a new Feishu bot.
// onMessage is called for every user message; onAbort is called when the user sends "stop".
func NewBot(appID, appSecret string, onMessage channels.MessageHandler, onAbort channels.AbortHandler) (*Bot, error) {
	client := lark.NewClient(appID, appSecret)
	return &Bot{
		appID:     appID,
		appSecret: appSecret,
		client:    client,
		onMessage: onMessage,
		onAbort:   onAbort,
	}, nil
}

// Run starts the WebSocket long connection and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	eventDispatcher := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			return b.handleMessageEvent(ctx, event)
		})

	wsClient := larkws.NewClient(b.appID, b.appSecret,
		larkws.WithEventHandler(eventDispatcher),
	)

	fmt.Println("[feishu] connecting via WebSocket long connection...")
	return wsClient.Start(ctx)
}

// handleMessageEvent processes incoming im.message.receive_v1 events.
func (b *Bot) handleMessageEvent(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if event.Event == nil || event.Event.Message == nil {
		return nil
	}
	msg := event.Event.Message

	// Only handle p2p (private) messages.
	if msg.ChatType == nil || *msg.ChatType != "p2p" {
		return nil
	}

	chatID := ""
	if msg.ChatId != nil {
		chatID = *msg.ChatId
	}
	if chatID == "" {
		return nil
	}

	messageID := ""
	if msg.MessageId != nil {
		messageID = *msg.MessageId
	}

	// Parse text content.
	text := ""
	if msg.MessageType != nil && *msg.MessageType == "text" && msg.Content != nil {
		var textMsg struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(*msg.Content), &textMsg); err == nil {
			text = textMsg.Text
		}
	}
	if text == "" {
		text = "(attachment)"
	}

	// Stop command.
	if strings.EqualFold(strings.TrimSpace(text), "stop") {
		chatInt64 := b.chatIDToInt64(chatID)
		if b.onAbort != nil {
			b.onAbort(chatInt64)
		}
		b.sendText(ctx, chatID, "已停止。") //nolint:errcheck
		return nil
	}

	// Get sender open_id as display name.
	senderName := ""
	if event.Event.Sender != nil &&
		event.Event.Sender.SenderId != nil &&
		event.Event.Sender.SenderId.OpenId != nil {
		senderName = *event.Event.Sender.SenderId.OpenId
	}

	fCtx := &feishuContext{
		bot:         b,
		chatID:      chatID,
		messageID:   messageID,
		messageText: text,
		senderName:  senderName,
	}

	b.onMessage(ctx, fCtx)
	return nil
}

// chatIDToInt64 returns a stable int64 for a Feishu string chat_id using FNV hash.
func (b *Bot) chatIDToInt64(chatID string) int64 {
	if v, ok := b.chatIDMap.Load(chatID); ok {
		return v.(int64)
	}
	h := fnv.New64a()
	h.Write([]byte(chatID))
	id := int64(h.Sum64() >> 1) // ensure non-negative
	b.chatIDMap.Store(chatID, id)
	return id
}

// sendText sends a plain text message to a Feishu chat_id.
func (b *Bot) sendText(ctx context.Context, chatID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	resp, err := b.client.Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(chatID).
				MsgType("text").
				Content(string(content)).
				Build()).
			Build())
	if err != nil {
		return "", fmt.Errorf("feishu sendText: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("feishu sendText: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data != nil && resp.Data.MessageId != nil {
		return *resp.Data.MessageId, nil
	}
	return "", nil
}

// sendCard sends an interactive card message to a Feishu chat_id.
func (b *Bot) sendCard(ctx context.Context, chatID, text string) (string, error) {
	content := buildCardContent(text)
	resp, err := b.client.Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(chatID).
				MsgType("interactive").
				Content(content).
				Build()).
			Build())
	if err != nil {
		return "", fmt.Errorf("feishu sendCard: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("feishu sendCard: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data != nil && resp.Data.MessageId != nil {
		return *resp.Data.MessageId, nil
	}
	return "", nil
}

// patchCard updates an existing interactive card message.
func (b *Bot) patchCard(ctx context.Context, msgID, text string) error {
	content := buildCardContent(text)
	resp, err := b.client.Im.Message.Patch(ctx,
		larkim.NewPatchMessageReqBuilder().
			MessageId(msgID).
			Body(larkim.NewPatchMessageReqBodyBuilder().
				Content(content).
				Build()).
			Build())
	if err != nil {
		return fmt.Errorf("feishu patchCard: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu patchCard: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// buildCardContent builds a simple interactive card JSON with markdown/plain_text content.
func buildCardContent(text string) string {
	type cardText struct {
		Tag     string `json:"tag"`
		Content string `json:"content"`
	}
	type cardElement struct {
		Tag  string   `json:"tag"`
		Text cardText `json:"text"`
	}
	type cardBody struct {
		Elements []cardElement `json:"elements"`
	}
	type card struct {
		Config map[string]bool `json:"config"`
		Body   cardBody        `json:"body"`
	}
	c := card{
		Config: map[string]bool{"wide_screen_mode": true},
		Body: cardBody{
			Elements: []cardElement{
				{Tag: "div", Text: cardText{Tag: "plain_text", Content: text}},
			},
		},
	}
	b, _ := json.Marshal(c)
	return string(b)
}

// extToFeishuFileType maps a file extension to a Feishu file_type string.
func extToFeishuFileType(ext string) string {
	switch ext {
	case "mp4", "mov", "avi", "mkv", "webm":
		return "mp4"
	case "mp3", "ogg", "wav", "m4a":
		return "mp4"
	default:
		return "stream"
	}
}

// -----------------------------------------------------------------------
// feishuContext implements channels.ChannelContext.

type feishuContext struct {
	bot         *Bot
	chatID      string // feishu chat_id (string)
	messageID   string // feishu message_id (used as MessageTS)
	messageText string
	senderName  string

	mu          sync.Mutex
	mainMsgID   string // feishu message_id of the main response card
	responded   bool
	parts       []string
}

func (c *feishuContext) ChatID() int64       { return c.bot.chatIDToInt64(c.chatID) }
func (c *feishuContext) MessageText() string { return c.messageText }
func (c *feishuContext) MessageTS() string   { return c.messageID }
func (c *feishuContext) SenderName() string  { return c.senderName }
func (c *feishuContext) Images() []types.ImageContent { return nil }

func (c *feishuContext) Respond(text string, _ bool) error {
	if strings.TrimSpace(text) == "[SILENT]" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.parts = append(c.parts, text)
	combined := strings.Join(c.parts, "\n")

	if c.mainMsgID == "" {
		msgID, err := c.bot.sendCard(context.Background(), c.chatID, combined)
		if err != nil {
			return err
		}
		c.mainMsgID = msgID
		c.responded = true
		return nil
	}
	return c.bot.patchCard(context.Background(), c.mainMsgID, combined)
}

func (c *feishuContext) ReplaceMessage(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.parts = []string{text}
	if c.mainMsgID == "" {
		msgID, err := c.bot.sendCard(context.Background(), c.chatID, text)
		if err != nil {
			return err
		}
		c.mainMsgID = msgID
		c.responded = true
		return nil
	}
	return c.bot.patchCard(context.Background(), c.mainMsgID, text)
}

func (c *feishuContext) RespondInThread(text string) error {
	_, err := c.bot.sendText(context.Background(), c.chatID, text)
	return err
}

func (c *feishuContext) SendCard(text string) (int, error) {
	msgID, err := c.bot.sendCard(context.Background(), c.chatID, text)
	if err != nil {
		return 0, err
	}
	id := int(c.bot.cardMsgCounter.Add(1))
	c.bot.cardMsgMap.Store(id, msgID)
	return id, nil
}

func (c *feishuContext) EditCard(msgID int, text string) error {
	v, ok := c.bot.cardMsgMap.Load(msgID)
	if !ok {
		return fmt.Errorf("feishu: unknown card msgID %d", msgID)
	}
	return c.bot.patchCard(context.Background(), v.(string), text)
}

func (c *feishuContext) SetWorking(working bool) error {
	if !working {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mainMsgID != "" {
		// Already have a main message; nothing to do.
		return nil
	}
	msgID, err := c.bot.sendCard(context.Background(), c.chatID, "⏳ 处理中...")
	if err != nil {
		return err
	}
	c.mainMsgID = msgID
	return nil
}

func (c *feishuContext) UploadFile(filePath, title string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("feishu UploadFile: open: %w", err)
	}
	defer f.Close()

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filePath)), ".")
	fileType := extToFeishuFileType(ext)
	name := filepath.Base(filePath)
	if title != "" {
		name = title
	}

	uploadResp, err := c.bot.client.Im.File.Create(context.Background(),
		larkim.NewCreateFileReqBuilder().
			Body(larkim.NewCreateFileReqBodyBuilder().
				FileType(fileType).
				FileName(name).
				File(io.Reader(f)).
				Build()).
			Build())
	if err != nil {
		return fmt.Errorf("feishu UploadFile: upload: %w", err)
	}
	if !uploadResp.Success() {
		return fmt.Errorf("feishu UploadFile: code=%d msg=%s", uploadResp.Code, uploadResp.Msg)
	}

	fileKey := ""
	if uploadResp.Data != nil && uploadResp.Data.FileKey != nil {
		fileKey = *uploadResp.Data.FileKey
	}

	content, _ := json.Marshal(map[string]string{"file_key": fileKey})
	sendResp, err := c.bot.client.Im.Message.Create(context.Background(),
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(c.chatID).
				MsgType("file").
				Content(string(content)).
				Build()).
			Build())
	if err != nil {
		return fmt.Errorf("feishu UploadFile: send: %w", err)
	}
	if !sendResp.Success() {
		return fmt.Errorf("feishu UploadFile: send: code=%d msg=%s", sendResp.Code, sendResp.Msg)
	}
	return nil
}

func (c *feishuContext) DeleteMessage() error {
	c.mu.Lock()
	msgID := c.mainMsgID
	c.mu.Unlock()
	if msgID == "" {
		return nil
	}
	resp, err := c.bot.client.Im.Message.Delete(context.Background(),
		larkim.NewDeleteMessageReqBuilder().
			MessageId(msgID).
			Build())
	if err != nil {
		return fmt.Errorf("feishu DeleteMessage: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu DeleteMessage: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}
