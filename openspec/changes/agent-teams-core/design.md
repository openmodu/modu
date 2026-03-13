## Architecture

### 数据模型

```
Hub
├── inboxes      map[agentID]chan string          (existing)
├── lastSeen     map[agentID]time.Time            (existing)
├── agentInfos   map[agentID]*AgentInfo           (new)
├── tasks        map[taskID]*Task                 (new)
├── taskCounter  uint64                           (new)
└── subscribers  []chan Event                     (new)

AgentInfo {
    ID          string
    Role        string        // e.g. "orchestrator", "coder", "researcher"
    Status      string        // "idle" | "busy"
    CurrentTask string        // task ID, empty if idle
    LastSeen    time.Time
}

Task {
    ID          string        // "task-<counter>"
    Description string
    CreatedBy   string        // agent ID
    AssignedTo  string        // agent ID
    Status      TaskStatus    // pending | running | completed | failed
    CreatedAt   time.Time
    UpdatedAt   time.Time
    Result      string
    Error       string
}

Event {
    Type    EventType   // agent.registered | agent.evicted | agent.updated | task.created | task.updated
    AgentID string
    TaskID  string
    Data    any         // AgentInfo or Task snapshot
}
```

### 命令协议扩展

现有命令不变，新增：

```
# Agent metadata
AGENT.SETROLE  <agent_id> <role>              → OK
AGENT.SETSTATUS <agent_id> <status> [task_id] → OK
AGENT.INFO     <agent_id>                     → JSON(AgentInfo)

# Task lifecycle
TASK.CREATE  <creator_id> <description>  → task_id
TASK.ASSIGN  <task_id> <agent_id>        → OK
TASK.START   <task_id>                   → OK
TASK.DONE    <task_id> <result>          → OK
TASK.FAIL    <task_id> <error>           → OK
TASK.LIST                                → JSON([]Task)
TASK.GET     <task_id>                   → JSON(Task)
```

### 事件订阅

Hub 维护一个订阅者列表，每个订阅者是一个 `chan Event`（缓冲 256）。
`Hub.Subscribe()` 返回 `<-chan Event`，`Hub.Unsubscribe(ch)` 移除。
所有状态变更（Register/Evict/SetAgentRole/SetAgentStatus/CreateTask/AssignTask/StartTask/CompleteTask/FailTask）
都调用内部 `publish(event)` 非阻塞推送。

### Dashboard HTTP Server

```
pkg/mailbox/dashboard/
└── dashboard.go    # Dashboard struct, NewDashboard(hub), Start(addr)

Routes:
  GET /              → HTML（内嵌，无外部依赖，纯原生 JS + CSS）
  GET /api/agents    → JSON []AgentInfo
  GET /api/tasks     → JSON []Task
  GET /api/tasks/:id → JSON Task
  GET /events        → SSE stream (text/event-stream)

SSE event format:
  event: agent.registered\ndata: {...}\n\n
  event: task.updated\ndata: {...}\n\n
```

Dashboard 订阅 Hub 事件，收到后广播给所有 SSE 连接。

### SpawnAgentTool

```go
// pkg/coding_agent/spawn_agent_tool.go
type SpawnAgentTool struct {
    mailbox *client.Agent    // mailbox client (orchestrator's)
    pollInterval time.Duration
    timeout      time.Duration
}

// Parameters (JSON Schema):
{
    "target_agent_id": string,   // which agent to delegate to
    "task_description": string   // what to do
}

// Execution flow:
// 1. TASK.CREATE (self as creator)
// 2. TASK.ASSIGN task_id → target_agent_id
// 3. MSG.SEND target_agent_id ← JSON{type:"task_assign", from:self, task_id:..., payload:{description:...}}
// 4. Poll TASK.GET task_id every pollInterval until status=completed|failed (or timeout)
// 5. Return task.Result or error
```

### 结构化消息协议

```go
// pkg/mailbox/message.go
type Message struct {
    Type    string          `json:"type"`    // "task_assign" | "task_result" | "query" | "info"
    From    string          `json:"from"`
    TaskID  string          `json:"task_id,omitempty"`
    Payload json.RawMessage `json:"payload,omitempty"`
}
```

### Agent Teams 示例架构

```
examples/agent_teams/
├── main.go             # 启动 mailbox server + dashboard + orchestrator + 2 workers
├── orchestrator/       # 使用 SpawnAgentTool 将任务委派给 worker-1 和 worker-2
└── worker/             # 接收 task_assign 消息，执行，调用 TASK.DONE

Flow:
orchestrator → TASK.CREATE "research topic A" → task-1
            → TASK.ASSIGN task-1 → worker-1
            → MSG.SEND worker-1 {type:task_assign, task_id:task-1}
            → TASK.CREATE "research topic B" → task-2
            → TASK.ASSIGN task-2 → worker-2
            → MSG.SEND worker-2 {type:task_assign, task_id:task-2}
            → poll task-1, task-2 until completed
            → aggregate results
```

## Key Design Decisions

1. **Hub 扩展而非新建**：共享同一把 `sync.RWMutex`，避免 agent 注册/任务创建之间的竞态
2. **非阻塞 publish**：订阅者 channel 满时跳过，不阻塞 Hub 操作
3. **Dashboard 独立包**：`pkg/mailbox/dashboard/` 只依赖 Hub，不依赖 server，可单独嵌入任何程序
4. **SpawnAgentTool 在 coding_agent**：符合项目分层（coding_agent 是高层工具集合），不污染 mailbox 包
5. **JSON 编码的结构化消息**：MSG.SEND 的消息内容仍然是字符串，由上层约定 JSON 格式，mailbox 层保持通用
