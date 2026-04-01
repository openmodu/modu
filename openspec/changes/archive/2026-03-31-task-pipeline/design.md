## Architecture

```
PublishPipeline(creatorID, steps...)
    │
    ▼
生成 pipelineID，创建 Pipeline 记录
发布 steps[0] 作为第一个 swarm 任务
    │
    ▼
Agent 认领并完成 step[0]
CompleteTask(taskID, result)
    │
    ├── 无 NextStepTemplate → 现有逻辑（标记 completed，结束）
    │
    └── 有 NextStepTemplate → 自动触发下一步
            │
            ▼
        构造 step[1] 描述（模板 + 上一步 result 注入）
        PublishTask(creatorID=pipelineID, desc, caps...)
        更新 Pipeline.CurrentStep++
        发布 EventTypePipelineStepCompleted
            │
            ▼
        step[N-1] 完成 → Pipeline 全部完成
        发布 EventTypePipelineCompleted
```

## Core Data Structures

### PipelineStep（新增）
```go
type PipelineStep struct {
    // DescriptionTemplate 是该步骤的任务描述模板。
    // 可以用 {{.PrevResult}} 引用上一步的输出结果。
    // 例如："根据以下资料生成摘要：\n{{.PrevResult}}"
    DescriptionTemplate string   `json:"description_template"`
    RequiredCaps        []string `json:"required_caps,omitempty"`
}
```

### Pipeline（新增）
```go
type Pipeline struct {
    ID          string         `json:"id"`
    CreatorID   string         `json:"creator_id"`
    Steps       []PipelineStep `json:"steps"`
    CurrentStep int            `json:"current_step"`  // 0-based，当前正在执行的步骤索引
    Status      string         `json:"status"`        // "running" | "completed" | "failed"
    CreatedAt   time.Time      `json:"created_at"`
    UpdatedAt   time.Time      `json:"updated_at"`
    // 收集每步结果，按步骤顺序存放
    Results     []string       `json:"results,omitempty"`
}
```

### Task（扩展）
```go
type Task struct {
    // ...existing fields...
    PipelineID       string `json:"pipeline_id,omitempty"`        // 所属 pipeline（若有）
    PipelineStepIdx  int    `json:"pipeline_step_idx,omitempty"`  // 在 pipeline 中的步骤索引（0-based）
    NextStepTemplate string `json:"next_step_template,omitempty"` // 下一步的 DescriptionTemplate（空=最后一步）
    NextStepCaps     []string `json:"next_step_caps,omitempty"`   // 下一步所需能力
}
```

### Hub（扩展）
```go
type Hub struct {
    // ...existing fields...
    pipelines map[string]*Pipeline
}
```

## Key Operations

### PublishPipeline(creatorID string, steps []PipelineStep) (string, error)

```
1. 校验：steps 长度 >= 2（1 步不需要 pipeline）
2. 生成 pipelineID（uuid 前缀 "pipe-"）
3. 创建 Pipeline{ID, CreatorID, Steps, Status="running", CurrentStep=0, Results=[]}
4. h.pipelines[pipelineID] = pipeline
5. 构造 step[0] 描述：直接使用 steps[0].DescriptionTemplate（无 PrevResult）
6. 创建 Task（swarm），附加 Pipeline 字段：
   - PipelineID = pipelineID
   - PipelineStepIdx = 0
   - NextStepTemplate = steps[1].DescriptionTemplate（若 len>1）
   - NextStepCaps = steps[1].RequiredCaps（若 len>1）
7. 入队 swarmQueue
8. 发布 EventTypePipelineStarted
9. 返回 pipelineID
```

### CompleteTask(taskID, agentID, result string)（修改）

在现有逻辑执行完毕（任务标记 completed）之后：

