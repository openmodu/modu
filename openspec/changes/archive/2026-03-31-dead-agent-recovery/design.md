## Architecture

```
evictionLoop (每 5s)
    │
    ▼
evictOfflineAgents()
    │ lastSeen[id] > 30s
    ├── recoverTask(agent.CurrentTask)   ← 新增
    │       │
    │       ├── SwarmOrigin=true, RecoveryCount < MaxTaskRecoveries
    │       │       → 重置任务状态为 pending，重新入队 swarmQueue
    │       │       → 发布 EventTypeTaskRecovered
    │       │
    │       ├── SwarmOrigin=true, RecoveryCount >= MaxTaskRecoveries
    │       │       → FailTask（附带 "max recoveries exceeded" 原因）
    │       │
    │       └── SwarmOrigin=false（显式分配任务）
    │               → FailTask（附带 "agent evicted" 原因）
    │
    └── 删除 agent（现有逻辑不变）
```

## Core Data Structures

### Task（扩展）
```go
type Task struct {
    // ...existing fields...
    RecoveryCount int `json:"recovery_count,omitempty"` // 被自动恢复的次数
}
```

### Hub（扩展）
```go
type Hub struct {
    // ...existing fields...
    maxTaskRecoveries int // 默认 3，通过 HubOption 配置
}
```

### HubOption（新增）
```go
// WithMaxTaskRecoveries 设置 swarm 任务最大自动恢复次数（默认 3）
func WithMaxTaskRecoveries(n int) HubOption
```

## Key Operations

### evictOfflineAgents()（修改）

```
1. 遍历 lastSeen，找超时 agent
2. 获取 agentInfo（需要在删除前获取）
3. 若 agentInfo.CurrentTask != "" → 调用 recoverTask(agentInfo.CurrentTask, agentID)
4. 执行现有删除逻辑（close inbox, delete maps, publish AgentEvicted）
```

### recoverTask(taskID, evictedAgentID string)（新增）

```
前置条件：持有 h.mu 写锁（由 evictOfflineAgents 持有）

1. 从 h.tasks 查找任务，若不存在则直接返回
2. 若任务状态不是 running，直接返回（可能已被其他途径完成）
3. 重置任务的 assignee 信息：
   - OwnerID = ""
   - AssignedTo = ""
   - Assignees = nil
4. if task.SwarmOrigin && task.RecoveryCount < h.maxTaskRecoveries:
   - task.RecoveryCount++
   - task.Status = TaskStatusPending
   - task.UpdatedAt = now
   - h.swarmQueue = append(h.swarmQueue, taskID)  // 重新入队（末尾，FIFO）
   - publishLocked(Event{Type: EventTypeTaskRecovered, TaskID: taskID, AgentID: evictedAgentID})
   - log.Printf("[Hub] Task %s recovered after agent %s evicted (attempt %d/%d)", ...)
   else:
   - reason := "agent evicted"
   - if task.SwarmOrigin { reason = "max recoveries exceeded" }
   - task.Status = TaskStatusFailed
   - task.Error = fmt.Sprintf("task failed: %s (agent: %s)", reason, evictedAgentID)
   - task.UpdatedAt = now
   - publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID})
   - log.Printf("[Hub] Task %s failed: %s", taskID, reason)
5. h.tasks[taskID] = task
6. h.store.SaveTask(task)（若 store 非 noop）
```

## Event 扩展

```go
// event.go 新增
EventTypeTaskRecovered EventType = "task.recovered"
```

Event payload:
```json
{
  "type": "task.recovered",
  "task_id": "task-abc",
  "agent_id": "worker-1",
  "data": { "recovery_count": 1, "max_recoveries": 3 }
}
```

## Server/Client 扩展

无新增命令。客户端通过订阅 SSE events 或轮询任务状态观察恢复行为。

可选：新增 `HUB.CONFIG` 命令（只读）供客户端查询 `maxTaskRecoveries` 配置，本期不做。

## 并发安全

`recoverTask` 在 `evictOfflineAgents` 持有写锁时调用，无需额外加锁。`h.store.SaveTask` 调用在锁内进行（与现有 CompleteTask/FailTask 一致）。

## 文件改动

```
pkg/mailbox/
  hub.go          ← 新增 maxTaskRecoveries 字段、WithMaxTaskRecoveries option、recoverTask()
                     修改 evictOfflineAgents() 在删除前调用 recoverTask
  hub_types.go    ← Task 新增 RecoveryCount 字段
  event.go        ← 新增 EventTypeTaskRecovered
```

无新包，无新文件（除测试外）。
