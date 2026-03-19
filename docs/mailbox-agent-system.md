# Mailbox：构建多 Agent 协作系统的底层基础设施

> 本文以 `examples/creative_team` 为贯穿全文的示例，从上层业务逻辑出发，逐步深入 Mailbox 的协议设计、Hub 内核、持久化层和实时 Dashboard，最后讨论如何将新的 Agent 集成进来。

---

## 零、设计灵感：Claude Code Teams

Mailbox 的设计直接来源于 **Claude Code Teams** 的实践方式。

Claude Code Teams 是 Anthropic 官方探索的多 Agent 编程协作模式：多个 Claude 实例并行工作，每个实例专注于一个子任务（如实现某个模块、审查某段代码、撰写测试），彼此通过结构化消息传递上下文与结果，由一个 Orchestrator 实例协调整体进度。这套模式揭示了几个核心洞察：

- **Agent 不需要共享内存**，消息即协议——每个 Agent 只看自己的信箱，通过消息理解上下文
- **Orchestrator 是指挥而非执行者**，它负责拆解目标、分配任务、聚合结果，而不亲自完成具体工作
- **任务是一等公民**，每条对话都应该关联到某个任务 ID，这样才能追溯、重放、审计
- **独立进程比线程更健壮**，每个 Agent 运行在自己的进程甚至机器上，崩溃不互相影响

Mailbox 将这套思路落地为一个可运行的基础设施：信箱解决消息隔离，Hub 解决状态同步，`task_id` 贯穿全程保证可追溯，`Store` 接口保证重启可恢复。

---

## 一、为什么需要 Mailbox

多个 AI Agent 协同工作时，最核心的问题是**通信**和**状态同步**：

- Agent A 怎么把任务交给 Agent B？
- A 怎么知道 B 干完了？
- 中间的对话记录存在哪里？
- 新 Agent 加进来需要改多少现有代码？

直接使用 HTTP 调用或 Redis pub/sub 可以解决部分问题，但随着 Agent 数量增多，会遇到服务发现、消息堆积、任务生命周期管理等问题。Mailbox 的目标是在这些之上提供一个**对 Agent 友好的消息信箱 + 任务跟踪系统**。

---

## 二、整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                        Mailbox Server                           │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                         Hub                             │   │
│  │                                                          │   │
│  │  inboxes:  map[agentID] chan string  (每个 Agent 一个信箱) │   │
│  │  agentInfos: map[agentID] AgentInfo  (角色/状态/心跳)     │   │
│  │  tasks:    map[taskID] Task          (任务生命周期)        │   │
│  │  conversations: map[taskID] []Entry  (对话记录)           │   │
│  │  subscribers: []chan Event           (事件总线)           │   │
│  │                                                          │   │
│  └──────────┬───────────────────────────────────────────────┘   │
│             │ Subscribe/Unsubscribe                             │
│  ┌──────────▼──────────┐    ┌──────────────────────────────┐   │
│  │     Dashboard       │    │       SQLite Store           │   │
│  │  (SSE 实时推送)      │    │  tasks / agent_roles /       │   │
│  │  HTTP :8082         │    │  conversations               │   │
│  └─────────────────────┘    └──────────────────────────────┘   │
│                                                                 │
│  对外协议：Redis Inline Protocol（基于 redcon）                   │
│  监听端口：:6382                                                  │
└──────────────┬──────────────────────────────────────────────────┘
               │  TCP（Redis 协议）
    ┌──────────┼─────────────────────────────────┐
    │          │                                 │
┌───▼────┐ ┌──▼─────────────┐ ┌────────────────▼──┐
│  PMO   │ │ topic-selector │ │     editor        │
│(orch.) │ │   (worker)     │ │    (worker)       │
│        │ │                │ │                   │
│ LLM ✓  │ │    LLM ✓       │ │     LLM ✓         │
└────────┘ └────────────────┘ └───────────────────┘
  独立进程    独立进程             独立进程
