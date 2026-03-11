### Requirement: Bot 通过长链接接收消息
飞书 Bot SHALL 通过 WebSocket 长链接（`larkcore.NewWSClient`）接收 `im.message.receive_v1` 事件，无需公网 IP 或 Webhook 配置。

#### Scenario: 收到私聊文本消息
- **WHEN** 用户在飞书私聊向 Bot 发送文本消息
- **THEN** Bot 调用 `onMessage(ctx, feishuCtx)`，`feishuCtx.MessageText()` 返回消息文本

#### Scenario: 收到 stop 指令
- **WHEN** 用户发送文本 "stop"（不区分大小写）
- **THEN** Bot 调用 `onAbort(chatID)` 并向用户回复已停止

#### Scenario: 群聊消息被忽略
- **WHEN** 消息来自群聊（chat_type != p2p）
- **THEN** Bot 不处理该消息，不触发任何 handler

### Requirement: 发送文本消息
Bot SHALL 支持向飞书用户发送和更新文本消息。

#### Scenario: 首次 Respond 发送新消息
- **WHEN** 调用 `Respond(text, _)` 且该对话尚无主消息
- **THEN** Bot 调用飞书 `create_message` API 发送新消息，保存消息 ID

#### Scenario: 后续 Respond 追加并更新消息
- **WHEN** 再次调用 `Respond(text, _)` 且主消息已存在
- **THEN** Bot 调用 `patch_message` API 更新消息内容（累积文本）

#### Scenario: ReplaceMessage 替换消息内容
- **WHEN** 调用 `ReplaceMessage(text)`
- **THEN** Bot 调用 `patch_message` API 将主消息内容替换为新文本

### Requirement: 卡片消息
Bot SHALL 支持发送和编辑飞书 Interactive Card 消息。

#### Scenario: SendCard 发送卡片
- **WHEN** 调用 `SendCard(text)`
- **THEN** Bot 发送一条卡片消息，返回内部整数 ID（映射到飞书消息 ID）

#### Scenario: EditCard 更新卡片
- **WHEN** 调用 `EditCard(msgID, text)`
- **THEN** Bot 根据内部 ID 查找飞书消息 ID，调用 `patch_message` 更新卡片内容

### Requirement: Typing 状态指示
Bot SHALL 在处理消息期间展示处理中状态。

#### Scenario: SetWorking(true) 发送占位消息
- **WHEN** 调用 `SetWorking(true)`
- **THEN** Bot 发送内容为 "⏳ 处理中..." 的消息作为主消息占位

#### Scenario: SetWorking(false) 清除状态
- **WHEN** 调用 `SetWorking(false)`
- **THEN** Bot 不发送额外消息（占位消息将被后续 Respond/ReplaceMessage 覆盖）

### Requirement: 文件上传
Bot SHALL 支持向飞书用户发送文件。

#### Scenario: UploadFile 发送文件
- **WHEN** 调用 `UploadFile(filePath, title)`
- **THEN** Bot 上传文件到飞书并以文件消息形式发送给用户

### Requirement: 消息元数据
Bot SHALL 通过 `ChannelContext` 暴露必要的消息元数据。

#### Scenario: ChatID 返回稳定 int64
- **WHEN** 调用 `ChatID()`
- **THEN** 返回与飞书 chat_id 字符串稳定映射的 int64 值，同一 chat_id 始终返回同一值

#### Scenario: MessageTS 用于去重
- **WHEN** 调用 `MessageTS()`
- **THEN** 返回飞书消息 ID（`message_id`），可用于去重判断
