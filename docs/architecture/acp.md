# ACP 集成架构

Modu 的 ACP 集成分成两部分：`pkg/acp` 负责启动和调用 ACP 子进程，`cmd/acp-gateway` 负责把 Agent、项目、会话和 Turn 暴露为 HTTP + SSE API。协议实现不依赖 Gateway，Gateway 也通过 `Runner` 接口隔离具体 Agent 类型。

本文描述当前源码。后续工作与验收条件见 [`ACP 路线图`](../plans/acp-roadmap.md)，HTTP 契约见 [`acp-gateway API`](../reference/acp-gateway-api.md)。

## 目标与边界

当前实现解决四件事：

- 通过 stdio 上的 LDJSON 读写 JSON-RPC 2.0，调用 ACP Agent。
- 把 ACP `session/update` 转成 Modu 的流式事件。
- 按 Gateway Session 隔离 Agent 进程和对话上下文。
- 让 HTTP 客户端提交 Turn、订阅结果并回答权限请求。

以下内容不属于这一层：

- `modu_code --acp` 是 ACP server，位于 `cmd/modu_code/internal/acp`，不复用 `pkg/acp/provider` 的 client 职责。
- Gateway 不提供通用远程文件读写 API；`/api/browse` 只用于列目录。
- `pkg/acp/client` 支持反向文件 RPC，但 Gateway 当前没有注入 `FSHandler`，因此 `fs/read_text_file` 和 `fs/write_text_file` 会被拒绝。
- iOS 或其他客户端 UI 不在本仓库实现。

## 分层

```text
HTTP 客户端
    │ REST + SSE
    ▼
cmd/acp-gateway
    handlers ── Store ── 每 Agent 工作队列
                         │
                         ▼
                     worker
                         │ Runner
                         ▼
                    acpRunner
                         │
                         ▼
pkg/acp/manager ── provider ── client ── process
                       │          │          │
                    bridge     jsonrpc    ACP 子进程
```

数据面与控制面分开：

- 数据面：Project、Profile、Session、Turn、事件和权限选择保存在 Gateway `Store` 中；启用数据库时同步写入 SQLite。
- 控制面：Agent 配置由 `pkg/acp/manager.Config` 管理；新增、修改、删除 Agent 会更新 Manager、Runner registry，并尝试写回配置文件。

Gateway 没有使用 `pkg/mailbox.Hub`。它有自己的 Store、队列和 worker；调用方应以当前 Project / Session / Turn API 为准。

## `pkg/acp` 的职责

### `jsonrpc`：消息模型

`pkg/acp/jsonrpc` 定义 Request、Response、Notification、Message 和标准错误。它只负责数据结构、序列化和消息判别，不做 IO。

这个边界让协议错误可以在不启动子进程的情况下测试：

```bash
go test ./pkg/acp/jsonrpc
```

### `process`：子进程传输

`pkg/acp/process` 包装 `exec.Cmd`，把 stdin/stdout 作为逐行消息传输，并独立消费 stderr。上层只看到 `client.Transport`，测试可以替换为内存实现。

Process 不理解 ACP 方法，也不负责会话状态。停止、异常退出和长消息边界由该包自己的测试覆盖。

### `client`：请求关联与反向 RPC

`pkg/acp/client` 为每个请求分配 id，通过 pending map 把 Response 交回等待方，同时把 Notification 分发给订阅者。

ACP Agent 也可以反向请求 Client：

| 方法 | 当前处理方式 |
|---|---|
| `session/request_permission` | 调用 `PermissionHandler`，返回用户选择的 option id |
| `fs/read_text_file` | 转给 `FSHandler`；未配置时返回错误 |
| `fs/write_text_file` | 转给 `FSHandler`；未配置时返回错误 |
| 其他方法 | 返回 `MethodNotFound` |

### `bridge`：事件翻译

`pkg/acp/bridge` 把 `session/update` Notification 翻译成 `types.StreamEvent`。主要映射包括文本增量、思考增量、工具开始、工具更新和工具结束。

Bridge 不持有连接和会话状态。协议字段变化时，应先增加 fixture 和单元测试，再调整翻译逻辑。

### `provider`：Modu Provider 适配

`pkg/acp/provider.Provider` 实现 `pkg/providers.Provider`。第一次调用时依次完成：

1. 根据 Agent 类型写入可识别的上下文文件（仅在有 system prompt 时）。
2. 启动 Client 的 Transport。
3. 发送 `initialize`。
4. 发送 `session/new`，绑定工作目录。
5. 对后续请求复用同一个 ACP session id。

每次 `Stream` 订阅 `session/update`，通过 Bridge 推送事件，并在 context 取消时发送 `session/cancel`。

一个 Provider 同一时间只应处理一个 Stream。并发隔离由 Manager 和 Gateway 的 Session key 负责。

### `manager`：配置和实例隔离

`pkg/acp/manager.Manager` 延迟创建 Provider 和子进程。普通调用以 `agentID + cwd` 为 key；Gateway 使用 `ProviderKeyed`，以 `agentID + gatewaySessionID` 为 key。