```

**三个分层：**

1. **协议层**：兼容 Redis 协议的自定义命令集，任何能发 Redis 命令的客户端都能接入
2. **Hub 层**：纯内存状态机，管理 Agent 注册/心跳/信箱/任务/对话
3. **持久化层**：Store 接口 + SQLite 实现，Hub 重启后状态可恢复

---

## 三、协议设计：为什么选 Redis 协议

Mailbox Server 使用 [redcon](https://github.com/tidwall/redcon) 实现了一套**自定义 Redis 协议命令**，而不是标准 Redis。

**这个选择的好处：**

- 客户端直接用 `go-redis/v9` —— 生态成熟，连接池、重试、超时都有现成实现
- Redis 协议是文本协议，telnet/redis-cli 都能调试
- 命令语义清晰，不需要 HTTP 的 header/path 设计
- 未来可以把 Mailbox Server 换成真正的 Redis 集群，只需更换命令处理逻辑

**完整命令集：**

```
# Agent 生命周期
AGENT.REG    <agent_id>              → OK          注册 Agent，创建信箱
AGENT.PING   <agent_id>              → PONG        心跳，刷新在线时间
AGENT.LIST                           → [id, ...]   列出所有在线 Agent
AGENT.INFO   <agent_id>              → JSON        获取 Agent 元数据
AGENT.SETROLE   <agent_id> <role>    → OK          设置角色
AGENT.SETSTATUS <agent_id> <status> [task_id] → OK 设置状态

# 消息收发
MSG.SEND  <target_id> <message>      → OK          投递消息到信箱
MSG.RECV  <agent_id>                 → message|nil 非阻塞取一条消息
MSG.BCAST <message>                  → OK          广播给所有 Agent

# 任务管理
TASK.CREATE <creator_id> <desc>      → task_id     创建任务
TASK.ASSIGN <task_id> <agent_id>     → OK          分配给 Agent
TASK.START  <task_id>                → OK          标记为运行中
TASK.DONE   <task_id> <result>       → OK          标记为完成
TASK.FAIL   <task_id> <error>        → OK          标记为失败
TASK.GET    <task_id>                → JSON        获取任务详情
TASK.LIST                            → JSON        获取所有任务

# 对话记录
CONV.GET    <task_id>                → JSON        获取任务的对话历史
```

---

## 四、消息格式：结构化 JSON Envelope

Agent 之间传递的不是裸字符串，而是统一的 JSON 信封：

```json
{
  "type": "task_assign",
  "from": "orchestrator",
  "task_id": "task-1",
  "payload": {
    "description": "请提供3个有深度的选题方向..."
  }
}
```

**消息类型：**

| type | 用途 |
|------|------|
| `task_assign` | Orchestrator → Worker：下发任务 |
| `task_result` | Worker → Orchestrator：汇报结果 |
| `chat` | 任意方向：自由对话（关联 task_id）|
| `query` | 主动查询 |
| `info` | 状态通知 |

`task_id` 字段是关键：**Hub 在转发每条消息时，会自动解析 JSON，凡是带 `task_id` 的消息都记录到对话日志**。调用方无需主动记录，消息流动即留痕。

```go
// hub.go — Send 方法
func (h *Hub) Send(targetID, message string) error {
    h.mu.Lock()
    defer h.mu.Unlock()

    ch <- message  // 投递到信箱

    // 透明地记录对话
    var msg Message
    if json.Unmarshal([]byte(message), &msg) == nil && msg.TaskID != "" {
        h.appendConversationLocked(msg.From, targetID, msg)
    }
    return nil
}
```

---

## 五、Hub 内核

Hub 是整个系统的状态中心，所有操作都经过它。

### 5.1 Agent 注册与心跳

```
AGENT.REG worker-1
→ 创建 inboxes["worker-1"] = make(chan string, 100)
→ 初始化 agentInfos["worker-1"] = {status: idle, role: ""}
→ 启动客户端侧 keepAlive goroutine（每 10s 发一次 AGENT.PING）

AGENT.PING worker-1
→ 更新 lastSeen["worker-1"] = now
```

后台有一个 eviction goroutine（每 5s 运行），超过 30s 没有心跳的 Agent 会被自动驱逐，其信箱被关闭，并推送 `agent.evicted` 事件。

### 5.2 任务生命周期

```
pending → running → completed
                 ↘ failed
