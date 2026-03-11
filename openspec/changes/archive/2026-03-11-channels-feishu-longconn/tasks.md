## 1. 依赖与目录初始化

- [x] 1.1 在根 go.mod 中添加 `github.com/larksuite/oapi-sdk-go/v3` 依赖
- [x] 1.2 创建目录 `pkg/channels/feishu/`

## 2. Bot 核心结构与长链接接入

- [x] 2.1 实现 `Bot` struct，包含 App ID/Secret、`onMessage`、`onAbort` 字段及内部状态（chatID 映射、消息 ID 映射）
- [x] 2.2 实现 `NewBot(appID, appSecret, onMessage, onAbort)` 构造函数，初始化飞书 SDK 客户端
- [x] 2.3 实现 `Bot.Run(ctx)` 方法，通过 `larkcore.NewWSClient` 建立长链接，注册 `im.message.receive_v1` 事件处理器，阻塞直到 ctx 取消

## 3. 消息路由与 feishuContext 构建

- [x] 3.1 实现事件处理函数 `handleMessageEvent`，过滤非 p2p 消息，解析 sender、chat_id、message_id、文本内容
- [x] 3.2 实现 `stop` 指令检测，触发 `onAbort` 并回复停止确认
- [x] 3.3 实现 `chatIDToInt64` 映射函数（sync.Map + 自增计数器）
- [x] 3.4 构建 `feishuContext` 并调用 `onMessage(ctx, feishuCtx)`

## 4. ChannelContext 实现

- [x] 4.1 实现 `Respond(text, _)`：首次调用发送新消息，后续调用 patch_message 更新（累积文本）
- [x] 4.2 实现 `ReplaceMessage(text)`：调用 patch_message 替换主消息内容
- [x] 4.3 实现 `RespondInThread(text)`：发送独立消息（飞书无 thread 概念，等同于普通消息）
- [x] 4.4 实现 `SendCard(text)` 和 `EditCard(msgID, text)`：发送/更新卡片消息，维护 int↔string 消息 ID 映射
- [x] 4.5 实现 `SetWorking(working bool)`：working=true 时发送 "⏳ 处理中..." 占位消息
- [x] 4.6 实现 `UploadFile(filePath, title)`：调用飞书文件上传 API 后发送文件消息
- [x] 4.7 实现 `DeleteMessage()`：调用飞书 delete_message API 删除主消息
- [x] 4.8 实现元数据方法：`ChatID()`、`MessageText()`、`MessageTS()`、`SenderName()`、`Images()`

## 5. 验证与集成

- [x] 5.1 编译验证：`go build ./pkg/channels/feishu/...`
- [x] 5.2 在 `examples/moms/main.go` 中添加飞书 Bot 的可选注册示例（通过环境变量 `FEISHU_APP_ID` / `FEISHU_APP_SECRET`）
