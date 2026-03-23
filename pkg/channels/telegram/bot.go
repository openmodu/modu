package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/channels"
	"github.com/openmodu/modu/pkg/types"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Bot is the Telegram bot. It handles the Telegram protocol only —
// all business logic is delegated via callbacks.
type Bot struct {
	api        *tgbotapi.BotAPI
	attachDir  string // directory to save downloaded attachments
	onMessage  channels.MessageHandler
	onAbort    channels.AbortHandler

	// approvalMu guards approvalCh.
	approvalMu sync.Mutex
	// approvalCh maps chatID → a one-shot channel waiting for the user's
	// next text message as a tool-approval response.  handleUpdate delivers
	// the text and removes the entry before calling onMessage.
	approvalCh map[int64]chan string
}

// NewBot creates a new Telegram bot.
// attachDir is where incoming file attachments are saved before being passed to onMessage.
// onMessage is called for every user message; onAbort is called when the user sends "stop".
func NewBot(token, attachDir string, onMessage channels.MessageHandler, onAbort channels.AbortHandler) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Telegram bot: %w", err)
	}

	if err := os.MkdirAll(attachDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create attach dir: %w", err)
	}

	return &Bot{
		api:        api,
		attachDir:  attachDir,
		onMessage:  onMessage,
		onAbort:    onAbort,
		approvalCh: make(map[int64]chan string),
	}, nil
}

// Username returns the bot's Telegram username (without @).
func (b *Bot) Username() string { return b.api.Self.UserName }

// Run starts receiving updates and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	// Remove any previously configured webhook so that getUpdates works.
	// When a webhook is set, Telegram sends updates (including callback
	// queries) to the webhook URL and does NOT return them via getUpdates,
	// which breaks the long-polling approval flow.
	if _, err := b.api.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: false}); err != nil {
		fmt.Printf("[telegram] warning: failed to delete webhook: %v\n", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	u.AllowedUpdates = []string{"message", "callback_query"}
	updates := b.api.GetUpdatesChan(u)


	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return nil
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			b.handleUpdate(ctx, update)
		}
	}
}

// handleUpdate dispatches one Telegram update.
func (b *Bot) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	// Inline-keyboard button press: deliver to the approval channel for that chat.
	if update.CallbackQuery != nil {
		cq := update.CallbackQuery
		// Acknowledge the callback so Telegram removes the loading spinner.
		_, _ = b.api.Request(tgbotapi.NewCallback(cq.ID, ""))
		if cq.Message == nil {
			// Inline-mode callbacks have no attached message; skip.
			return
		}
		chatID := cq.Message.Chat.ID
		fmt.Printf("[telegram] callback query: chatID=%d data=%q\n", chatID, cq.Data)
		b.approvalMu.Lock()
		if ch, ok := b.approvalCh[chatID]; ok {
			delete(b.approvalCh, chatID)
			b.approvalMu.Unlock()
			ch <- cq.Data
		} else {
			fmt.Printf("[telegram] no pending approval for chatID=%d (data=%q dropped)\n", chatID, cq.Data)
			b.approvalMu.Unlock()
		}
		return
	}

	msg := update.Message
	if msg == nil {
		return
	}
	// Accept text messages or attachment-only messages.
	if msg.Text == "" && msg.Document == nil && msg.Photo == nil && msg.Audio == nil && msg.Video == nil {
		return
	}

	chatID := msg.Chat.ID
	ts := fmt.Sprintf("%d", msg.MessageID)

	// If a tool-approval callback is waiting for this chat's next message,
	// deliver it and skip normal message handling.
	b.approvalMu.Lock()
	if ch, ok := b.approvalCh[chatID]; ok {
		delete(b.approvalCh, chatID)
		b.approvalMu.Unlock()
		ch <- msg.Text
		return
	}
	b.approvalMu.Unlock()

	// Stop command.
	if strings.EqualFold(strings.TrimSpace(msg.Text), "stop") {
		if b.onAbort != nil {
			b.onAbort(chatID)
		}
		b.sendText(chatID, "_Stopped._", tgbotapi.ModeMarkdownV2)
		return
	}

	// Extract text.
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" && (msg.Document != nil || msg.Photo != nil || msg.Audio != nil || msg.Video != nil) {
		text = "(file attachment)"
	}

	senderName := senderDisplayName(msg.From)
	userID := fmt.Sprintf("%d", msg.From.ID)

	// Download attachments.
	chatAttachDir := filepath.Join(b.attachDir, fmt.Sprintf("%d", chatID), "attachments")
	attachmentPaths := b.downloadAttachments(msg, chatAttachDir)

	// Show "typing" while processing.
	typingTick := time.NewTicker(4 * time.Second)
	typingStop := make(chan struct{})
	go func() {
		defer typingTick.Stop()
		_, _ = b.api.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
		for {
			select {
			case <-typingStop:
				return
			case <-typingTick.C:
				_, _ = b.api.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
			}
		}
	}()

	// Build tgContext and call the message handler.
	tgCtx := &tgContext{
		bot:         b,
		chatID:      chatID,
		userID:      userID,
		messageText: text,
		messageTS:   ts,
		senderName:  senderName,
		attachments: attachmentPaths,
	}

	// Run the handler in a goroutine so the bot's Run loop can continue
	// processing other updates (e.g. inline-keyboard callback queries for
	// tool-approval) while the message is being handled.
	go func() {
		b.onMessage(ctx, tgCtx)
		close(typingStop)
	}()
}

