# channels

Agent 通信的消息通道接口及实现（Telegram、飞书等）。

## 概述

`pkg/channels` 提供统一的消息通道运行时接口。业务代码把 `Channel` 接到通用
bridge，然后只通过 `ChannelContext` 回复，不需要知道飞书、Telegram 等平台协议细节。

## 接口

`Channel` 是一个入站消息平台的统一运行时接口：

```go
type Channel interface {
	Name() string
	Run(ctx context.Context) error
	SetMessageHandler(MessageHandler)
	SetAbortHandler(AbortHandler)
}
```

`ChannelContext` 是单条入站消息的回复接口：

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
飞书机器人 API 的实现。WebSocket bot 支持私聊和群聊事件；宿主应用需要限制
入口时可调用 `SetAllowedChatIDs` 指定允许的飞书 chat_id。消息 handler 会异步派发，
让飞书事件回调不用等待 agent 执行完成即可确认投递。通过 `RespondInThread` 或兼容
接口 `feishu.SendText` 发送的 Markdown 会转换为飞书 `post` 富文本；标题、段落、
列表、引用、代码块、任务项、表格和链接不会再以原始 Markdown 标记显示。“处理中”
等交互卡片仍保持原有卡片格式。每条允许接收的入站消息在派发给 handler 前，会先在
原消息上添加“灵光一闪”（`StatusFlashOfInspiration`）表情回复；添加失败只记录诊断
日志，不会丢弃消息。飞书应用需要具备 `im:message` 或
`im:message.reactions:write_only` 权限之一。

## 使用

宿主可以直接设置 `MessageHandler`，也可以用通用 bridge 接到
`coding_agent.CodingSession`：

```go
bot, _ := feishu.NewBot(appID, appSecret, nil, nil)
bot.SetAllowedChatIDs(chatIDs)
channels.StartCodingBridge(ctx, channels.CodingBridgeOptions{
    Channel: bot,
    Session: session,
})
```

通用 bridge 会按通道名、chat ID 和 `ChannelContext.MessageTS()` 对入站消息做去重。
Channel 实现应从 `MessageTS` 返回稳定的平台消息 ID，这样平台重投同一消息时，
重复事件会在进入 UI 或 agent 队列前被忽略。

非 agent 场景仍可直接使用 channel：

```go
bot.SetMessageHandler(func(ctx context.Context, chCtx channels.ChannelContext) {
    text := chCtx.MessageText()
    chCtx.Respond("你说：" + text, true)
})
bot.Run(ctx)
```

## 目录结构

```
pkg/channels/
├── channel.go      # Channel 和 ChannelContext 接口
├── bridge.go       # 通用 channel <-> CodingSession bridge
├── feishu/         # 飞书实现
└── telegram/       # Telegram 实现
```
