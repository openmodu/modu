# Mailbox Agent 系统架构

Mailbox 把多 Agent 协作收敛为一套状态机：Agent 通过独立收件箱交换消息，任务与项目提供可查询的生命周期，Store 保存需要跨重启恢复的数据。它可以嵌入同一进程，也可以通过 Redis 线协议服务多个进程。

它不是消息队列、工作流引擎或 Agent 运行时。默认配置不会持久化消息，事件允许丢失，Pipeline 也不能跨重启恢复；LLM 调用、任务拆分和业务重试由接入方负责。

API 细节见 [中文参考](../reference/mailbox.zh-CN.md) 和 [English reference](../reference/mailbox.md)。

## 两种部署方式

### 进程内

调用方直接创建 `mailbox.Hub`，Agent 与 Hub 共享进程。这个方式没有网络协议和序列化开销，适合测试、单进程编排和嵌入式宿主。

```text
orchestrator ─┐
worker A ─────┼──▶ Hub ──▶ Store
worker B ─────┘      └──▶ Event subscribers
```

### 跨进程

`server.MailboxServer` 把同一个 Hub 暴露为 Redis 线协议的自定义命令；`client.MailboxClient` 使用 `go-redis` 发送这些命令。这里复用的是协议和客户端，不是 Redis 数据库。

```text
MailboxClient ── Redis wire protocol ──▶ MailboxServer ──▶ Hub
MailboxClient ── Redis wire protocol ──▶       │          ├── Store
Dashboard    ◀──────── HTTP/SSE ───────────────┘          └── inboxes
```

Server 当前不提供认证和 TLS。跨机器部署前必须在网络层限制访问，或在服务前增加认证与加密代理。

## Hub 保存什么

Hub 在内存中维护这些状态：

| 状态 | 作用 | 是否通过 `Store` 持久化 |
|---|---|---|
| `inboxes` | 每个在线 Agent 的缓冲收件箱 | 否 |
| `agentInfos` / `lastSeen` | 角色、忙闲状态、当前任务和心跳 | 仅角色 |
| `tasks` / `swarmQueue` | 任务状态与待认领队列 | Task 持久化，队列启动时重建 |
| `projects` | 一组相关任务 | 是 |
| `conversations` | 按 Task ID 归档的消息摘要 | 是 |
| `pipelines` | 顺序步骤和中间结果 | 否 |
| `subscribers` | 进程内事件订阅者 | 否 |

默认 `noopStore` 不写磁盘。`sqlitestore` 保存 Task、Project、Agent Role 和 Conversation；它不保存在线状态、收件箱内容、订阅者或 Pipeline 对象。

## Agent 生命周期

注册会创建容量为 100 的收件箱，并把 Agent 状态初始化为 `idle`。重复注册不会替换已有收件箱，但会刷新在线时间。

```text
Register
  ├─ create inbox when absent
  ├─ restore persisted role when present
  └─ publish agent.registered

Heartbeat ──▶ refresh LastSeen

30 seconds without heartbeat
  ├─ recover an owned swarm task when possible
  ├─ close inbox
  ├─ remove active Agent state
  └─ publish agent.evicted
```

Hub 每 5 秒检查一次心跳，超过 30 秒未更新就驱逐 Agent。`MailboxClient.Register` 会启动每 10 秒一次的保活；它使用传入的 `context.Context` 控制协程退出。

Agent 被驱逐时，Hub 会尝试恢复其正在处理的 Swarm 任务。任务重新入队的次数由 `maxTaskRecoveries` 限制，默认是 3。普通手动分配任务不会因此自动变成 Swarm 任务。

## 消息与对话记录

`Hub.Send` 接受任意字符串，但只有能够解析为 Mailbox `Message`、带有 `task_id` 且类型不是 `delegate` 的消息，才会自动进入对话记录。常用构造器会生成统一 JSON：

```json
{
  "type": "task_assign",
  "from": "orchestrator",
  "task_id": "task-1",
  "payload": {
    "description": "整理发布说明"
  }
}
```

发送过程遵循明确的失败语义：

- 目标 Agent 不存在：返回 `ErrAgentNotFound`；
- 目标收件箱已满：立即返回错误，不阻塞；
- 投递成功：把消息放入收件箱，并按条件追加对话记录；
- `Recv`：非阻塞读取，没有消息或 Agent 不存在时返回 `ok=false`；
- `Broadcast`：非阻塞写入所有收件箱，已满的目标会被跳过，调用方拿不到逐个失败结果。

因此 Mailbox 不提供“至少一次”或“恰好一次”投递。需要可靠交付时，调用方必须增加确认、重试和幂等键；只重试 `Send` 而不做去重，会产生重复业务动作。

## 四种任务组织方式

### 手动分配

Orchestrator 创建任务，再明确指定一个或多个 Agent：

```text
CreateTask → AssignTask → StartTask → CompleteTask
                                   ↘ FailTask
```

