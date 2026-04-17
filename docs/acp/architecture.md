# ACP Integration — 架构设计

> 目标：在 modu 里原生支持 [Agent Client Protocol](https://agentclientprotocol.com/protocol/overview)，把 Claude Code / Codex CLI / Gemini CLI 等外部 agent 作为 modu 的一等公民接入；远端（iOS / 任意 HTTP 客户端）可以下发任务，家里的机器执行。

## 1. 设计目标

| 目标 | 验收点 |
|---|---|
| G1 — 接入 ACP agent | modu 能起一个 ACP 子进程（Claude Code / Codex / Gemini），跑完一轮 prompt 拿到流式输出 |
| G2 — 融入 modu 抽象 | ACP agent 被包装成 `providers.Provider`，上层 `pkg/agent` 无感使用 |
| G3 — 多 agent 并发 | 通过 swarm/mailbox 把多个 ACP agent 并发调度 |
| G4 — 远端触发 | HTTP API 接受外部 `POST /tasks`，任务进 mailbox 队列 |
| G5 — 全链路可观测 | 所有事件进 `pkg/trace`，跨 agent 执行过程可回放 |
| G6 — iOS 可用（最后做） | 提供稳定的 HTTP + SSE 接口供 iOS 调用 |

**非目标**：
- 实现 ACP **server side**（modu 不需要"被当作 agent 暴露"）
- 完整的 MCP 服务器支持（`session/new` 里的 `mcpServers` 先传空）
- iOS 端 UI（后续独立迭代）

## 2. 总体分层

```
┌───────────────────────────────────────────────────────────────┐
│                     iOS / 其他 HTTP 客户端                      │
└──────────────────────────────┬────────────────────────────────┘
                               │ HTTP + SSE
                               ▼
┌───────────────────────────────────────────────────────────────┐
│                      cmd/acp-gateway                          │
│        POST /tasks · GET /tasks/:id · GET /tasks/:id/stream    │
│   ┌────────────────────────────────────────────────────────┐  │
│   │                   modu/pkg/mailbox (Hub)                │  │
│   │              任务队列 · 持久化 · 事件总线                 │  │
│   └─────────────────────┬──────────────────────────────────┘  │
│                         │                                      │
│          ┌──────────────┼──────────────┐                       │
│          ▼              ▼              ▼                       │
│   ┌─────────────┐ ┌─────────────┐ ┌─────────────┐             │
│   │ ACP Worker  │ │ ACP Worker  │ │ ACP Worker  │ (swarm)     │
│   │  (claude)   │ │   (codex)   │ │  (gemini)   │             │
│   └──────┬──────┘ └──────┬──────┘ └──────┬──────┘             │
│          │               │               │                     │
│   ┌──────▼───────────────▼───────────────▼──────────────────┐ │
│   │                    pkg/acp                               │ │
│   │  manager → client → process → jsonrpc                    │ │
│   │     │         │                                          │ │
│   │     │         └── bridge (ACP update ↔ AgentEvent)       │ │
│   │     │                                                    │ │
│   │     └── provider (adapts ACP as modu.Provider)           │ │
│   └──────────────────────────────────────────────────────────┘ │
└──────────────────────────────┬────────────────────────────────┘
                               │ stdio / JSON-RPC 2.0 (LDJSON)
         ┌─────────────────────┼─────────────────────┐
         ▼                     ▼                     ▼
   ┌──────────┐          ┌──────────┐         ┌──────────┐
   │  claude- │          │  codex-  │         │ gemini-  │
   │ code-acp │          │   acp    │         │   acp    │
   └──────────┘          └──────────┘         └──────────┘
```

## 3. 模块职责

### `pkg/acp/jsonrpc` — JSON-RPC 2.0 协议层

- 定义 `Request` / `Response` / `Notification` / `Message`（union）/ `Error`
- 提供 `IsRequest / IsResponse / IsNotification` 三种判别
- 标准错误码：`ParseError / InvalidRequest / MethodNotFound / InvalidParams / InternalError`
- **不涉及 IO**，纯数据结构 + `encoding/json`

### `pkg/acp/process` — ACP 子进程生命周期

- 封装 `exec.Cmd`，管理 stdin/stdout/stderr
- `Start()` / `Stop()`（graceful：close stdin → SIGINT → 3s → SIGKILL）
- stdout 按行读（LDJSON），Scanner buffer 预留 10 MB（默认 64 KB 会爆）
- stderr 独立 goroutine 避免阻塞 stdout
- 状态机：`Idle → Starting → Running → (Error|Stopped)`
- **不解析 JSON-RPC 内容**，只负责把每行 bytes 交给上层

### `pkg/acp/client` — RPC 客户端 + 反向 RPC 派发

- 在 Process 上加一层 `Request(method, params) (*Message, error)`：
  - 分配递增 ID，塞 `pending map[int]chan *Message`
  - 读循环拿到 response 按 ID 唤醒对应 channel
- 反向 RPC：agent 会向 client 发起 request（这是 ACP 特殊点，不是纯单向）：
  - `session/request_permission` — 危险操作确认 → 回调用户注入的 `PermissionHandler`
  - `fs/read_text_file` / `fs/write_text_file` — agent 请 client 代读代写（受 sandbox 限制）→ 回调用户注入的 `FSHandler`
- `OnNotification(fn) func()` — 订阅 notification 流，返回 cleanup 闭包（和 modu `Agent.Subscribe` 对齐）

### `pkg/acp/bridge` — ACP ↔ modu 事件翻译

ACP 的 `session/update` 通知有多种 `sessionUpdate` 子类型，每种映射到一个 modu `AgentEvent`：

| ACP `sessionUpdate` | modu `AgentEvent` | 备注 |
|---|---|---|
| `agent_message_chunk` (text) | `EventTypeMessageUpdate` + `text_delta` | 文本流 |
| `agent_thought_chunk` | `EventTypeMessageUpdate` + `thinking` | 思考流 |
| `tool_call` | `EventTypeToolExecutionStart` | 工具调用开始 |
| `tool_call_update` (status=in_progress) | `EventTypeToolExecutionUpdate` | 进度 |
| `tool_call_update` (status=completed/error) | `EventTypeToolExecutionEnd` | 结束 |
| `available_commands_update` | 自定义 meta event | 可选 |

**纯函数**，不持有状态；输入 `*jsonrpc.Message`，输出 `[]AgentEvent`。方便单测。

### `pkg/acp/provider` — ACP-as-Provider 适配器

把一个 ACP agent 包装成 modu 的 `providers.Provider`：

```go
type ACPProvider struct {
    id     string
    client *acp.Client
    sessions map[string]string  // cwd → ACP sessionId
}

func (p *ACPProvider) ID() string { return p.id }
func (p *ACPProvider) Stream(ctx, model, llmCtx, opts) (stream.Stream, error) {
    // 1. 确保 client 已 initialize
    // 2. 复用或创建 session（按 cwd）
    // 3. 订阅 notification → 通过 bridge 转成 SimpleStreamEvent
    // 4. 调 session/prompt 阻塞等完成
    // 5. 返回 stream.Stream
}
```

**好处**：上层 `pkg/agent.Agent` 完全不用改，换 provider 就是换 model.ProviderID；swarm / trace / mailbox 全部免费继承。

### `pkg/acp/manager` — 多 agent 池

- 从配置文件加载 agents（schema 参考 acpone 的 `acpone.config.json`）
- 按 ID 索引 `map[string]*Client`，延迟启动（首次用到才 `Start`）
- 路由策略：`@mention` → keyword → default（可选，先不加，默认 caller 指定 agentID）

### `cmd/acp-gateway` — HTTP 入口（远端触发）

最小 HTTP API（Go 原生 `net/http`，不引入框架）：

```
POST   /api/tasks             发任务 { agent, prompt, cwd } → 返回 taskID
GET    /api/tasks/:id         查状态（status / result / error）
GET    /api/tasks/:id/stream  SSE 订阅事件流
GET    /api/agents            列出可用 agent
POST   /api/tasks/:id/approve 审批权限请求 { optionId }
```

- 鉴权：静态 `Authorization: Bearer <token>`（从 env 或 config 读）
- 事件存储：复用 `pkg/mailbox` 的 SQLite store
- 任务调度：任务发到 mailbox swarm queue，ACP worker claim 并执行

## 4. 关键数据流

### 4.1 单次 prompt 端到端（iOS 视角）

```
iOS                  gateway               mailbox(Hub)         ACP worker           claude-code-acp
  │                     │                      │                    │                       │
  │  POST /api/tasks    │                      │                    │                       │
  ├────────────────────▶│                      │                    │                       │
  │                     │  PublishTask(...)    │                    │                       │
  │                     ├─────────────────────▶│                    │                       │
  │  201 {taskID}       │                      │                    │                       │
  │◀────────────────────┤                      │                    │                       │
  │                     │                      │  ClaimTask()       │                       │
  │                     │                      │◀───────────────────┤                       │
  │  GET /tasks/:id/stream  (SSE)              │                    │                       │
  ├────────────────────▶│                      │                    │                       │
  │                     │  Subscribe(taskID)   │                    │                       │
  │                     ├─────────────────────▶│                    │                       │
  │                     │                      │                    │  session/prompt       │
  │                     │                      │                    ├──────────────────────▶│
  │                     │                      │                    │  session/update *     │
  │                     │                      │                    │◀──────────────────────│
  │                     │                      │  event via hub     │                       │
  │                     │                      │◀───────────────────┤                       │
  │  SSE: update...     │                      │                    │                       │
  │◀────────────────────┤                      │                    │                       │
  │                     │                      │  CompleteTask()    │                       │
  │                     │                      │◀───────────────────┤                       │
  │  SSE: done          │                      │                    │                       │
  │◀────────────────────┤                      │                    │                       │
```

### 4.2 反向 RPC（权限确认）

Agent 要做危险操作（`rm -rf` / 写文件）时：

```
claude-code-acp → session/request_permission → ACP client
                                                   │
                                                   ▼
                                    PermissionHandler 回调
                                                   │
                                                   ▼
                           mailbox event: permission_request
                                                   │
                                                   ▼
                                    gateway SSE → iOS
                                                   │
                                           用户点"允许"
                                                   ▼
                            POST /api/tasks/:id/approve → ACP client
                                                   │
                                                   ▼
                             Response { outcome: selected, optionId }
                                                   │
                                                   ▼
                                          claude-code-acp 继续
```

## 5. 关键设计选型

### 5.1 为什么包装成 Provider 而不是 AgentTool？
- ACP agent 本身是一个有记忆、有工具的完整 agent；当 Tool 用等于"agent 里套 agent"，事件流语义混乱。
- 当 Provider 用语义就是"换个后端模型"，modu 的 `Agent` / `CodingAgent` / swarm 全部不改。

### 5.2 为什么 session 按 cwd 区分而不是全局一个？
- ACP session 绑定工作目录，切目录需要新 session；
- 同 cwd 的多次 prompt 复用同一 session 保留上下文；
- 不同 cwd 的任务并行互不干扰。

### 5.3 为什么 gateway 走 mailbox 而不是直接 call Provider？
- 同步 HTTP + 长任务（LLM 可能跑几分钟）容易被代理/NAT 掐断；
- 异步队列：请求"发任务"立即返回 taskID，后续 SSE 订阅；
- 断线重连能力：SSE 可以 reconnect，mailbox 保留最后状态；
- 天然支持多 worker 横向扩展。

### 5.4 为什么 bridge 是纯函数？
- 单测最好写（给 ACP notification fixture → 断言产出的 `AgentEvent`）
- ACP 协议规范还在演进，改 bridge 不影响其他模块

## 6. 配置文件 schema

`~/.modu/acp.config.json`（沿用 acpone 的形态，略作精简）：

```json
{
  "agents": [
    {
      "id": "claude",
      "name": "Claude Code",
      "command": "npx",
      "args": ["-y", "@zed-industries/claude-code-acp"],
      "env": {
        "ANTHROPIC_API_KEY": "sk-...",
        "API_TIMEOUT_MS": "600000"
      },
      "permissionMode": "default"
    },
    {
      "id": "codex",
      "name": "Codex CLI",
      "command": "npx",
      "args": ["-y", "@zed-industries/codex-acp"],
      "env": { "OPENAI_API_KEY": "sk-..." },
      "permissionMode": "default"
    }
  ],
  "defaultAgent": "claude",
  "gateway": {
    "addr": "0.0.0.0:7080",
    "authToken": "env:MODU_ACP_TOKEN"
  }
}
```

## 7. 测试策略

| 层 | 手段 |
|---|---|
| jsonrpc | 纯单测：marshal/unmarshal/判别/边界 |
| process | 单测：起一个 `echo` / `cat` 进程做 stdio 回声，验证读写 / graceful shutdown |
| client | 单测：用 goroutine 模拟 agent（两对 pipe），覆盖 request/response/notification/reverse-request |
| bridge | 单测：加载 fixture notifications（JSON 文件），断言输出的 AgentEvent 序列 |
| provider | 单测：mock ACP server goroutine，覆盖 initialize → session/new → session/prompt → 完整流 |
| manager | 单测：配置 roundtrip + 池状态 |
| gateway | httptest：handler 级别，mailbox 用 in-memory hub |
| E2E | `examples/acp_e2e/`：真启动 `claude-code-acp`（或 mock ACP），跑一个 prompt，断言最终事件流 |

**单测优先**。E2E 在 CI 上需要配 key，先在本地跑通。

## 8. 安全与权限

- **API token**：gateway 强制 `Authorization: Bearer`，token 只在家里机器的 env
- **ACP 密钥**：每个 agent 的 API key 只在 modu 进程 env，不经网络
- **反向 fs 请求**：`FSHandler` 默认拒绝 ALL；只在用户显式授权的目录白名单内允许读写
- **权限审批**：危险操作（shell / write）默认 `default` 模式，必须经过 `approve` API；iOS 侧要有明确的允许/拒绝 UI
- **Tailscale 推荐**：gateway 监听内网接口即可，不对公网暴露

## 9. 可观测性

复用 `pkg/trace`：
- 每个 ACP client 初始化时 attach 一个 `trace.Recorder`
- 所有 `AgentEvent` 和 mailbox event 都进 trace，可回放
- gateway 同时写入 `trace/gateway-*.jsonl`，用于审计 iOS 下发的任务

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| ACP 协议还在演进 | bridge 做成纯翻译层，协议变只改一处 |
| ACP agent 进程崩溃 | manager 侧加 restart 策略 + 超时 |
| iOS 离线 / 断线 | mailbox 持久化 + SSE reconnect + 任务 id 对齐 |
| agent stdout 超大响应 | Scanner buffer 10 MB（acpone 验证过） |
| 反向 fs 请求泄漏隐私 | 默认拒绝 + 用户白名单 + 审计日志 |
| 多 worker 资源爆掉 | swarm 的 MaxAgents 限制 + 任务队列背压 |
