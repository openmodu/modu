package moms

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/skills"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Bot is the Telegram bot that processes messages.
type Bot struct {
	api         *tgbotapi.BotAPI
	store       *Store
	sandbox     *Sandbox
	workingDir  string
	llmModel    *providers.Model
	getAPIKey   func(provider string) (string, error)
	settings    *Settings
	registryMgr *skills.RegistryManager
	searchCache *skills.SearchCache

	mu      sync.Mutex
	runners map[int64]*Runner
	queue   map[int64]chan *queuedMessage
}

type queuedMessage struct {
	update      tgbotapi.Update
	ts          string
	attachments []string
}

// NewBot creates a new Telegram bot.
func NewBot(token string, sandbox *Sandbox, workingDir string, llmModel *providers.Model, getAPIKey func(provider string) (string, error), registryMgr *skills.RegistryManager, searchCache *skills.SearchCache) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Telegram bot: %w", err)
	}
	fmt.Printf("[moms] authorized as @%s\n", api.Self.UserName)

	return &Bot{
		api:         api,
		store:       NewStore(workingDir),
		sandbox:     sandbox,
		workingDir:  workingDir,
		llmModel:    llmModel,
		getAPIKey:   getAPIKey,
		settings:    NewSettingsManager(workingDir),
		registryMgr: registryMgr,
		searchCache: searchCache,
		runners:     make(map[int64]*Runner),
		queue:       make(map[int64]chan *queuedMessage),
	}, nil
}

// Run starts receiving updates and blocks until context is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	fmt.Println("[moms] Listening for Telegram messages...")

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
	msg := update.Message
	if msg == nil || msg.Text == "" {
		// Handle file/document attachments even without text.
		if update.Message != nil && (update.Message.Document != nil || update.Message.Photo != nil || update.Message.Audio != nil || update.Message.Video != nil) {
			msg = update.Message
		} else {
			return
		}
	}

	chatID := msg.Chat.ID
	ts := fmt.Sprintf("%d", msg.MessageID)

	// Stop command.
	if strings.EqualFold(strings.TrimSpace(msg.Text), "stop") {
		b.mu.Lock()
		runner := b.runners[chatID]
		b.mu.Unlock()
		if runner != nil && runner.IsRunning() {
			runner.Abort()
			b.sendText(chatID, "_Stopped._", tgbotapi.ModeMarkdownV2)
		} else {
			b.sendText(chatID, "_Nothing running._", tgbotapi.ModeMarkdownV2)
		}
		return
	}

	// Extract text (handle caption for files).
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}

	senderName := senderDisplayName(msg.From)
	userID := fmt.Sprintf("%d", msg.From.ID)

	// Handle attachments.
	var attachmentPaths []string
	attachmentPaths = b.downloadAttachments(msg, chatID)

	// Build attachment description.
	if len(attachmentPaths) > 0 && text == "" {
		text = "(file attachment)"
	}

	// Log the incoming message.
	_ = b.store.LogUserMessage(chatID, ts, userID, senderName, text, attachmentPaths)

	fmt.Printf("[moms] chat %d @%s: %s\n", chatID, senderName, truncateStr(text, 80))

	// Queue message to per-chat goroutine.
	b.mu.Lock()
	ch, exists := b.queue[chatID]
	if !exists {
		ch = make(chan *queuedMessage, 32)
		b.queue[chatID] = ch
		go b.processQueue(ctx, chatID, ch)
	}
	b.mu.Unlock()

	select {
	case ch <- &queuedMessage{update: update, ts: ts, attachments: attachmentPaths}:
	default:
		b.sendText(chatID, "_Too many messages queued. Please wait._", tgbotapi.ModeMarkdownV2)
	}
}

// processQueue processes messages for a single chat sequentially.
func (b *Bot) processQueue(ctx context.Context, chatID int64, ch chan *queuedMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case qm, ok := <-ch:
			if !ok {
				return
			}
			b.processMessage(ctx, chatID, qm)
		}
	}
}

// processMessage runs the agent for one message.
func (b *Bot) processMessage(ctx context.Context, chatID int64, qm *queuedMessage) {
	msg := qm.update.Message

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}

	senderName := senderDisplayName(msg.From)

	// Show "typing" while working.
	typing := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	_, _ = b.api.Send(typing)

	// Ticker to keep "typing" alive.
	typingTicker := time.NewTicker(4 * time.Second)
	typingStop := make(chan struct{})
	go func() {
		defer typingTicker.Stop()
		for {
			select {
			case <-typingStop:
				return
			case <-typingTicker.C:
				_, _ = b.api.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
			}
		}
	}()
	defer close(typingStop)

	runner := b.getOrCreateRunner(chatID)

	tgCtx := &tgContext{
		bot:         b,
		chatID:      chatID,
		messageText: text,
		messageTS:   qm.ts,
		senderName:  senderName,
		attachments: qm.attachments,
	}

	result := runner.Run(ctx, tgCtx)

	if result.StopReason == "aborted" {
		b.sendText(chatID, "_Aborted._", tgbotapi.ModeMarkdownV2)
	} else if result.Error != nil {
		fmt.Printf("[moms] chat %d error: %v\n", chatID, result.Error)
	}
}

// newRunnerFromBot creates a Runner using Bot's sandbox, model, and working dir.
func newRunnerFromBot(b *Bot, chatID int64) *Runner {
	return NewRunner(b.sandbox, b.workingDir, chatID, b.llmModel, b.getAPIKey, b.settings, b.registryMgr, b.searchCache)
}

