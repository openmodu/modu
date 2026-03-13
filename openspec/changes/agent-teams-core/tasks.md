## 1. Hub 扩展：AgentInfo + Task 数据结构与管理方法

- [x] 1.1 在 `pkg/mailbox/hub.go` 中新增 `AgentInfo` struct、`Task` struct、`TaskStatus` 枚举
- [x] 1.2 Hub 增加 `agentInfos map[string]*AgentInfo`、`tasks map[string]*Task`、`taskCounter uint64` 字段
- [x] 1.3 实现 `SetAgentRole(agentID, role string) error`
- [x] 1.4 实现 `SetAgentStatus(agentID, status, taskID string) error`
- [x] 1.5 实现 `GetAgentInfo(agentID string) (AgentInfo, error)`
- [x] 1.6 实现 `CreateTask(creatorID, description string) (string, error)` → 返回 task_id
- [x] 1.7 实现 `AssignTask(taskID, agentID string) error`
- [x] 1.8 实现 `StartTask(taskID string) error`
- [x] 1.9 实现 `CompleteTask(taskID, result string) error`
- [x] 1.10 实现 `FailTask(taskID, errMsg string) error`
- [x] 1.11 实现 `GetTask(taskID string) (Task, error)`
- [x] 1.12 实现 `ListTasks() []Task`
- [x] 1.13 Register 时同步初始化 AgentInfo；eviction 时清理 AgentInfo
- [x] 1.14 编写单元测试 `pkg/mailbox/hub_task_test.go`：覆盖全部新方法，含并发安全测试

## 2. Hub 事件订阅机制

- [x] 2.1 新增 `pkg/mailbox/event.go`：定义 `EventType`、`Event` struct
- [x] 2.2 Hub 增加 `subscribers []chan Event` 字段
- [x] 2.3 实现 `Hub.Subscribe() <-chan Event`（缓冲 256）
- [x] 2.4 实现 `Hub.Unsubscribe(ch <-chan Event)`
- [x] 2.5 在所有状态变更方法中调用内部 `publish(event)` 非阻塞推送
- [x] 2.6 编写单元测试 `pkg/mailbox/event_test.go`：订阅/取消订阅、事件触发验证

## 3. Server 命令扩展

- [x] 3.1 在 `pkg/mailbox/server/server.go` 中新增 `AGENT.SETROLE` 命令处理
- [x] 3.2 新增 `AGENT.SETSTATUS` 命令处理（可选第三个参数 task_id）
- [x] 3.3 新增 `AGENT.INFO` 命令处理，返回 JSON
- [x] 3.4 新增 `TASK.CREATE` 命令处理，返回 task_id
- [x] 3.5 新增 `TASK.ASSIGN` 命令处理
- [x] 3.6 新增 `TASK.START` 命令处理
- [x] 3.7 新增 `TASK.DONE` 命令处理
- [x] 3.8 新增 `TASK.FAIL` 命令处理
- [x] 3.9 新增 `TASK.LIST` 命令处理，返回 JSON 数组
- [x] 3.10 新增 `TASK.GET` 命令处理，返回 JSON
- [x] 3.11 编写集成测试 `pkg/mailbox/server/server_task_test.go`：启动测试服务器，通过 Redis client 验证所有新命令

## 4. 结构化消息协议

- [x] 4.1 新增 `pkg/mailbox/message.go`：定义 `Message` struct 和 `MessageType` 常量
- [x] 4.2 添加 `NewTaskAssignMessage`、`NewTaskResultMessage` 构造函数
- [x] 4.3 添加 `ParseMessage(s string) (Message, error)` 解析函数
- [x] 4.4 编写单元测试 `pkg/mailbox/message_test.go`

## 5. Dashboard HTTP Server

- [x] 5.1 创建 `pkg/mailbox/dashboard/dashboard.go`，定义 `Dashboard` struct
- [x] 5.2 实现 `NewDashboard(hub *mailbox.Hub) *Dashboard`
- [x] 5.3 实现 `Dashboard.Start(ctx context.Context, addr string) error`（启动 HTTP server）
- [x] 5.4 实现 `GET /api/agents` → JSON `[]AgentInfo`
- [x] 5.5 实现 `GET /api/tasks` → JSON `[]Task`
- [x] 5.6 实现 `GET /api/tasks/{id}` → JSON `Task`
- [x] 5.7 实现 `GET /events` → SSE（订阅 Hub 事件，连接断开时取消订阅）
- [x] 5.8 实现 `GET /` → 内嵌 HTML（agents grid + tasks list，SSE 实时更新，无外部依赖）
- [x] 5.9 编写单元测试 `pkg/mailbox/dashboard/dashboard_test.go`：REST API 返回格式验证，SSE 连接测试

## 6. Client SDK 扩展

- [x] 6.1 在 `pkg/mailbox/client/agent.go` 中新增 `SetRole(ctx, role string) error`
- [x] 6.2 新增 `SetStatus(ctx, status, taskID string) error`
- [x] 6.3 新增 `GetAgentInfo(ctx, agentID string) (mailbox.AgentInfo, error)`
- [x] 6.4 新增 `CreateTask(ctx, description string) (string, error)`
- [x] 6.5 新增 `AssignTask(ctx, taskID, agentID string) error`
- [x] 6.6 新增 `StartTask(ctx, taskID string) error`
- [x] 6.7 新增 `CompleteTask(ctx, taskID, result string) error`
- [x] 6.8 新增 `FailTask(ctx, taskID, errMsg string) error`
- [x] 6.9 新增 `ListTasks(ctx context.Context) ([]mailbox.Task, error)`
- [x] 6.10 新增 `GetTask(ctx, taskID string) (mailbox.Task, error)`
- [x] 6.11 编写单元测试 `pkg/mailbox/client/client_task_test.go`（需要运行中的测试服务器）

## 7. SpawnAgentTool

- [x] 7.1 新增 `pkg/coding_agent/spawn_agent_tool.go`，定义 `SpawnAgentTool` struct
- [x] 7.2 实现 `AgentTool` 接口的 `Name()`、`Label()`、`Description()`、`Parameters()`
- [x] 7.3 实现 `Execute(ctx, toolCallID, args, onUpdate)`：CreateTask → AssignTask → MSG.SEND → 轮询 TASK.GET → 返回结果
- [x] 7.4 实现 `NewSpawnAgentTool(mailboxClient, opts...)` 构造函数，支持 pollInterval/timeout 配置
- [x] 7.5 编写单元测试 `pkg/coding_agent/spawn_agent_tool_test.go`（mock mailbox client）

## 8. examples/agent_teams Demo

- [x] 8.1 创建 `examples/agent_teams/main.go`：启动 mailbox server + dashboard（`:8080`）+ 3 个 goroutine（orchestrator + worker-1 + worker-2）
- [x] 8.2 orchestrator 逻辑：创建 2 个任务，分别委派给 worker-1/2，等待完成，打印汇总结果
- [x] 8.3 worker 逻辑：注册自己的角色，轮询 inbox，收到 task_assign 消息后处理，调用 TASK.DONE

## 9. README 更新

- [x] 9.1 在 `README.md` 中新增 "Agent Teams" 章节，介绍 mailbox 扩展功能（任务注册表、agent 元数据、看板）
- [x] 9.2 添加 SpawnAgentTool 使用说明
- [x] 9.3 添加 examples/agent_teams 运行说明
- [x] 9.4 更新功能列表
