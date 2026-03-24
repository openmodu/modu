# Mailbox

一个多 Agent 消息协调中心，提供 Agent 注册、点对点消息传递、任务/项目生命周期管理以及对话日志功能。

## 架构

```
┌─────────────────────────────────────────────┐
│                    Hub                       │
│                                             │
│  agentID → inbox (chan string, cap=100)     │
│  tasks   → Task{Assignees, Status, Result}  │
│  projects → Project{TaskIDs, Status}        │
│  conversations → []ConversationEntry        │
│                                             │
│  事件总线: hub.Subscribe() <-chan Event       │
└─────────────────────────────────────────────┘
         │ Store 接口
         ▼
  SQLiteStore / noopStore (默认)
```

每个 Agent 在 Hub 注册后会获得一个带缓冲的收件箱（容量 100）。消息为 JSON 字符串，按 Agent ID 进行路由。Hub 负责心跳跟踪，并自动剔除超过 30 秒无活动的 Agent。

## 快速开始

### 嵌入模式（进程内）

```go
import "github.com/openmodu/modu/pkg/mailbox"

hub := mailbox.NewHub()

hub.Register("director")
hub.Register("writer")

hub.SetAgentRole("director", "director")
hub.SetAgentRole("writer", "copywriter")

// 构建并发送消息
msg, _ := mailbox.NewTaskAssignMessage("director", "task-1", "编写产品文案")
hub.Send("writer", msg)

// 非阻塞接收
raw, ok := hub.Recv("writer")
if ok {
    m, _ := mailbox.ParseMessage(raw)
    p, _ := mailbox.ParseTaskAssignPayload(m)
    fmt.Println(p.Description) // "编写产品文案"
}
```

### 分布式模式（基于 Redis 的服务器）

当 Agent 运行在不同的进程或机器上时，使用 `client.MailboxClient`，它通过 Redis 自定义命令与服务器通信：

```go
import "github.com/openmodu/modu/pkg/mailbox/client"

c := client.NewMailboxClient("writer", "localhost:6379")
ctx := context.Background()

c.Register(ctx)   // 注册并启动后台心跳（每 10 秒 PING 一次）
c.SetRole(ctx, "copywriter")

taskID, _ := c.CreateTask(ctx, "编写产品文案")
c.AssignTask(ctx, taskID, "writer")
c.StartTask(ctx, taskID)

// ... 执行工作 ...

c.CompleteTask(ctx, taskID, "文案已完成：简洁且引人入胜。")
```

## API 参考

### Hub

```go
// 默认 hub — 无持久化，重启后数据丢失
hub := mailbox.NewHub()

// 带持久化存储
store, _ := sqlitestore.New("./mailbox.db")
hub := mailbox.NewHub(mailbox.WithStore(store))
```

#### Agent 管理

```go
hub.Register(agentID string)
hub.Heartbeat(agentID string) error
hub.SetAgentRole(agentID, role string) error
hub.SetAgentStatus(agentID, status, taskID string) error  // status: "idle" | "busy"
hub.GetAgentInfo(agentID string) (AgentInfo, error)
hub.ListAgents() []string
hub.ListAgentInfos() []AgentInfo
```

#### 消息传递

```go
hub.Send(targetID, message string) error  // 如果收件箱已满则返回错误
hub.Recv(agentID string) (string, bool)   // 非阻塞
hub.Broadcast(message string)             // 发送给所有已注册的 Agent
```

#### 任务管理

```go
// 创建任务，可选指定所属项目
hub.CreateTask(creatorID, description string, projectID ...string) (string, error)

// 分配给一个或多个 Agent（可多次调用）
hub.AssignTask(taskID, agentID string) error

hub.StartTask(taskID string) error

// 记录 Agent 的结果。只有当所有分配的 Agent 都提交后，任务才变为 "completed"。
hub.CompleteTask(taskID, agentID, result string) error

hub.FailTask(taskID, errMsg string) error
hub.GetTask(taskID string) (Task, error)
hub.ListTasks(projectID ...string) []Task  // 可选按项目过滤
```

#### 项目管理

```go
hub.CreateProject(creatorID, name string) (string, error)
hub.GetProject(projectID string) (Project, error)
hub.CompleteProject(projectID string) error
hub.ListProjects() []Project
```

#### 事件订阅

```go
events := hub.Subscribe()   // 返回 <-chan Event (缓冲容量 256)
defer hub.Unsubscribe(events)

for e := range events {
    switch e.Type {
    case mailbox.EventTypeAgentRegistered:
    case mailbox.EventTypeAgentEvicted:
    case mailbox.EventTypeAgentUpdated:
    case mailbox.EventTypeTaskCreated:
    case mailbox.EventTypeTaskUpdated:
    case mailbox.EventTypeProjectCreated:
    case mailbox.EventTypeProjectUpdated:
    case mailbox.EventTypeConversationAdded:
    }
}
```

#### 对话日志

携带 `task_id` 的消息会自动追加到对话日志中：

```go
hub.GetConversation(taskID string) []ConversationEntry
```

### 消息辅助函数

```go
// 构造函数
mailbox.NewTaskAssignMessage(from, taskID, description string) (string, error)
mailbox.NewTaskResultMessage(from, taskID, result, errMsg string) (string, error)
mailbox.NewChatMessage(from, taskID, text string) (string, error)

// 解析
msg, err := mailbox.ParseMessage(raw)
switch msg.Type {
case mailbox.MessageTypeTaskAssign:
    p, _ := mailbox.ParseTaskAssignPayload(msg)
case mailbox.MessageTypeTaskResult:
    p, _ := mailbox.ParseTaskResultPayload(msg)
case mailbox.MessageTypeChat:
    p, _ := mailbox.ParseChatPayload(msg)
}
```

