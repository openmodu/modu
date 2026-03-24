# channels

Agent 通信的消息通道接口及实现（Telegram、飞书等）。

## 概述

`pkg/channels` 提供了一个统一的 `ChannelContext` 接口，允许 Agent 在不知道具体平台细节的情况下与各种消息平台进行交互。这主要用于 `moms` 机器人系统。

## 接口: ChannelContext

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

## 支持的通道

### Telegram
Telegram Bot API 的实现。

### 飞书 (Feishu/Lark)
飞书机器人 API 的实现。

## 使用

通常，通道实现会监听传入的消息并调用 `MessageHandler`：

```go
type MessageHandler func(ctx context.Context, chCtx ChannelContext)
```

示例（概念性）：

```go
tgChannel := telegram.NewTelegramChannel(token)
tgChannel.Start(func(ctx context.Context, chCtx channels.ChannelContext) {
    text := chCtx.MessageText()
    chCtx.Respond("你说：" + text, true)
})
```

## 目录结构

```
pkg/channels/
├── channel.go      # 接口定义
├── feishu/         # 飞书实现
└── telegram/       # Telegram 实现
```
