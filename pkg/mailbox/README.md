# Mailbox

A channel-based hub for managing communication between goroutines in concurrent applications.

## Overview

`mailbox` provides a centralized message routing system inspired by the actor model. It allows multiple senders to communicate with multiple receivers through named channels, making it ideal for building event-driven architectures and concurrent systems.

### Core Concepts

- **Hub**: Central message router that manages all mailbox instances
- **Mailbox**: Named channel within a hub, managed automatically when created
- **SendEvent**: Send a message to a specific mailbox by name
- **SubscribesTo**: Register for automatic mailbox creation and subscription
- **Unsubscribe**: Remove from a mailbox's subscriber list

### Key Benefits

1. **No manual channel management**: Mailboxes are created automatically when first subscribed
2. **Thread-safe**: All operations are safe to call from multiple goroutines concurrently
3. **Decoupled communication**: Senders and receivers don't need direct references to each other
4. **Automatic cleanup**: Unused mailboxes are cleaned up after a period of inactivity

## API Reference

### Hub Operations

```go
// Create a new hub instance
hub := mailbox.NewHub()

// Start the hub (required before using SendEvent or SubscribesTo)
hub.Start()

// Stop and close all mailboxes
hub.Close()
```

### Sending Messages

```go
// Send an event to a specific mailbox by name
mailbox.SendEvent(hub, "channel-name", myMessage)

// Example with typed message
type UserCreated struct {
    UserID string
    Email  string
}

mailbox.SendEvent(hub, "users", UserCreated{UserID: "123", Email: "user@example.com"})
```

### Subscribing to Mailboxes

```go
// Register a type for automatic mailbox creation and subscription
type MySubscriber struct {
    hub   *mailbox.Hub
    items chan Item
}

func (s *MySubscriber) SubscribesTo() []string {
    return []string{"items-channel"}
}

func (s *MySubscriber) OnHubStarted(h *mailbox.Hub) {
    // Called after hub starts - subscribe and create mailbox
    s.items = make(chan Item, 10)
    h.Subscribe("items-channel", s)
}
```

### Manual Subscription

```go
// Create a new mailbox manually
type MyMessage struct {
    Data string
}

mailbox := hub.NewMailbox[MyMessage]("my-channel")

// Subscribe to an existing mailbox
var subscriber MySubscriber
hub.Subscribe("my-channel", &subscriber)

// Unsubscribe from a mailbox
hub.Unsubscribe(&subscriber)
```

### Task Management

```go
// Create and start a task with automatic cleanup
task := hub.NewTask(func() error {
    // Task logic here
    return nil
})

task.Start()
task.Stop()  // Graceful shutdown
task.Close() // Force close immediately
```

### Project Management

```go
// Create a project with optional cleanup hook
project := hub.NewProject("my-project", func() {
    // Cleanup logic when all tasks complete
    fmt.Println("All tasks completed")
})

// Add task to project
project.AddTask(task)
```

### Event Subscription

```go
// Subscribe to hub-level events
hub.On(mailbox.HubStarted, func(h *mailbox.Hub) {
    fmt.Println("Hub started!")
})
hub.On(mailbox.HubClosed, func(h *mailbox.Hub) {
    fmt.Println("Hub closed!")
})

// Subscribe to mailbox events
hub.On(mailbox.MailboxCreated, func(m *mailbox.Mailbox[any]) {
    fmt.Printf("Mailbox created: %s\n", m.Name())
})
hub.On(mailbox.MailboxDeleted, func(m *mailbox.Mailbox[any]) {
    fmt.Printf("Mailbox deleted: %s\n", m.Name())
})
```

## Store Interface

The `Store` interface provides persistence for mailbox metadata:

```go
type Store interface {
    // Get retrieves a mailbox by name, or nil if not found
    Get(name string) *MailboxEntry
    
    // Put stores a mailbox entry (created/updated)
    Put(entry *MailboxEntry)
    
    // Delete removes a mailbox entry
    Delete(name string)
    
    // List returns all mailbox entries
    List() []*MailboxEntry
}
```

### In-Memory Store (Default)

```go
type MemoryStore struct {
    mu     sync.RWMutex
    items  map[string]*MailboxEntry
}

func NewMemoryStore() *MemoryStore {
    return &MemoryStore{items: make(map[string]*MailboxEntry)}
}
```

