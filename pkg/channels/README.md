# channels

Messaging channel interface and implementations (Telegram, Feishu, etc.) for Agent communication.

## Overview

`pkg/channels` provides a unified `ChannelContext` interface that allows Agents to interact with various messaging platforms without knowing the platform-specific details. This is primarily used by the `moms` bot system.

## Interface: ChannelContext

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
Implementation of the Feishu Bot API.

## Usage

Typically, a channel implementation will listen for incoming messages and invoke a `MessageHandler`:

```go
type MessageHandler func(ctx context.Context, chCtx ChannelContext)
```

Example (conceptual):

```go
tgChannel := telegram.NewTelegramChannel(token)
tgChannel.Start(func(ctx context.Context, chCtx channels.ChannelContext) {
    text := chCtx.MessageText()
    chCtx.Respond("You said: " + text, true)
})
```

## Package Structure

```
pkg/channels/
├── channel.go      # Interface definitions
├── feishu/         # Feishu implementation
└── telegram/       # Telegram implementation
```