多参与者任务只有在所有 Assignee 都提交结果后才进入 `completed`。调用 `CompleteTask` 时必须传实际 Agent ID，否则 Hub 无法记录每个参与者的结果。

### 能力队列

Agent 先通过 `SetCapabilities` 声明能力，发布方用 `PublishTask` 指定要求。`ClaimTask` 原子地取走队列中第一个能力集合满足要求的任务。

```text
PublishTask(required caps)
       ▼
pending swarm queue
       ▼ ClaimTask(agent caps)
running → completed | failed
```

没有满足能力的 Agent 时，任务留在队列。这个状态不是错误，也不会自动超时。

### 对抗式验证

需要独立复核时，使用验证型任务：

```text
PublishValidatedTask
  ▼
worker claims and submits result
  ▼
validating + new task requiring "validate"
  ▼
validator score >= threshold ──▶ validated
validator score < threshold  ──▶ re-queue or failed
```

验证者应与执行者分离。Hub 会保存得分、反馈、历史和重试次数；当分数不足且仍有重试额度时，原任务携带反馈重新入队。

### Pipeline

Pipeline 把至少两个能力队列步骤串联起来。每一步完成后，Hub 把结果替换到下一步 `DescriptionTemplate` 的 `{{.PrevResult}}`，然后发布下一个 Swarm 任务。

```text
step 0 result ──▶ render step 1 ──▶ queue
step 1 result ──▶ render step 2 ──▶ queue
...
```

Pipeline 对象只存在内存中。Task 会通过 Store 保存，但 Hub 重启后不会重建 Pipeline 当前步骤和结果列表。需要可恢复编排时，应在 Mailbox 之上保存工作流状态，不能只依赖 `PublishPipeline`。

## 持久化与恢复

Hub 启动时从 Store 恢复 Task、Project、Agent Role 和 Conversation，并根据已保存的 Task ID / Project ID 调整计数器，避免新 ID 与历史记录碰撞。

Swarm 队列只从同时满足以下条件的 Task 重建：

- `SwarmOrigin == true`；
- 状态为 `pending`；
- 没有 Assignee。

普通 `CreateTask` 产生的待处理任务不会在重启后被误放进 Swarm 队列。Agent 在线状态不会恢复；Agent 必须重新注册，持久化角色才会重新挂到该 Agent 上。

Store 写入失败目前通过日志报告，部分 Hub 方法不会把持久化错误返回给调用方。这意味着“内存状态已更新、磁盘未成功保存”是可能的。把 SQLite 文件放在不可靠介质上，或实现远程 Store 时，必须监控这些日志并设计外部一致性策略。

## 事件与 Dashboard

`Hub.Subscribe` 返回容量为 256 的 channel。状态变化会非阻塞发布；订阅者消费过慢时，Hub 丢弃新事件，而不是拖慢任务和消息处理。

事件适合刷新 UI，不适合作为审计日志或可靠业务总线。Dashboard 的 SSE 连接会先读取 Hub 快照，再消费增量事件；如果需要纠正事件丢失造成的显示偏差，应重新请求快照。

Dashboard 读取 Agents、Projects、Tasks、Conversations 和 Pipelines，并通过 HTTP/SSE 展示。它不改变 Hub 的持久化与投递保证。

## Redis 协议边界

Server 支持以下命令族：

- `AGENT.*`：注册、心跳、列表、角色、状态和能力；
- `MSG.*`：发送、接收和广播；
- `TASK.*`：手动任务、能力队列和验证；
- `PROJ.*`：项目创建、查询、完成和列表；
- `PIPELINE.*`：发布、查询和列表；
- `CONV.GET`：读取任务对话。

命令语义以 `pkg/mailbox/server/server.go` 和客户端方法为准。协议兼容只表示 Redis 客户端能够传输这些命令，不表示标准 Redis Server 能执行 Mailbox 的自定义命令。

## 接入判据

选择 Mailbox 前先回答四个问题：

1. 消息丢失是否允许？不允许就补确认、重试、幂等和持久队列。
2. 状态是否必须跨重启恢复？必须恢复就使用持久 Store，并避开内存 Pipeline 状态。
3. 任务由 Orchestrator 指派还是按能力认领？前者用手动任务，后者用 Swarm。
4. 结果是否需要独立复核？需要就使用验证型任务，并为 validator 声明 `validate` 能力。

如果第一题的答案是“不允许”，Mailbox 只能作为协调状态层，不能单独承担消息系统。

## 端到端示例

`examples/creative_team` 展示一个 Server、两个 Worker 和一个 Orchestrator 分进程协作：

```bash
go run ./examples/creative_team mailbox
go run ./examples/creative_team topic-selector
go run ./examples/creative_team editor
go run ./examples/creative_team orchestrator "写一篇关于孤独与创造力的短文"
```

示例默认连接 `localhost:6382`，Dashboard 监听 `0.0.0.0:8082`，并使用 `creative_team.db` 保存 Store 支持的数据。LLM 地址和模型由示例自己的环境变量配置，不属于 Mailbox 协议。
