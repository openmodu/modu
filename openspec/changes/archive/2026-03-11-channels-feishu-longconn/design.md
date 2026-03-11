## Context

`pkg/channels/` 定义了 `ChannelContext` 接口，Telegram 已实现此接口（`pkg/channels/telegram/bot.go`）。飞书开放平台支持 WebSocket 长链接模式，Bot 主动建立连接接收事件，无需公网 IP 和 Webhook。

飞书官方 Go SDK（`github.com/larksuite/oapi-sdk-go`）内置长链接支持，封装了鉴权、心跳、重连逻辑。

## Goals / Non-Goals

**Goals:**
- 实现 `pkg/channels/feishu/bot.go`，与 Telegram bot 结构对齐
- 通过飞书 SDK 的 WebSocket 长链接接收 `im.message.receive_v1` 事件
- 实现完整的 `ChannelContext`：文本消息、卡片消息（Interactive Card）、文件上传、typing 状态
- 支持 `stop` 指令触发 `AbortHandler`

**Non-Goals:**
- 群组消息路由（仅处理私聊 / P2P 消息）
- 富文本（Post）格式消息
- 飞书机器人主动发起会话

## Decisions

### 1. 使用官方 SDK 的长链接模式

选择 `github.com/larksuite/oapi-sdk-go/v3` + `larkcore.NewWSClient()`，而非手写 WebSocket 客户端。

**理由**：SDK 处理了鉴权（AppID/AppSecret）、token 刷新、心跳、自动重连等复杂逻辑，与 Telegram 的 `GetUpdatesChan` 类似，是最低维护成本的选择。

### 2. 卡片消息使用飞书 Interactive Card

`SendCard` / `EditCard` 对应飞书的富文本卡片（card）消息，通过 `patch_message` API 更新。消息 ID 用 string 类型，但 `ChannelContext` 接口要求 int——通过内部 map（`msgID → feishuMsgID`）做映射，对外暴露自增整数 ID。

### 3. ChatID 映射

飞书的 chat_id 是字符串（如 `oc_xxx`），`ChannelContext.ChatID()` 返回 int64。维护内部 `sync.Map[string]int64` 做字符串到 int64 的稳定映射（hash 或自增）。简单方案：使用 `sync.Map` + 自增计数器，首次见到新 chat_id 时分配一个 int64。

### 4. Typing 状态

飞书无原生 "typing" API，`SetWorking` 通过发送/更新一条含 "⏳ 处理中..." 的占位消息实现，工作完成后由 `ReplaceMessage` 覆盖该消息。

## Risks / Trade-offs

- **飞书消息 ID 为字符串** → 使用内部映射，增加了少量状态维护复杂度。可接受。
- **SDK 版本升级** → 固定 SDK 版本在 go.mod，按需升级。
- **长链接断线重连** → SDK 自动处理，Bot.Run() 只需等待 ctx 取消。
- **私聊 vs 群聊** → 初版仅支持私聊（P2P），群聊消息被忽略，后续可扩展。

## Migration Plan

1. `go get github.com/larksuite/oapi-sdk-go/v3`
2. 实现 `pkg/channels/feishu/bot.go`
3. 在示例 app（`examples/moms/`）中可选注册 Feishu Bot
4. 通过环境变量 `FEISHU_APP_ID` / `FEISHU_APP_SECRET` 配置

## Open Questions

- 是否需要支持群聊（@Bot 触发）？初版暂不支持。
- 文件上传大小限制？飞书单文件上传 API 支持最大 30MB。
