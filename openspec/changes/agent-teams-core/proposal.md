## Why

项目已有完整的 agent 引擎（`pkg/agent/`）和 mailbox 消息传递系统（`pkg/mailbox/`），但缺少 agent teams 协作所需的核心基础设施：没有任务注册表、没有 agent 元数据（角色/状态）、没有结构化委派机制、没有可视化看板。要让多个 agent 协同完成复杂任务（orchestrator 拆解任务 → 委派给 worker agent → 收集结果），需要补齐这些基础层。

## What Changes

- 扩展 `pkg/mailbox/hub.go`：增加 AgentInfo（角色/状态/当前任务）和 Task（任务注册表）数据结构及管理方法
- 扩展 `pkg/mailbox/server/server.go`：新增 `AGENT.SETROLE`、`AGENT.SETSTATUS`、`AGENT.INFO`、`TASK.*` 命令
- 新增 Hub 事件订阅机制：agent/task 状态变更时向订阅者推送事件
- 新增 `pkg/mailbox/dashboard/`：HTTP server，提供 REST API + SSE + 内嵌 HTML 看板
- 扩展 `pkg/mailbox/client/agent.go`：添加 task 和 agent metadata 相关方法
- 新增 `pkg/coding_agent/spawn_agent_tool.go`：实现 `SpawnAgentTool`，允许 orchestrator agent 委派任务给其他 agent 并等待结果
- 新增 `examples/agent_teams/`：orchestrator + 2 个 worker 的完整示例
- 更新 `README.md`

## Capabilities

### New Capabilities

- `mailbox-task-registry`：任务创建/分配/状态跟踪（pending → running → completed/failed），通过 Redis 协议命令操作
- `mailbox-agent-metadata`：agent 角色和状态管理，可查询任意 agent 当前在做什么
- `mailbox-events`：Hub 内部事件流，支持多订阅者，agent/task 状态变更实时推送
- `mailbox-dashboard`：HTTP 看板服务，REST API + SSE 实时推送 + 内嵌 HTML 可视化界面
- `spawn-agent-tool`：coding_agent 可用的工具，实现 orchestrator 向其他 agent 委派任务并等待结果

### Modified Capabilities

- `mailbox-hub`：在现有消息传递基础上增加 AgentInfo 和 Task 管理，向后兼容
- `mailbox-server`：新增命令，不改动现有命令
- `mailbox-client`：新增方法，不改动现有方法

## Impact

- 无破坏性改动，现有 mailbox 用法完全兼容
- 新文件：`pkg/mailbox/dashboard/dashboard.go`、`pkg/coding_agent/spawn_agent_tool.go`、`examples/agent_teams/`
- 新增依赖：无（使用标准库 `net/http`）