### Store 接口

```go
type Store interface {
    SaveTask(task Task) error
    LoadTasks() ([]Task, error)
    SaveProject(project Project) error
    LoadProjects() ([]Project, error)
    SaveAgentRole(agentID, role string) error
    LoadAgentRoles() (map[string]string, error)
    SaveConversation(entry ConversationEntry) error
    LoadConversations() (map[string][]ConversationEntry, error)
    Close() error
}
```

| 实现 | 包 | 说明 |
|---|---|---|
| `noopStore` | `mailbox` (内部默认) | 无持久化 |
| `SQLiteStore` | `mailbox/sqlitestore` | 纯 Go，无 CGO，使用 `modernc.org/sqlite` |

```go
import "github.com/openmodu/modu/pkg/mailbox/sqlitestore"

store, err := sqlitestore.New("./mailbox.db")
defer store.Close()

hub := mailbox.NewHub(mailbox.WithStore(store))
```

## 示例：创意团队协作

导演 Agent 创建一个项目，将其拆分为并行的任务，并派遣给文案、视觉设计师和作曲家。每个 Agent 并发工作，并在完成后汇报。

```go
package main

import (
    "fmt"
    "sync"

    "github.com/openmodu/modu/pkg/mailbox"
)

func main() {
    hub := mailbox.NewHub()

    // 注册创意团队
    members := map[string]string{
        "director": "director",
        "writer":   "copywriter",
        "designer": "visual-designer",
        "composer": "music-composer",
    }
    for id, role := range members {
        hub.Register(id)
        hub.SetAgentRole(id, role)
    }

    // 导演创建项目
    projID, _ := hub.CreateProject("director", "春季活动")

    // 拆分为三个并行任务
    type job struct {
        desc     string
        assignee string
    }
    jobs := []job{
        {"编写一段 30 秒的广告剧本，带有温暖的春天气息", "writer"},
        {"设计主视觉海报：色调清新，以产品为中心", "designer"},
        {"创作一段 30 秒的轻快背景音乐", "composer"},
    }

    taskIDs := make([]string, len(jobs))
    for i, j := range jobs {
        taskID, _ := hub.CreateTask("director", j.desc, projID)
        hub.AssignTask(taskID, j.assignee)
        taskIDs[i] = taskID

        msg, _ := mailbox.NewTaskAssignMessage("director", taskID, j.desc)
        hub.Send(j.assignee, msg)
    }

    // 每个 Agent 并发处理任务
    var wg sync.WaitGroup
    mockResults := map[string]string{
        "writer":   `剧本："春天不会等待 —— 你也不应等待。"`,
        "designer": "主视觉：樱花、产品居中、暖金色调",
        "composer": "BGM: C 大调, 钢琴 + 弦乐, BPM=90, 30s",
    }

    for _, agentID := range []string{"writer", "designer", "composer"} {
        wg.Add(1)
        go func(id string) {
            defer wg.Done()

            raw, _ := hub.Recv(id)
            msg, _ := mailbox.ParseMessage(raw)

            hub.StartTask(msg.TaskID)
            hub.SetAgentStatus(id, "busy", msg.TaskID)

            result := mockResults[id]
            hub.CompleteTask(msg.TaskID, id, result)
            hub.SetAgentStatus(id, "idle", "")

            reply, _ := mailbox.NewTaskResultMessage(id, msg.TaskID, result, "")
            hub.Send("director", reply)
        }(agentID)
    }

    wg.Wait()

    // 导演审查所有结果
    fmt.Println("=== 创意团队交付成果 ===")
    for i, taskID := range taskIDs {
        task, _ := hub.GetTask(taskID)
        fmt.Printf("[任务 %d] %s\n  → %s\n", i+1, task.Description, task.Result)
    }

    hub.CompleteProject(projID)
    proj, _ := hub.GetProject(projID)
    fmt.Printf("\n项目 %q 状态: %s\n", proj.Name, proj.Status)

    // 检查对话日志
    fmt.Println("\n=== 对话日志 ===")
    for _, taskID := range taskIDs {
        for _, entry := range hub.GetConversation(taskID) {
            fmt.Printf("[%s] %s → %s: %s\n",
                entry.MsgType, entry.From, entry.To, entry.Content)
        }
    }
}
```

**示例输出：**

```
=== 创意团队交付成果 ===
[任务 1] 编写一段 30 秒的广告剧本，带有温暖的春天气息
  → 剧本："春天不会等待 —— 你也不应等待。"
[任务 2] 设计主视觉海报：色调清新，以产品为中心
  → 主视觉：樱花、产品居中、暖金色调
[任务 3] 创作一段 30 秒的轻快背景音乐
  → BGM: C 大调, 钢琴 + 弦乐, BPM=90, 30s

项目 "春季活动" 状态: completed

=== 对话日志 ===
[task_assign] director → writer: 编写一段 30 秒的广告剧本，带有温暖的春天气息
[task_result] writer → director: 剧本："春天不会等待 —— 你也不应等待。"
...
```

## 注意事项

- **心跳 (Heartbeat)**: 超过 30 秒无活动的 Agent 将被剔除，其收件箱将被关闭。`MailboxClient` 会自动每 10 秒发送一次 PING。
- **收件箱容量**: 每个 Agent 的收件箱可容纳 100 条消息。当已满时 `Send` 会返回错误 —— 调用者负责背压处理。
- **多分配者任务**: 只有当所有分配的 Agent 都使用其 `agentID` 调用 `CompleteTask` 后，任务才会转为 `completed` 状态。
- **事件传递**: 事件以非阻塞方式传递。处理缓慢的订阅者在超过 256 条缓冲后会丢失事件，而不会阻塞 Hub。
- **线程安全**: 所有 Hub 方法都是并发安全的。
