package channels

import (
	"context"

	"github.com/openmodu/modu/pkg/types"
)

// ChannelContext is the interface the runner uses to communicate back to a
// messaging channel (Telegram, Feishu, Slack, etc.).
type ChannelContext interface {
	// Respond appends text to the main response message (creates it on first call).
	Respond(text string, shouldLog bool) error
	// ReplaceMessage replaces the entire main message text.
	ReplaceMessage(text string) error
	// RespondInThread posts a follow-up message in the same chat (used for tool details).
	RespondInThread(text string) error
	// SendCard sends a standalone card message and returns its message ID.
	SendCard(text string) (int, error)
	// EditCard replaces the text of a previously sent card message.
	EditCard(msgID int, text string) error
	// SetWorking toggles the "...working" indicator on the main message.
	SetWorking(working bool) error
	// UploadFile sends the file at filePath to the channel.
	UploadFile(filePath, title string) error
	// DeleteMessage deletes the main response message.
	DeleteMessage() error
	// ChatID returns the chat this context belongs to.
	ChatID() int64
	// MessageText returns the user's message text.
	MessageText() string
	// MessageTS returns a unique string for the message (used for dedup).
	MessageTS() string
	// SenderName returns the human-readable sender name.
	SenderName() string
	// Images returns any image attachments provided with the message.
	Images() []types.ImageContent
}

// MessageHandler is called by a channel when a new message arrives.
// The implementation (moms Dispatcher) processes the message and responds via chCtx.
type MessageHandler func(ctx context.Context, chCtx ChannelContext)

// AbortHandler is called by a channel when the user sends a "stop" command.
type AbortHandler func(chatID int64)