```
if task.PipelineID == "" → return（无 Pipeline，现有行为）

pipeline := h.pipelines[task.PipelineID]
pipeline.Results = append(pipeline.Results, result)
nextStepIdx := task.PipelineStepIdx + 1

if task.NextStepTemplate == "" {
    // 这是最后一步
    pipeline.Status = "completed"
    pipeline.UpdatedAt = now
    publishLocked(EventTypePipelineCompleted)
    return
}

// 构造下一步描述：替换 {{.PrevResult}}
nextDesc := strings.ReplaceAll(task.NextStepTemplate, "{{.PrevResult}}", result)

// 是否还有再下一步？
var nextNextTemplate string
var nextNextCaps []string
if nextStepIdx+1 < len(pipeline.Steps) {
    nextNextTemplate = pipeline.Steps[nextStepIdx+1].DescriptionTemplate
    nextNextCaps = pipeline.Steps[nextStepIdx+1].RequiredCaps
}

// 发布下一步任务
nextTask := Task{
    ID:               newID("task-"),
    Description:      nextDesc,
    CreatedBy:        task.PipelineID,   // creator = pipeline ID，便于追踪
    Status:           TaskStatusPending,
    SwarmOrigin:      true,
    RequiredCaps:     task.NextStepCaps,
    PipelineID:       task.PipelineID,
    PipelineStepIdx:  nextStepIdx,
    NextStepTemplate: nextNextTemplate,
    NextStepCaps:     nextNextCaps,
    CreatedAt:        now,
    UpdatedAt:        now,
}
h.tasks[nextTask.ID] = nextTask
h.swarmQueue = append(h.swarmQueue, nextTask.ID)
pipeline.CurrentStep = nextStepIdx
pipeline.UpdatedAt = now

publishLocked(EventTypePipelineStepCompleted, {pipelineID, stepIdx, nextTaskID})
```

## Template 渲染

使用 `strings.ReplaceAll` 替换 `{{.PrevResult}}`，保持轻量（无需引入 `text/template`）。

若需要完整 Go 模板语法（条件、循环），可在后续升级时替换为 `text/template`，接口不变。

## Event 扩展

```go
// event.go 新增
EventTypePipelineStarted       EventType = "pipeline.started"
EventTypePipelineStepCompleted EventType = "pipeline.step.completed"
EventTypePipelineCompleted     EventType = "pipeline.completed"
EventTypePipelineFailed        EventType = "pipeline.failed"
```

`Event` 新增字段：
```go
type Event struct {
    // ...existing fields...
    PipelineID string `json:"pipeline_id,omitempty"`
    StepIdx    int    `json:"step_idx,omitempty"`
}
```

## Server Commands

| Command | Args | Returns |
|---------|------|---------|
| `PIPELINE.PUBLISH` | `<creator_id> <json_steps>` | pipeline_id |
| `PIPELINE.GET` | `<pipeline_id>` | pipeline JSON |

`json_steps` 为 `[]PipelineStep` 的 JSON 字符串。

## Dashboard 扩展

- `GET /api/pipelines` — 返回所有 Pipeline 列表
- `GET /api/pipelines/{id}` — 单个 Pipeline 详情（含 Results 和 CurrentStep）

## 文件改动

```
pkg/mailbox/
  hub_types.go      ← 新增 PipelineStep、Pipeline 类型；Task 扩展 Pipeline 字段
  hub.go            ← Hub 新增 pipelines map，New() 初始化
  hub_task.go       ← CompleteTask() 末尾添加 pipeline 触发逻辑
  hub_swarm.go      ← 新增 PublishPipeline()
  event.go          ← 新增 pipeline 事件类型；Event 新增 PipelineID/StepIdx 字段
  server/server.go  ← 新增 PIPELINE.PUBLISH、PIPELINE.GET 命令
  client/agent.go   ← 新增 PublishPipeline、GetPipeline 方法
  dashboard/
    dashboard.go    ← 新增 /api/pipelines 路由
examples/
  pipeline_demo/
    main.go         ← 三步 Pipeline 示例
```
