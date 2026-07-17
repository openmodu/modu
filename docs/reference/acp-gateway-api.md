# acp-gateway HTTP API 参考

`acp-gateway` 把本机配置的 ACP Agent 暴露为 HTTP + SSE 接口。当前 API 没有版本前缀，仍可能发生不兼容调整；客户端应以本文和 `cmd/acp-gateway/handlers.go` 中注册的路由为准。

## 启动

Gateway 必须读取一份 ACP 配置。默认依次查找：

1. 当前目录的 `acp.config.json`
2. `~/.modu/acp.config.json`
3. `~/.config/modu/acp.json`

最小配置：

```json
{
  "version": 1,
  "agents": [
    {
      "id": "claude",
      "name": "Claude Code",
      "command": "npx",
      "args": ["-y", "@zed-industries/claude-code-acp"]
    }
  ],
  "defaultAgent": "claude"
}
```

```bash
export MODU_ACP_TOKEN=dev-token
go run ./cmd/acp-gateway -config ./acp.config.json
```

默认监听 `:7080`，默认把运行数据写入当前目录的 `acp-gateway.db`。传 `-db ''` 会关闭持久化，同时禁用 TokenKit 接口。

## 鉴权与错误

`GET /` 和 `GET /healthz` 不鉴权。其余路由在 `MODU_ACP_TOKEN` 非空时要求：

```text
Authorization: Bearer <MODU_ACP_TOKEN>
```

未设置 `MODU_ACP_TOKEN` 时鉴权关闭，只适合本地开发。成功响应通常是 `application/json`；错误响应由 `http.Error` 返回一行纯文本。时间字段由 Go 以 RFC 3339 兼容格式编码。

## 资源关系

```text
Project ── Session ── Turn
 工作目录    Agent 与上下文    一次 prompt/response
                 │
              Profile
       可复用的 Agent + system prompt
```

同一 Session 同时只能运行一个 Turn。不同 Session 即使使用同一 Agent 和工作目录，也会创建彼此独立的 ACP 会话上下文。

## 路由总览

以下列表直接对应 `cmd/acp-gateway/handlers.go` 中的 `buildRouter`。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/` | 内置 Web 页面；免鉴权 |
| `GET` | `/healthz` | 存活检查；免鉴权 |
| `GET` | `/api/info` | 版本、运行时间、连接数、Agent 数和系统指标 |
| `GET` | `/api/agents` | 列出 Agent |
| `POST` | `/api/agents` | 新增 Agent |
| `GET` | `/api/agents/{id}` | 查询 Agent |
| `PUT` | `/api/agents/{id}` | 更新 Agent |
| `DELETE` | `/api/agents/{id}` | 删除 Agent |
| `POST` | `/api/agents/{id}/restart` | 停止已有进程，下次使用时重建 |
| `GET` | `/api/browse` | 浏览本机绝对路径 |
| `GET` | `/api/projects` | 列出 Project |
| `POST` | `/api/projects` | 创建 Project |
| `GET` | `/api/projects/{id}` | 查询 Project |
| `DELETE` | `/api/projects/{id}` | 删除 Project 及其 Session、Turn |
| `GET` | `/api/profiles` | 列出 Profile |
| `POST` | `/api/profiles` | 创建 Profile |
| `GET` | `/api/profiles/{id}` | 查询 Profile |
| `PUT` | `/api/profiles/{id}` | 更新 Profile |
| `DELETE` | `/api/profiles/{id}` | 删除 Profile |
| `GET` | `/api/sessions` | 列出 Session，可按 Project 过滤 |
| `POST` | `/api/sessions` | 创建 Session |
| `GET` | `/api/sessions/{id}` | 查询 Session 及完整 Turn 历史 |
| `DELETE` | `/api/sessions/{id}` | 删除 Session 及其 Turn |
| `POST` | `/api/sessions/{id}/cancel` | 取消 Session 当前运行中的 Turn |
| `POST` | `/api/sessions/{id}/turns` | 提交一个 Turn |
| `GET` | `/api/sessions/{id}/turns/{turnId}/stream` | 订阅 SSE；也接受 `POST` |
| `POST` | `/api/sessions/{id}/turns/{turnId}/approve` | 回答权限请求 |
| `GET` | `/api/sessions/{id}/turns/{turnId}/events` | 读取带时间戳的事件历史 |
| `GET` | `/api/tokenkit/overview` | 查询 TokenKit 仪表盘数据 |
| `POST` | `/api/tokenkit/scan` | 扫描本地 Agent 用量 |
| `GET` | `/api/tokenkit/records` | 查询原始用量记录 |
| `POST` | `/api/tokenkit/codex-status` | 解析并保存 Codex `/status` 文本 |
| `GET` | `/api/tokenkit/codex-status/latest` | 查询最近一次 Codex 状态 |

## 最短调用流程

### 1. 创建 Project

`path` 必须是 Gateway 所在机器上已经存在的目录。

```bash
curl -sS http://localhost:7080/api/projects \
  -H "Authorization: Bearer $MODU_ACP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"modu","path":"/absolute/path/to/modu"}'
```

成功返回 `201`：

```json
{
  "id": "proj-1",
  "name": "modu",
  "path": "/absolute/path/to/modu",
  "createdAt": "2026-07-17T00:00:00Z"
}
```

### 2. 创建 Session

```bash
curl -sS http://localhost:7080/api/sessions \
  -H "Authorization: Bearer $MODU_ACP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"projectId":"proj-1","agent":"claude","title":"检查测试"}'
