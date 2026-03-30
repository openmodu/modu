# pkg/swarm

Auto-scaling Agent Swarm manager built on top of the [mailbox](../mailbox) infrastructure.

## Overview

Agent Teams use a fixed Orchestrator that explicitly assigns tasks to named workers. A Swarm is the opposite: there is **no orchestrator**. Tasks are published to a shared queue and agents compete to claim them autonomously.

| | Agent Teams | Agent Swarm |
|---|---|---|
| Task assignment | Orchestrator pushes to a specific agent | Any caller publishes; agents pull competitively |
| Roles | Pre-defined (editor, reviewer, …) | Dynamic — whoever has the capability claims the work |
| Scaling | Manual (fixed pool) | Automatic — `Swarm` monitors load and spawns/despawns |
| Claim mechanism | None | `ClaimTask` — atomic, first-match-wins |

## How It Works

```
Publisher ──► PublishTask(desc, caps...) ──► [Swarm Queue]
                                                   │
                                    ClaimTask(agentID)   ← atomic, capability-matched
                                                   │
                              Agent A ─────────────┤
                              Agent B ─────────────┤  compete for tasks
                              Agent C ─────────────┘
                                        │
                                   Swarm Manager (background)
                              monitors queue_len vs idle_agents
                                  → spawn / despawn agents
```

### Capability matching

Agents declare a capability list when they register. Tasks can optionally declare required capabilities. `ClaimTask` only returns a task if the agent's capabilities are a superset of the task's requirements. A task with no required capabilities can be claimed by any agent.

### Auto-scaling rules

| Condition | Action |
|---|---|
| `queue > 0` and `idle == 0` and `current < MaxAgents` | Spawn one agent |
| `queue / idle > ScaleUpRatio` and `current < MaxAgents` | Spawn one agent |
| `queue == 0` and `busy == 0` and `current > MinAgents` | Despawn one agent |

## Usage

### 1. Implement AgentFactory

```go
type MyFactory struct{ addr string }

func (f *MyFactory) Spawn(ctx context.Context, agentID string, caps []string) error {
    go runAgent(ctx, agentID, f.addr, caps)
    return nil
}
```

### 2. Create and start the Swarm

```go
hub := srv.Hub()

sw := swarm.New(hub, &MyFactory{addr: mailboxAddr}, swarm.SpawnPolicy{
    MinAgents:     2,
    MaxAgents:     8,
    Capabilities:  []string{"text-processing"},
    ScaleUpRatio:  1.5,           // spawn when queue/idle > 1.5
    CheckInterval: 2 * time.Second,
})
sw.Start()
defer sw.Stop()
```

### 3. Publish tasks (from anywhere)

```go
c := client.NewMailboxClient("my-service", mailboxAddr)
taskID, err := c.PublishTask(ctx, "summarise this article", "text-processing")
```

### 4. Agent loop

```go
func runAgent(ctx context.Context, agentID, addr string, caps []string) {
    c := client.NewMailboxClient(agentID, addr)
    _ = c.Register(ctx)
    _ = c.SetCapabilities(ctx, caps...)
    _ = c.SetStatus(ctx, "idle", "")

    for {
        task, err := c.ClaimTask(ctx)
        if err != nil || task == nil {
            time.Sleep(400 * time.Millisecond)
            continue
        }
        _ = c.SetStatus(ctx, "busy", task.ID)

        result := doWork(ctx, task)

        _ = c.CompleteTask(ctx, task.ID, result)
        _ = c.SetStatus(ctx, "idle", "")
    }
}
```

## Swarm with Adversarial Validation

If you want queue-based execution with a second-pass reviewer, publish tasks through `PublishValidatedTask` instead of `PublishTask`.

```go
taskID, _ := publisher.PublishValidatedTask(ctx,
    "Write a short product summary",
    2,   // max retries
    0.7, // pass threshold
    "text-processing",
)

task, _ := worker.ClaimTask(ctx)
validateTaskID, _ := worker.SubmitForValidation(ctx, task.ID, "Draft result")

validateTask, _ := validator.ClaimTask(ctx)
_ = validator.SubmitValidation(ctx, validateTask.ID, 0.85, "Looks good")

_ = taskID
_ = validateTaskID
```

This creates a separate validation task that requires the `validate` capability. If validation fails, the original swarm task is re-queued with feedback context until `MaxRetries` is exhausted. For the full state machine and task fields, see [pkg/mailbox](../mailbox/README.md).

## Mailbox Protocol

Three new server commands support swarm mode:

| Command | Description |
|---|---|
| `TASK.PUBLISH <creator> <desc> [cap...]` | Push a task onto the swarm queue |
| `TASK.CLAIM <agent_id>` | Atomically claim the first matching task; returns task JSON or null |
| `TASK.QUEUE` | List all tasks currently waiting in the queue |
| `AGENT.SETCAPS <agent_id> <cap...>` | Declare an agent's capabilities |

## Running the demo

```bash
go run ./examples/swarm_demo/
```

Opens a dashboard at `http://localhost:8083` showing agents being spawned as tasks arrive and scaled down when the queue drains.
