package feishu

import (
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/crosszan/modu/pkg/channels"
	"github.com/crosszan/modu/pkg/types"
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

	// card slot int → feishu message_id (string), for SendCard/EditCard
	cardMsgMap sync.Map
	nextCardID atomic.Int64
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
		}).
		OnP2MessageReactionCreatedV1(func(_ context.Context, _ *larkim.P2MessageReactionCreatedV1) error {
			return nil
		}).
		OnP2MessageReactionDeletedV1(func(_ context.Context, _ *larkim.P2MessageReactionDeletedV1) error {
			return nil
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

	msgType := ""
	if msg.MessageType != nil {
		msgType = *msg.MessageType
	}

	// Parse text content.
	text := ""
	if msgType == "text" && msg.Content != nil {
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

	// Download image attachment if present.
	var images []types.ImageContent
	if msgType == "image" && msg.Content != nil && messageID != "" {
		var imgMsg struct {
			ImageKey string `json:"image_key"`
		}
		if err := json.Unmarshal([]byte(*msg.Content), &imgMsg); err == nil && imgMsg.ImageKey != "" {
			if img, err := b.downloadImage(ctx, messageID, imgMsg.ImageKey); err == nil {
				images = append(images, img)
			} else {
				fmt.Printf("[feishu] downloadImage failed: %v\n", err)
			}
		}
	}

	fCtx := &feishuContext{
		bot:         b,
		chatID:      chatID,
		messageID:   messageID,
		messageText: text,
		senderName:  senderName,
		images:      images,
	}

	// Add a "processing" reaction to the incoming message so the user knows it was received.
	if messageID != "" {
		if rid, err := b.addReaction(ctx, messageID, "THUMBSUP"); err == nil {
			fCtx.reactionID = rid
		} else {
			fmt.Printf("[feishu] addReaction failed: %v\n", err)
		}
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

// downloadImage downloads an image attachment from a message and returns it as base64-encoded ImageContent.
func (b *Bot) downloadImage(ctx context.Context, messageID, imageKey string) (types.ImageContent, error) {
	resp, err := b.client.Im.MessageResource.Get(ctx,
		larkim.NewGetMessageResourceReqBuilder().
			MessageId(messageID).
			FileKey(imageKey).
			Type("image").
			Build())
	if err != nil {
		return types.ImageContent{}, fmt.Errorf("feishu downloadImage: %w", err)
	}
	if !resp.Success() {
		return types.ImageContent{}, fmt.Errorf("feishu downloadImage: code=%d msg=%s", resp.Code, resp.Msg)
	}
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, resp.File); err != nil {
		return types.ImageContent{}, fmt.Errorf("feishu downloadImage: read: %w", err)
	}
	return types.ImageContent{
		Type:     "image",
		Data:     base64.StdEncoding.EncodeToString(buf.Bytes()),
		MimeType: "image/jpeg",
	}, nil
}

// addReaction adds an emoji reaction to a message and returns the reaction_id.
func (b *Bot) addReaction(ctx context.Context, msgID, emojiType string) (string, error) {
	resp, err := b.client.Im.MessageReaction.Create(ctx,
		larkim.NewCreateMessageReactionReqBuilder().
			MessageId(msgID).
			Body(larkim.NewCreateMessageReactionReqBodyBuilder().
				ReactionType(larkim.NewEmojiBuilder().EmojiType(emojiType).Build()).
				Build()).
			Build())
	if err != nil {
		return "", fmt.Errorf("feishu addReaction: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("feishu addReaction: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data != nil && resp.Data.ReactionId != nil {
		return *resp.Data.ReactionId, nil
	}
	return "", nil
}

// removeReaction removes an emoji reaction from a message.
func (b *Bot) removeReaction(ctx context.Context, msgID, reactionID string) error {
	resp, err := b.client.Im.MessageReaction.Delete(ctx,
		larkim.NewDeleteMessageReactionReqBuilder().
			MessageId(msgID).
			ReactionId(reactionID).
			Build())
	if err != nil {
		return fmt.Errorf("feishu removeReaction: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu removeReaction: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// deleteMessage deletes a Feishu message by message_id.
func (b *Bot) deleteMessage(ctx context.Context, msgID string) error {
	resp, err := b.client.Im.Message.Delete(ctx,
		larkim.NewDeleteMessageReqBuilder().
			MessageId(msgID).
			Build())
	if err != nil {
		return fmt.Errorf("feishu deleteMessage: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu deleteMessage: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
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

	mu         sync.Mutex
	mainMsgID  string // feishu message_id of the current response text message
	responded  bool
	parts      []string
	reactionID string              // reaction_id of the "processing" emoji on the incoming message
	images     []types.ImageContent
}

func (c *feishuContext) ChatID() int64       { return c.bot.chatIDToInt64(c.chatID) }
func (c *feishuContext) MessageText() string { return c.messageText }
func (c *feishuContext) MessageTS() string   { return c.messageID }
func (c *feishuContext) SenderName() string  { return c.senderName }
func (c *feishuContext) Images() []types.ImageContent { return c.images }

// clearReaction removes the "processing" reaction from the incoming message (idempotent).
// Must be called with mu held.
func (c *feishuContext) clearReaction() {
	if c.reactionID == "" {
		return
	}
	rid := c.reactionID
	c.reactionID = ""
	go func() { //nolint:errcheck
		_ = c.bot.removeReaction(context.Background(), c.messageID, rid)
	}()
}

func (c *feishuContext) Respond(text string, _ bool) error {
	if strings.TrimSpace(text) == "[SILENT]" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.parts = append(c.parts, text)
	combined := strings.Join(c.parts, "\n")

	c.clearReaction()

	// Send as a new message each time (text messages cannot be edited in Feishu).
	// We do NOT delete the previous message to avoid showing "消息已撤回".
	msgID, err := c.bot.sendText(context.Background(), c.chatID, combined)
	if err != nil {
		return err
	}
	c.mainMsgID = msgID
	c.responded = true
	return nil
}

func (c *feishuContext) ReplaceMessage(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.parts = []string{text}
	c.clearReaction()
	if c.mainMsgID != "" {
		_ = c.bot.deleteMessage(context.Background(), c.mainMsgID)
		c.mainMsgID = ""
	}
	msgID, err := c.bot.sendText(context.Background(), c.chatID, text)
	if err != nil {
		return err
	}
	c.mainMsgID = msgID
	c.responded = true
	return nil
}

func (c *feishuContext) RespondInThread(text string) error {
	_, err := c.bot.sendText(context.Background(), c.chatID, text)
	return err
}

func (c *feishuContext) SendCard(text string) (int, error) {
	msgID, err := c.bot.sendText(context.Background(), c.chatID, text)
	if err != nil {
		return 0, err
	}
	// Use a simple counter via the string msgID stored in a local map on Bot.
	// Re-use the cardMsgMap but store string msgID directly via a fresh counter.
	id := int(c.bot.nextCardID.Add(1))
	c.bot.cardMsgMap.Store(id, msgID)
	return id, nil
}

func (c *feishuContext) EditCard(id int, text string) error {
	v, ok := c.bot.cardMsgMap.Load(id)
	if !ok {
		return fmt.Errorf("feishu: unknown card id %d", id)
	}
	oldMsgID := v.(string)
	_ = c.bot.deleteMessage(context.Background(), oldMsgID)
	newMsgID, err := c.bot.sendText(context.Background(), c.chatID, text)
	if err != nil {
		return err
	}
	c.bot.cardMsgMap.Store(id, newMsgID)
	return nil
}

func (c *feishuContext) SetWorking(_ bool) error { return nil }

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
	return c.bot.deleteMessage(context.Background(), msgID)
}
