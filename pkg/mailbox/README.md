# Mailbox

A multi-agent message coordination hub providing agent registration, point-to-point messaging, task/project lifecycle management, and conversation logging.

## Architecture

```
┌─────────────────────────────────────────────┐
│                    Hub                       │
│                                             │
│  agentID → inbox (chan string, cap=100)     │
│  tasks   → Task{Assignees, Status, Result}  │
│  projects → Project{TaskIDs, Status}        │
│  conversations → []ConversationEntry        │
│                                             │
│  Event bus: hub.Subscribe() <-chan Event    │
└─────────────────────────────────────────────┘
         │ Store interface
         ▼
  SQLiteStore / noopStore (default)
```

Each agent registers with the Hub and receives a buffered inbox (capacity 100). Messages are JSON strings routed by agent ID. The Hub handles heartbeat tracking and evicts agents inactive for more than 30 seconds.

## Quick Start

### Embedded Mode (in-process)

```go
import "github.com/openmodu/modu/pkg/mailbox"

hub := mailbox.NewHub()

hub.Register("director")
hub.Register("writer")

hub.SetAgentRole("director", "director")
hub.SetAgentRole("writer", "copywriter")

// Build and send a message
msg, _ := mailbox.NewTaskAssignMessage("director", "task-1", "Write product copy")
hub.Send("writer", msg)

// Non-blocking receive
raw, ok := hub.Recv("writer")
if ok {
    m, _ := mailbox.ParseMessage(raw)
    p, _ := mailbox.ParseTaskAssignPayload(m)
    fmt.Println(p.Description) // "Write product copy"
}
```

### Distributed Mode (Redis-backed server)

When agents run in separate processes or machines, use `client.MailboxClient`, which communicates with the server via Redis custom commands:

```go
import "github.com/openmodu/modu/pkg/mailbox/client"

c := client.NewMailboxClient("writer", "localhost:6379")
ctx := context.Background()

c.Register(ctx)   // registers and starts a background keepalive (PING every 10s)
c.SetRole(ctx, "copywriter")

taskID, _ := c.CreateTask(ctx, "Write product copy")
c.AssignTask(ctx, taskID, "writer")
c.StartTask(ctx, taskID)

// ... do work ...

c.CompleteTask(ctx, taskID, "Copy done: concise and compelling.")
```

## API Reference

### Hub

```go
// Default hub — no persistence, data is lost on restart
hub := mailbox.NewHub()

// With a persistent store
store, _ := sqlitestore.New("./mailbox.db")
hub := mailbox.NewHub(mailbox.WithStore(store))
```

#### Agent Management

```go
hub.Register(agentID string)
hub.Heartbeat(agentID string) error
hub.SetAgentRole(agentID, role string) error
hub.SetAgentStatus(agentID, status, taskID string) error  // status: "idle" | "busy"
hub.GetAgentInfo(agentID string) (AgentInfo, error)
hub.ListAgents() []string
hub.ListAgentInfos() []AgentInfo
```

#### Messaging

```go
hub.Send(targetID, message string) error  // returns error if inbox is full
hub.Recv(agentID string) (string, bool)   // non-blocking
hub.Broadcast(message string)             // deliver to all registered agents
```

#### Task Management

```go
// Create a task, optionally scoped to a project
hub.CreateTask(creatorID, description string, projectID ...string) (string, error)

// Assign to one or more agents (callable multiple times)
hub.AssignTask(taskID, agentID string) error

hub.StartTask(taskID string) error

// Record an agent's result. Task becomes "completed" once all assignees submit.
hub.CompleteTask(taskID, agentID, result string) error

hub.FailTask(taskID, errMsg string) error
hub.GetTask(taskID string) (Task, error)
hub.ListTasks(projectID ...string) []Task  // optional project filter
```

#### Project Management

```go
hub.CreateProject(creatorID, name string) (string, error)
hub.GetProject(projectID string) (Project, error)
hub.CompleteProject(projectID string) error
hub.ListProjects() []Project
```

#### Event Subscription

```go
events := hub.Subscribe()   // returns <-chan Event (buffered 256)
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

#### Conversation Log

Messages that carry a `task_id` are automatically appended to the conversation log:

```go
hub.GetConversation(taskID string) []ConversationEntry
```

### Message Helpers

```go
// Constructors
mailbox.NewTaskAssignMessage(from, taskID, description string) (string, error)
mailbox.NewTaskResultMessage(from, taskID, result, errMsg string) (string, error)
mailbox.NewChatMessage(from, taskID, text string) (string, error)

