## 1. Hub 扩展：Swarm Queue + Capabilities

- [ ] 1.1 在 `AgentInfo` 中添加 `Capabilities []string`
- [ ] 1.2 在 `Task` 中添加 `RequiredCaps []string`
- [ ] 1.3 在 `Hub` 中添加 `swarmQueue []string` 字段
- [ ] 1.4 实现 `SetCapabilities(agentID string, caps []string) error`
- [ ] 1.5 实现 `PublishTask(creatorID, description string, requiredCaps ...string) (string, error)`（不要求 creator 是注册 agent）
- [ ] 1.6 实现 `ClaimTask(agentID string) (Task, bool)`（原子认领，能力匹配）
- [ ] 1.7 实现 `SwarmQueueLen() int`
- [ ] 1.8 实现 `ListSwarmQueue() []Task`
- [ ] 1.9 在 `loadFromStore` 中重建 swarmQueue（恢复 status=pending 且无 assignee 的任务）

## 2. Event 扩展

- [ ] 2.1 在 `event.go` 中新增 `EventTypeSwarmTaskPublished`
- [ ] 2.2 新增 `EventTypeSwarmTaskClaimed`

## 3. Server 命令扩展

- [ ] 3.1 新增 `AGENT.SETCAPS <agent_id> <cap1> [cap2...]` 命令
- [ ] 3.2 新增 `TASK.PUBLISH <creator_id> <description> [cap1 cap2...]` 命令
- [ ] 3.3 新增 `TASK.CLAIM <agent_id>` 命令（返回 task JSON 或 null）
- [ ] 3.4 新增 `TASK.QUEUE` 命令（返回 JSON 数组）

## 4. Client 扩展

- [ ] 4.1 新增 `SetCapabilities(ctx, caps ...string) error`
- [ ] 4.2 新增 `PublishTask(ctx, description string, caps ...string) (string, error)`
- [ ] 4.3 新增 `ClaimTask(ctx) (*mailbox.Task, error)`（返回 nil 表示队列空）
- [ ] 4.4 新增 `ListSwarmQueue(ctx) ([]mailbox.Task, error)`

## 5. pkg/swarm/swarm.go

- [ ] 5.1 定义 `AgentFactory` 接口：`Spawn(ctx, agentID, caps) error`
- [ ] 5.2 定义 `SpawnPolicy` struct
- [ ] 5.3 定义 `Swarm` struct（hub, factory, policy, agents map, counters）
- [ ] 5.4 实现 `New(hub, factory, policy) *Swarm`
- [ ] 5.5 实现 `Start()`：spawn MinAgents 并启动伸缩循环
- [ ] 5.6 实现 `Stop()`：cancel 所有 agent context
- [ ] 5.7 实现 `scale()`：扩容/缩容逻辑
- [ ] 5.8 实现 `AgentCount() int`

## 6. examples/swarm_demo/main.go

- [ ] 6.1 启动 mailbox server（`:16381`）+ dashboard（`:8083`）
- [ ] 6.2 创建 Swarm（MinAgents=2, MaxAgents=5，能力: text-processing）
- [ ] 6.3 实现 `inProcessFactory`：Spawn 启动 goroutine
- [ ] 6.4 实现 `runSwarmAgent`：注册、声明能力、循环 ClaimTask、处理、CompleteTask
- [ ] 6.5 实现 `publishTasks`：延迟发布多个任务，模拟外部流量
- [ ] 6.6 Demo 输出清晰的日志：哪个 agent 认领了哪个任务
