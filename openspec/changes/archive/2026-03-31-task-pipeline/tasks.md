## 1. hub_types.go 扩展

- [x] 1.1 新增 `PipelineStep` struct（`DescriptionTemplate string`, `RequiredCaps []string`）
- [x] 1.2 新增 `Pipeline` struct（`ID`, `CreatorID`, `Steps []PipelineStep`, `CurrentStep int`, `Status string`, `Results []string`, `CreatedAt`, `UpdatedAt`）
- [x] 1.3 在 `Task` 中添加 `PipelineID string`（`json:"pipeline_id,omitempty"`）
- [x] 1.4 在 `Task` 中添加 `PipelineStepIdx int`（`json:"pipeline_step_idx,omitempty"`）
- [x] 1.5 在 `Task` 中添加 `NextStepTemplate string`（`json:"next_step_template,omitempty"`）
- [x] 1.6 在 `Task` 中添加 `NextStepCaps []string`（`json:"next_step_caps,omitempty"`）

## 2. event.go 扩展

- [x] 2.1 新增 `EventTypePipelineStarted EventType = "pipeline.started"`
- [x] 2.2 新增 `EventTypePipelineStepCompleted EventType = "pipeline.step.completed"`
- [x] 2.3 新增 `EventTypePipelineCompleted EventType = "pipeline.completed"`
- [x] 2.4 新增 `EventTypePipelineFailed EventType = "pipeline.failed"`
- [x] 2.5 在 `Event` struct 中添加 `PipelineID string`（`json:"pipeline_id,omitempty"`）
- [x] 2.6 在 `Event` struct 中添加 `StepIdx int`（`json:"step_idx,omitempty"`）

## 3. hub.go 扩展

- [x] 3.1 在 `Hub` struct 中添加 `pipelines map[string]*Pipeline`
- [x] 3.2 在 `New()` 中初始化 `pipelines: make(map[string]*Pipeline)`

## 4. hub_swarm.go 扩展

- [x] 4.1 实现 `PublishPipeline(creatorID string, steps []PipelineStep) (string, error)`：
  - 校验 steps >= 2
  - 生成 pipelineID（`"pipe-" + uuid`）
  - 创建并存储 Pipeline 记录
  - 构造 step[0] Task，填充 PipelineID/StepIdx/NextStepTemplate/NextStepCaps
  - 入队 swarmQueue
  - 发布 EventTypePipelineStarted
  - 返回 pipelineID
- [x] 4.2 实现 `GetPipeline(pipelineID string) (Pipeline, bool)`

## 5. hub_task.go 扩展（CompleteTask 末尾）

- [x] 5.1 在 `CompleteTask` 成功后添加 pipeline 触发逻辑：
  - task.PipelineID 为空则 return（现有行为不变）
  - 查找 pipeline，追加 result 到 pipeline.Results
  - task.NextStepTemplate 为空（最后一步）：pipeline.Status="completed"，发布 PipelineCompleted
  - 否则：用 `strings.ReplaceAll` 替换 `{{.PrevResult}}`，创建下一步 Task，入队，发布 PipelineStepCompleted

## 6. server/server.go 扩展

- [x] 6.1 新增 `PIPELINE.PUBLISH <creator_id> <json_steps>` 命令（返回 pipeline_id）
- [x] 6.2 新增 `PIPELINE.GET <pipeline_id>` 命令（返回 pipeline JSON 或 null）

## 7. client/agent.go 扩展

- [x] 7.1 新增 `PublishPipeline(ctx, creatorID string, steps []mailbox.PipelineStep) (string, error)`
- [x] 7.2 新增 `GetPipeline(ctx, pipelineID string) (*mailbox.Pipeline, error)`

## 8. dashboard/dashboard.go 扩展

- [x] 8.1 新增 `GET /api/pipelines` 路由（返回所有 Pipeline JSON 数组）
- [x] 8.2 新增 `GET /api/pipelines/{id}` 路由（返回单个 Pipeline 详情）

## 9. examples/pipeline_demo/main.go

- [x] 9.1 在 `examples/swarm_demo/main.go` 中扩展 Pipeline 演示（复用现有 server/dashboard/agents，不新建 demo）
- [x] 9.2 复用现有 `text-processing` 能力 agent，无需新增 agent 类型
- [x] 9.3 定义三步 Pipeline（调研→撰写→润色），12s 后自动发布
- [x] 9.4 轮询 GetPipeline，输出进度与最终结果
- [x] 9.5 同文件新增 Dead Agent Recovery 演示（demoDeadAgentRecovery），8s 后启动 doomed-agent

## 10. 测试

- [x] 10.1 `TestPipelineTwoSteps`：发布 2 步 pipeline → agent 完成 step[0] → 断言 step[1] 自动入队（pending，PipelineStepIdx=1，描述包含上步结果）→ agent 完成 step[1] → pipeline.Status="completed"
- [x] 10.2 `TestPipelineThreeSteps`：验证三步流水线全链路传递（result 逐步注入）
- [x] 10.3 `TestPipelinePrevResultInjection`：`{{.PrevResult}}` 被正确替换为上步实际结果
- [x] 10.4 `TestPipelineLastStep`：最后一步完成时发布 PipelineCompleted 事件，pipeline.Results 包含所有步骤结果
- [x] 10.5 `TestPublishPipelineValidation`：steps < 2 时返回错误
- [x] 10.6 `TestGetPipeline`：PublishPipeline 后可通过 pipelineID 查到 pipeline，CurrentStep 随任务完成递增