这个选择避免了两个 Gateway Session 指向同一工作目录时共享对话上下文。代价是每个 Session 可能持有独立 Agent 子进程，调用方需要控制 Session 数和每 Agent worker 数。

Manager 还负责运行时新增、更新、删除和重启 Agent。更新或重启会停止该 Agent 已创建的 Provider，下一次 Turn 再延迟创建。

## Gateway 数据模型

```text
Project
  └─ Session (agent, profile, cwd, status)
       └─ Turn (prompt, result, usage, status)

Profile (agent, reusable system prompt)
```

- Project 保存 Gateway 所在机器上的绝对工作目录。
- Session 固定 Agent、Project 和可选 Profile，并拥有有序的 Turn 历史。
- Turn 是一次 prompt/response，状态为 `pending`、`running`、`completed` 或 `failed`。
- Profile 为新 Session 提供 Agent 和 system prompt；删除 Profile 不会反向修改已有 Session。

同一 Session 运行中不能再提交 Turn。Store 会返回冲突，避免并发写入同一 ACP 对话。

## Turn 数据流

```text
客户端                 handler       Store/queue       worker       ACP Agent
  │ POST /turns           │               │               │              │
  ├──────────────────────▶│ AddTurn       │               │              │
  │◀──── 202 pending ─────┤──────────────▶│ enqueue       │              │
  │                       │               ├──────────────▶│              │
  │ GET /stream           │               │               │ prompt       │
  ├──────────────────────▶│ Subscribe     │               ├─────────────▶│
  │                       │               │◀── events ─────┤◀── updates ──┤
  │◀──── SSE event ───────┤◀──────────────┤               │              │
  │◀──── SSE status ──────┤◀──────────────┤ complete/fail │              │
```

Store 为每个 Agent 建一个有界 Turn 队列。Server 启动时按 `-workers` 为每个 Agent创建 worker；运行时新增 Agent 会启动一个 worker。

事件历史的内存上限是每个 Turn 256 条。订阅者加入时会先收到缓存事件；订阅者消费过慢时，新事件可能被丢弃，执行线程不会被阻塞。最终结果和状态仍保存在 Turn 中。

## 权限数据流

```text
ACP Agent
  │ session/request_permission
  ▼
client.PermissionHandler
  │ 按 agent + cwd 查找当前 Turn
  ▼
Store.AwaitPermission ── SSE permission ──▶ HTTP 客户端
  ▲                                               │
  └──────── POST /approve {toolCallId, optionId} ─┘
```

Worker 在执行前登记 `agentID + cwd → turnID`，结束后清理。没有匹配的活跃 Turn 时，Gateway 优先返回 Agent 提供的拒绝选项；没有拒绝选项则返回空 id。

`/approve` 只接受当前正在等待的 `toolCallId`。迟到、重复或不匹配的选择返回 `409`。

## 持久化与恢复

默认 SQLite 文件为 `acp-gateway.db`。Gateway 启动时加载 Project、Profile、Session 和 Turn；状态变化后同步更新数据库。

传 `-db ''` 会使用纯内存 Store。这样做也会禁用 TokenKit，因为 TokenKit 复用同一个数据库连接。

SSE 订阅本身不持久化，事件历史只保存在当前进程内存中。客户端断线后可以在进程仍存活时重连并接收缓存；Gateway 重启后不能依靠 `/events` 恢复旧流式增量，应以持久化的 Turn 最终状态为准。

## 安全边界

- Bearer token 为空会关闭鉴权。生产环境必须设置 `MODU_ACP_TOKEN`，并限制监听地址或通过可信网络暴露。
- `/api/browse` 可以列出 Gateway 用户有权读取的任意绝对目录。它会隐藏点文件，但这不是安全沙箱。
- Project path 在创建时只验证目录存在；Agent 的实际文件权限由 Gateway 进程用户决定。
- Agent 配置可以包含环境变量和可执行命令。动态 Agent 管理接口只应开放给受信任客户端。
- Profile system prompt 对 Claude、Codex、Gemini 分别写入 `CLAUDE.md`、`AGENTS.md`、`GEMINI.md`；这会修改 Project 目录中的同名文件。

最后一条有明确代价：如果工作区已有上下文文件，system prompt 写入会覆盖它。使用 Profile 前必须确认该行为符合预期。

## 验收

协议层和 Gateway 分开验证：

```bash
go test ./pkg/acp/...
go test ./cmd/acp-gateway
```

`examples/acp_e2e/mock_acp_agent` 可作为无外部模型的测试进程，但仓库目前没有覆盖完整 Gateway 进程、SSE 和权限回调的自动化端到端脚本。

架构变更至少要满足以下三项：

1. 对应包的单元测试通过。
2. Handler、SSE 或权限流程变化时，`cmd/acp-gateway/server_test.go` 有覆盖。
3. 路由、数据模型或安全边界变化时，同步更新本文与 API 参考。
