[English](README.md) | [中文](README_zh.md)

# 消息通道

`pkg/channels` 定义 Telegram、飞书的入站消息边界，并可把任一平台接入 `coding_agent.CodingSession`。本包不管理机器人凭据、会话创建或 Agent 策略，这些配置由宿主负责。

## 接口

`Channel` 管理一个平台的运行时：

```go
type Channel interface {
	Name() string
	Run(ctx context.Context) error
	SetMessageHandler(MessageHandler)
	SetAbortHandler(AbortHandler)
}
```

`ChannelContext` 表示一条入站消息，以及回复该消息时可用的操作：

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

## 接入编程会话

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

Bridge 按通道名、Chat ID 和 `MessageTS()` 对入站消息去重。Channel 实现必须从 `MessageTS` 返回稳定的平台消息 ID，否则平台重投可能多次进入 UI 或 Agent 队列。

非 Agent 场景可以直接绑定 Handler：

```go
bot.SetMessageHandler(func(ctx context.Context, message channels.ChannelContext) {
	_ = message.Respond("你说："+message.MessageText(), true)
})
err := bot.Run(ctx)
```

## 平台行为

### Telegram

`pkg/channels/telegram` 在 `Channel` 接口后实现 Telegram Bot API。

### 飞书 / Lark

`pkg/channels/feishu` 通过 WebSocket 接收私聊和群聊事件。`SetAllowedChatIDs` 可限制允许的会话。Handler 异步执行，因此事件回调不必等待 Agent 完成就能确认投递。

`RespondInThread` 和 `feishu.SendText` 会把 Markdown 标题、段落、列表、引用、代码块、任务项、表格和链接转换成飞书 `post` 内容。允许接收的消息在派发前会添加 `StatusFlashOfInspiration` 表情；添加失败只记录日志，不会丢弃消息。应用需要 `im:message` 或 `im:message.reactions:write_only` 权限。

## 目录

```text
pkg/channels/
├── channel.go      # Channel 和 ChannelContext 契约
├── bridge.go       # Channel 到 CodingSession 的 Bridge
├── feishu/         # 飞书实现
└── telegram/       # Telegram 实现
```
