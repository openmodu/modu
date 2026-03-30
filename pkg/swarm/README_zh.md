# pkg/swarm

基于 [mailbox](../mailbox) 构建的自动伸缩 Agent Swarm 管理器。

## 概览

Agent Teams 使用固定的 Orchestrator，把任务显式分配给命名 worker。Swarm 则相反：**没有固定调度者**。任务会被发布到共享队列，Agent 自主竞争认领。

| | Agent Teams | Agent Swarm |
|---|---|---|
| 任务分配 | Orchestrator 推送给指定 Agent | 任意调用方发布任务，Agent 竞争式拉取 |
| 角色 | 预定义（editor、reviewer 等） | 动态，谁具备能力谁就认领 |
| 扩缩容 | 手动管理固定池 | `Swarm` 根据负载自动增减 Agent |
| 认领机制 | 无 | `ClaimTask`，原子化、先到先得 |

## 工作方式

```
Publisher ──► PublishTask(desc, caps...) ──► [Swarm Queue]
                                                   │
                                    ClaimTask(agentID)   ← 原子认领，按能力匹配
                                                   │
                              Agent A ─────────────┤
                              Agent B ─────────────┤  并发竞争任务
                              Agent C ─────────────┘
                                        │
                                   Swarm Manager（后台）
                              监控 queue_len 和 idle_agents
                                  → spawn / despawn agents
```

### 能力匹配

Agent 注册后可声明能力列表。任务也可以声明所需能力。只有当 Agent 的能力集合覆盖任务要求时，`ClaimTask` 才会返回该任务。未声明能力要求的任务可被任意 Agent 认领。

### 自动伸缩规则

| 条件 | 动作 |
|---|---|
| `queue > 0` 且 `idle == 0` 且 `current < MaxAgents` | 新增一个 Agent |
| `queue / idle > ScaleUpRatio` 且 `current < MaxAgents` | 新增一个 Agent |
| `queue == 0` 且 `busy == 0` 且 `current > MinAgents` | 缩减一个 Agent |

## 使用方式

### 1. 实现 AgentFactory

```go
type MyFactory struct{ addr string }

func (f *MyFactory) Spawn(ctx context.Context, agentID string, caps []string) error {
    go runAgent(ctx, agentID, f.addr, caps)
    return nil
}
```

### 2. 创建并启动 Swarm

```go
hub := srv.Hub()

sw := swarm.New(hub, &MyFactory{addr: mailboxAddr}, swarm.SpawnPolicy{
    MinAgents:     2,
    MaxAgents:     8,
    Capabilities:  []string{"text-processing"},
    ScaleUpRatio:  1.5,           // queue/idle > 1.5 时扩容
    CheckInterval: 2 * time.Second,
})
sw.Start()
defer sw.Stop()
```

### 3. 从任意位置发布任务

```go
c := client.NewMailboxClient("my-service", mailboxAddr)
taskID, err := c.PublishTask(ctx, "summarise this article", "text-processing")
```

### 4. Worker 循环

```go
func runAgent(ctx context.Context, agentID, addr string, caps []string) {
    c := client.NewMailboxClient(agentID, addr)
    _ = c.Register(ctx)
    _ = c.SetCapabilities(ctx, caps...)
    _ = c.SetStatus(ctx, "idle", "")

    for {
        task, err := c.ClaimTask(ctx)
        if err != nil || task == nil {
            time.Sleep(400 * time.Millisecond)
            continue
        }
        _ = c.SetStatus(ctx, "busy", task.ID)

        result := doWork(ctx, task)

        _ = c.CompleteTask(ctx, task.ID, result)
        _ = c.SetStatus(ctx, "idle", "")
    }
}
```

## 带对抗式验证的 Swarm

如果你想要“队列执行 + 二次复核”，可以用 `PublishValidatedTask` 替代 `PublishTask`。

```go
taskID, _ := publisher.PublishValidatedTask(ctx,
    "写一段简短的产品介绍",
    2,   // 最大重试次数
    0.7, // 通过阈值
    "text-processing",
)

task, _ := worker.ClaimTask(ctx)
validateTaskID, _ := worker.SubmitForValidation(ctx, task.ID, "初稿结果")

validateTask, _ := validator.ClaimTask(ctx)
_ = validator.SubmitValidation(ctx, validateTask.ID, 0.85, "整体可用")

_ = taskID
_ = validateTaskID
```

系统会自动创建一个需要 `validate` 能力的验证任务。若验证失败，原始 swarm 任务会带着反馈重新入队，直到耗尽 `MaxRetries`。更完整的状态机和任务字段说明见 [pkg/mailbox](../mailbox/README_zh.md)。

## Mailbox 协议

Swarm 模式新增了以下服务端命令：

| 命令 | 说明 |
|---|---|
| `TASK.PUBLISH <creator> <desc> [cap...]` | 向 swarm 队列推入任务 |
| `TASK.CLAIM <agent_id>` | 原子认领第一个匹配任务，返回任务 JSON 或 null |
| `TASK.QUEUE` | 列出当前等待中的队列任务 |
| `AGENT.SETCAPS <agent_id> <cap...>` | 声明 Agent 能力列表 |

## 运行示例

```bash
go run ./examples/swarm_demo/
```

会打开 `http://localhost:8083` 的 Dashboard，可以看到任务到来时自动扩容、队列清空后自动缩容。
