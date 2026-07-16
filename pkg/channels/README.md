# channels

Messaging channel interface and implementations (Telegram, Feishu, etc.) for Agent communication.

## Overview

`pkg/channels` provides a unified channel runtime interface for messaging
platforms. Business code wires a `Channel` into a generic bridge and responds
through `ChannelContext`, without knowing platform-specific protocol details.

## Interfaces

`Channel` is the runtime surface for an inbound messaging platform:

```go
type Channel interface {
	Name() string
	Run(ctx context.Context) error
	SetMessageHandler(MessageHandler)
	SetAbortHandler(AbortHandler)
}
```

`ChannelContext` is the surface for one inbound message:

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

## Supported Channels

### Telegram
Implementation of the Telegram Bot API.

### Feishu (Lark)
Implementation of the Feishu Bot API. The WebSocket bot accepts private and
group chat events. Call `SetAllowedChatIDs` when the host application needs to
restrict inbound messages to specific Feishu chat IDs. Message handlers are
dispatched asynchronously so the Feishu event callback can acknowledge delivery
without waiting for the agent run to finish. Outbound Markdown passed through
`RespondInThread` or the compatibility `feishu.SendText` helper is converted to
Feishu `post` rich text: headings, paragraphs, lists, quotes, code blocks, task
items, tables, and links are represented without leaking raw Markdown syntax.
Before dispatching an accepted inbound message, the bot adds Feishu's
`StatusFlashOfInspiration` ("Flash of inspiration") reaction to the original
message. Reaction failures are diagnostic only and do not drop the message.
The app needs either the `im:message` or `im:message.reactions:write_only`
permission. Interactive working-state cards keep their existing card format.

## Usage

Hosts can either set a `MessageHandler` directly, or connect a channel to a
`coding_agent.CodingSession` with the generic bridge:

```go
bot, _ := feishu.NewBot(appID, appSecret, nil, nil)
bot.SetAllowedChatIDs(chatIDs)
channels.StartCodingBridge(ctx, channels.CodingBridgeOptions{
    Channel: bot,
    Session: session,
})
```

The generic bridge deduplicates inbound messages by channel name, chat ID, and
`ChannelContext.MessageTS()`. Channel implementations should return a stable
platform message ID from `MessageTS` so retried deliveries are ignored before
they reach the UI or agent queue.

Direct channel usage is still available for non-agent consumers:

```go
bot.SetMessageHandler(func(ctx context.Context, chCtx channels.ChannelContext) {
    text := chCtx.MessageText()
    chCtx.Respond("You said: " + text, true)
})
bot.Run(ctx)
```

## Package Structure

```
pkg/channels/
├── channel.go      # Channel and ChannelContext interfaces
├── bridge.go       # Generic channel <-> CodingSession bridge
├── feishu/         # Feishu implementation
└── telegram/       # Telegram implementation
```
