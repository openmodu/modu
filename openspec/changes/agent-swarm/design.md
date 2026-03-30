## Architecture

```
外部发布者
    │  PublishTask(desc, caps...)
    ▼
[Swarm Queue] ← 无 assignee 的 pending 任务队列（FIFO）
    │
    │  ClaimTask(agentID)  ← 原子操作，首个匹配能力的 agent 获得任务
    ▼
Agent A ──┐
Agent B ──┤  竞争认领，无中间人
Agent C ──┘
    │
    ▼
  Swarm Manager（后台）
  监控 queue_len vs idle_agents → 触发 AgentFactory.Spawn / Despawn
```

## Core Data Structures

### AgentInfo（扩展）
```go
type AgentInfo struct {
    // ...existing fields...
    Capabilities []string `json:"capabilities,omitempty"`
}
```

### Task（扩展）
```go
type Task struct {
    // ...existing fields...
    RequiredCaps []string `json:"required_caps,omitempty"`
}
```

### Hub（扩展）
```go
type Hub struct {
    // ...existing fields...
    swarmQueue []string  // 等待被认领的 task ID（FIFO）
}
```

## Key Operations

### PublishTask（不要求 creator 是注册 agent）
```
1. 生成 task ID，创建 Task（status=pending, no assignee）
2. 追加 task ID 到 swarmQueue
3. 发布 EventTypeSwarmTaskPublished 事件
4. 返回 task ID
```

### ClaimTask（原子）
```
1. 获取 agentID 的 capabilities（from agentInfos）
2. 遍历 swarmQueue（FIFO），找第一个满足 requiredCaps ⊆ agentCaps 的任务
3. 从 swarmQueue 移除该任务
4. AssignTask（设 ownerID = agentID）
5. StartTask（status = running）
6. SetAgentStatus(agentID, "busy", taskID)
7. 返回 task 快照
```

### Capability Matching
```
requiredCaps = []  → 任意 agent 都能认领
requiredCaps = ["x","y"] → agent 的 caps 必须包含 x 和 y
```

## Swarm Manager

```go
type SpawnPolicy struct {
    MinAgents     int
    MaxAgents     int
    Capabilities  []string
    ScaleUpRatio  float64       // 当 queue/idle > ratio 时扩容（默认 1.0）
    CheckInterval time.Duration // 默认 2s
}
```

**伸缩逻辑（每 CheckInterval 执行）：**
- 扩容：`queueLen > 0 && idleCount == 0 && current < MaxAgents` → spawn 1 个
- 缩容：`queueLen == 0 && busyCount == 0 && current > MinAgents` → despawn 1 个

## Server Commands

| Command | Args | Returns |
|---------|------|---------|
| `AGENT.SETCAPS` | `<agent_id> <cap1> [cap2...]` | OK |
| `TASK.PUBLISH` | `<creator_id> <description> [cap1 cap2...]` | task_id |
| `TASK.CLAIM` | `<agent_id>` | task JSON \| null |
| `TASK.QUEUE` | _(none)_ | JSON array of queued tasks |

## File Layout

```
pkg/
  mailbox/
    hub.go          ← 扩展：swarm queue + 方法
    event.go        ← 扩展：swarm event types
    server/
      server.go     ← 扩展：新命令
    client/
      agent.go      ← 扩展：新方法
  swarm/
    swarm.go        ← 新包：Swarm + AgentFactory + SpawnPolicy
examples/
  swarm_demo/
    main.go         ← 新示例
```
