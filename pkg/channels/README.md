[English](README.md) | [中文](README_zh.md)

# Messaging channels

`pkg/channels` defines the inbound messaging boundary for Telegram and Feishu and can bridge either platform to a `coding_agent.CodingSession`. It does not own bot credentials, session creation, or agent policy; the host configures those concerns.

## Interfaces

`Channel` owns one platform runtime:

```go
type Channel interface {
	Name() string
	Run(ctx context.Context) error
	SetMessageHandler(MessageHandler)
	SetAbortHandler(AbortHandler)
}
```

`ChannelContext` represents one inbound message and the operations available while responding:

```go
type ChannelContext interface {
	Respond(text string, shouldLog bool) error
	ReplaceMessage(text string) error
	RespondInThread(text string) error
	SendCard(text string) (int, error)
	EditCard(msgID int, text string) error
	SetWorking(working bool) error
	UploadFile(filePath, title string) error
	DeleteMessage() error
	ChatID() int64
	MessageText() string
	MessageTS() string
	SenderName() string
	Images() []types.ImageContent
}
```

## Connect a coding session

```go
bot, err := feishu.NewBot(appID, appSecret, nil, nil)
if err != nil {
	return err
}
bot.SetAllowedChatIDs(chatIDs)

channels.StartCodingBridge(ctx, channels.CodingBridgeOptions{
	Channel: bot,
	Session: session,
})
```

The bridge deduplicates inbound messages by channel name, chat ID, and `MessageTS()`. A Channel implementation must return a stable platform message ID from `MessageTS`; otherwise platform retries can reach the UI or agent queue more than once.

For non-agent consumers, attach a handler directly:

```go
bot.SetMessageHandler(func(ctx context.Context, message channels.ChannelContext) {
	_ = message.Respond("You said: "+message.MessageText(), true)
})
err := bot.Run(ctx)
```

## Platform behavior

### Telegram

`pkg/channels/telegram` implements the Telegram Bot API behind `Channel`.

### Feishu / Lark

`pkg/channels/feishu` receives private and group events over WebSocket. `SetAllowedChatIDs` restricts accepted chats. Handlers run asynchronously so the event callback can acknowledge delivery without waiting for the agent.

`RespondInThread` and `feishu.SendText` convert Markdown headings, paragraphs, lists, quotes, code blocks, task items, tables, and links to Feishu `post` content. Accepted messages receive the `StatusFlashOfInspiration` reaction before dispatch; a reaction failure is logged but does not discard the message. The app needs `im:message` or `im:message.reactions:write_only` permission.

## Layout

```text
pkg/channels/
├── channel.go      # Channel and ChannelContext contracts
├── bridge.go       # Channel to CodingSession bridge
├── feishu/         # Feishu implementation
└── telegram/       # Telegram implementation
```