// Parsing
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

### Store Interface

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

| Implementation | Package | Notes |
|---|---|---|
| `noopStore` | `mailbox` (internal default) | No persistence |
| `SQLiteStore` | `mailbox/sqlitestore` | Pure Go, no CGO, uses `modernc.org/sqlite` |

```go
import "github.com/openmodu/modu/pkg/mailbox/sqlitestore"

store, err := sqlitestore.New("./mailbox.db")
defer store.Close()

hub := mailbox.NewHub(mailbox.WithStore(store))
```

## Example: Creative Team Collaboration

A director agent creates a project, breaks it into parallel tasks, and dispatches them to a copywriter, visual designer, and composer. Each agent works concurrently and reports back when done.

```go
package main

import (
    "fmt"
    "sync"

    "github.com/openmodu/modu/pkg/mailbox"
)

func main() {
    hub := mailbox.NewHub()

    // Register the creative team
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

    // Director creates the project
    projID, _ := hub.CreateProject("director", "Spring Campaign")

    // Break into three parallel tasks
    type job struct {
        desc     string
        assignee string
    }
    jobs := []job{
        {"Write a 30-second ad script with a warm spring vibe", "writer"},
        {"Design the key visual poster: fresh tones, product-centered", "designer"},
        {"Compose a 30-second upbeat background track", "composer"},
    }

    taskIDs := make([]string, len(jobs))
    for i, j := range jobs {
        taskID, _ := hub.CreateTask("director", j.desc, projID)
        hub.AssignTask(taskID, j.assignee)
        taskIDs[i] = taskID

        msg, _ := mailbox.NewTaskAssignMessage("director", taskID, j.desc)
        hub.Send(j.assignee, msg)
    }

    // Each agent processes its task concurrently
    var wg sync.WaitGroup
    mockResults := map[string]string{
        "writer":   `Script: "Spring doesn't wait — neither should you."`,
        "designer": "Key visual: cherry blossoms, product center-frame, warm gold palette",
        "composer": "BGM: C major, piano + strings, BPM=90, 30s",
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

    // Director reviews all results
    fmt.Println("=== Creative Team Deliverables ===")
    for i, taskID := range taskIDs {
        task, _ := hub.GetTask(taskID)
        fmt.Printf("[Task %d] %s\n  → %s\n", i+1, task.Description, task.Result)
    }

    hub.CompleteProject(projID)
    proj, _ := hub.GetProject(projID)
    fmt.Printf("\nProject %q status: %s\n", proj.Name, proj.Status)

    // Inspect the conversation log
    fmt.Println("\n=== Conversation Log ===")
    for _, taskID := range taskIDs {
        for _, entry := range hub.GetConversation(taskID) {
            fmt.Printf("[%s] %s → %s: %s\n",
                entry.MsgType, entry.From, entry.To, entry.Content)
        }
    }
}
```

**Sample output:**

```
=== Creative Team Deliverables ===
[Task 1] Write a 30-second ad script with a warm spring vibe
  → Script: "Spring doesn't wait — neither should you."
[Task 2] Design the key visual poster: fresh tones, product-centered
  → Key visual: cherry blossoms, product center-frame, warm gold palette
[Task 3] Compose a 30-second upbeat background track
  → BGM: C major, piano + strings, BPM=90, 30s

Project "Spring Campaign" status: completed

=== Conversation Log ===
[task_assign] director → writer: Write a 30-second ad script with a warm spring vibe
[task_result] writer → director: Script: "Spring doesn't wait — neither should you."
...
```

## Notes

- **Heartbeat**: Agents inactive for more than 30 seconds are evicted and their inbox is closed. `MailboxClient` sends a PING every 10 seconds automatically.
- **Inbox capacity**: Each agent inbox holds 100 messages. `Send` returns an error when full — callers are responsible for backpressure handling.
- **Multi-assignee tasks**: A task only transitions to `completed` once every assigned agent calls `CompleteTask` with their own `agentID`.
- **Event delivery**: Events are delivered non-blocking. Slow subscribers will drop events beyond the 256-entry buffer without blocking the Hub.
- **Thread safety**: All Hub methods are safe for concurrent use.
