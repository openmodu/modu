## Why

当前 `evictOfflineAgents()` 在检测到 agent 心跳超时后，直接删除 agent 信息，但**不处理其正在运行的任务**。这导致：

- 被驱逐 agent 的任务永远卡在 `running` 状态
- 没有任何恢复机制，任务结果永远丢失
- 在不稳定网络或长任务场景下，任务可靠性极低

对于 Swarm 场景，任务应该是"至少执行一次"的——agent 挂了，任务要能被别的 agent 重新领取。

## What Changes

- 扩展 `evictOfflineAgents()`：在删除 agent 前，检查其 `CurrentTask`，对卡住的任务执行恢复
- 新增 `recoverTask(taskID string)`：将 SwarmOrigin 的任务重新入队；非 Swarm 任务标记为 failed（附带原因说明）
- 新增 `EventTypeTaskRecovered` 事件
- 新增 `Task.RecoveryCount int` 字段：记录被恢复的次数，防止无限重试
- 新增 `Hub` 配置项 `MaxTaskRecoveries int`（默认 3）

## Capabilities

### New Capabilities

- `task-auto-recovery`：agent 被驱逐时，其 running 任务自动重新入队（SwarmOrigin=true）或标记 failed

### Modified Capabilities

- `mailbox-hub`：evictionLoop 增加恢复逻辑，向后兼容
- `mailbox-event`：新增 task.recovered 事件类型

## Impact

- 无破坏性改动，现有显式分配任务的行为不变（会被标记 failed，不会静默丢失）
- Swarm 任务在 agent 崩溃后自动恢复，提升 Swarm 可靠性
- 新字段 `RecoveryCount` 向后兼容（omitempty）