```

每次状态变更都触发两件事：
1. 持久化到 SQLite（`store.SaveTask`）
2. 向所有 Dashboard 订阅者推送 `task.updated` 事件

### 5.3 对话自动记录

`appendConversationLocked` 根据消息类型提取可读文本：

```go
switch msg.Type {
case MessageTypeTaskAssign:
    content = payload.Description       // 任务描述
case MessageTypeTaskResult:
    content = payload.Result            // 任务结果
case MessageTypeChat:
    content = payload.Text              // 对话内容
}
entry := ConversationEntry{At, From, To, TaskID, MsgType, Content}
h.conversations[taskID] = append(...)
h.store.SaveConversation(entry)         // 持久化
h.publishLocked(EventTypeConversationAdded, entry)  // 推送 Dashboard
```

### 5.4 事件总线

Hub 内置发布订阅：

```go
sub := hub.Subscribe()   // 返回 <-chan Event，容量 256
defer hub.Unsubscribe(sub)

for e := range sub {
    switch e.Type {
    case "task.updated":      // ...
    case "conversation.added": // ...
    }
}
```

Dashboard 就是通过这个机制实时感知所有状态变化，而不需要轮询。

---

## 六、持久化层

### Store 接口

```go
type Store interface {
    SaveTask(task Task) error
    LoadTasks() ([]Task, error)
    SaveAgentRole(agentID, role string) error
    LoadAgentRoles() (map[string]string, error)
    SaveConversation(entry ConversationEntry) error
    LoadConversations() (map[string][]ConversationEntry, error)
    Close() error
}
```

默认是 `noopStore`（什么都不存）。插入 SQLite 实现只需一行：

```go
store, _ := sqlitestore.New("creative_team.db")
s := server.NewMailboxServer(mailbox.WithStore(store))
```

Hub 启动时调用 `loadFromStore()`，自动恢复：
- 所有历史任务（含状态、结果）
- 所有 Agent 角色
- 所有对话记录

**SQLite schema：**

```sql
CREATE TABLE tasks (
    id TEXT PRIMARY KEY, description TEXT, created_by TEXT,
    assigned_to TEXT, status TEXT, created_at INTEGER,
    updated_at INTEGER, result TEXT, error TEXT
);

CREATE TABLE agent_roles (
    agent_id TEXT PRIMARY KEY, role TEXT
);

CREATE TABLE conversations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    at INTEGER, from_agent TEXT, to_agent TEXT,
    task_id TEXT, msg_type TEXT, content TEXT
);
```

Store 是接口，替换为 PostgreSQL、MongoDB 或分布式存储不需要改 Hub 代码。

---

## 七、实时 Dashboard

Dashboard 订阅 Hub 事件，通过 **SSE（Server-Sent Events）** 推送给浏览器。

```
Hub → Event Channel → Dashboard goroutine → SSE stream → Browser JS
```

**SSE 事件类型与对应 UI 操作：**

| SSE 事件 | 浏览器操作 |
|----------|-----------|
| `snapshot.agents` | 初始化 Agent 状态栏 |
| `snapshot.tasks` | 初始化任务列表 |
| `agent.updated` | 更新 Agent 状态点（绿/橙脉动）|
| `task.created` | 左侧添加新任务卡片 |
| `task.updated` | 更新 badge 状态，插入/替换结果卡片 |
| `conversation.added` | 在 Session Log 中插入新消息（result card 之前）|

浏览器端纯 JS，无外部依赖，内置 Markdown 渲染（`renderMd()`），支持标题、粗体、行内代码、代码块、列表、引用等。

---

## 八、creative_team：端到端流程

三个独立进程，通过 Mailbox 协作完成一篇文章的创作。

```bash
# 终端 1
go run ./examples/creative_team mailbox
# → 启动 Mailbox Server(:6382) + Dashboard(:8082)

# 终端 2
go run ./examples/creative_team topic-selector
# → 注册为 worker，等待任务

# 终端 3
go run ./examples/creative_team editor
# → 注册为 worker，等待任务

# 终端 4
go run ./examples/creative_team orchestrator "写一篇关于AI时代人类创造力的短文"
# → 注册为 orchestrator，驱动整个流程
```

### 三阶段流程

```
Phase 1: PMO → topic-selector
  ┌─────────────────────────────────────────────────────────────┐
  │ LLM 生成选题任务简报                                          │
  │ TASK.CREATE orchestrator "请提供3个选题方向..."               │
  │ TASK.ASSIGN task-1 topic-selector                           │
  │ MSG.SEND topic-selector {task_assign, task-1, 简报内容}      │
  │                                                             │
  │ topic-selector: LLM 处理 → TASK.DONE task-1 "选题A/B/C..."  │
  │ 期间双方通过 chat 消息自由沟通进展                             │
  └─────────────────────────────────────────────────────────────┘