// AwaitApproval registers a one-shot channel that will receive the next text
// message sent to chatID.  The message is intercepted before onMessage, so it
// is treated as an approval response rather than a new prompt.
// Call CancelApproval to remove the registration if the context is cancelled.
func (b *Bot) AwaitApproval(chatID int64) <-chan string {
	ch := make(chan string, 1)
	b.approvalMu.Lock()
	b.approvalCh[chatID] = ch
	b.approvalMu.Unlock()
	return ch
}

// CancelApproval removes any pending approval wait for chatID.
func (b *Bot) CancelApproval(chatID int64) {
	b.approvalMu.Lock()
	delete(b.approvalCh, chatID)
	b.approvalMu.Unlock()
}

// SendText exposes sendText for use by the application layer (e.g. approval prompts).
func (b *Bot) SendText(chatID int64, text string) error {
	_, err := b.sendText(chatID, text, "")
	return err
}

// SendApprovalKeyboard sends a tool-approval prompt with four inline buttons
// (Allow / Always Allow / Deny / Always Deny). The button callback data is
// the short key consumed by the approval handler: y, a, n, d.
// Returns the sent message so the caller can remove the keyboard after the
// user responds (via RemoveKeyboard).
func (b *Bot) SendApprovalKeyboard(chatID int64, toolName string) (tgbotapi.Message, error) {
	text := fmt.Sprintf("▶ Tool: %s\n\nApprove execution?", toolName)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Allow", "y"),
			tgbotapi.NewInlineKeyboardButtonData("✅✅ Always Allow", "a"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Deny", "n"),
			tgbotapi.NewInlineKeyboardButtonData("❌❌ Always Deny", "d"),
		),
	)
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyMarkup = keyboard
	return b.api.Send(m)
}

// RemoveKeyboard removes the inline keyboard from a previously sent message,
// preventing stale approval buttons from interfering with future approvals.
func (b *Bot) RemoveKeyboard(chatID int64, messageID int) {
	edit := tgbotapi.NewEditMessageReplyMarkup(chatID, messageID,
		tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}},
	)
	_, _ = b.api.Request(edit)
}

// sendText sends a plain or MarkdownV2 message to a chat.
func (b *Bot) sendText(chatID int64, text, parseMode string) (tgbotapi.Message, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	return b.api.Send(msg)
}