```

`projectId` 和 `agent` 必填。也可以传 `profileId`；如果省略 `agent`，Gateway 会使用 Profile 的 `agentId`。

### 3. 提交 Turn

```bash
curl -sS http://localhost:7080/api/sessions/sess-1/turns \
  -H "Authorization: Bearer $MODU_ACP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"运行相关测试并说明失败原因"}'
```

成功返回 `202` 和状态为 `pending` 的 Turn。Session 已有 `running` Turn 时返回 `409`。

### 4. 订阅输出

```bash
curl -N http://localhost:7080/api/sessions/sess-1/turns/turn-1/stream \
  -H "Authorization: Bearer $MODU_ACP_TOKEN"
```

SSE 事件有三类：

| `event:` | `data:` 内容 |
|---|---|
| `status` | 完整 Turn 快照，状态为 `running`、`completed` 或 `failed` |
| `event` | Provider 的 `types.StreamEvent` 载荷 |
| `permission` | `toolCallId`、标题、类型和可选决策 |

订阅开始时会重放内存中保留的历史事件，单个 Turn 最多保留 256 条。Turn 完成或失败后连接关闭。需要带时间戳的历史记录时调用 `/events`。

### 5. 回答权限请求

从 `permission` 事件中读取 `toolCallId` 和某个 `optionId`：

```bash
curl -sS http://localhost:7080/api/sessions/sess-1/turns/turn-1/approve \
  -H "Authorization: Bearer $MODU_ACP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"toolCallId":"call-1","optionId":"allow-once"}'
```

没有对应的待处理权限请求时返回 `409`。

## Agent

新增和更新 Agent 使用 `pkg/acp/manager.AgentConfig` 的 JSON 结构：

```json
{
  "id": "claude",
  "name": "Claude Code",
  "command": "npx",
  "args": ["-y", "@zed-industries/claude-code-acp"],
  "env": {"ANTHROPIC_API_KEY": "..."},
  "permissionMode": "default",
  "weeklyLimit": 0,
  "resetDay": "monday"
}
```

`POST /api/agents` 要求 `id` 和 `command`。`PUT /api/agents/{id}` 以路径中的 id 为准。动态修改会尝试写回启动时实际读取的配置文件。

## Profile

Profile 保存可复用的 Agent 与 system prompt：

```json
{
  "name": "Go reviewer",
  "agentId": "claude",
  "systemPrompt": "只审查 Go 代码，不修改文件。",
  "description": "Go 代码审查",
  "icon": "review"
}
```

`name` 和 `agentId` 必填，且 `agentId` 必须已经注册。删除 Profile 不会删除已有 Session 中保存的 `profileId`。

## 文件浏览

```text
GET /api/browse?path=/absolute/path&dirs=true
```

`path` 为空时默认浏览用户主目录，且必须是绝对路径。`dirs=true` 只返回目录。接口会隐藏名称以 `.` 开头的条目；它用于项目选择器，不是文件内容读取 API。

## TokenKit

TokenKit 依赖 Gateway 的 SQLite 数据库；使用 `-db ''` 启动时，这组接口返回 `503`。

`GET /api/tokenkit/overview` 支持 `start`、`end`、`days` 和 `limit`。`days` 最大为 366。

`GET /api/tokenkit/records` 支持 `app`、`source`、`model`、`workspace`、`start`、`end`、`limit`、`offset` 和 `asc`。

手动扫描：

```text
POST /api/tokenkit/scan?target=all|codex|claude-code|gemini
```

还可按需传 `timezone`、`codexHome`、`claudeHome` 和 `geminiTelemetryLog` 覆盖启动参数。

保存 Codex 状态时提交 `text` 或 `raw`，可选 `capturedAt`：

```json
{
  "text": "<Codex /status 的原始文本>",
  "capturedAt": "2026-07-17T00:00:00Z"
}
```

## 启动参数

| 参数 | 默认值 | 作用 |
|---|---|---|
| `-addr` | `:7080` | HTTP 监听地址 |
| `-config` | 自动查找 | ACP 配置路径 |
| `-workers` | `1` | 每个 Agent 的 worker 数 |
| `-db` | `acp-gateway.db` | SQLite 路径；空值关闭持久化 |
| `-cwd` | 配置或进程目录 | 默认工作目录 |
| `-tokenkit-sync-interval` | `5m` | TokenKit 后台同步周期；`0` 关闭 |
| `-tokenkit-timezone` | 本地时区 | TokenKit 日期时区 |
| `-tokenkit-codex-home` | `~/.codex` | Codex 数据目录 |
| `-tokenkit-claude-home` | `~/.claude` | Claude 数据目录 |
| `-tokenkit-gemini-log` | 自动探测 | Gemini telemetry 日志 |

构建时可以覆盖版本：

```bash
go build -ldflags "-X main.Version=1.2.0" ./cmd/acp-gateway
```

## 验收

Handler 和状态机测试：

```bash
go test ./cmd/acp-gateway
```

ACP 协议栈测试：

```bash
go test ./pkg/acp/...
```

路由发生变化时，先更新本页的路由总览和调用流程，再提交实现。