Phase 2: PMO → editor
  ┌─────────────────────────────────────────────────────────────┐
  │ LLM 从选题结果中挑选最佳方向                                  │
  │ TASK.CREATE orchestrator "创作简报：主题/角度/结构..."        │
  │ TASK.ASSIGN task-2 editor                                   │
  │ MSG.SEND editor {task_assign, task-2, 创作简报}              │
  │                                                             │
  │ editor: LLM 创作 → TASK.DONE task-2 "正文..."               │
  └─────────────────────────────────────────────────────────────┘

Phase 3: PMO 写编辑寄语
  ┌─────────────────────────────────────────────────────────────┐
  │ LLM 基于成稿写2-3句编辑寄语                                   │
  │ 进入持续监听循环，等待 worker 的后续消息（Ctrl+C 退出）         │
  └─────────────────────────────────────────────────────────────┘
```

### waitForTask：带对话的等待

Orchestrator 等待任务完成时，并不是简单轮询，而是同时处理 chat 消息：

```go
func waitForTask(ctx, c, llm, taskID, workerID, chatPrompts) string {
    for {
        task, _ := c.GetTask(ctx, taskID)
        if task.Status == TaskStatusCompleted {
            return task.Result  // 完成，返回结果
        }

        // 处理 worker 主动发来的 chat
        for {
            raw, _ := c.Recv(ctx)
            if raw == "" { break }
            parsed, _ := mailbox.ParseMessage(raw)
            if parsed.Type == MessageTypeChat {
                reply := llmCall(ctx, llm, parsed.Text)
                sendChat(ctx, c, parsed.From, taskID, reply)
            }
        }

        // 定时主动问询（每 10s 发一次预设问题）
        if time.Now().After(nextChatAt) {
            sendChat(ctx, c, workerID, taskID, chatPrompts[chatIdx])
        }

        time.Sleep(500ms)
    }
}
```

---

## 九、集成新 Agent

集成一个新 Agent 只需三步，不需要修改任何现有代码。

### Step 1：注册与角色声明

```go
c := client.NewMailboxClient("reviewer", mailboxAddr)
c.Register(ctx)        // 创建信箱，启动心跳
c.SetRole(ctx, "worker")
```

### Step 2：实现消息处理循环

```go
for {
    raw, _ := c.Recv(ctx)
    if raw == "" {
        time.Sleep(150 * time.Millisecond)
        continue
    }

    msg, _ := mailbox.ParseMessage(raw)

    switch msg.Type {
    case mailbox.MessageTypeTaskAssign:
        payload, _ := mailbox.ParseTaskAssignPayload(msg)
        c.StartTask(ctx, msg.TaskID)
        c.SetStatus(ctx, "busy", msg.TaskID)

        // 用 LLM 处理任务
        result := llmCall(ctx, myLLM, payload.Description)

        c.CompleteTask(ctx, msg.TaskID, result)
        c.SetStatus(ctx, "idle", "")

    case mailbox.MessageTypeChat:
        // 任务进行中时响应对话
        if isActive {
            reply := llmCall(ctx, myLLM, chatText)
            sendChat(ctx, c, msg.From, msg.TaskID, reply)
        }
    }
}
```

### Step 3：让 Orchestrator 知道新 Agent 存在

Orchestrator 通过 `AGENT.LIST` 发现在线 Agent，或直接按 ID 下发任务：

```go
// 方式 A：直接指定 ID
c.AssignTask(ctx, taskID, "reviewer")

