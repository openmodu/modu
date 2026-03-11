## Why

项目已有 Telegram channel 实现，但缺少飞书（Feishu/Lark）接入。飞书在国内企业广泛使用，通过官方 WebSocket 长链接方式接收消息，无需公网 Webhook，部署更简单。

## What Changes

- 新增 `pkg/channels/feishu/` 包，实现飞书 Bot 长链接接入
- 实现 `channels.ChannelContext` 接口，对接飞书消息发送 API
- 支持文本消息收发、卡片消息（Interactive Card）、文件上传
- 支持 `stop` 指令触发 `AbortHandler`

## Capabilities

### New Capabilities

- `feishu-channel`: 飞书 Bot 频道，通过 WebSocket 长链接接收消息，实现与 Telegram channel 对等的功能集（消息收发、卡片、文件上传、typing 状态）

### Modified Capabilities

（无）

## Impact

- 新增依赖：飞书开放平台 Go SDK（`github.com/larksuite/oapi-sdk-go`）
- 新文件：`pkg/channels/feishu/bot.go`
- 对现有代码无破坏性改动，`ChannelContext` 接口不变