// downloadAttachments downloads any files attached to a message.
func (b *Bot) downloadAttachments(msg *tgbotapi.Message, dir string) []string {
	_ = os.MkdirAll(dir, 0o755)
	var paths []string

	tryDownload := func(fileID, filename string) {
		url, err := b.api.GetFileDirectURL(fileID)
		if err != nil {
			fmt.Printf("[telegram] failed to get URL for fileID %s: %v\n", fileID, err)
			return
		}
		if filename == "" {
			filename = fileID
		}
		dest := filepath.Join(dir, fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filename))
		if err := downloadFile(dest, url); err != nil {
			fmt.Printf("[telegram] failed to download attachment: %v\n", err)
			return
		}
		paths = append(paths, dest)
	}

	if msg.Document != nil {
		tryDownload(msg.Document.FileID, msg.Document.FileName)
	}
	if msg.Audio != nil {
		tryDownload(msg.Audio.FileID, msg.Audio.FileName)
	}
	if msg.Video != nil {
		tryDownload(msg.Video.FileID, msg.Video.FileName)
	}
	if len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		tryDownload(photo.FileID, fmt.Sprintf("photo_%d.jpg", time.Now().UnixMilli()))
	}
	return paths
}

// senderDisplayName returns a human-readable sender name.
func senderDisplayName(u *tgbotapi.User) string {
	if u == nil {
		return "unknown"
	}
	if u.UserName != "" {
		return "@" + u.UserName
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name != "" {
		return name
	}
	return fmt.Sprintf("user%d", u.ID)
}

// downloadFile downloads a URL to destPath.
func downloadFile(destPath, url string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, url)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// -----------------------------------------------------------------------
// tgContext implements channels.ChannelContext.

type tgContext struct {
	bot         *Bot
	chatID      int64
	userID      string
	messageText string
	messageTS   string
	senderName  string
	attachments []string

	mu           sync.Mutex
	mainMsgID    int
	responded    bool
	lastResponse string
	parts        []string
}

func (c *tgContext) ChatID() int64       { return c.chatID }
func (c *tgContext) UserID() string      { return c.userID }
func (c *tgContext) MessageText() string { return c.messageText }
func (c *tgContext) MessageTS() string   { return c.messageTS }
func (c *tgContext) SenderName() string  { return c.senderName }

func (c *tgContext) Images() []types.ImageContent {
	var images []types.ImageContent
	for _, path := range c.attachments {
		ext := strings.ToLower(filepath.Ext(path))
		var mimeType string
		switch ext {
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		case ".png":
			mimeType = "image/png"
		case ".gif":
			mimeType = "image/gif"
		case ".webp":
			mimeType = "image/webp"
		}
		if mimeType != "" {
			data, err := os.ReadFile(path)
			if err == nil {
				images = append(images, types.ImageContent{
					Type:     "image",
					Data:     base64.StdEncoding.EncodeToString(data),
					MimeType: mimeType,
				})
			}
		}
	}
	return images
}

func (c *tgContext) Respond(text string, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if strings.TrimSpace(text) == "[SILENT]" {
		return nil
	}

	c.parts = append(c.parts, text)
	combined := strings.Join(c.parts, "\n")
	c.lastResponse = combined

	if c.mainMsgID == 0 {
		m, err := c.bot.sendText(c.chatID, combined, "")
		if err != nil {
			m, err = c.bot.sendText(c.chatID, escapeMarkdown(combined), tgbotapi.ModeMarkdownV2)
		}
		if err != nil {
			return err
		}
		c.mainMsgID = m.MessageID
		c.responded = true
	} else {
		edit := tgbotapi.NewEditMessageText(c.chatID, c.mainMsgID, combined)
		_, err := c.bot.api.Send(edit)
		if err != nil {
			m, err2 := c.bot.sendText(c.chatID, text, "")
			if err2 != nil {
				return err
			}
			c.mainMsgID = m.MessageID
		}
	}
	return nil
}

func (c *tgContext) ReplaceMessage(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.parts = []string{text}
	c.lastResponse = text
	if c.mainMsgID == 0 {
		m, err := c.bot.sendText(c.chatID, text, "")
		if err != nil {
			return err
		}
		c.mainMsgID = m.MessageID
		c.responded = true
		return nil
	}
	edit := tgbotapi.NewEditMessageText(c.chatID, c.mainMsgID, text)
	_, err := c.bot.api.Send(edit)
	return err
}

func (c *tgContext) RespondInThread(text string) error {
	_, err := c.bot.sendText(c.chatID, text, tgbotapi.ModeMarkdownV2)
	if err != nil {
		_, err = c.bot.sendText(c.chatID, stripMarkdown(text), "")
	}
	return err
}

func (c *tgContext) SendCard(text string) (int, error) {
	m, err := c.bot.sendText(c.chatID, text, tgbotapi.ModeMarkdownV2)
	if err != nil {
		m, err = c.bot.sendText(c.chatID, stripMarkdown(text), "")
	}
	if err != nil {
		return 0, err
	}
	return m.MessageID, nil
}

func (c *tgContext) EditCard(msgID int, text string) error {
	edit := tgbotapi.NewEditMessageText(c.chatID, msgID, text)
	edit.ParseMode = tgbotapi.ModeMarkdownV2
	_, err := c.bot.api.Send(edit)
	if err != nil {
		plain := tgbotapi.NewEditMessageText(c.chatID, msgID, stripMarkdown(text))
		_, err = c.bot.api.Send(plain)
	}
	return err
}

func (c *tgContext) SetWorking(_ bool) error { return nil }

func (c *tgContext) UploadFile(filePath, title string) error {
	const maxSize = 50 * 1024 * 1024
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("cannot stat file: %w", err)
	}
	if info.Size() > maxSize {
		return fmt.Errorf("file too large (%d MB), Telegram limit is 50 MB. Consider compressing or splitting the file",
			info.Size()/(1024*1024))
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(filePath))
	name := filepath.Base(filePath)
	reader := tgbotapi.FileReader{Name: name, Reader: f}

	fmt.Printf("[telegram] uploading %s (%d KB) to chat %d\n", name, info.Size()/1024, c.chatID)
	start := time.Now()

	switch ext {
	case ".mp4", ".mov", ".avi", ".mkv", ".webm":
		vid := tgbotapi.NewVideo(c.chatID, reader)
		if title != "" {
			vid.Caption = title
		}
		_, err = c.bot.api.Send(vid)
	default:
		doc := tgbotapi.NewDocument(c.chatID, reader)
		if title != "" {
			doc.Caption = title
		}
		_, err = c.bot.api.Send(doc)
	}
	fmt.Printf("[telegram] upload %s done in %v (err=%v)\n", name, time.Since(start), err)
	return err
}

func (c *tgContext) DeleteMessage() error {
	c.mu.Lock()
	msgID := c.mainMsgID
	c.mu.Unlock()
	if msgID == 0 {
		return nil
	}
	del := tgbotapi.NewDeleteMessage(c.chatID, msgID)
	_, err := c.bot.api.Send(del)
	return err
}

// Attachments returns the paths of downloaded attachments (moms reads these directly).
func (c *tgContext) Attachments() []string { return c.attachments }

// -----------------------------------------------------------------------
// helpers

func escapeMarkdown(s string) string {
	r := strings.NewReplacer(
		"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]",
		"(", "\\(", ")", "\\)", "~", "\\~", "`", "\\`",
		">", "\\>", "#", "\\#", "+", "\\+", "-", "\\-",
		"=", "\\=", "|", "\\|", "{", "\\{", "}", "\\}",
		".", "\\.", "!", "\\!",
	)
	return r.Replace(s)
}

func stripMarkdown(s string) string {
	r := strings.NewReplacer("*", "", "_", "", "`", "", "~", "", "\\", "")
	return r.Replace(s)
}