// 方式 B：动态发现
agents, _ := c.ListAgents(ctx)
// 选择合适的 agent...
```

### 完整的 Agent 模板

```go
func runMyAgent(agentID, role, systemPrompt string) {
    model := setupModel()
    ctx, cancel := signal.NotifyContext(context.Background(),
        syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    c := client.NewMailboxClient(agentID, mailboxAddr())
    c.Register(ctx)
    c.SetRole(ctx, role)

    llm := newLLMAgent(model, systemPrompt)
    var activeTaskSender string

    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        raw, _ := c.Recv(ctx)
        if raw == "" {
            time.Sleep(150 * time.Millisecond)
            continue
        }

        msg, _ := mailbox.ParseMessage(raw)

        switch msg.Type {
        case mailbox.MessageTypeTaskAssign:
            payload, _ := mailbox.ParseTaskAssignPayload(msg)
            activeTaskSender = msg.From
            c.StartTask(ctx, msg.TaskID)
            c.SetStatus(ctx, "busy", msg.TaskID)

            sendChat(ctx, c, activeTaskSender, msg.TaskID, "收到，处理中...")
            result := llmCall(ctx, llm, payload.Description)
            c.CompleteTask(ctx, msg.TaskID, result)
            c.SetStatus(ctx, "idle", "")
            sendChat(ctx, c, activeTaskSender, msg.TaskID, "完成，请查收。")
            activeTaskSender = ""  // 防止任务结束后继续灌水

        case mailbox.MessageTypeChat:
            if activeTaskSender == "" {
                continue  // 无活跃任务，不响应
            }
            text := getChatText(msg)
            reply := llmCall(ctx, llm, text)
            sendChat(ctx, c, msg.From, msg.TaskID, reply)
        }
    }
}
```

---

## 十、可扩展性分析

### 横向扩展：多个同角色 Worker

Mailbox 本身不阻止多个 Worker 注册相同角色。可以在 Orchestrator 侧实现简单的负载均衡：

```go
// 从 AGENT.LIST 中筛选 role == "worker" 且 status == "idle" 的 Agent
agents, _ := c.ListAgents(ctx)
for _, id := range agents {
    info, _ := c.GetAgentInfo(ctx, id)
    if info.Role == "worker" && info.Status == "idle" {
        c.AssignTask(ctx, taskID, id)
        break
    }
}
```

### 跨进程/跨机器

所有 Agent 只需要能访问 Mailbox Server 的 TCP 端口（默认 6382）。设置 `MAILBOX_ADDR` 环境变量即可跨机器部署：

```bash
MAILBOX_ADDR=10.0.0.1:6382 go run ./examples/creative_team editor
```

### 替换存储后端

将 SQLite 换为 PostgreSQL 只需实现 `Store` 接口：

```go
type PostgresStore struct{ db *sql.DB }
func (s *PostgresStore) SaveTask(t mailbox.Task) error { ... }
// ... 其他方法

server.NewMailboxServer(mailbox.WithStore(&PostgresStore{db}))
```

### 替换 LLM

`llmCall` 只依赖 `pkg/agent.Agent` 抽象，通过 `providers.Register` 注册任意 OpenAI 兼容接口：

```go
providers.Register(openai.New("my-provider",
    openai.WithBaseURL("https://api.openai.com/v1"),
    openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
))
model := &types.Model{ID: "gpt-4o", ProviderID: "my-provider"}
```

### Agent 能力扩展：工具调用

`pkg/agent.Agent` 支持 Tool Use，Worker 可以在处理任务时调用外部工具（搜索、代码执行等），对 Mailbox 层完全透明：

```go
llm := agent.NewAgent(agent.AgentConfig{
    InitialState: &agent.AgentState{
        SystemPrompt: systemPrompt,
        Model:        model,
        Tools:        []types.Tool{searchTool, codeTool},
    },
})
```

---

## 十一、设计取舍与后续方向

| 方向 | 当前 | 可扩展到 |
|------|------|---------|
| 传输协议 | Redis 协议（TCP） | gRPC、WebSocket |
| 消息队列 | 内存 channel（容量 100）| Kafka、NATS |
| 存储 | SQLite | PostgreSQL、分布式 KV |
| Agent 发现 | 手动指定 ID | 服务注册中心（Consul、etcd）|
| 任务调度 | Orchestrator 手动分配 | 基于能力的自动匹配 |
| 认证 | 无 | mTLS、Token |

**核心设计原则不变：**

- Agent 只需关心"收消息 → 处理 → 发消息"的三步循环
- Mailbox 负责消息路由、任务状态、对话记录，Agent 无感知
- Store/Provider 均为接口，替换底层实现不影响上层业务逻辑
