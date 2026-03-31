## 1. hub_types.go 扩展

- [x] 1.1 在 `Task` 中添加 `RecoveryCount int` 字段（`json:"recovery_count,omitempty"`）

## 2. event.go 扩展

- [x] 2.1 新增 `EventTypeTaskRecovered EventType = "task.recovered"`

## 3. hub.go 核心改动

- [x] 3.1 在 `Hub` struct 中添加 `maxTaskRecoveries int` 字段
- [x] 3.2 新增 `HubOption`：`WithMaxTaskRecoveries(n int) HubOption`，默认值 3
- [x] 3.3 在 `New()` 中设置 `maxTaskRecoveries` 默认值（3）并应用 option
- [x] 3.4 实现 `recoverTask(taskID, evictedAgentID string)`（在写锁内调用）：
  - 查找任务，不存在或状态非 running 时直接返回
  - 重置任务 assignee 字段（OwnerID、AssignedTo、Assignees）
  - SwarmOrigin=true 且 RecoveryCount < maxTaskRecoveries：重置状态为 pending，入队，发布 EventTypeTaskRecovered
  - 否则：标记 failed，写入错误原因，发布 EventTypeTaskUpdated
  - 保存到 store
- [x] 3.5 修改 `evictOfflineAgents()`：在删除 agent 前，若 `agentInfo.CurrentTask != ""`，调用 `recoverTask`

## 4. SQLite store 扩展

- [x] 4.1 在 `sqlitestore` 的 `tasks` 表中添加 `recovery_count INTEGER DEFAULT 0` 列（迁移兼容：用 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`）
- [x] 4.2 在 `SaveTask` 中包含 `recovery_count` 字段
- [x] 4.3 在 `loadTask` / `GetTask` 中读取 `recovery_count` 字段

## 5. 测试

- [x] 5.1 `TestRecoverSwarmTask`：注册 agent → 发布 swarm 任务 → agent 认领（running）→ 模拟 agent 超时（直接调用 `evictOfflineAgents`）→ 断言任务重新进入 swarm 队列（pending，RecoveryCount=1）
- [x] 5.2 `TestRecoverSwarmTask_MaxRetries`：RecoveryCount 达到上限后，再次驱逐 → 断言任务变为 failed
- [x] 5.3 `TestRecoverExplicitTask`：显式分配的任务（SwarmOrigin=false）→ agent 被驱逐 → 断言任务变为 failed（不入队）
- [x] 5.4 `TestRecoverTask_AlreadyCompleted`：任务已 completed → 驱逐 agent → 断言任务状态不变（幂等）
- [x] 5.5 `TestEventTaskRecovered`：验证恢复时发布了 `task.recovered` 事件