### Redis Store (Example)

```go
type RedisStore struct {
    client *redis.Client
    prefix string
}

func NewRedisStore(client *redis.Client, prefix string) *RedisStore {
    return &RedisStore{client: client, prefix: prefix}
}
```

## Usage Examples

### Simple Event Broadcasting

```go
package main

import (
    "fmt"
    "time"
    "github.com/openmodu/modu/pkg/mailbox"
)

type Message struct {
    Content string
}

func main() {
    hub := mailbox.NewHub()
    hub.Start()
    defer hub.Close()
    
    // Subscriber 1
    ch1 := make(chan Message, 10)
    hub.Subscribe("messages", &struct {
        items chan Message
    }{items: ch1})
    
    // Subscriber 2
    ch2 := make(chan Message, 10)
    hub.Subscribe("messages", &struct {
        items chan Message
    }{items: ch2})
    
    // Send message - both subscribers receive it
    go func() {
        time.Sleep(100 * time.Millisecond)
        mailbox.SendEvent(hub, "messages", Message{Content: "Hello!"})
    }()
    
    // Receive messages
    for i := 0; i < 2; i++ {
        select {
        case msg := <-ch1:
            fmt.Printf("Subscriber 1 received: %s\n", msg.Content)
        case msg := <-ch2:
            fmt.Printf("Subscriber 2 received: %s\n", msg.Content)
        }
    }
}
```

### Task-Based Processing

```go
package main

import (
    "fmt"
    "time"
    "github.com/openmodu/modu/pkg/mailbox"
)

type WorkItem struct {
    ID   int
    Data string
}

func main() {
    hub := mailbox.NewHub()
    hub.Start()
    defer hub.Close()
    
    // Create processor task
    itemCh := make(chan WorkItem, 10)
    
    processor := hub.NewTask(func() error {
        for item := range itemCh {
            fmt.Printf("Processing item %d: %s\n", item.ID, item.Data)
            time.Sleep(500 * time.Millisecond)
        }
        return nil
    })
    
    // Start processor and subscribe to work items
    processor.Start()
    hub.Subscribe("work", &struct {
        items chan WorkItem
    }{items: itemCh})
    
    // Send work items
    go func() {
        for i := 1; i <= 3; i++ {
            mailbox.SendEvent(hub, "work", WorkItem{ID: i, Data: fmt.Sprintf("task-%d", i)})
            time.Sleep(200 * time.Millisecond)
        }
    }()
    
    // Wait for completion
    time.Sleep(2 * time.Second)
}
```

### Project with Cleanup Hook

```go
package main

import (
    "fmt"
    "time"
    "github.com/openmodu/modu/pkg/mailbox"
)

type Event struct {
    Type string
}

func main() {
    hub := mailbox.NewHub()
    hub.Start()
    defer hub.Close()
    
    // Create project with cleanup callback
    project := hub.NewProject("event-processor", func() {
        fmt.Println("All events processed!")
    })
    
    eventCh := make(chan Event, 10)
    
    processor := hub.NewTask(func() error {
        for event := range eventCh {
            fmt.Printf("Handling %s event\n", event.Type)
            time.Sleep(300 * time.Millisecond)
        }
        return nil
    })
    
    project.AddTask(processor)
    processor.Start()
    hub.Subscribe("events", &struct {
        events chan Event
    }{events: eventCh})
    
    // Send some events
    go func() {
        for _, t := range []string{"start", "update", "end"} {
            mailbox.SendEvent(hub, "events", Event{Type: t})
            time.Sleep(100 * time.Millisecond)
        }
    }()
    
    // Wait for project to complete
    time.Sleep(2 * time.Second)
}
```

## Thread Safety

All Hub, Mailbox, and Task operations are thread-safe. You can safely:
- Send events from multiple goroutines to the same mailbox
- Subscribe/unsubscribe mailboxes concurrently
- Start/stop tasks while sending events

## Best Practices

1. **Always call `hub.Start()`** before using any Hub methods
2. **Use buffered channels** for high-throughput scenarios
3. **Subscribe early** - subscribe as soon as possible after hub creation
4. **Unsubscribe when done** - prevent memory leaks by unsubscribing unused handlers
5. **Handle errors in tasks** - ensure task functions return appropriate error values
6. **Use projects for related tasks** - group related tasks into projects for lifecycle management