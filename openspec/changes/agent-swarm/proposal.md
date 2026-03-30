## Why

现有的 Agent Teams 是**结构化协作**：有固定 Orchestrator、预定义角色、显式任务分配。这对确定性流程很好，但缺乏弹性——队列积压时无法自动扩容，新任务必须由 Orchestrator 手动分发。

真正的 Swarm 模式解决三个问题：
1. **无固定 Orchestrator**：任何人（外部系统、其他 agent）都能向公共队列发布任务
2. **竞争认领**：多个 agent 自主抢占任务，无需中间人调度
3. **动态伸缩**：任务积压时自动 spawn 新 agent；队列空闲时自动回收

## What Changes

- 扩展 `pkg/mailbox/hub.go`：新增 swarm 任务队列、能力（capabilities）机制、`PublishTask` / `ClaimTask` 原子操作
- 扩展 `pkg/mailbox/server/server.go`：新增 `TASK.PUBLISH`、`TASK.CLAIM`、`TASK.QUEUE`、`AGENT.SETCAPS` 命令
- 扩展 `pkg/mailbox/client/agent.go`：新增对应客户端方法
- 新增 `pkg/swarm/swarm.go`：Swarm 管理器，含 `AgentFactory` 接口、`SpawnPolicy`、后台自动伸缩循环
- 新增 `examples/swarm_demo/main.go`：完整可运行示例

## Capabilities

### New Capabilities

- `swarm-task-queue`：任务发布到公共队列（无指定 assignee），agent 原子竞争认领
- `swarm-capabilities`：agent 声明能力列表，任务可声明所需能力，认领时自动匹配
- `swarm-auto-scaling`：Swarm 管理器监控队列深度与空闲 agent 比例，自动 spawn / despawn agent

### Modified Capabilities

- `mailbox-hub`：向后兼容，在现有任务管理基础上增加 swarm 队列
- `mailbox-server`：新增命令，不改动现有命令
- `mailbox-client`：新增方法，不改动现有方法

## Impact

- 无破坏性改动，现有 agent teams 用法完全兼容
- 新包：`pkg/swarm/`
- 新示例：`examples/swarm_demo/`