// getOrCreateRunner returns the persistent runner for a chat.
func (b *Bot) getOrCreateRunner(chatID int64) *Runner {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r, ok := b.runners[chatID]; ok {
		return r
	}
	r := newRunnerFromBot(b, chatID)
	b.runners[chatID] = r
	return r
}

// TriggerEvent sends an event message to a chat (called from EventsWatcher).
func (b *Bot) TriggerEvent(ctx context.Context, chatID int64, filename, text string) {
	runner := b.getOrCreateRunner(chatID)

	tgCtx := &tgContext{
		bot:         b,
		chatID:      chatID,
		messageText: text,
		messageTS:   fmt.Sprintf("event-%s-%d", filename, time.Now().UnixMilli()),
		senderName:  "event",
	}

	result := runner.Run(ctx, tgCtx)

	if tgCtx.responded && !strings.Contains(strings.ToUpper(tgCtx.lastResponse), "[SILENT]") {
		// Response already sent.
	} else if !tgCtx.responded {
		// No response - silent.
	}
	_ = result
}

// sendText sends a plain or markdown message to a chat.
func (b *Bot) sendText(chatID int64, text, parseMode string) (tgbotapi.Message, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	return b.api.Send(msg)
}

// downloadAttachments downloads any files attached to a message.
func (b *Bot) downloadAttachments(msg *tgbotapi.Message, chatID int64) []string {
	var paths []string
	attachDir := filepath.Join(b.store.ChatDir(chatID), "attachments")
	_ = os.MkdirAll(attachDir, 0o755)

	tryDownload := func(fileID, filename string) {
		url, err := b.api.GetFileDirectURL(fileID)
		if err != nil {
			fmt.Printf("[moms] failed to get URL for fileID %s: %v\n", fileID, err)
			return
		}
		if filename == "" {
			filename = fileID
		}
		dest := filepath.Join(attachDir, fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filename))
		if err := DownloadFile(dest, url, ""); err != nil {
			fmt.Printf("[moms] failed to download attachment: %v\n", err)
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
		// Largest photo.
		photo := msg.Photo[len(msg.Photo)-1]
		tryDownload(photo.FileID, fmt.Sprintf("photo_%d.jpg", time.Now().UnixMilli()))
	}

	return paths
}

// senderDisplayName returns a human-readable name.
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

// -----------------------------------------------------------------------
// tgContext implements TelegramContext.

type tgContext struct {
	bot         *Bot
	chatID      int64
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
func (c *tgContext) MessageText() string { return c.messageText }
func (c *tgContext) MessageTS() string   { return c.messageTS }
func (c *tgContext) SenderName() string  { return c.senderName }

func (c *tgContext) Images() []providers.ImageContent {
	var images []providers.ImageContent
	for _, path := range c.attachments {
		ext := strings.ToLower(filepath.Ext(path))
		mimeType := ""
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
				images = append(images, providers.ImageContent{
					Type:     "image",
					Data:     base64Encode(string(data)),
					MimeType: mimeType,
				})
			}
		}
	}
	return images
}

func base64Encode(raw string) string {
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func (c *tgContext) Respond(text string, shouldLog bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Skip [SILENT] token messages.
	if strings.TrimSpace(text) == "[SILENT]" {
		return nil
	}

	c.parts = append(c.parts, text)
	combined := strings.Join(c.parts, "\n")
	c.lastResponse = combined

	if c.mainMsgID == 0 {
		// First message.
		m, err := c.bot.sendText(c.chatID, combined, "")
		if err != nil {
			// Try with escaped markdown.
			m, err = c.bot.sendText(c.chatID, escapeMarkdown(combined), tgbotapi.ModeMarkdownV2)
		}
		if err != nil {
			return err
		}
		c.mainMsgID = m.MessageID
		c.responded = true
	} else {
		// Edit existing message.
		edit := tgbotapi.NewEditMessageText(c.chatID, c.mainMsgID, combined)
		_, err := c.bot.api.Send(edit)
		if err != nil {
			// If message is too long or can't edit, send new.
			m, err2 := c.bot.sendText(c.chatID, text, "")
			if err2 != nil {
				return err
			}
			c.mainMsgID = m.MessageID
		}
	}

	if shouldLog {
		ts := fmt.Sprintf("bot-%d", time.Now().UnixMilli())
		_ = c.bot.store.LogBotResponse(c.chatID, ts, text)
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
	// Telegram doesn't have threads like Slack; send as a normal message.
	_, err := c.bot.sendText(c.chatID, text, tgbotapi.ModeMarkdownV2)
	if err != nil {
		// Fallback to plain text.
		_, err = c.bot.sendText(c.chatID, stripMarkdown(text), "")
	}
	return err
}

func (c *tgContext) SetWorking(working bool) error {
	// No-op: we use chat actions (typing) in processMessage instead.
	return nil
}

func (c *tgContext) UploadFile(filePath, title string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	doc := tgbotapi.NewDocument(c.chatID, tgbotapi.FileReader{
		Name:   filepath.Base(filePath),
		Reader: f,
	})
	if title != "" {
		doc.Caption = title
	}
	_, err = c.bot.api.Send(doc)
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

// stripMarkdown removes common markdown formatting for plain-text fallback.
func stripMarkdown(s string) string {
	replacer := strings.NewReplacer("*", "", "_", "", "`", "", "~", "", "\\", "")
	return replacer.Replace(s)
}
