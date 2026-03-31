## Why

当前 Swarm 的任务是**完全独立**的，没有依赖关系。如果想做"A 完成后自动触发 B，B 完成后自动触发 C"这类流水线，必须由外部 Orchestrator 手动轮询任务状态后再发布下一个——逻辑分散、容易遗漏、增加网络往返。

Pipeline 模式将这种顺序关系**内建到 Hub**：定义好一组步骤，Hub 自动在每步完成时触发下一步，并将上一步的结果注入到下一步的描述中。

典型用例：
- LLM 工作流：`搜集资料 → 生成草稿 → 审校润色 → 发布`
- 数据处理：`解析原始数据 → 清洗 → 分析 → 生成报告`
- 代码生成：`理解需求 → 生成代码 → 运行测试 → Code Review`

## What Changes

- 新增 `PipelineStep` / `Pipeline` 类型：描述一组有序步骤，每步可指定 `RequiredCaps` 和描述模板
- 在 `Task` 中新增 `PipelineID`、`PipelineStep`、`NextStepTemplate` 字段：记录该任务在 Pipeline 中的位置
- 扩展 `CompleteTask()`：若任务属于 Pipeline 且有下一步，自动发布下一个 swarm 任务（注入上一步结果）
- 新增 `Hub.PublishPipeline()`：一次性注册整条流水线，发布第一步任务
- 新增 `PIPELINE.PUBLISH` server 命令 + client 方法
- 新增 `examples/pipeline_demo/main.go`：完整三步流水线示例

## Capabilities

### New Capabilities

- `task-pipeline`：定义并执行多步顺序 Pipeline，每步结果自动流入下一步

### Modified Capabilities

- `mailbox-hub`：CompleteTask 增加 Pipeline 触发逻辑，向后兼容（无 Pipeline 字段的任务行为不变）
- `mailbox-server`：新增 `PIPELINE.PUBLISH` 命令
- `mailbox-client`：新增 `PublishPipeline` 方法

## Impact

- 无破坏性改动，现有任务完全兼容
- 新类型和字段均 `omitempty`，不影响现有序列化
- 新示例：`examples/pipeline_demo/`
